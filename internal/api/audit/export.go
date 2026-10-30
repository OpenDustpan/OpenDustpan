package audit

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// channelHeader is the request header that declares where a request comes
// from.
const channelHeader = "Dustpan-Channel"

// exportTimeLimit bounds the run time of one export. The scan reads one entry
// for each sequence, so a request for a large log ends by itself and a client
// restarts it with fromSequence at the first sequence it has not got.
const exportTimeLimit = 30 * time.Minute

// exportFlushEvery is the count of lines after which the export sends its
// buffer to the client. The export holds one entry at a time, so its memory
// does not depend on the size of the log.
const exportFlushEvery = 256

// exportFormatJSON is the one format of the export: one JSON object for each
// line.
const exportFormatJSON = "json"

// Errors of the export. classify maps them to the answers of the console.
var (
	// errInvalidFormat reports a format value that the export does not
	// serve.
	errInvalidFormat = errors.New("invalid format: send json")
	// errInvalidTime reports a from or to value that is not a number of
	// Unix milliseconds.
	errInvalidTime = errors.New("invalid from or to: send Unix milliseconds, zero or more")
	// errInvalidRange reports a time range whose end is before its start.
	errInvalidRange = errors.New("invalid range: send to not before from")
	// errInvalidFromSequence reports a fromSequence value that is not a
	// positive number.
	errInvalidFromSequence = errors.New("invalid fromSequence: send a positive number")
	// errExportNotRecorded reports an export whose audit entry the server
	// could not write.
	errExportNotRecorded = errors.New("the audit entry of the export was not written: no entries sent")
)

// exportRequest is a checked export request. Zero From or To means no bound.
type exportRequest struct {
	fromMs       int64
	toMs         int64
	hasFrom      bool
	hasTo        bool
	fromSequence int64
}

// exportLine is one line of the export: the wire shape of an entry of the
// list and the scope of the entry. A reader needs the scope to compute the
// hash again, because the hash covers it.
//
// Contract: never remove or rename these fields.
type exportLine struct {
	auditEntry

	Namespace string `json:"namespace"`
}

// parseExport reads and checks the query of an export request. It rejects a
// value that is not valid; it never changes one.
func parseExport(r *http.Request) (exportRequest, error) {
	query := queryOf(r)

	var out exportRequest

	if format := query.Get("format"); format != "" && format != exportFormatJSON {
		return out, errInvalidFormat
	}

	var err error

	if out.fromMs, out.hasFrom, err = parseMs(query.Get("from")); err != nil {
		return out, err
	}

	if out.toMs, out.hasTo, err = parseMs(query.Get("to")); err != nil {
		return out, err
	}

	if out.hasFrom && out.hasTo && out.toMs < out.fromMs {
		return out, errInvalidRange
	}

	out.fromSequence = 1

	if raw := query.Get("fromSequence"); raw != "" {
		seq, err := parseSequence(raw)
		if err != nil {
			return out, errInvalidFromSequence
		}

		out.fromSequence = seq
	}

	return out, nil
}

// parseMs reads an optional number of Unix milliseconds. The second result
// is false for an empty value.
func parseMs(raw string) (int64, bool, error) {
	if raw == "" {
		return 0, false, nil
	}

	ms, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || ms < 0 {
		return 0, false, errInvalidTime
	}

	return ms, true, nil
}

// contains reports whether the entry time is in the time range of the request.
func (q exportRequest) contains(atMs int64) bool {
	return (!q.hasFrom || atMs >= q.fromMs) && (!q.hasTo || atMs <= q.toMs)
}

// description returns the object of the audit entry of the export: the
// scope, the format and every bound of the request.
func (q exportRequest) description(scope namespace.Name) string {
	parts := []string{"audit-export", scope.String(), "format=" + exportFormatJSON}
	if q.hasFrom {
		parts = append(parts, "from="+strconv.FormatInt(q.fromMs, 10))
	}

	if q.hasTo {
		parts = append(parts, "to="+strconv.FormatInt(q.toMs, 10))
	}

	parts = append(parts, "fromSequence="+strconv.FormatInt(q.fromSequence, 10))

	return strings.Join(parts, " ")
}

