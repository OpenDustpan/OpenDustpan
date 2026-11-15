package server

// Restore routes of the installation API (ADR 38 decision 1): start a
// restore, list the restores this installation ran, read one restore with its
// manifest, and cancel a restore before its swap.
//
// POST /api/dustpan/v1/restores starts a restore in the background and
// answers 202 with the restore ID. The body names the backup ID from the
// configured target, whether to check only, and whether to cancel active
// work. Only one restore runs at a time; a second start answers 409. A worker
// of any instance runs the restore, and any instance answers its state. GET
// /api/dustpan/v1/restores lists the rows of the restores table, newest
// first. GET /api/dustpan/v1/restores/{id} answers the state (checking,
// applying, succeeded or failed) and, when the restore succeeded, the
// manifest of the restored backup. The backup taken before the apply appears
// in GET and List only after the service takes it.
//
// POST /api/dustpan/v1/restores/{id}/cancel stops a restore before its swap
// and answers 202. A restore that a cancel already stopped answers 202 again.
// A restore that swapped the data answers 409 with restore.swapped, a restore
// that ended answers 409 with restore.ended, and an unknown ID answers 404.
//
// All four need the action server:restore on the installation (@server).
// The backup comes only from the restore service the server was wired with:
// no request field, header or query value sets the target.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/identity"
)

// The paths of the restore routes.
const (
	restoresPath      = "/api/dustpan/v1/restores"
	restorePath       = "/api/dustpan/v1/restores/{id}"
	restoreCancelPath = "/api/dustpan/v1/restores/{id}/cancel"
)

// maxRestoreBodyBytes bounds the start-restore body, which holds only the
// backup ID with the check-only and force flags.
const maxRestoreBodyBytes = 4 << 10

// startRestoreRequest asks for one restore: the backup ID from the configured
// target, whether to check only, and whether to cancel active work first.
type startRestoreRequest struct {
	BackupID  string `json:"backupID"`
	CheckOnly bool   `json:"checkOnly"`
	Force     bool   `json:"force"`
}

// startRestoreResponse answers a started restore: its ID. The restore runs
// in the background; poll GET /api/dustpan/v1/restores/{id} until it leaves
// checking and applying, then read the backup taken before it there. The 202
// never names a pre-backup ID: the service takes that backup only after the
// checks pass, so a failed check must not name a backup that was never taken.
type startRestoreResponse struct {
	ID       string               `json:"id"`
	Warnings []restoreWarningJSON `json:"warnings"`
	// MissingBlobLimit is the effective bound on missing live blobs.
	MissingBlobLimit missingBlobLimitJSON `json:"missingBlobLimit"`
}

// missingBlobLimitJSON is the bound on the live blobs that the backup before
// a restore may find missing. The restore stops before it changes live data
// when the count of missing blobs is above count, or when its share of the
// blobs that the live data names is above percent and the count is above
// percentAbove. No setting changes the bound.
type missingBlobLimitJSON struct {
	Count        int `json:"count"`
	Percent      int `json:"percent"`
	PercentAbove int `json:"percentAbove"`
}

// blobFindingsJSON is what a backup found at the live blobs that its data
// names: the counts, and the first keys in key order. keys is never null.
type blobFindingsJSON struct {
	Named   int      `json:"named"`
	Missing int      `json:"missing"`
	Damaged int      `json:"damaged"`
	Keys    []string `json:"keys"`
}

// restoreWarningJSON is one warning of a restore. The code is stable, so a
// client can test it and show its own text. The restore goes on after a
// warning.
type restoreWarningJSON struct {
	Code string `json:"code"`
	Text string `json:"text"`
}

// devDataJSON states the dev data of a restore: the removal of a restore that
// ran, or, for a check-only restore, the removal that it would make. It names
// no token, no session ID and no more than the report holds.
type devDataJSON struct {
	// Principals is the number of dev principals.
	Principals int64 `json:"principals"`
	// Classes is the count of each class of removed rows.
	Classes map[string]int64 `json:"classes"`
}

// restoreSummaryJSON is one row of the restores table. Times are UTC in RFC
// 3339 with nanoseconds. EndedAt and Error stay empty while the restore runs.
// PreBackupID stays empty when the restore stopped before the apply, as with
// a check-only restore.
type restoreSummaryJSON struct {
	ID          string `json:"id"`
	State       string `json:"state"`
	BackupID    string `json:"backupID"`
	PreBackupID string `json:"preBackupID,omitempty"`
	RequestedBy string `json:"requestedBy"`
	StartedAt   string `json:"startedAt"`
	EndedAt     string `json:"endedAt,omitempty"`
	Error       string `json:"error,omitempty"`
	// ErrorCode is the catalog code of the refusal that ended the restore,
	// when it has one.
	ErrorCode string `json:"errorCode,omitempty"`
	// Warnings is always a list, empty when the restore has none.
	Warnings []restoreWarningJSON `json:"warnings"`
}

