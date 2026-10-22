package inventory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// channelHeader names the request header that says where a request came
// from, for the audit entry (ADR 60).
const channelHeader = "Dustpan-Channel"

// maxRestoreBody bounds the body of a restore request: one short name.
const maxRestoreBody = 4096

// maxPurgeWithinDays bounds the purgeWithinDays query value: ten years.
const maxPurgeWithinDays = 3650

// day is the length of one day for the purgeWithinDays query value.
const day = 24 * time.Hour

// errInvalidQuery reports a deleted-units query value that does not parse.
var errInvalidQuery = errors.New("invalid query parameter")

// errInvalidBody reports a restore body that is not one JSON object with an
// optional name.
var errInvalidBody = errors.New("invalid request body: send {} or {\"name\": \"<new name>\"}")

// errUnknownTool reports a deleted-unit path with a tool that the server
// does not serve. classify maps it to 404, as an unknown unit.
var errUnknownTool = errors.New("unknown tool")

// deletedUnitEntry is one deleted unit as the console reads it (ADR 47). Tool
// is the tool label, Reference the unit reference under its name before the
// delete, Name the tool's own name of the unit at the time of the delete, and
// UnitID its stable ID, which the restore and purge routes take. DeletedAt
// and PurgeAfter are RFC 3339 times in UTC, as the other tool-neutral APIs
// write times. PurgeAfter is the end of the retention period: the retention
// sweep purges the unit after it.
//
// Contract: never remove or rename these fields.
type deletedUnitEntry struct {
	Tool       string `json:"tool"`
	Reference  string `json:"reference"`
	Namespace  string `json:"namespace"`
	Name       string `json:"name"`
	UnitID     string `json:"unitID"`
	DeletedAt  string `json:"deletedAt"`
	PurgeAfter string `json:"purgeAfter"`
}

// listDeletedUnitsResponse is one page of deleted units. Total counts every
// deleted unit of the namespace that matches the filters and that the caller
// may read. NextPageToken is omitted on the last page.
//
// Contract: never remove or rename these fields.
type listDeletedUnitsResponse struct {
	NextPageToken string             `json:"nextPageToken,omitempty"`
	Units         []deletedUnitEntry `json:"units"`
	Total         int                `json:"total"`
}

