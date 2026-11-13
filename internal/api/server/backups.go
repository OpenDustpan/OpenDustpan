package server

// Backup routes of the installation API (ADR 30 decision 9): start a backup,
// list the backups this installation made, read one backup with its
// manifest, and check a backup in the configured target.
//
// POST /api/server/backups starts a backup in the background and answers 202
// with the ID. Only one backup runs at a time; a second start answers 409.
// GET /api/server/backups lists the rows of the backups table, newest first.
// GET /api/server/backups/{id} answers the state (running, succeeded or
// failed) and, when the backup succeeded, the manifest.
//
// All four need the action server:backup on the installation (@server). The
// target comes only from the backup service the server was wired with: no
// request field, header or query value sets it.

import (
	"errors"
	"net/http"
	"time"

	denials "opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/errcode"
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
// succeeded, the manifest the backup wrote last. UnverifiedKeyLimit is the
// bound on the unverified keys of a backup as this server applies it.
// UnverifiedKeyCount is the count of the unverified keys of the manifest, and
// 0 for a backup without one.
type getBackupResponse struct {
	ID                 string           `json:"id"`
	State              string           `json:"state"`
	StartedAt          string           `json:"startedAt"`
	EndedAt            string           `json:"endedAt,omitempty"`
	Target             string           `json:"target"`
	Error              string           `json:"error,omitempty"`
	Manifest           *backup.Manifest `json:"manifest,omitempty"`
	UnverifiedKeyLimit int              `json:"unverifiedKeyLimit"`
	UnverifiedKeyCount int              `json:"unverifiedKeyCount"`
}

// startBackup starts a backup in the background and answers 202 with its ID.
// A second start while one runs answers 409.
func (h *Handler) startBackup(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	if h.backups == nil {
		h.writeError(ctx, w, errNoBackupTarget)

		return
	}

	if !h.admitStart(ctx, w) {
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
		ID:                 record.ID.String(),
		State:              string(record.State),
		StartedAt:          formatBackupTime(record.StartedAt),
		EndedAt:            "",
		Target:             record.Target,
		Error:              record.Error,
		Manifest:           nil,
		UnverifiedKeyLimit: h.backups.UnverifiedKeyLimit(),
		UnverifiedKeyCount: 0,
	}

	if !record.EndedAt.IsZero() {
		doc.EndedAt = formatBackupTime(record.EndedAt)
	}

	if record.State != backup.StateSucceeded {
		h.writeJSON(ctx, w, http.StatusOK, doc)

		return
	}

	manifest, err := h.backups.ReadManifest(ctx, id)
	if errors.Is(err, backup.ErrCorrupt) {
		// The stored manifest is missing, cut or of another format. The
		// answer gives the cause that the check route gives, so the caller
		// can act without a second request.
		h.writeCoded(ctx, w, failCoded(http.StatusConflict, denials.Clean(err.Error(), maxCauseBytes),
			errcode.BackupCorrupt, errcode.Identifier("backup", id.String())))

		return
	}

	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	doc.Manifest = &manifest
	doc.UnverifiedKeyCount = len(manifest.Unverified)

	h.writeJSON(ctx, w, http.StatusOK, doc)
}

// maxCauseBytes is the largest length, in bytes, of the cause of a corrupt
// backup in an answer.
const maxCauseBytes = 512

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

// checkBackupResponse answers the backup check: whether the configured
// target holds every object the manifest names with the recorded size.
// Complete is false with the first missing or short object in Error.
type checkBackupResponse struct {
	ID       string `json:"id"`
	Complete bool   `json:"complete"`
	Error    string `json:"error,omitempty"`
}

// checkBackup checks the backup in the configured target and answers the
// result. The server reads the target, not the command (ADR 39). A complete
// backup answers 200 with complete true. A backup the target holds
// incompletely answers 200 with complete false and the first missing or
// short object by key, so the command prints it and exits 1. An ID with no
// backup row answers 404 like GET /api/server/backups/{id}: callers can tell
// unknown from corrupt. A malformed ID answers 400. A storage failure
// answers 500.
func (h *Handler) checkBackup(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	if h.backups == nil {
		h.writeError(ctx, w, errNoBackupTarget)

		return
	}

	id, err := backup.ParseID(r.PathValue("id"))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if _, err := h.backups.Get(ctx, id); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if err := h.backups.Check(ctx, id); err != nil {
		if errors.Is(err, backup.ErrCorrupt) {
			h.writeJSON(ctx, w, http.StatusOK, checkBackupResponse{
				ID:       id.String(),
				Complete: false,
				Error:    err.Error(),
			})

			return
		}

		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, checkBackupResponse{ID: id.String(), Complete: true, Error: ""})
}
