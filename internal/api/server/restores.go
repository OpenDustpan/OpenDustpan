package server

// Restore routes of the installation API (ADR 38 decision 1): start a
// restore, list the restores this installation ran, and read one restore
// with its manifest.
//
// POST /api/server/restores starts a restore in the background and answers
// 202 with the restore ID. The body names the backup ID from the configured
// target, whether to check only, and whether to cancel active work. Only one
// restore runs at a time; a second start answers 409. GET
// /api/server/restores lists the rows of the restores table, newest first.
// GET /api/server/restores/{id} answers the state (checking, applying,
// succeeded or failed) and, when the restore succeeded, the manifest of the
// restored backup. The backup taken before the apply appears in GET and List
// only after the service takes it.
//
// All three need the action server:restore on the installation (@server).
// The backup comes only from the restore service the server was wired with:
// no request field, header or query value sets the target.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/identity"
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
// in the background; poll GET /api/server/restores/{id} until it leaves
// checking and applying, then read the backup taken before it there. The 202
// never names a pre-backup ID: the service takes that backup only after the
// checks pass, so a failed check must not name a backup that was never taken.
type startRestoreResponse struct {
	ID string `json:"id"`
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
}

// listRestoresResponse lists the restores this installation ran, newest first.
type listRestoresResponse struct {
	Restores []restoreSummaryJSON `json:"restores"`
}

// getRestoreResponse answers one restore: its row and, when the restore
// succeeded, the manifest of the restored backup.
type getRestoreResponse struct {
	ID          string           `json:"id"`
	State       string           `json:"state"`
	BackupID    string           `json:"backupID"`
	PreBackupID string           `json:"preBackupID,omitempty"`
	RequestedBy string           `json:"requestedBy"`
	StartedAt   string           `json:"startedAt"`
	EndedAt     string           `json:"endedAt,omitempty"`
	Error       string           `json:"error,omitempty"`
	Manifest    *backup.Manifest `json:"manifest,omitempty"`
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

	h.writeJSON(ctx, w, http.StatusAccepted, startRestoreResponse{ID: id.String()})
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

// restoreAuditObject names one restore for its audit entry, with whether the
// access tables were replaced or kept.
func restoreAuditObject(restoreID, backupID, accessMode string) string {
	return "restore " + restoreID + " of backup " + backupID + " (access tables " + accessMode + ")"
}

// parseStartRestoreRequest decodes the start body: the whole body must be one
// JSON value with a backup ID and no other field. The target is never a
// request field (ADR 30 decision 9): an unknown field such as target answers
// 400 instead of being silently ignored.
func parseStartRestoreRequest(w http.ResponseWriter, r *http.Request) (startRestoreRequest, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRestoreBodyBytes))
	if err != nil {
		return startRestoreRequest{}, errInvalidBody
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
		Manifest:    nil,
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
	}

	if record.PreBackupID != "" {
		doc.PreBackupID = record.PreBackupID.String()
	}

	if !record.EndedAt.IsZero() {
		doc.EndedAt = formatBackupTime(record.EndedAt)
	}

	return doc
}