// restoredUnitResponse is the live unit after a restore.
//
// Contract: never remove or rename these fields.
type restoredUnitResponse struct {
	Tool      string `json:"tool"`
	Reference string `json:"reference"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	UnitID    string `json:"unitID"`
}

// restoreRequest is the optional body of a restore: a new name of the unit.
// For a Pulumi stack it is the stack name; the stack keeps its project.
// Collections (#478) will add a collection field here, for a unit whose
// collection is gone; see inventory.Lifecycle.
type restoreRequest struct {
	Name string `json:"name"`
}

// cursorWire is the opaque cursor of the deleted-units list, as base64url
// JSON. Clients pass it back unchanged.
type cursorWire struct {
	Tool   string `json:"t"`
	Name   string `json:"n"`
	UnitID string `json:"u"`
	// Fingerprint is a digest of the namespace and the filters of the query
	// that made the cursor, so a cursor works only for that query.
	Fingerprint string `json:"f"`
}

// DeletedReadScope answers which deleted units of ns the principal p may
// read (unit:read): every unit when all is true, else the units with the
// stable IDs in ids. The list filters the store query with it, so the page,
// the cursor and the total hold no unit that p may not read.
type DeletedReadScope interface {
	ReadableUnits(ctx context.Context, p identity.Principal, ns namespace.Name) (all bool, ids []string, err error)
}

// toolCode maps a tool label or code from a query to the stored code.
func toolCode(s string) string {
	switch s {
	case "Pulumi":
		return "pulumi"
	case "OpenTofu":
		return "opentofu"
	default:
		return s
	}
}

// toDeletedUnitEntry maps one deleted entry to its wire shape.
func toDeletedUnitEntry(e coreinventory.DeletedEntry) deletedUnitEntry {
	return deletedUnitEntry{
		Tool:       toolLabel(e.Tool),
		Reference:  e.Tool + ":" + e.Namespace.String() + "/" + e.Name,
		Namespace:  e.Namespace.String(),
		Name:       e.Name,
		UnitID:     e.UnitID,
		DeletedAt:  formatTime(e.DeletedAt),
		PurgeAfter: formatTime(e.PurgeAfter),
	}
}

// formatTime writes t as an RFC 3339 time in UTC.
func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// encodeCursor returns the opaque form of c for the query with the
// fingerprint fingerprint, or "" for no cursor.
func encodeCursor(c *coreinventory.DeletedCursor, fingerprint string) string {
	if c == nil {
		return ""
	}

	raw, err := json.Marshal(cursorWire{Tool: c.Tool, Name: c.Name, UnitID: c.UnitID, Fingerprint: fingerprint})
	if err != nil {
		return ""
	}

	return base64.RawURLEncoding.EncodeToString(raw)
}

// decodeCursor parses an opaque cursor of the query with the fingerprint
// fingerprint. An empty value is no cursor. A cursor that another query made
// gives errPageToken.
func decodeCursor(s, fingerprint string) (*coreinventory.DeletedCursor, error) {
	if s == "" {
		return nil, nil //nolint:nilnil // No cursor is a valid answer: the first page.
	}

	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("%w: cursor", errInvalidQuery)
	}

	var w cursorWire
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, fmt.Errorf("%w: cursor", errInvalidQuery)
	}

	if w.Fingerprint != fingerprint {
		return nil, errPageToken
	}

	return &coreinventory.DeletedCursor{Tool: w.Tool, Name: w.Name, UnitID: w.UnitID}, nil
}

// intQuery parses the query value name as an int from low to limit. An empty
// value is 0.
func intQuery(r *http.Request, name string, low, limit int) (int, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, nil
	}

	n, err := strconv.Atoi(raw)
	if err != nil || n < low || n > limit {
		return 0, fmt.Errorf("%w: %s must be a number from %d to %d", errInvalidQuery, name, low, limit)
	}

	return n, nil
}

// deletedFingerprint returns a short digest of the namespace and the filters
// of a list of deleted units, so a page token works only for the query that
// made it. purgeWithin is the purge window in days, or "" for none. The page
// size can change from page to page, so it is not part of the digest.
func deletedFingerprint(ns namespace.Name, tool, namePrefix, purgeWithin string) string {
	parts := []string{"ns=" + ns.String(), "tool=" + tool, "namePrefix=" + namePrefix}
	if purgeWithin != "" {
		parts = append(parts, "purgeWithinDays="+purgeWithin)
	}

	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))

	return hex.EncodeToString(sum[:8])
}

// deletedQuery builds the store query of a list request for ns and the
// fingerprint of its filters. It does not set the read scope.
func (h *Handler) deletedQuery(r *http.Request, ns namespace.Name) (coreinventory.DeletedQuery, string, error) {
	limit, err := intQuery(r, "pageSize", 1, coreinventory.MaxDeletedPageSize)
	if err != nil {
		return coreinventory.DeletedQuery{}, "", err
	}

	days, err := intQuery(r, "purgeWithinDays", 0, maxPurgeWithinDays)
	if err != nil {
		return coreinventory.DeletedQuery{}, "", err
	}

	tool, prefix := toolCode(r.URL.Query().Get("tool")), r.URL.Query().Get("namePrefix")
	purgeWithin := r.URL.Query().Has("purgeWithinDays")

	window := ""
	if purgeWithin {
		window = strconv.Itoa(days)
	}

	fingerprint := deletedFingerprint(ns, tool, prefix, window)

	cursor, err := decodeCursor(r.URL.Query().Get("pageToken"), fingerprint)
	if err != nil {
		return coreinventory.DeletedQuery{}, "", err
	}

	var before time.Time
	if purgeWithin {
		before = h.now().Add(time.Duration(days) * day)
	}

	return coreinventory.DeletedQuery{
		PurgeBefore: before,
		After:       cursor,
		Namespace:   ns,
		Tool:        tool,
		NamePrefix:  prefix,
		UnitIDs:     nil,
		Limit:       limit,
		Restricted:  false,
	}, fingerprint, nil
}

// listDeletedUnits answers GET /api/dustpan/v1/namespaces/{namespace}/deleted-units with one page of
// the deleted units of the namespace (ADR 47). The route wrapper already
// allowed namespace:read on the namespace. The store query holds only the
// units that the caller may read, so the page, the cursor and the total
// never show another unit. The query values tool, namePrefix,
// purgeWithinDays, pageSize and pageToken filter and page the list.
func (h *Handler) listDeletedUnits(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()
	ns := obj.Namespace()

	if _, err := h.namespaces.Get(ctx, ns); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	q, fingerprint, err := h.deletedQuery(r, ns)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	all, ids, err := h.readScope.ReadableUnits(ctx, p, ns)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if !all {
		q.Restricted = true
		q.UnitIDs = ids
	}

	page, err := h.deleted.QueryDeletedUnits(ctx, q)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	units := make([]deletedUnitEntry, 0, len(page.Entries))
	for i := range page.Entries {
		units = append(units, toDeletedUnitEntry(page.Entries[i]))
	}

	h.writeJSON(ctx, w, http.StatusOK, listDeletedUnitsResponse{
		NextPageToken: encodeCursor(page.Next, fingerprint), Units: units, Total: page.Total,
	})
}

// deletedObject is the unit reference of a deleted unit under its name before
// the delete, with its stable ID. The ID keeps the grants of a new unit with
// the same name away from the deleted unit.
func deletedObject(e coreinventory.DeletedEntry) (identity.Object, error) {
	obj, err := identity.UnitObject(e.Tool, e.Namespace, e.Name)
	if err != nil {
		return identity.Object{}, fmt.Errorf("unit reference: %w", err)
	}

	return obj.WithUnitID(e.UnitID), nil
}

// resolveDeletedUnit returns the unit reference of the deleted unit in the
// path: the tool and unit ID in the path and the namespace in the
// organization query value. An unknown tool answers 404. An unknown unit
// resolves to a reference with the unit ID as its name, so the route
// authorizes it before the handler finds that it does not exist: a caller
// without the action gets 403 for an unknown unit and for a forbidden one
// alike, and only a caller with the action learns that it is unknown (404).
func (h *Handler) resolveDeletedUnit(_ http.ResponseWriter, r *http.Request) (identity.Object, error) {
	nsObj, err := resolveListUnits(nil, r)
	if err != nil {
		return identity.Object{}, err
	}

	tool := r.PathValue("tool")
	if life, ok := h.lifecycles[tool]; !ok || life == nil {
		return identity.Object{}, unknownTool(tool)
	}

	unitID := r.PathValue("unitID")

	e, err := h.deleted.GetDeletedUnit(r.Context(), nsObj.Namespace(), tool, unitID)
	if errors.Is(err, coreinventory.ErrNotFound) {
		obj, objErr := identity.UnitObject(tool, nsObj.Namespace(), unitID)
		if objErr != nil {
			return identity.Object{}, fmt.Errorf("unit reference: %w", objErr)
		}

		return obj.WithUnitID(unitID), nil
	}

	if err != nil {
		return identity.Object{}, fmt.Errorf("deleted unit: %w", err)
	}

	return deletedObject(e)
}

// unknownTool is the error of a tool that the server does not serve. It
// answers 404, as an unknown unit.
func unknownTool(tool string) error {
	return fmt.Errorf("%w: %w %q", coreinventory.ErrNotFound, errUnknownTool, tool)
}

// restoreUnit answers POST /api/inventory/deleted-units/{tool}/{unitID}/restore
// (ADR 47). The body is {} or {"name": "<new name>"}. It answers 200 with
// the live unit, 409 when a live unit holds the name, and 404 for an unknown
// or purged unit. The tool writes the audit entry in the restore transaction.
func (h *Handler) restoreUnit(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	req, err := readRestoreRequest(r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	life, ok := h.lifecycles[obj.Tool()]
	if !ok || life == nil {
		h.writeError(ctx, w, unknownTool(obj.Tool()))

		return
	}

	if !h.deletedUnitExists(w, r, obj) {
		return
	}

	if !h.guardStateWrite(w, r, p, obj, stateCheck{action: identity.ActionUnitRestore, op: gate.OperationRollback}) {
		return
	}

	unitID := obj.UnitID()
	entry := changeEntry(r, p, obj, identity.ActionUnitRestore)

	name, err := life.Restore(ctx, obj.Namespace(), unitID, req.Name, entry)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, restoredUnitResponse{
		Tool:      toolLabel(obj.Tool()),
		Reference: obj.Tool() + ":" + obj.Namespace().String() + "/" + name,
		Namespace: obj.Namespace().String(),
		Name:      name,
		UnitID:    unitID,
	})
}

// deletedUnitExists answers 404 and returns false for a unit that is not a
// deleted unit, so that an unknown or purged unit answers as the tool would
// answer. The state guard fails closed for such a unit too: the check of the
// guard does not rely on this one.
func (h *Handler) deletedUnitExists(w http.ResponseWriter, r *http.Request, obj identity.Object) bool {
	ctx := r.Context()

	_, err := h.deleted.GetDeletedUnit(ctx, obj.Namespace(), obj.Tool(), obj.UnitID())
	if err != nil {
		h.writeError(ctx, w, err)

		return false
	}

	return true
}

// readRestoreRequest reads the optional body of a restore. An empty body is
// a restore under the name before the delete. A body with another field, or
// with more than one JSON value, answers 400.
func readRestoreRequest(r *http.Request) (restoreRequest, error) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxRestoreBody+1))
	if err != nil || len(raw) > maxRestoreBody {
		return restoreRequest{}, errInvalidBody
	}

	var req restoreRequest
	if len(raw) == 0 {
		return req, nil
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&req); err != nil || dec.More() {
		return restoreRequest{}, errInvalidBody
	}

	return req, nil
}

// purgeUnit answers DELETE /api/inventory/deleted-units/{tool}/{unitID}: the
// final delete of a deleted unit before its retention period ends (ADR 47).
// It answers 204, and 404 for a live, unknown or purged unit. The tool
// writes the audit entry in the purge transaction.
func (h *Handler) purgeUnit(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	life, ok := h.lifecycles[obj.Tool()]
	if !ok || life == nil {
		h.writeError(ctx, w, unknownTool(obj.Tool()))

		return
	}

	if !h.deletedUnitExists(w, r, obj) {
		return
	}

	if !h.guardStateWrite(w, r, p, obj, stateCheck{action: identity.ActionUnitPurge, op: gate.OperationDelete}) {
		return
	}

	entry := changeEntry(r, p, obj, identity.ActionUnitPurge)

	if err := life.Purge(ctx, obj.Namespace(), obj.UnitID(), entry); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// changeEntry returns the audit entry of the kind change for a restore or a
// purge of the unit obj (ADR 60, decision 4). The tool appends it in the
// transaction of the change and stamps the time.
func changeEntry(r *http.Request, p identity.Principal, obj identity.Object, action identity.Action) audit.Entry {
	return audit.Entry{
		At:            time.Time{},
		Namespace:     obj.Namespace(),
		Subject:       p.Subject,
		IDPSubject:    "",
		TokenID:       "",
		Action:        string(action),
		Object:        obj.String(),
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      clientip.String(r),
		UserAgent:     truncateUserAgent(r.UserAgent()),
		Channel:       sessionauth.AuditChannel(r, channelHeader),
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     requestid.FromContext(r.Context()),
		ObjectID:      obj.UnitID(),
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

// truncateUserAgent caps s at audit.MaxUserAgentLen bytes, at a rune
// boundary.
func truncateUserAgent(s string) string {
	if len(s) <= audit.MaxUserAgentLen {
		return s
	}

	cut := audit.MaxUserAgentLen
	for cut > 0 && !utf8RuneStart(s[cut]) {
		cut--
	}

	return s[:cut]
}

// utf8RuneStart reports whether b starts a UTF-8 sequence.
func utf8RuneStart(b byte) bool {
	const continuationMask, continuationBits = 0xC0, 0x80

	return b&continuationMask != continuationBits
}