// exportEntries answers GET /api/audit/export with the entries of the scope
// oldest first, one JSON object for each line, from the first sequence that
// the request names to the last sequence at the start of the export. A time
// range keeps the entries inside it. Each line holds the previous hash and
// the hash, so a reader can check the range offline.
//
// The handler reads the entries one at a time by sequence, so its memory does
// not depend on the size of the log. It writes the audit entry of the export
// before the first line and sends nothing when that write fails. An entry
// that the store cannot read after the first line ends the answer by an
// aborted transfer, so a reader never mistakes a short export for a whole one.
func (h *Handler) exportEntries(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	req, err := parseExport(r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if h.reader == nil {
		h.writeError(ctx, w, errNoEntryReader)

		return
	}

	ns, err := h.auditScope(ctx, obj)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	last, found, err := h.reader.LastEntry(ctx, ns)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if err := h.recordExport(r, p, ns, req); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	if !found {
		return
	}

	scanCtx, cancel := context.WithTimeout(ctx, h.exportLimit)
	defer cancel()

	if err := h.streamExport(scanCtx, w, ns, req, last.Sequence); err != nil {
		h.logger.ErrorContext(ctx, "audit export aborted",
			slog.String("scope", ns.String()), slog.String("error", err.Error()))

		// The status line has left. Ending the transfer by force is the one
		// way to tell the client that the lines are not whole.
		panic(http.ErrAbortHandler)
	}
}

// streamExport writes the lines of the export for the sequences from
// req.fromSequence to lastSeq.
func (h *Handler) streamExport(
	ctx context.Context, w http.ResponseWriter, ns namespace.Name, req exportRequest, lastSeq int64,
) error {
	out := &exportWriter{buf: bufio.NewWriter(w), ctl: http.NewResponseController(w), written: 0}

	for seq := req.fromSequence; seq <= lastSeq; seq++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("export canceled: %w", err)
		}

		e, err := h.reader.EntryAt(ctx, ns, seq)
		if err != nil {
			return fmt.Errorf("read sequence %d: %w", seq, err)
		}

		if e.Namespace != ns {
			// Defense in depth: the store reads by scope. This check stays so
			// that an export never holds an entry of another scope.
			return fmt.Errorf("read sequence %d: %w", seq, audit.ErrEntryNotFound)
		}

		if !req.contains(e.AtMs()) {
			continue
		}

		if err := out.write(e); err != nil {
			return fmt.Errorf("write sequence %d: %w", seq, err)
		}
	}

	if err := out.buf.Flush(); err != nil {
		return fmt.Errorf("send lines: %w", err)
	}

	return nil
}

// exportWriter writes the lines of an export and sends them to the client
// in groups of exportFlushEvery lines.
type exportWriter struct {
	buf     *bufio.Writer
	ctl     *http.ResponseController
	written int
}

// write adds the line of e and sends the group when it is full.
func (o *exportWriter) write(e audit.Entry) error {
	line, err := json.Marshal(exportLine{auditEntry: toAuditEntry(e), Namespace: e.Namespace.String()})
	if err != nil {
		return fmt.Errorf("encode line: %w", err)
	}

	if _, err := o.buf.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("buffer line: %w", err)
	}

	o.written++
	if o.written%exportFlushEvery != 0 {
		return nil
	}

	if err := o.buf.Flush(); err != nil {
		return fmt.Errorf("send lines: %w", err)
	}

	// A writer that cannot flush keeps the lines until the end.
	if err := o.ctl.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return fmt.Errorf("flush lines: %w", err)
	}

	return nil
}

// recordExport writes the audit entry of the export in the scope that it
// reads: kind audit-read, with the range in the object. It fails closed.
func (h *Handler) recordExport(r *http.Request, p identity.Principal, ns namespace.Name, req exportRequest) error {
	action := identity.ActionNamespaceReadAudit
	if ns == audit.ServerScope {
		action = identity.ActionServerReadAudit
	}

	entry := audit.Entry{
		At:            time.Now().UTC(),
		Namespace:     ns,
		Subject:       p.Subject,
		IDPSubject:    "",
		TokenID:       p.TokenID,
		Action:        string(action),
		Object:        req.description(ns),
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      clientip.String(r),
		UserAgent:     audit.TruncateUserAgent(apidesc.Header(r, "User-Agent")),
		Channel:       audit.NormalizeChannel(apidesc.Header(r, channelHeader)),
		Kind:          audit.KindAuditRead,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     requestid.FromContext(r.Context()),
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: string(p.Kind),
		JobID:         p.JobID,
	}

	ctx := r.Context()

	if ns == audit.ServerScope {
		server, ok := h.entries.(audit.ServerStore)
		if !ok {
			return fmt.Errorf("%w: the store keeps no installation entries", errExportNotRecorded)
		}

		if _, err := server.AppendServer(ctx, entry); err != nil {
			return notRecorded(err)
		}

		return nil
	}

	target, err := h.namespaces.Get(ctx, ns)
	if err != nil {
		return notRecorded(err)
	}

	if _, err := h.entries.Append(ctx, target.Fence(), entry); err != nil {
		return notRecorded(err)
	}

	return nil
}

// notRecorded returns the error of an export whose entry the server could not
// write. A cause that the shared module answers in its own words (a fenced
// namespace, a namespace that is not at home, maintenance mode) stays as it
// is, so the client gets 409, 421 or 503 and not a 500. Any other cause is
// the error errExportNotRecorded.
func notRecorded(cause error) error {
	if _, known := problem.Shared(cause); known {
		return cause
	}

	return fmt.Errorf("%w: %w", errExportNotRecorded, cause)
}
