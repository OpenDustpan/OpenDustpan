package server

// Backup routes of the installation API (ADR 30 decision 9): start a backup,
// list the backups this installation made, and read one backup with its
// manifest.
//
// POST /api/server/backups starts a backup in the background and answers 202
// with the ID. Only one backup runs at a time; a second start answers 409.
// GET /api/server/backups lists the rows of the backups table, newest first.
// GET /api/server/backups/{id} answers the state (running, succeeded or
// failed) and, when the backup succeeded, the manifest.
//
// All three need the action server:backup on the installation (@server). The
// target comes only from the backup service the server was wired with: no
// request field, header or query value sets it.

import (
	"errors"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/identity"
)

// errNoBackupTarget reports a backup route on a server with no backup target.
// The target is a server setting (--backup-target), so without it no backup
// can run.
var errNoBackupTarget = errors.New("no backup target is configured")

// backupIDPrefix prefixes every backup ID the routes mint.
const backupIDPrefix = "bkp-"

// startBackupResponse answers a started backup: its ID. The backup runs in
// the background; poll GET /api/server/backups/{id} until it leaves running.
type startBackupResponse struct {
	ID string `json:"id"`
}

// backupSummaryJSON is one row of the backups table: the backups this
// installation made. Times are UTC in RFC 3339 with nanoseconds. EndedAt and
// Error stay empty while the backup runs.
type backupSummaryJSON struct {
	ID        string `json:"id"`
	State     string `json:"state"`
	StartedAt string `json:"startedAt"`
	EndedAt   string `json:"endedAt,omitempty"`
	Target    string `json:"target"`
	Error     string `json:"error,omitempty"`
}

// listBackupsResponse lists the backups this installation made, newest first.
type listBackupsResponse struct {
	Backups []backupSummaryJSON `json:"backups"`
}

// getBackupResponse answers one backup: its row and, when the backup
// succeeded, the manifest the backup wrote last.
type getBackupResponse struct {
	ID        string           `json:"id"`
	State     string           `json:"state"`
	StartedAt string           `json:"startedAt"`
	EndedAt   string           `json:"endedAt,omitempty"`
	Target    string           `json:"target"`
	Error     string           `json:"error,omitempty"`
	Manifest  *backup.Manifest `json:"manifest,omitempty"`
}

// startBackup starts a backup in the background and answers 202 with its ID.
// A second start while one runs answers 409.
func (h *Handler) startBackup(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	if h.backups == nil {
		h.writeError(ctx, w, errNoBackupTarget)

		return
	}

	id := backup.ID(ident.NewID(backupIDPrefix))

	if err := h.backups.Start(ctx, id); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusAccepted, startBackupResponse{ID: id.String()})
}

// listBackups answers every backup row, newest first.
func (h *Handler) listBackups(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	if h.backups == nil {
		h.writeError(ctx, w, errNoBackupTarget)

		return
	}

	records, err := h.backups.List(ctx)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	summaries := make([]backupSummaryJSON, 0, len(records))
	for _, record := range records {
		summaries = append(summaries, summarizeBackup(record))
	}

	h.writeJSON(ctx, w, http.StatusOK, listBackupsResponse{Backups: summaries})
}

// getBackup answers one backup: its state and, when the backup succeeded,
// the manifest. A missing backup answers 404.
func (h *Handler) getBackup(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	if h.backups == nil {
		h.writeError(ctx, w, errNoBackupTarget)

		return
	}

	id := backup.ID(r.PathValue("id"))

	record, err := h.backups.Get(ctx, id)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	doc := getBackupResponse{
		ID:        record.ID.String(),
		State:     string(record.State),
		StartedAt: formatBackupTime(record.StartedAt),
		EndedAt:   "",
		Target:    record.Target,
		Error:     record.Error,
		Manifest:  nil,
	}

	if !record.EndedAt.IsZero() {
		doc.EndedAt = formatBackupTime(record.EndedAt)
	}

	if record.State != backup.StateSucceeded {
		h.writeJSON(ctx, w, http.StatusOK, doc)

		return
	}

	manifest, err := h.backups.ReadManifest(ctx, id)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	doc.Manifest = &manifest

	h.writeJSON(ctx, w, http.StatusOK, doc)
}

// summarizeBackup renders one backup row for the list.
func summarizeBackup(record backup.Record) backupSummaryJSON {
	doc := backupSummaryJSON{
		ID:        record.ID.String(),
		State:     string(record.State),
		StartedAt: formatBackupTime(record.StartedAt),
		EndedAt:   "",
		Target:    record.Target,
		Error:     record.Error,
	}

	if !record.EndedAt.IsZero() {
		doc.EndedAt = formatBackupTime(record.EndedAt)
	}

	return doc
}

// formatBackupTime answers t in UTC in RFC 3339 with nanoseconds, the wire
// form of backup times.
func formatBackupTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}
