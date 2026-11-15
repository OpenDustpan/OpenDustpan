package tfe

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

const (
	// stateVersionOutputType is the JSON:API type of state version outputs.
	stateVersionOutputType = "state-version-outputs"
	// outputIDSeparator joins the state version ID and the output name in a
	// state version output ID. Neither side holds it: version IDs come from
	// ident and output names are Terraform identifiers.
	outputIDSeparator = "."
	// linkSelf is the self link of a resource.
	linkSelf = "self"
	// legacyTypeString, legacyTypeArray and legacyTypeObject are the coarse
	// type names of the legacy TFE type attribute of an output.
	legacyTypeString = "string"
	legacyTypeArray  = "array"
	legacyTypeObject = "object"
)

// stateVersionAttributes are the attributes of a state version document: the
// serial, lineage, MD5 and size the client declared at create, the status,
// the absolute download URL and, only on the create response, the absolute
// upload URLs of the capability the content follows through. Holder names who
// held the lock when the version was declared, force marks a forced version,
// restored-from names the version a restore copied, empty otherwise,
// fence-by names the subject of the force-unlock that wrote the version,
// empty otherwise, and unlocked-write marks a version declared without the
// lock.
type stateVersionAttributes struct {
	CreatedAt          time.Time             `json:"created-at"`                             //nolint:tagliatelle // The TFE wire name; it must not change.
	DownloadURL        string                `json:"hosted-state-download-url"`              //nolint:tagliatelle // The TFE wire name; it must not change.
	UploadURL          string                `json:"hosted-state-upload-url,omitempty"`      //nolint:tagliatelle // The TFE wire name; it must not change.
	JSONUploadURL      string                `json:"hosted-json-state-upload-url,omitempty"` //nolint:tagliatelle // The TFE wire name; it must not change.
	Status             workspace.StateStatus `json:"status"`
	Serial             int64                 `json:"serial"`
	Lineage            string                `json:"lineage"`
	MD5                string                `json:"md5"`
	Size               int64                 `json:"size"`
	ResourcesProcessed bool                  `json:"resources-processed"` //nolint:tagliatelle // The TFE wire name; it must not change.
	TerraformVersion   string                `json:"terraform-version"`   //nolint:tagliatelle // The TFE wire name; it must not change.
	Holder             string                `json:"holder"`
	Force              bool                  `json:"force"`
	RestoredFrom       string                `json:"restored-from"`  //nolint:tagliatelle // The TFE wire name; it must not change.
	FenceBy            string                `json:"fence-by"`       //nolint:tagliatelle // The TFE wire name; it must not change.
	UnlockedWrite      bool                  `json:"unlocked-write"` //nolint:tagliatelle // The TFE wire name; it must not change.
}

// relationshipList is one JSON:API relationship with several targets: the
// outputs of a state version.
type relationshipList struct {
	Data []relationshipRef `json:"data"`
}

// stateVersionResource is one state version as a JSON:API resource.
type stateVersionResource struct {
	ID            string                      `json:"id"`
	Type          string                      `json:"type"`
	Attributes    stateVersionAttributes      `json:"attributes"`
	Relationships map[string]relationshipList `json:"relationships"`
	Links         map[string]string           `json:"links,omitempty"`
}

// stateVersionDocument is the response of a state version route.
type stateVersionDocument struct {
	Data stateVersionResource `json:"data"`
}

// restoreMeta holds the message of a restore response: a restore writes old
// state as a new version and changes no infrastructure.
type restoreMeta struct {
	Message string `json:"message"`
}

// restoreStateVersionDocument is the response of the restore route: the new
// state version and the message that no infrastructure changed.
type restoreStateVersionDocument struct {
	Data stateVersionResource `json:"data"`
	Meta restoreMeta          `json:"meta"`
}

// restoreMessage tells the client that a restore writes old state as a new
// version and changes no infrastructure.
const restoreMessage = "Restore writes old state as a new version. It changes no infrastructure."

// stateVersionListDocument is the response of the state version list route:
// one page of finalized versions, newest first, with the meta.pagination
// object the client reads.
type stateVersionListDocument struct {
	Data []stateVersionResource `json:"data"`
	Meta workspaceListMeta      `json:"meta"`
}