// listRestoresResponse lists the restores this installation ran, newest first.
type listRestoresResponse struct {
	Restores []restoreSummaryJSON `json:"restores"`
}

// getRestoreResponse answers one restore: its row and, when the restore
// succeeded, the manifest of the restored backup.
type getRestoreResponse struct {
	ID          string `json:"id"`
	State       string `json:"state"`
	BackupID    string `json:"backupID"`
	PreBackupID string `json:"preBackupID,omitempty"`
	RequestedBy string `json:"requestedBy"`
	StartedAt   string `json:"startedAt"`
	EndedAt     string `json:"endedAt,omitempty"`
	Error       string `json:"error,omitempty"`
	// ErrorCode is the catalog code of the refusal that ended the restore,
	// when it has one.
	ErrorCode string           `json:"errorCode,omitempty"`
	Manifest  *backup.Manifest `json:"manifest,omitempty"`
	// Warnings is always a list, empty when the restore has none.
	Warnings []restoreWarningJSON `json:"warnings"`
	// DevData is set when the restore found or removed dev data.
	DevData *devDataJSON `json:"devData,omitempty"`
	// PreBackupFound is set when the backup before the restore found live
	// blobs missing or damaged.
	PreBackupFound *blobFindingsJSON `json:"preBackupFound,omitempty"`
	// NotHeld is set when the restored backup holds no copy of some blobs
	// that its data names. The restore leaves those live keys as they are.
	NotHeld *blobFindingsJSON `json:"notHeld,omitempty"`
	// MissingBlobLimit is the effective bound on missing live blobs.
	MissingBlobLimit missingBlobLimitJSON `json:"missingBlobLimit"`
}

// startRestore starts a restore in the background and answers 202 with its
// ID. A second start while one runs answers 409. The caller needs
// server:restore; whether the restore replaces the access tables depends on
// server:restore-access, which only the site administrator holds. Without it
// the restore keeps the current access tables. The entry below records the
// request; the restore service writes the surviving entry after the swap,
// because the swap replaces audit_entries from the backup.
func (h *Handler) startRestore(
	w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	if h.restores == nil {
		h.writeError(ctx, w, errNoBackupTarget)

		return
	}

	req, err := parseStartRestoreRequest(w, r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	// A restore is a durable operation that a worker runs: with no worker,
	// the start answers 503 before it stores anything.
	if !h.admitStart(ctx, w) {
		return
	}

	id := backup.MintRestoreID()
	keepAccess := !canReplaceAccess(ctx, h, p)

	startErr := h.restores.Start(ctx, id, backup.RestoreRequest{
		BackupID:         backup.ID(req.BackupID),
		PreBackupID:      "",
		CheckOnly:        req.CheckOnly,
		Force:            req.Force,
		RequestedBy:      p.Subject,
		KeepAccessTables: keepAccess,
	})
	if startErr != nil {
		h.writeError(ctx, w, startErr)

		return
	}

	accessMode := "replaced"
	if keepAccess {
		accessMode = "kept"
	}

	h.appendServerChange(r, p, serverChange{
		at:      time.Now().UTC(),
		action:  identity.ActionServerRestore,
		object:  restoreAuditObject(id.String(), req.BackupID, accessMode),
		outcome: audit.OutcomeSuccess,
	})

	// The warnings that the kinds decide are on the row from the start.
	warnings := []restoreWarningJSON{}

	if record, getErr := h.restores.Get(ctx, id); getErr == nil {
		warnings = warningsJSON(record)
	}

	h.writeJSON(ctx, w, http.StatusAccepted, startRestoreResponse{
		ID: id.String(), Warnings: warnings, MissingBlobLimit: missingBlobLimit(),
	})
}

// canReplaceAccess reports whether p may replace the access tables in a
// restore: the authorizer allows server:restore-access on the installation.
// A denied action means the restore keeps the access tables; any other
// authorizer failure denies the replace, so the restore keeps them too.
func canReplaceAccess(
	ctx context.Context, h *Handler, p identity.Principal,
) bool {
	if h.authz == nil {
		return false
	}

	if err := h.authz.Authorize(ctx, p, identity.ActionServerRestoreAccess, identity.ServerObject()); err != nil {
		return false
	}

	return true
}

// restoreAuditObject names the request of one restore for its audit entry,
// with whether the restore will replace or keep the access tables. The entry
// records that the server accepted the request, not how the restore ends.
func restoreAuditObject(restoreID, backupID, accessMode string) string {
	return "restore " + restoreID + " of backup " + backupID + " requested (access tables will be " + accessMode + ")"
}

// parseStartRestoreRequest decodes the start body: the whole body must be one
// JSON value with a backup ID and no other field. The target is never a
// request field (ADR 30 decision 9): an unknown field such as target answers
// 400 instead of being silently ignored.
func parseStartRestoreRequest(w http.ResponseWriter, r *http.Request) (startRestoreRequest, error) {
	body, err := readBodyUpTo(w, r, maxRestoreBodyBytes, errInvalidBody)
	if err != nil {
		return startRestoreRequest{}, err
	}

	var req startRestoreRequest

	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&req); err != nil {
		return startRestoreRequest{}, errInvalidBody
	}

	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return startRestoreRequest{}, errInvalidBody
	}

	if strings.TrimSpace(req.BackupID) == "" {
		return startRestoreRequest{}, errInvalidBody
	}

	req.BackupID = strings.TrimSpace(req.BackupID)

	// A restore of an ID that is not a backup ID can never succeed, so the
	// route refuses it before the restore service makes a restore row.
	if _, err := backup.ParseID(req.BackupID); err != nil {
		return startRestoreRequest{}, err
	}

	return req, nil
}

