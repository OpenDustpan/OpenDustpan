package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// Bounds of the boundary check of a read. Each value of the request is
// checked against its bound before any state is read.
const (
	// maxRefBytes bounds the unit reference of the path.
	maxRefBytes = 512
	// maxCursorBytes bounds the cursor of a page. The domain packages have the
	// same cap.
	maxCursorBytes = 512
	// maxPulumiVersionDigits bounds the version of a stack: the number of an
	// update, at most 2^40.
	maxPulumiVersionDigits = 13
	// maxTofuVersionBytes bounds the ID of a state version of a workspace.
	maxTofuVersionBytes = 64
)

// refusedReason is the reason of the audit entry of a read that the domain
// package refused. The refusal reason of the domain package follows it.
const refusedReason = "masked state read refused: "

// resolveUnit returns the unit object of the path. The reference must parse
// as a unit of a tool that the read serves; any other value fails before
// authorization, with an answer that repeats no part of it.
func resolveUnit(_ http.ResponseWriter, r *http.Request) (identity.Object, error) {
	raw := r.PathValue("ref")
	if raw == "" || len(raw) > maxRefBytes || !utf8.ValidString(raw) ||
		strings.ContainsFunc(raw, func(c rune) bool { return c < 0x20 || c == 0x7f }) {
		return identity.Object{}, errBadRef
	}

	obj, err := identity.ParseObject(raw)
	if err != nil || !obj.IsUnit() {
		return identity.Object{}, errBadRef
	}

	switch obj.Tool() {
	case stack.InventoryTool, workspace.InventoryTool:
		return obj, nil
	default:
		return identity.Object{}, errBadRef
	}
}

// readArgs are the values of one read after the boundary check.
type readArgs struct {
	obj     identity.Object
	p       identity.Principal
	version string
	cursor  string
	sink    func(identity.Principal, identity.Object, string)
}

// read answers GET /api/dustpan/v1/units/{ref}/state with one page of the
// masked state of the unit.
func (h *Handler) read(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	args, err := h.readArgsOf(ctx, r, p, obj)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	page, err := h.readUnit(ctx, args)

	switch {
	case err == nil:
		h.writeJSON(ctx, w, page)
	case args.version == "" && (errors.Is(err, stack.ErrNoState) || errors.Is(err, workspace.ErrNoState)):
		h.writeNoState(ctx, w, obj)
	default:
		h.writeError(ctx, w, err)
	}
}

// readArgsOf returns the values of the query of r after the boundary check.
// A refused cursor reports its refusal to the sink.
func (h *Handler) readArgsOf(
	ctx context.Context, r *http.Request, p identity.Principal, obj identity.Object,
) (readArgs, error) {
	q := apidesc.QueryOf(r, apidesc.UnitsStateLabels())
	if q.Err() != nil || len(q.Undeclared()) > 0 {
		return readArgs{}, errBadQuery
	}

	args := readArgs{
		obj: obj, p: p, version: q.Get(apidesc.UnitsStateVersion), cursor: q.Get(apidesc.UnitsStateCursor),
		sink: h.sinkFor(ctx, r),
	}

	// Defense in depth: the domain packages refuse a cursor above the same
	// cap; this check stays so that an oversized cursor never reaches them.
	if len(args.cursor) > maxCursorBytes {
		args.sink(p, obj, stack.RefusalBadPath)

		return readArgs{}, errBadPath
	}

	return args, nil
}

// readUnit reads one page of the unit of args, under the timeout of the
// handler.
func (h *Handler) readUnit(ctx context.Context, args readArgs) (apidesc.UnitStatePage, error) {
	ctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()

	switch args.obj.Tool() {
	case stack.InventoryTool:
		return h.readStack(ctx, args)
	case workspace.InventoryTool:
		return h.readWorkspace(ctx, args)
	default:
		// Defense in depth: resolveUnit allows the two tools only; this case
		// stays so that a new caller cannot read a unit of another tool.
		return apidesc.UnitStatePage{}, errBadRef
	}
}

// sinkFor returns the sink of the refusals that the domain package of the
// tool reports. Each refusal writes its audit entry through the recorder of
// the handler, within the bound of the denial limiter of the server.
func (h *Handler) sinkFor(ctx context.Context, r *http.Request) func(identity.Principal, identity.Object, string) {
	return func(p identity.Principal, obj identity.Object, reason string) {
		h.guard.Record(ctx, r, p, refusal.Denial{
			Action: identity.ActionUnitRead, Object: obj, Reason: refusedReason + reason, ReasonWithin: nil, Window: 0,
		})
	}
}