// stateVersionOutputAttributes are the attributes of one output: the name,
// sensitivity, legacy type, value and detailed type the client sent at
// create. The CLI decodes the value against the detailed type and falls back
// to the full state when the detailed type is missing.
type stateVersionOutputAttributes struct {
	Name         string `json:"name"`
	Sensitive    bool   `json:"sensitive"`
	Type         string `json:"type"`
	Value        any    `json:"value"`
	DetailedType any    `json:"detailed-type"` //nolint:tagliatelle // The TFE wire name; it must not change.
}

// stateVersionOutputResource is one output as a JSON:API resource.
type stateVersionOutputResource struct {
	ID         string                       `json:"id"`
	Type       string                       `json:"type"`
	Attributes stateVersionOutputAttributes `json:"attributes"`
	Links      map[string]string            `json:"links,omitempty"`
}

// stateVersionOutputListDocument is the response of an outputs route: every
// output on one page, so the page holds the whole list.
type stateVersionOutputListDocument struct {
	Data []stateVersionOutputResource `json:"data"`
	Meta workspaceListMeta            `json:"meta"`
}

// stateVersionOutputDocument is the response of a single output route.
type stateVersionOutputDocument struct {
	Data stateVersionOutputResource `json:"data"`
}

// storedOutput is one entry of the stored outputs document.
type storedOutput = workspace.StoredOutput

// outputID mints the ID of the output called name of sv.
func outputID(sv workspace.StateVersion, name string) string {
	return sv.ID.String() + outputIDSeparator + name
}

// parseOutputID splits id into its state version ID and output name. An ID
// without both sides fails with workspace.ErrStateVersionNotFound, so the
// route answers 404 without saying which side is wrong.
func parseOutputID(id string) (workspace.StateVersionID, string, error) {
	version, name, ok := strings.Cut(id, outputIDSeparator)
	if !ok || version == "" || name == "" {
		return "", "", fmt.Errorf("%w: %q", workspace.ErrStateVersionNotFound, id)
	}

	return workspace.StateVersionID(version), name, nil
}

// parseStoredOutputs reads the stored outputs document into the map of
// outputs by name. It accepts the flat map the CLI sends and the wrapped
// form with an "outputs" key. Anything else fails.
func parseStoredOutputs(raw []byte) (map[string]storedOutput, error) {
	return workspace.ParseStoredOutputs(raw)
}

// stateOutputs renders the stored outputs of sv as TFE output resources,
// ordered by name, with the values of sensitive outputs kept. The resources
// are shared: callers that serve the list must nil sensitive values, and only
// the single output route serves them. A version without outputs renders an
// empty list. A document that is not valid JSON fails, as the stored data is corrupt.
func stateOutputs(sv workspace.StateVersion) ([]stateVersionOutputResource, error) {
	if len(sv.Outputs) == 0 {
		return []stateVersionOutputResource{}, nil
	}

	outputs, err := parseStoredOutputs(sv.Outputs)
	if err != nil {
		return nil, fmt.Errorf("decode outputs of state %s: %w", sv.ID, err)
	}

	out := make([]stateVersionOutputResource, 0, len(outputs))

	for _, name := range slices.Sorted(maps.Keys(outputs)) {
		stored := outputs[name]

		value, err := rawValue(stored.Value)
		if err != nil {
			return nil, fmt.Errorf("decode output %q of state %s: %w", name, sv.ID, err)
		}

		detailed, err := rawValue(stored.Type)
		if err != nil {
			return nil, fmt.Errorf("decode type of output %q of state %s: %w", name, sv.ID, err)
		}

		id := outputID(sv, name)

		out = append(out, stateVersionOutputResource{
			ID:   id,
			Type: stateVersionOutputType,
			Attributes: stateVersionOutputAttributes{
				Name:         name,
				Sensitive:    stored.Sensitive,
				Type:         legacyOutputType(value, detailed),
				Value:        value,
				DetailedType: detailed,
			},
			Links: map[string]string{linkSelf: "/api/v2/state-version-outputs/" + id},
		})
	}

	return out, nil
}

// rawValue decodes raw JSON into an any for a response document. Empty input
// decodes as null.
func rawValue(raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		raw = json.RawMessage("null")
	}

	var value any

	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("decode JSON value: %w", err)
	}

	return value, nil
}

// legacyOutputType answers the coarse TFE type attribute of an output. The
// CLI ignores it and decodes the value against the detailed type instead, so
// this is a hint for the console only. A known detailed type decides;
// otherwise the JSON shape of the value does.
func legacyOutputType(value, detailed any) string {
	if name := coarseName(detailed); name != "" {
		return name
	}

	switch value.(type) {
	case string:
		return legacyTypeString
	case bool:
		return "bool"
	case float64:
		return "number"
	case []any:
		return legacyTypeArray
	case map[string]any:
		return legacyTypeObject
	default:
		return legacyTypeString
	}
}

