package tfe

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/identity"
	"opendustpan.org/opendustpan/workspace"
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
// upload URLs of the capability the content follows through.
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

// storedOutputs is the json-state-outputs document the client sent at
// create: the JSON rendering of the root outputs, kept opaque in the state
// version. Each entry holds the cty value and its cty type in JSON form.
// The CLI sends the flat map of outputs at the top level; the wrapped form
// with an "outputs" key stays accepted for versions stored before the flat
// shape was served.
type storedOutputs struct {
	Outputs map[string]storedOutput `json:"outputs"`
}

// storedOutput is one entry of the stored outputs document.
type storedOutput struct {
	Sensitive bool            `json:"sensitive"`
	Value     json.RawMessage `json:"value"`
	Type      json.RawMessage `json:"type"`
}

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
	var wrapped storedOutputs

	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, fmt.Errorf("decode stored outputs: %w", err)
	}

	if wrapped.Outputs != nil {
		return wrapped.Outputs, nil
	}

	var flat map[string]storedOutput

	if err := json.Unmarshal(raw, &flat); err != nil {
		return nil, fmt.Errorf("decode stored outputs: %w", err)
	}

	if flat == nil {
		return map[string]storedOutput{}, nil
	}

	return flat, nil
}

// stateOutputs renders the stored outputs of sv as TFE output resources,
// ordered by name. A version without outputs renders an empty list. A
// document that is not valid JSON fails, as the stored data is corrupt.
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
// not a capability.
func (h *Handler) downloadState(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
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

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if _, err := io.Copy(w, content); err != nil {
		h.logger.WarnContext(ctx, "write response failed", slog.String("error", err.Error()))
	}
}

// readCurrentOutputs serves GET
// /api/v2/workspaces/{id}/current-state-version-outputs. A workspace without
// state answers 404.
func (h *Handler) readCurrentOutputs(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	sv, err := h.workspaces.Current(ctx, workspace.ID(r.PathValue("id")))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeStateOutputs(ctx, w, sv)
}

// readStateOutput serves GET /api/v2/state-version-outputs/{output_id}. An
// unknown output answers 404.
func (h *Handler) readStateOutput(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object,
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

	outputs, err := stateOutputs(sv)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	for _, output := range outputs {
		if output.Attributes.Name == name {
			h.writeJSON(ctx, w, http.StatusOK, stateVersionOutputDocument{Data: output})

			return
		}
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
// list, with the meta.pagination object the client reads.
func (h *Handler) writeStateOutputs(ctx context.Context, w http.ResponseWriter, sv workspace.StateVersion) {
	outputs, err := stateOutputs(sv)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	pages := 0
	if len(outputs) > 0 {
		pages = 1
	}

	h.writeJSON(ctx, w, http.StatusOK, stateVersionOutputListDocument{
		Data: outputs,
		Meta: workspaceListMeta{
			Pagination: paginate(workspacePage{number: 1, size: len(outputs)}, len(outputs), pages),
		},
	})
}

// resolveStateVersionID returns the unit of the finalized state version in
// the state_version_id path value. Unknown IDs and versions that are not
// finalized fail, so a pending version is never readable.
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
// id: its workspace resolved through GetByID.
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
