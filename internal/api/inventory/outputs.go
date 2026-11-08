package inventory

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/outputsharesvc"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/outputshare"
)

// tagOutputs is the tag of the output routes in the description.
const tagOutputs = "outputs"

// outputsPath is the route that reads the root outputs of the latest state of
// a unit.
const outputsPath = namespacePath + "/units/{unit_id}/outputs"

// errOutputsStateMoved is the 409 message of a read that a new state crossed.
const errOutputsStateMoved = "the state of the unit changed during the read: send the request again"

// unitOutputsDocument is the answer of the outputs route.
//
// Contract: never remove or rename these fields.
type unitOutputsDocument struct {
	// Tool is the tool of the unit, for example "pulumi" or "opentofu".
	Tool string `json:"tool"`
	// UnitID is the stable ID of the unit.
	UnitID string `json:"unitId"` //nolint:tagliatelle // The tool-neutral API names this field in lower camel case.
	// StateID names the state that the outputs come from.
	StateID string `json:"stateId"` //nolint:tagliatelle // The tool-neutral API names this field in lower camel case.
	// Outputs holds the root outputs, ordered by name.
	Outputs []unitOutputEntry `json:"outputs"`
}

// unitOutputEntry is one root output. A secret output has no value.
type unitOutputEntry struct {
	// Name is the name of the output.
	Name string `json:"name"`
	// Secret is true for an output that the tool marked secret or sensitive.
	Secret bool `json:"secret"`
	// Value is the JSON value of a plain output. A secret output has none.
	Value json.RawMessage `json:"value,omitempty"`
}

// outputsBody is the description of the answer of the outputs route.
//
//nolint:gochecknoglobals // A fixed value; Go has no constant structs.
var outputsBody = apidesc.Typed[unitOutputsDocument]("UnitOutputs", apidesc.Declare[json.RawMessage](
	map[string]any{"description": "Any JSON value."},
))

// outputRoutes returns the row of the outputs route. The caller needs
// unit:read-outputs on the unit and nothing else: a principal that holds only
// that action reads the outputs. The route finds the unit by its ID alone, so a
// denied caller gets the 404 of an unknown unit.
func (h *Handler) outputRoutes() []Route {
	return []Route{{
		Method:           http.MethodGet,
		Path:             outputsPath,
		Scope:            route.ScopeAuthorized,
		Actions:          []identity.Action{identity.ActionUnitReadOutputs},
		NamespaceActions: nil,
		Resolve:          h.resolveOutputsUnit,
		DeniedAsNotFound: true,
		Serve:            h.getOutputs,
	}}
}

// resolveOutputsUnit returns the unit reference of the live unit whose stable
// ID is in the path. A unit of another namespace than the path gives the
// error of an unknown unit.
func (h *Handler) resolveOutputsUnit(_ http.ResponseWriter, r *http.Request) (identity.Object, error) {
	entry, err := h.units.GetUnit(r.Context(), r.PathValue("unit_id"))
	if err != nil {
		if errors.Is(err, coreinventory.ErrUnitNotFound) {
			return identity.Object{}, coreinventory.ErrUnitNotFound
		}

		return identity.Object{}, fmt.Errorf("inventory unit: %w", err)
	}

	obj, err := unitObject(entry)
	if err != nil {
		return identity.Object{}, err
	}

	if obj.Namespace().String() != r.PathValue("namespace") {
		return identity.Object{}, coreinventory.ErrUnitNotFound
	}

	return obj, nil
}

// getOutputs answers GET on the outputs route with the root outputs of the
// latest state. The route wrapper has allowed unit:read-outputs. The reader then asks the output sharing checker, which
// reads the latest state. No answer holds the value of a secret output.
func (h *Handler) getOutputs(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	entry, err := h.units.GetUnit(ctx, r.PathValue("unit_id"))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	// The read can see a rename between the resolver and the handler. The
	// handler answers 404 then, because the wrapper authorized the old name.
	if got, objErr := unitObject(entry); objErr != nil || got.String() != obj.String() {
		h.writeError(ctx, w, coreinventory.ErrUnitNotFound)

		return
	}

	result, err := h.outputs.Read(ctx, p, obj.WithUnitID(entry.UnitID))
	if err != nil {
		h.writeOutputsError(w, r, p, obj, err)

		return
	}

	doc := unitOutputsDocument{
		Tool: entry.Tool, UnitID: entry.UnitID, StateID: result.StateID,
		Outputs: make([]unitOutputEntry, 0, len(result.Outputs)),
	}

	for _, output := range result.Outputs {
		doc.Outputs = append(
			doc.Outputs,
			unitOutputEntry{Name: output.Name, Secret: output.Secret, Value: output.Value},
		)
	}

	h.writeJSON(ctx, w, http.StatusOK, doc)
}

// writeOutputsError answers the error of a read of outputs. The checker has
// written the audit entry of its own denial, so the handler writes none. A
// producer that does not exist answers as an unknown unit. A state that the
// client encrypted answers 409 with the reason, for every caller. A state that moved answers 409.
// A unit with no state answers 404 with a message that names the unit.
func (h *Handler) writeOutputsError(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object, err error,
) {
	ctx := r.Context()

	switch {
	case errors.Is(err, outputshare.ErrUnknownProducer):
		h.writeError(ctx, w, coreinventory.ErrUnitNotFound)
	case errors.Is(err, outputshare.ErrClientEncrypted):
		h.writeFailure(ctx, w, problem.New(http.StatusConflict, errcode.UnitClientEncrypted, clientEncryptedMessage))
	case errors.Is(err, outputsharesvc.ErrNoState):
		h.writeFailure(
			ctx,
			w,
			problem.New(
				http.StatusNotFound,
				errcode.UnitNoState,
				fmt.Sprintf("the unit %s has no state yet", obj),
				errcode.Identifier("unit", obj.String()),
			),
		)
	case errors.Is(err, outputshare.ErrStateMoved):
		h.writeFailure(ctx, w, problem.New(http.StatusConflict, errcode.UnitStateMoved, errOutputsStateMoved))
	default:
		h.writeError(ctx, w, err)
	}
}

// clientEncryptedMessage is the 409 message of a read of outputs from a state
// that the client encrypted, for every caller. It names the reason and no
// object.
const clientEncryptedMessage = "the client encrypted the state of the unit, " +
	"so the server cannot read its outputs: read them with the tool that holds the encryption key"