// coarseName maps a cty type in JSON form to its coarse name: primitives keep
// their name, lists, sets and tuples are arrays, maps and objects are
// objects. Anything else answers "", so the caller falls back to the value.
func coarseName(detailed any) string {
	switch shape := detailed.(type) {
	case string:
		switch shape {
		case legacyTypeString, "number", "bool":
			return shape
		default:
			return ""
		}
	case []any:
		if len(shape) == 0 {
			return ""
		}

		kind, ok := shape[0].(string)
		if !ok {
			return ""
		}

		switch kind {
		case "list", "set", "tuple":
			return legacyTypeArray
		case "map", "object":
			return legacyTypeObject
		default:
			return ""
		}
	default:
		return ""
	}
}

// stateVersionDocument renders sv as a JSON:API resource: the serial,
// lineage, MD5 and size the client declared at create, the status, the
// absolute download URL and the outputs relationship.
func (h *Handler) stateVersionDocument(
	r *http.Request, sv workspace.StateVersion,
) (stateVersionResource, error) {
	outputs, err := stateOutputs(sv)
	if err != nil {
		return stateVersionResource{}, err
	}

	refs := make([]relationshipRef, 0, len(outputs))
	for _, output := range outputs {
		refs = append(refs, relationshipRef{ID: output.ID, Type: stateVersionOutputType})
	}

	return stateVersionResource{
		ID:   sv.ID.String(),
		Type: stateVersionType,
		Attributes: stateVersionAttributes{
			CreatedAt:          sv.CreatedAt,
			DownloadURL:        h.ExternalBaseURL(r) + "/api/v2/state-versions/" + sv.ID.String() + "/download",
			UploadURL:          "",
			JSONUploadURL:      "",
			Status:             sv.Status,
			Serial:             sv.Serial,
			Lineage:            sv.Lineage,
			MD5:                hex.EncodeToString(sv.MD5[:]),
			Size:               sv.Size,
			ResourcesProcessed: true,
			TerraformVersion:   "",
			Holder:             sv.Holder,
			Force:              sv.Force,
			RestoredFrom:       sv.RestoredFrom.String(),
			FenceBy:            sv.FenceBy,
			UnlockedWrite:      sv.UnlockedWrite,
		},
		Relationships: map[string]relationshipList{"outputs": {Data: refs}},
		Links:         map[string]string{linkSelf: "/api/v2/state-versions/" + sv.ID.String()},
	}, nil
}

// readCurrentState serves GET /api/v2/workspaces/{id}/current-state-version.
// A workspace without state answers 404 as a JSON:API error.
func (h *Handler) readCurrentState(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	sv, err := h.workspaces.Current(ctx, workspace.ID(r.PathValue("id")))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeStateVersion(ctx, w, r, sv)
}

// readStateVersion serves GET /api/v2/state-versions/{state_version_id}. An
// unknown ID and a version that is not finalized answer 404.
func (h *Handler) readStateVersion(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	sv, err := h.workspaces.ReadState(ctx, workspace.StateVersionID(r.PathValue("state_version_id")))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeStateVersion(ctx, w, r, sv)
}