// listRestores answers every restore row, newest first.
func (h *Handler) listRestores(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	if h.restores == nil {
		h.writeError(ctx, w, errNoBackupTarget)

		return
	}

	records, err := h.restores.List(ctx)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	summaries := make([]restoreSummaryJSON, 0, len(records))
	for i := range records {
		summaries = append(summaries, summarizeRestore(records[i]))
	}

	h.writeJSON(ctx, w, http.StatusOK, listRestoresResponse{Restores: summaries})
}

// getRestore answers one restore: its state and, when the restore succeeded,
// the manifest of the restored backup. A missing restore answers 404.
func (h *Handler) getRestore(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	if h.restores == nil {
		h.writeError(ctx, w, errNoBackupTarget)

		return
	}

	id := backup.RestoreID(r.PathValue("id"))

	record, err := h.restores.Get(ctx, id)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	doc := getRestoreResponse{
		ID:          record.ID.String(),
		State:       string(record.State),
		BackupID:    record.BackupID.String(),
		PreBackupID: "",
		RequestedBy: record.RequestedBy,
		StartedAt:   formatBackupTime(record.StartedAt),
		EndedAt:     "",
		Error:       record.Error,
		ErrorCode:   errorCodeOf(record),
		Manifest:    nil,
		Warnings:    warningsJSON(record),
		DevData:     devDataOf(record),

		PreBackupFound:   preBackupFoundOf(record),
		NotHeld:          notHeldOf(record),
		MissingBlobLimit: missingBlobLimit(),
	}

	if record.PreBackupID != "" {
		doc.PreBackupID = record.PreBackupID.String()
	}

	if !record.EndedAt.IsZero() {
		doc.EndedAt = formatBackupTime(record.EndedAt)
	}

	if record.State != backup.RestoreSucceeded {
		h.writeJSON(ctx, w, http.StatusOK, doc)

		return
	}

	manifest, err := h.restores.ReadManifest(ctx, record.BackupID)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	doc.Manifest = &manifest

	h.writeJSON(ctx, w, http.StatusOK, doc)
}

// summarizeRestore renders one restore row for the list.
func summarizeRestore(record backup.RestoreRecord) restoreSummaryJSON {
	doc := restoreSummaryJSON{
		ID:          record.ID.String(),
		State:       string(record.State),
		BackupID:    record.BackupID.String(),
		PreBackupID: "",
		RequestedBy: record.RequestedBy,
		StartedAt:   formatBackupTime(record.StartedAt),
		EndedAt:     "",
		Error:       record.Error,
		ErrorCode:   errorCodeOf(record),
		Warnings:    warningsJSON(record),
	}

	if record.PreBackupID != "" {
		doc.PreBackupID = record.PreBackupID.String()
	}

	if !record.EndedAt.IsZero() {
		doc.EndedAt = formatBackupTime(record.EndedAt)
	}

	return doc
}

// warningsJSON renders the warnings of a restore row as a list that is never
// nil.
func warningsJSON(record backup.RestoreRecord) []restoreWarningJSON {
	warnings := record.Warnings()
	out := make([]restoreWarningJSON, 0, len(warnings))

	for _, w := range warnings {
		out = append(out, restoreWarningJSON{Code: string(w.Code), Text: w.Text})
	}

	return out
}

