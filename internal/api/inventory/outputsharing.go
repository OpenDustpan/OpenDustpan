package inventory

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/outputshare"
)

// maxOutputSharingBodyBytes bounds the body of a change of the output sharing
// setting. The body holds one field with a value of at most 9 characters.
const maxOutputSharingBodyBytes = 1024

// outputSharingPath is the route of the output sharing setting of a
// namespace.
const outputSharingPath = namespacePath + "/output-sharing"

// errOutputSharingBody names the body that a change of the setting needs. Its
// text is the error of the 400 answer.
var errOutputSharingBody = errors.New(
	`invalid output sharing body: provide {"newUnitDefault":"nobody"} or {"newUnitDefault":"namespace"}`,
)

// outputSharingDocument is the output sharing setting of a namespace on the
// wire. NewUnitDefault is the sharing rule that a new unit of the namespace
// gets: "nobody" or "namespace".
//
// Contract: never remove or rename these fields.
type outputSharingDocument struct {
	NewUnitDefault string `json:"newUnitDefault"`
}

// outputSharingRequest is the body of a change of the setting. The field is a
// pointer so that a missing value differs from an empty one.
type outputSharingRequest struct {
	NewUnitDefault *string `dustpan:"required" json:"newUnitDefault"`
}

// outputSharingRoutes returns the rows of the output sharing setting of a
// namespace. The object of both routes is the namespace of the path, so only
// a grant on the namespace reaches it. A read needs namespace:read, and a
// change needs unit:manage-sharing.
func (h *Handler) outputSharingRoutes() []Route {
	space := func(method string, action identity.Action, serve RouteHandler) Route {
		return Route{
			Method:           method,
			Path:             outputSharingPath,
			Scope:            route.ScopeAuthorized,
			Actions:          []identity.Action{action},
			Resolve:          resolveCollectionNamespace,
			NamespaceActions: nil,
			DeniedAsNotFound: false,
			Serve:            serve,
		}
	}

	return []Route{
		space(http.MethodGet, identity.ActionNamespaceRead, h.getOutputSharing),
		space(http.MethodPut, identity.ActionUnitManageSharing, h.putOutputSharing),
	}
}

// getOutputSharing serves GET on the setting route. It answers 200 with the
// setting. A namespace that never set it answers "nobody".
func (h *Handler) getOutputSharing(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	d, err := h.outputSharing.Setting(ctx, p, obj.Namespace())
	if err != nil {
		denial := refusal.Denial{
			Action: identity.ActionNamespaceRead, Object: obj, Reason: "", ReasonWithin: nil, Window: 0,
		}
		h.writeOutputSharingFailure(w, r, p, denial, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, outputSharingDocument{NewUnitDefault: string(d)})
}

// putOutputSharing serves PUT on the setting route. It answers 200 with the
// setting as stored. A body that is not one object with a value of the
// setting answers 400 and changes nothing. The change and its audit entry are
// one transaction in the store. It changes no rule of a unit that exists.
func (h *Handler) putOutputSharing(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	d, err := readOutputSharing(w, r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	entry := auditEntry(r, p, obj, identity.ActionUnitManageSharing, "")

	if err := h.outputSharing.SetSetting(ctx, p, obj.Namespace(), d, entry); err != nil {
		denial := refusal.Denial{
			Action: identity.ActionUnitManageSharing, Object: obj, Reason: "", ReasonWithin: nil, Window: 0,
		}
		h.writeOutputSharingFailure(w, r, p, denial, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, outputSharingDocument{NewUnitDefault: string(d)})
}

// writeOutputSharingFailure answers the error of a setting route. A refusal
// of the service writes an audit entry of the outcome denied: the route check
// has allowed the request, so the middleware has not recorded it. d holds the
// action and the object of the route.
func (h *Handler) writeOutputSharingFailure(
	w http.ResponseWriter, r *http.Request, p identity.Principal, d refusal.Denial, err error,
) {
	if errors.Is(err, identity.ErrDenied) {
		h.guard.Record(r.Context(), r, p, d)
	}

	h.writeError(r.Context(), w, err)
}

// readOutputSharing decodes the body of a change of the setting. It refuses an
// unknown field, a body that is not one object, a body without the value, and
// a value that is not a value of the setting.
func readOutputSharing(w http.ResponseWriter, r *http.Request) (outputshare.Default, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxOutputSharingBodyBytes))
	if err != nil {
		return "", errOutputSharingBody
	}

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()

	var in outputSharingRequest

	if err := dec.Decode(&in); err != nil {
		return "", errOutputSharingBody
	}

	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return "", errOutputSharingBody
	}

	if in.NewUnitDefault == nil {
		return "", errOutputSharingBody
	}

	// The allowlist of the boundary. The service and the store check the
	// value again.
	switch d := outputshare.Default(*in.NewUnitDefault); d {
	case outputshare.DefaultNobody, outputshare.DefaultNamespace:
		return d, nil
	default:
		return "", errOutputSharingBody
	}
}