// downloadState serves GET
// /api/v2/state-versions/{state_version_id}/download. It streams the state
// content. The CLI sends its token, so this is a normal authorized route,
// not a capability. It writes one audit entry before it sends a byte, of the
// kind reveal for a person and tool-operation for any other caller. A failed
// append answers 500 with no content, so a download without its entry never
// reaches the CLI.
func (h *Handler) downloadState(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	sv, err := h.workspaces.ReadState(ctx, workspace.StateVersionID(r.PathValue("state_version_id")))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	content, err := h.workspaces.Open(ctx, sv)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	defer func() {
		if err := content.Close(); err != nil {
			h.logger.WarnContext(ctx, "close state content failed", slog.String("error", err.Error()))
		}
	}()

	if err := h.appendDownloadAudit(r, p, obj, sv.ID.String()); err != nil {
		h.failAudit(ctx)
		h.writeInternal(ctx, w)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", noStoreValue)
	w.WriteHeader(http.StatusOK)

	if _, err := io.Copy(w, content); err != nil {
		h.logger.WarnContext(ctx, "write response failed", slog.String("error", err.Error()))
	}
}

// restoreStateVersion serves POST
// /api/v2/state-versions/{state_version_id}/actions/restore. It makes the
// finalized version current again as a new, forced version and answers 200
// with the new version and the message that no infrastructure changed. An
// unknown ID and a version that is not finalized answer 404. A lock that
// another principal holds answers 409 with a detail holding "is locked by
// User" and the holder. A success writes one info log line with the action,
// the unit and the principal and an empty released value, and never with the
// request path or query (ADR 28 decision 10, ADR 32 decision 7). The handler
// owns the logger through its Config.
func (h *Handler) restoreStateVersion(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()
	from := workspace.StateVersionID(r.PathValue("state_version_id"))

	src, err := h.workspaces.ReadState(ctx, from)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	expect, ok := h.guardRestore(w, r, p, obj, src)
	if !ok {
		return
	}

	restored, err := h.restoreVersion(ctx, src.Workspace, from, p.Subject, expect)
	if err != nil {
		h.recordChangedRestore(r, p, obj, expect, err)

		if errors.Is(err, workspace.ErrLockedByOther) {
			holder := h.refusingHolder(ctx, src.Workspace, err)
			if holder == "" {
				h.writeClassError(ctx, w, errorClass{
					status: http.StatusConflict, title: titleConflict, detail: err.Error(),
					code: errcode.WorkspaceLocked, args: nil,
				})

				return
			}

			h.writeClassError(ctx, w, lockConflict(src.Workspace, holder))

			return
		}

		h.writeError(ctx, w, err)

		return
	}

	doc, err := h.stateVersionDocument(r, restored)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.logger.InfoContext(
		ctx,
		"restore completed",
		slog.String("action", "restore"),
		slog.String("object", obj.String()),
		slog.String("principal", p.Subject),
		slog.String("released", ""),
	)

	h.writeJSON(ctx, w, http.StatusOK, restoreStateVersionDocument{
		Data: doc,
		Meta: restoreMeta{Message: restoreMessage},
	})
}

// readCurrentOutputs serves GET
// /api/v2/workspaces/{id}/current-state-version-outputs. A workspace without
// state answers 404. The output sharing checker decides the read after the
// route table needed unit:read-outputs.
func (h *Handler) readCurrentOutputs(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	sv, err := h.workspaces.Current(ctx, workspace.ID(r.PathValue("id")))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	// The list leaves out the value of a secret output, so it needs no
	// unit:read-secrets.
	read := outputRead{obj: obj, workspace: sv.Workspace, version: sv.ID, secrets: false}
	if err := h.authorizeOutputRead(ctx, r, p, read); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeStateOutputs(ctx, w, sv)
}

// readStateOutput serves GET /api/v2/state-version-outputs/{output_id}. An
// unknown output answers 404. The read of an output of the current state
// version goes through the output sharing checker, which needs
// unit:read-secrets for a sensitive output. The read of an older state version
// needs unit:read, and unit:read-secrets for a sensitive output. A sensitive output writes one audit entry of
// the kind tool-operation with the value path of the output before it
// answers: a failed append answers 500 with no value (ADR 41 decision 5). A
// non-sensitive output writes no entry.
func (h *Handler) readStateOutput(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	version, name, err := parseOutputID(r.PathValue("output_id"))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	sv, err := h.workspaces.ReadState(ctx, version)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	// Defense in depth: authorizeOutputRead checks unit:read for an older state
	// version again below. This check stays so that the answer for an older
	// state version does not depend on whether the output name exists.
	if err = h.authorizeVersionRead(ctx, r, p, obj, sv); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	outputs, err := stateOutputs(sv)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	for _, output := range outputs {
		if output.Attributes.Name != name {
			continue
		}

		read := outputRead{obj: obj, workspace: sv.Workspace, version: sv.ID, secrets: output.Attributes.Sensitive}
		if err := h.authorizeOutputRead(ctx, r, p, read); err != nil {
			h.writeError(ctx, w, err)

			return
		}

		if output.Attributes.Sensitive {
			if err := h.appendToolOperationAudit(r, p, obj, sv.ID.String(), "/outputs/"+name); err != nil {
				h.failAudit(ctx)
				h.writeInternal(ctx, w)

				return
			}

			w.Header().Set("Cache-Control", noStoreValue)
		}

		h.writeJSON(ctx, w, http.StatusOK, stateVersionOutputDocument{Data: output})

		return
	}

	h.writeError(ctx, w, fmt.Errorf("%w: %q", workspace.ErrStateVersionNotFound, r.PathValue("output_id")))
}

// writeStateVersion writes one state version document.
func (h *Handler) writeStateVersion(
	ctx context.Context, w http.ResponseWriter, r *http.Request, sv workspace.StateVersion,
) {
	doc, err := h.stateVersionDocument(r, sv)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, stateVersionDocument{Data: doc})
}

