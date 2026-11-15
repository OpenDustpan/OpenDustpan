package job

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/history"
)

const (
	// RedactMarker is the text that replaces each match of a redaction.
	RedactMarker = "[redacted]"
	// MaxSubstitutions is the most substitutions of one redaction.
	MaxSubstitutions = 100
	// MaxSubstitutionBytes is the longest match of a substitution, in bytes.
	MaxSubstitutionBytes = 4096
	// MaxRedactLogBytes is the largest log version that a redaction reads, in
	// bytes. The service holds the log in memory.
	MaxRedactLogBytes = 64 << 20
)

// RedactLog implements Service. Each check fails closed, and the order is the
// order of what a caller may learn:
//
//  1. The reason and the substitutions, at the boundary.
//  2. The job, then job:read (a denial is ErrNotFound, as Get does), then
//     unit:redact on the unit of the job.
//  3. The log version: it must be a version of a step of this job, closed,
//     and not redacted.
//
// The service replaces each match in one pass over the old text, so the
// marker never matches a substitution. It writes the result as the next
// version, and then redacts the old version through the history model. A
// failure of the second step leaves the next version in place.
func (s *service) RedactLog(ctx context.Context, a Actor, id ID, req RedactRequest) (LogRef, error) {
	err := ctx.Err()
	if err != nil {
		return LogRef{}, fmt.Errorf("redact log: %w", err)
	}

	// Fail closed: with no artifact store the server cannot redact.
	if s.artifacts == nil {
		return LogRef{}, fmt.Errorf("%w: this server keeps no store for job logs", ErrUnsupported)
	}

	err = checkRedaction(&req)
	if err != nil {
		return LogRef{}, err
	}

	j, err := s.authorizedFor(ctx, a, id, ActionUnitRedact)
	if err != nil {
		return LogRef{}, err
	}

	old := logVersionOf(&j, req.Version)
	if old == nil {
		return LogRef{}, fmt.Errorf("%w: job %s has no log version %s", ErrNotFound, j.ID, clip(req.Version))
	}

	// current is false: the redaction writes the next version before it
	// redacts this one.
	err = CheckLogRedaction(old, false, history.CauseRequest)
	if err != nil {
		return LogRef{}, fmt.Errorf("redact log: %w", err)
	}

	if !old.RedactedAt.IsZero() {
		return LogRef{}, fmt.Errorf("%w: log version %s", history.ErrRedacted, old.ID)
	}

	text, err := s.readLog(ctx, &j, old)
	if err != nil {
		return LogRef{}, err
	}

	out, matches, err := substitute(ctx, text, req.Substitutions)
	if err != nil {
		return LogRef{}, err
	}

	if matches == 0 {
		return LogRef{}, fmt.Errorf("%w: no substitution matches log version %s", ErrInvalid, old.ID)
	}

	return s.replaceLog(ctx, &j, a, &logRedaction{old: old, text: out, matches: matches, reason: req.Reason})
}

// checkRedaction is the boundary check of a redaction: 1 to MaxSubstitutions
// substitutions with a match of 1 to MaxSubstitutionBytes bytes, and a reason
// that history.ValidateReason accepts. It refuses a value and never changes it.
func checkRedaction(req *RedactRequest) error {
	err := history.ValidateReason(req.Reason)
	if err != nil {
		return fmt.Errorf("%w: reason: %w", ErrInvalid, err)
	}

	subs := req.Substitutions
	if len(subs) == 0 || len(subs) > MaxSubstitutions {
		return fmt.Errorf("%w: send 1 to %d substitutions, not %d", ErrInvalid, MaxSubstitutions, len(subs))
	}

	for i := range subs {
		if len(subs[i].Match) == 0 || len(subs[i].Match) > MaxSubstitutionBytes {
			return fmt.Errorf("%w: the match of substitution %d must hold 1 to %d bytes", ErrInvalid, i,
				MaxSubstitutionBytes)
		}
	}

	return nil
}

// logVersionOf returns the log version of j with the ID version, from the step
// records of its phases, or nil. Only a version that the job holds is found, so
// a caller never redacts the log of another job.
func logVersionOf(j *Job, version string) *LogRef {
	for i := range j.Phases {
		for k := range j.Phases[i].Steps {
			if l := j.Phases[i].Steps[k].Log; l != nil && l.ID == version {
				return l
			}
		}
	}

	return nil
}

// readLog returns the text of the log version old. It refuses a log over
// MaxRedactLogBytes.
func (s *service) readLog(ctx context.Context, j *Job, old *LogRef) ([]byte, error) {
	// Defense in depth: the size in the record and the bound of the reader below
	// both stop a log that is too large. Neither relies on the other.
	if old.Size > MaxRedactLogBytes {
		return nil, fmt.Errorf("%w: log version %s is larger than %d bytes", ErrInvalid, old.ID, MaxRedactLogBytes)
	}

	rc, err := s.artifacts.OpenLog(ctx, j.Spec.Unit.Namespace(), old.ID)
	if err != nil {
		return nil, fmt.Errorf("open log %s: %w", old.ID, err)
	}

	text, readErr := io.ReadAll(io.LimitReader(rc, MaxRedactLogBytes+1))

	err = errors.Join(readErr, rc.Close())
	if err != nil {
		return nil, fmt.Errorf("read log %s: %w", old.ID, err)
	}

	if len(text) > MaxRedactLogBytes {
		return nil, fmt.Errorf("%w: log version %s is larger than %d bytes", ErrInvalid, old.ID, MaxRedactLogBytes)
	}

	return text, nil
}