// devDataOf renders the dev data of a restore row, or nil when the row holds
// no report that names dev data.
func devDataOf(record backup.RestoreRecord) *devDataJSON {
	if record.DevReport == nil || !record.DevReport.HasDev() {
		return nil
	}

	classes := make(map[string]int64, len(record.DevReport.Classes))

	for class, n := range record.DevReport.Classes {
		if n > 0 {
			classes[string(class)] = n
		}
	}

	return &devDataJSON{Principals: record.DevReport.Classes[backup.RemovedPrincipals], Classes: classes}
}

// errorCodeOf returns the catalog code of the refusal that ended the restore
// of record, or an empty string. The code comes from a stored row, so it is
// checked against the codes that a restore writes. A row from another
// version or a changed report can hold any string: an unknown code answers
// the generic catalog code, and the stored string is never shown.
func errorCodeOf(record backup.RestoreRecord) string {
	if record.DevReport == nil || record.DevReport.ErrorCode == "" {
		return ""
	}

	if !slices.Contains(backup.RestoreErrorCodes(), record.DevReport.ErrorCode) {
		return errcode.ServerInternal
	}

	return string(record.DevReport.ErrorCode)
}

// missingBlobLimit returns the effective bound on missing live blobs.
func missingBlobLimit() missingBlobLimitJSON {
	limit := backup.EffectiveMissingBlobLimit()

	return missingBlobLimitJSON{Count: limit.Count, Percent: limit.Percent, PercentAbove: limit.PercentAbove}
}

// preBackupFoundOf renders what the backup before the restore of record found
// at the live blobs, or nil when it found none.
func preBackupFoundOf(record backup.RestoreRecord) *blobFindingsJSON {
	if record.DevReport == nil {
		return nil
	}

	return findingsJSON(record.DevReport.PreBackupFound)
}

// notHeldOf renders the blobs that the restored backup of record holds no
// copy of, or nil when it holds every blob that its data names.
func notHeldOf(record backup.RestoreRecord) *blobFindingsJSON {
	if record.DevReport == nil {
		return nil
	}

	return findingsJSON(record.DevReport.NotHeld)
}

// findingsJSON renders found, or nil when found is nil or names no blob.
func findingsJSON(found *backup.BlobFindings) *blobFindingsJSON {
	if found == nil || found.Empty() {
		return nil
	}

	keys := make([]string, 0, len(found.Keys))
	keys = append(keys, found.Keys...)

	return &blobFindingsJSON{Named: found.Named, Missing: found.Missing, Damaged: found.Damaged, Keys: keys}
}

// cancelRestoreResponse answers an accepted cancel: the ID of the restore.
// Poll GET /api/dustpan/v1/restores/{id} until the restore leaves checking
// and applying.
type cancelRestoreResponse struct {
	ID string `json:"id"`
}

// cancelRestore stops a restore before its swap and answers 202. It writes an
// audit entry for each cancel that names a valid restore ID: success for an
// accepted cancel, failure for a refused one.
func (h *Handler) cancelRestore(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	if h.restores == nil {
		h.writeError(ctx, w, errNoBackupTarget)

		return
	}

	// The ID must match the pattern of the restore IDs. An ID that cannot
	// exist answers as an unknown restore, before any read.
	id, err := backup.ParseRestoreID(r.PathValue("id"))
	if err != nil {
		h.writeError(ctx, w, backup.ErrRestoreNotFound)

		return
	}

	cancelErr := h.restores.Cancel(ctx, id)

	outcome := audit.OutcomeSuccess
	if cancelErr != nil {
		outcome = audit.OutcomeFailure
	}

	if !errors.Is(cancelErr, backup.ErrRestoreNotFound) {
		h.appendServerChange(r, p, serverChange{
			at:      time.Now().UTC(),
			action:  identity.ActionServerRestore,
			object:  "cancel of restore " + id.String(),
			outcome: outcome,
		})
	}

	switch {
	case cancelErr == nil:
		h.writeJSON(ctx, w, http.StatusAccepted, cancelRestoreResponse{ID: id.String()})
	case errors.Is(cancelErr, backup.ErrRestoreSwapped):
		h.writeCoded(ctx, w, failCoded(http.StatusConflict, backup.ErrRestoreSwapped.Error(),
			errcode.RestoreSwapped, errcode.Identifier("restore", id.String())))
	case errors.Is(cancelErr, backup.ErrRestoreNotRunning):
		h.writeCoded(ctx, w, failCoded(http.StatusConflict, backup.ErrRestoreNotRunning.Error(),
			errcode.RestoreEnded, errcode.Identifier("restore", id.String())))
	default:
		h.writeError(ctx, w, cancelErr)
	}
}