// writeStateOutputs writes the outputs of sv as one page holding the whole
// list, with the meta.pagination object the client reads. A sensitive output
// reports null as its value in the list; the CLI reads the value from the
// single output route.
func (h *Handler) writeStateOutputs(ctx context.Context, w http.ResponseWriter, sv workspace.StateVersion) {
	outputs, err := stateOutputs(sv)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	for i := range outputs {
		if outputs[i].Attributes.Sensitive {
			outputs[i].Attributes.Value = nil
		}
	}

	pages := 0
	if len(outputs) > 0 {
		pages = 1
	}

	h.writeJSON(ctx, w, http.StatusOK, stateVersionOutputListDocument{
		Data: outputs,
		Meta: workspaceListMeta{
			Pagination: paginate(workspacePage{search: "", number: 1, size: len(outputs)}, len(outputs), pages),
		},
	})
}

// listFilters reads the workspace reference of a state version list from the
// filter query: filter[organization][name] and filter[workspace][name]. A
// missing filter fails with a 400 error; a bad name fails with a 422 error.
func listFilters(r *http.Request) (workspace.Ref, error) {
	query := r.URL.Query()
	org := query.Get("filter[organization][name]")
	name := query.Get("filter[workspace][name]")

	if org == "" || name == "" {
		return workspace.Ref{}, errMissingFilter
	}

	ns, err := namespace.ParseName(org)
	if err != nil {
		return workspace.Ref{}, err
	}

	parsed, err := workspace.ParseName(name)
	if err != nil {
		return workspace.Ref{}, err
	}

	return workspace.Ref{Namespace: ns, Name: parsed}, nil
}

// resolveStateVersionList returns the unit the state version list acts on:
// the workspace the two filters name. A missing filter fails with a 400
// error. It never reads the store, so a denied caller cannot tell an
// existing workspace from an unknown one; the handler answers 404 for an
// unknown workspace after authorization.
func resolveStateVersionList(
	_ http.ResponseWriter, r *http.Request,
) (identity.Object, error) {
	ref, err := listFilters(r)
	if err != nil {
		return identity.Object{}, err
	}

	return unitObject(ref)
}