// substitute replaces each match of subs in text with RedactMarker, in one
// pass: at each position the longest match wins, so that no tail of a longer
// secret stays in the text. It returns the new text and the number of matches.
// It stops when ctx ends, and it refuses a new text of more than
// MaxRedactLogBytes bytes: a short match grows by the length of the marker.
func substitute(ctx context.Context, text []byte, subs []Substitution) ([]byte, int, error) {
	out := make([]byte, 0, len(text))
	matches := 0

	for i := 0; i < len(text); {
		// Check the context at a fixed interval, not at each byte.
		if i&0xffff == 0 {
			err := ctx.Err()
			if err != nil {
				return nil, 0, fmt.Errorf("redact log: %w", err)
			}
		}

		n := longestMatchAt(text[i:], subs)
		if n == 0 {
			out = append(out, text[i])
			i++
		} else {
			out = append(out, RedactMarker...)
			i += n
			matches++
		}

		if len(out) > MaxRedactLogBytes {
			return nil, 0, fmt.Errorf("%w: the new text is larger than %d bytes", ErrInvalid, MaxRedactLogBytes)
		}
	}

	return out, matches, nil
}

// longestMatchAt returns the length of the longest match of subs that is a
// prefix of text, or 0.
func longestMatchAt(text []byte, subs []Substitution) int {
	longest := 0

	for k := range subs {
		if len(subs[k].Match) > longest && bytes.HasPrefix(text, subs[k].Match) {
			longest = len(subs[k].Match)
		}
	}

	return longest
}

// logRedaction is what replaceLog writes: the log version old, the new text
// that replaces it, and the number of matches.
type logRedaction struct {
	old     *LogRef
	text    []byte
	matches int
	reason  string
}

// replaceLog writes the next version of old with the new text, and then
// redacts old through the history model, with one audit entry that holds the
// number of matches and never the text.
func (s *service) replaceLog(ctx context.Context, j *Job, a Actor, lr *logRedaction) (LogRef, error) {
	ns := j.Spec.Unit.Namespace()

	fence, err := s.fences.Fence(ctx, ns)
	if err != nil {
		return LogRef{}, fmt.Errorf("fence of %s: %w", ns, err)
	}

	next, err := s.artifacts.ReplaceLog(ctx, LogReplacement{
		Namespace: ns, Fence: fence, From: lr.old.ID, Body: bytes.NewReader(lr.text), By: a.Principal.Subject,
	})
	if errors.Is(err, ErrConflict) {
		// Fail closed. A retry of a redaction whose second step failed ends here:
		// the next version exists, and this service cannot find the older version
		// (the step record keeps only the current one), so it cannot redact it.
		return LogRef{}, fmt.Errorf(
			"replace log %s: %w: a newer version exists, and this version of the service cannot redact an older one; "+
				"the log version %s still holds the text", lr.old.ID, err, lr.old.ID,
		)
	}

	if err != nil {
		return LogRef{}, fmt.Errorf("replace log %s: %w", lr.old.ID, err)
	}

	next.Matches = lr.matches

	r := history.Redaction{
		At: s.now().UTC(), Item: LogItem(historyUnit(j), lr.old.ID), ID: history.NewRedactionID(),
		By:     history.Actor{Subject: a.Principal.Subject, TokenID: a.Principal.TokenID, JobID: ""},
		Reason: lr.reason, Cause: history.CauseRequest, ItemKey: lr.old.ItemKey, AuditHash: "", Blobs: nil,
		Targets: nil,
	}

	_, err = s.artifacts.Redact(ctx, r, nil, s.redactEntry(j, a, lr, &next), fence)
	if err != nil {
		return LogRef{}, fmt.Errorf(
			"redact log %s: %w: the new version %s exists, and the log version %s still holds the text; "+
				"a retry cannot redact it yet", lr.old.ID, err, next.ID, lr.old.ID,
		)
	}

	return next, nil
}

// historyUnit returns the history reference of the unit of j.
func historyUnit(j *Job) history.Unit {
	return history.Unit{Namespace: j.Spec.Unit.Namespace(), Tool: j.Spec.Unit.Tool(), ID: j.Spec.Unit.UnitID()}
}

// redactEntry returns the audit entry of a redaction: who, on which unit, the
// job, the old and the new version, and the number of matches. It never holds
// the text of a substitution.
func (s *service) redactEntry(j *Job, a Actor, lr *logRedaction, next *LogRef) audit.Entry {
	was := lr.old.ID

	changes := []audit.Change{
		{Key: changeKeyJob, Old: nil, New: string(j.ID), Layer: "", TakesEffect: ""},
		{Key: "log", Old: &was, New: next.ID, Layer: "", TakesEffect: ""},
		{Key: "matches", Old: nil, New: strconv.Itoa(lr.matches), Layer: "", TakesEffect: ""},
	}

	if a.Agent != "" {
		changes = append(changes, audit.Change{Key: changeKeyAgent, Old: nil, New: a.Agent, Layer: "", TakesEffect: ""})
	}

	return s.changeEntry(j, a, ActionUnitRedact, changes, lr.reason)
}