// readStack reads one page of the masked state of the stack of args.
func (h *Handler) readStack(ctx context.Context, args readArgs) (apidesc.UnitStatePage, error) {
	project, name, ok := strings.Cut(args.obj.UnitName(), "/")
	if !ok {
		return apidesc.UnitStatePage{}, errUnitNotFound
	}

	ref, err := stack.ParseRef(args.obj.Namespace().String(), project, name)
	if err != nil {
		return apidesc.UnitStatePage{}, errUnitNotFound
	}

	sel, err := stackSelector(args.version)
	if err != nil {
		args.sink(args.p, args.obj, stack.RefusalBadPath)

		return apidesc.UnitStatePage{}, err
	}

	st, err := h.stacks.Get(ctx, ref)
	if err != nil {
		return apidesc.UnitStatePage{}, fmt.Errorf("find the stack: %w", err)
	}

	unit := args.obj.WithUnitID(st.ID.String())

	page, err := h.stackStates.ReadMasked(ctx, stack.ReadMaskedArgs{
		Ref: ref, Object: unit, Selector: sel, Principal: args.p, Authorizer: h.authz, Key: h.key,
		Sink: args.sink, Cursor: args.cursor,
	})
	if err != nil {
		return apidesc.UnitStatePage{}, fmt.Errorf("masked read of the stack: %w", err)
	}

	markers := markersOf(page.Markers, func(m stack.MaskedMarker) string { return m.Path })

	return pageOf(args.obj, page.Version, markers, page.ValuePaths, page.NextCursor), nil
}

// readWorkspace reads one page of the masked state of the workspace of args.
func (h *Handler) readWorkspace(ctx context.Context, args readArgs) (apidesc.UnitStatePage, error) {
	name, err := workspace.ParseName(args.obj.UnitName())
	if err != nil {
		return apidesc.UnitStatePage{}, errUnitNotFound
	}

	ref := workspace.Ref{Namespace: args.obj.Namespace(), Name: name}

	sel, err := workspaceSelector(args.version)
	if err != nil {
		args.sink(args.p, args.obj, workspace.RefusalBadPath)

		return apidesc.UnitStatePage{}, err
	}

	ws, err := h.workspaces.Get(ctx, ref)
	if err != nil {
		return apidesc.UnitStatePage{}, fmt.Errorf("find the workspace: %w", err)
	}

	unit := args.obj.WithUnitID(ws.ID.String())

	page, err := h.workspaces.ReadMasked(ctx, workspace.ReadMaskedArgs{
		Ref: ref, Object: unit, Selector: sel, Principal: args.p, Authorizer: h.authz, Key: h.key,
		Sink: args.sink, Cursor: args.cursor,
	})
	if err != nil {
		return apidesc.UnitStatePage{}, fmt.Errorf("masked read of the workspace: %w", err)
	}

	markers := markersOf(page.Markers, func(m workspace.MaskedMarker) string { return m.Path })

	return pageOf(args.obj, page.Version, markers, page.ValuePaths, page.NextCursor), nil
}

// markersOf returns the wire markers of the markers of a tool. Each wire
// marker is a secret: the read has no other kind of marker.
func markersOf[M any](in []M, path func(M) string) []apidesc.MaskedStateMarker {
	out := make([]apidesc.MaskedStateMarker, 0, len(in))
	for _, m := range in {
		out = append(out, apidesc.MaskedStateMarker{Secret: true, Path: path(m)})
	}

	return out
}

// pageOf returns the body of one page. The unit is the reference of the path,
// with no unit ID.
func pageOf(
	obj identity.Object, version string, markers []apidesc.MaskedStateMarker, paths []string, next string,
) apidesc.UnitStatePage {
	if paths == nil {
		paths = []string{}
	}

	return apidesc.UnitStatePage{
		Unit: obj.String(), Version: version, Markers: markers, ValuePaths: paths, NextCursor: next,
	}
}

// stackSelector returns the version of a stack read: the current version for
// the empty value, or the number of an update, in decimal digits only.
func stackSelector(raw string) (stack.StateSelector, error) {
	if raw == "" {
		return stack.StateSelector{Latest: true, Version: 0}, nil
	}

	if len(raw) > maxPulumiVersionDigits || strings.ContainsFunc(raw, func(r rune) bool { return r < '0' || r > '9' }) {
		return stack.StateSelector{}, errBadPath
	}

	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v < 1 {
		return stack.StateSelector{}, errBadPath
	}

	return stack.StateSelector{Latest: false, Version: v}, nil
}

// workspaceSelector returns the version of a workspace read: the current
// version for the empty value, or the ID of a state version: "sv-", then
// lowercase letters, digits and hyphens.
func workspaceSelector(raw string) (workspace.VersionSelector, error) {
	if raw == "" {
		return workspace.VersionSelector{Latest: true, ID: ""}, nil
	}

	if len(raw) > maxTofuVersionBytes || !strings.HasPrefix(raw, "sv-") ||
		strings.ContainsFunc(raw, func(r rune) bool {
			return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-'
		}) {
		return workspace.VersionSelector{}, errBadPath
	}

	return workspace.VersionSelector{Latest: false, ID: workspace.StateVersionID(raw)}, nil
}

// writeJSON writes the page with 200.
func (h *Handler) writeJSON(ctx context.Context, w http.ResponseWriter, page apidesc.UnitStatePage) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(page); err != nil && !errors.Is(err, context.Canceled) {
		h.logger.WarnContext(ctx, "write response failed", slog.String("error", err.Error()))
	}
}