// listStateVersions serves GET /api/v2/state-versions: the finalized state
// versions of the filtered workspace, newest first, with page[number] and
// page[size] and the meta.pagination object the client reads. A missing
// filter answers 400; an unknown workspace answers 404.
func (h *Handler) listStateVersions(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	ref, err := listFilters(r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	ws, err := h.workspaces.Get(ctx, ref)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	page, err := parseWorkspacePage(r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	size := min(page.size, workspace.MaxStatePageSize)
	offset := 0

	if page.number > 1 {
		if page.number-1 > math.MaxInt/size {
			offset = math.MaxInt
		} else {
			offset = (page.number - 1) * size
		}
	}

	list, total, err := h.workspaces.History(ctx, ws.ID, workspace.Page{Limit: size, Offset: offset})
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	pages := 0
	if total > 0 {
		pages = (total-1)/size + 1
	}

	items := make([]stateVersionResource, 0, len(list))

	for i := range list {
		doc, err := h.stateVersionDocument(r, list[i])
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}
		items = append(items, doc)
	}

	h.writeJSON(ctx, w, http.StatusOK, stateVersionListDocument{
		Data: items,
		Meta: workspaceListMeta{
			Pagination: paginate(workspacePage{search: "", number: page.number, size: size}, total, pages),
		},
	})
}

// resolveStateVersionID returns the unit of the finalized state version in
// the state_version_id path value. Unknown IDs and versions that are not
// finalized fail with absence, which the wrapper answers as 403 for a caller
// without rights and 404 otherwise, so a pending version is never readable.
func (h *Handler) resolveStateVersionID(_ http.ResponseWriter, r *http.Request) (identity.Object, error) {
	return h.resolveStateVersion(r, workspace.StateVersionID(r.PathValue("state_version_id")))
}

// resolveStateOutputID returns the unit of the state version in the
// output_id path value, so a single output authorizes as a state read of its
// workspace. The output itself is resolved in the handler.
func (h *Handler) resolveStateOutputID(_ http.ResponseWriter, r *http.Request) (identity.Object, error) {
	version, _, err := parseOutputID(r.PathValue("output_id"))
	if err != nil {
		return identity.Object{}, err
	}

	return h.resolveStateVersion(r, version)
}

// resolveStateVersion returns the unit of the finalized state version with
// id: its workspace resolved through GetByID. Absence fails with the
// workspace or state version error, which the wrapper answers as 403 for a
// caller without rights and 404 otherwise.
func (h *Handler) resolveStateVersion(
	r *http.Request, id workspace.StateVersionID,
) (identity.Object, error) {
	sv, err := h.workspaces.ReadState(r.Context(), id)
	if err != nil {
		return identity.Object{}, err
	}

	ws, err := h.workspaces.GetByID(r.Context(), sv.Workspace)
	if err != nil {
		return identity.Object{}, err
	}

	return unitObject(ws.Ref)
}

// rollbackState reads what a restore of the version from changes in the state
// of the workspace id: the tool-neutral change that the gates read as the
// facts state.resources and state.removed. It returns the error of the read
// and a nil change when it cannot read the state; the guard then refuses the
// write.
func (h *Handler) rollbackState(
	ctx context.Context, id workspace.ID, from workspace.StateVersionID,
) (*gate.StateChange, error) {
	rc, err := h.workspaces.ReadRollbackChange(ctx, id, from)
	if err != nil {
		// A state over a size limit is a limit and not a fault of the store.
		// The refusal then has the code gate.limit.
		if errors.Is(err, workspace.ErrTooManyResources) || errors.Is(err, workspace.ErrTooLarge) {
			return nil, fmt.Errorf("%w: read the state change of the restore: %w", gate.ErrLimit, err)
		}

		return nil, fmt.Errorf("read the state change of the restore: %w", err)
	}

	if rc.Encrypted {
		// A state that the client encrypted is a known condition. The facts
		// are not available, the record says why, and the restore goes on
		// under the gates that apply.
		return &gate.StateChange{
			Current: string(rc.Current), Resources: 0, Removed: nil, Unavailable: "state is client-encrypted",
		}, nil
	}

	return &gate.StateChange{
		Current: string(rc.Current), Resources: rc.Resources, Removed: rc.Removed, Unavailable: "",
	}, nil
}

// restoreVersion restores the version from. An empty expect restores with no
// check of the current version (the gates are off). A set expect restores only
// if that version is still the current one.
func (h *Handler) restoreVersion(
	ctx context.Context, id workspace.ID, from workspace.StateVersionID, holder string, expect workspace.StateVersionID,
) (workspace.StateVersion, error) {
	if expect == "" {
		return h.workspaces.Restore(ctx, id, from, holder)
	}

	return h.workspaces.RestoreExpecting(ctx, id, from, holder, expect)
}

// guardRestore asks the state guard about the restore of the version from. It
// returns the current version that the facts came from, which the restore must
// still find, and true when the restore may go on. With the gates off no gate
// reads a fact, so the server reads no state and the expected version is empty.
func (h *Handler) guardRestore(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
	src workspace.StateVersion,
) (workspace.StateVersionID, bool) {
	check := newStateCheck(identity.ActionUnitWrite, gate.OperationRollback)
	expect := workspace.StateVersionID("")

	if h.admit.Effective().Mode != stategate.ModeOff {
		check.state, check.stateErr = h.rollbackState(r.Context(), src.Workspace, src.ID)
		if check.state != nil {
			expect = workspace.StateVersionID(check.state.Current)
		}
	}

	return expect, h.guardStateWrite(w, r, p, obj, check)
}

// recordChangedRestore writes the denied audit entry of a restore that found
// another current version than the one the gate decision used. The decision
// used facts of a state that is no longer current, so the write is refused.
func (h *Handler) recordChangedRestore(
	r *http.Request, p identity.Principal, obj identity.Object, expect workspace.StateVersionID, err error,
) {
	if expect == "" || !errors.Is(err, workspace.ErrConcurrentModification) {
		return
	}

	h.guard.Record(r.Context(), r, p, refusal.Denial{
		Action: identity.ActionUnitWrite, Object: obj, ReasonWithin: nil, Window: 0,
		Reason: "state.write refused: the current state version changed after the gate decision",
	})
}
