package server

import (
	"context"
	"errors"
	"net/http"
	"net/netip"

	"opendustpan.org/opendustpan/internal/agent"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/identity"
)

// egressAllowlistPath is the route pattern of the read and the change of the
// egress allowlist of the installation.
const egressAllowlistPath = "/api/server/egress-allowlist"

// maxEgressAllowlistBodyBytes bounds the body of an egress allowlist change.
// The list holds at most 64 hosts and 64 rules, so this is ample.
const maxEgressAllowlistBodyBytes = 32 * 1024

// errEgressAllowlistBody names the body that an egress allowlist change
// needs.
var errEgressAllowlistBody = &codedError{
	status:  http.StatusBadRequest,
	code:    errcode.AgentConnectionAllowlistInvalid,
	args:    nil,
	message: `invalid egress allowlist body: provide {"hosts":["example.com"],"cidrs":[{"cidr":"10.0.0.0/8","port":443,"http":false}]}`,
}

// cidrRuleRequest is one CIDR rule of a change body.
type cidrRuleRequest struct {
	CIDR string `json:"cidr"`
	Port int    `json:"port"`
	HTTP bool   `json:"http"`
}

// egressAllowlistRequest is the body of a change. The pointers tell a missing
// or null field from an empty list, so a change must name both "hosts" and
// "cidrs".
type egressAllowlistRequest struct {
	Hosts *[]string          `json:"hosts"`
	CIDRs *[]cidrRuleRequest `json:"cidrs"`
}

// cidrRuleResponse is one CIDR rule on the wire.
type cidrRuleResponse struct {
	CIDR string `json:"cidr"`
	Port int    `json:"port"`
	HTTP bool   `json:"http"`
}

// egressAllowlistResponse is the egress allowlist on the wire. The lists are
// empty, never null, when the installation has no entry.
//
// Contract: never remove or rename these fields. The console reads only this
// API, and it updates on its own schedule.
type egressAllowlistResponse struct {
	Hosts []string           `json:"hosts"`
	CIDRs []cidrRuleResponse `json:"cidrs"`
}

// egressAllowlistOf returns the wire form of a.
func egressAllowlistOf(a egress.Allowlist) egressAllowlistResponse {
	body := egressAllowlistResponse{
		Hosts: append([]string{}, a.Hosts...),
		CIDRs: make([]cidrRuleResponse, 0, len(a.CIDRs)),
	}

	for _, rule := range a.CIDRs {
		body.CIDRs = append(body.CIDRs, cidrRuleResponse{
			CIDR: rule.CIDR.String(), Port: rule.Port, HTTP: rule.HTTP,
		})
	}

	return body
}

// egressAllowlistInvalid returns the coded error of an invalid allowlist.
func egressAllowlistInvalid(err error) error {
	return &codedError{
		status: http.StatusBadRequest, code: errcode.AgentConnectionAllowlistInvalid, args: nil,
		message: err.Error(),
	}
}

// readEgressAllowlistInput converts the request to the domain type. It
// returns the coded error of a rule whose CIDR is not valid.
func readEgressAllowlistInput(in egressAllowlistRequest) (egress.Allowlist, error) {
	a := egress.Allowlist{
		Hosts: make([]string, 0, len(*in.Hosts)),
		CIDRs: make([]egress.CIDRRule, 0, len(*in.CIDRs)),
	}

	a.Hosts = append(a.Hosts, (*in.Hosts)...)

	for _, rule := range *in.CIDRs {
		prefix, err := netip.ParsePrefix(rule.CIDR)
		if err != nil {
			return egress.Allowlist{Hosts: nil, CIDRs: nil}, &codedError{
				status: http.StatusBadRequest, code: errcode.AgentConnectionAllowlistInvalid, args: nil,
				message: "The egress allowlist rule has no valid CIDR range: provide a range such as 10.0.0.0/8.",
			}
		}

		// A prefix with host bits set names a different range than its
		// text: refuse it, do not mask it to the range that it holds.
		if prefix.Masked() != prefix {
			return egress.Allowlist{Hosts: nil, CIDRs: nil}, &codedError{
				status: http.StatusBadRequest, code: errcode.AgentConnectionAllowlistInvalid, args: nil,
				message: "The egress allowlist rule has host bits set: use the network address, such as 10.20.0.0/16.",
			}
		}

		a.CIDRs = append(a.CIDRs, egress.CIDRRule{CIDR: prefix, Port: rule.Port, HTTP: rule.HTTP})
	}

	return a, nil
}

// egressAllowlistRoutes returns the egress allowlist rows of the route table
// (each route declares its action and object): a read and a change. Nil
// service means no agent store is
// configured: no route is mounted. Every route needs
// server:manage-settings on @server.
func (h *Handler) egressAllowlistRoutes() []Route {
	if h.agent == nil {
		return nil
	}

	return []Route{
		authorizedRoute(
			http.MethodGet, egressAllowlistPath, []identity.Action{identity.ActionServerManageSettings},
			resolveServer, h.readEgressAllowlist,
		),
		authorizedRoute(
			http.MethodPut, egressAllowlistPath, []identity.Action{identity.ActionServerManageSettings},
			resolveServer, h.writeEgressAllowlist,
		),
	}
}

// readEgressAllowlist serves GET /api/server/egress-allowlist. It answers 200
// with the stored list, or the empty list when none was stored.
func (h *Handler) readEgressAllowlist(
	w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	a, err := h.agent.Allowlist(ctx, p)
	if err != nil {
		h.writeAgentRefusal(w, r, p, identity.ActionServerManageSettings, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, egressAllowlistOf(a))
}

// writeEgressAllowlist serves PUT /api/server/egress-allowlist. It answers
// 200 with the stored list. A body that lacks "hosts" or "cidrs" answers 400
// and changes no list.
func (h *Handler) writeEgressAllowlist(
	w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	body, readErr := readBodyUpTo(w, r, maxEgressAllowlistBodyBytes, errEgressAllowlistBody)
	if readErr != nil {
		h.writeAgentRefusal(w, r, p, identity.ActionServerManageSettings, readErr)

		return
	}

	var in egressAllowlistRequest
	if err := decodeBody(body, &in, errEgressAllowlistBody); err != nil {
		h.writeAgentRefusal(w, r, p, identity.ActionServerManageSettings, err)

		return
	}

	if in.Hosts == nil || in.CIDRs == nil {
		h.writeAgentRefusal(w, r, p, identity.ActionServerManageSettings, errEgressAllowlistBody)

		return
	}

	a, inputErr := readEgressAllowlistInput(in)
	if inputErr != nil {
		h.writeAgentRefusal(w, r, p, identity.ActionServerManageSettings, inputErr)

		return
	}

	// Defense in depth: the service and the store check the allowlist too;
	// this check stays so that a new caller cannot skip it.
	if err := agent.CheckAllowlist(a); err != nil {
		h.writeAgentRefusal(w, r, p, identity.ActionServerManageSettings, egressAllowlistInvalid(err))

		return
	}

	for _, rule := range a.CIDRs {
		// Defense in depth: the service and the store check the range of
		// each rule too; this check stays so that a new caller cannot skip
		// it.
		if err := checkEgressAllowlistRange(rule); err != nil {
			h.writeAgentRefusal(w, r, p, identity.ActionServerManageSettings, err)

			return
		}
	}

	if err := h.setEgressAllowlist(ctx, r, p, a); err != nil {
		h.writeAgentRefusal(w, r, p, identity.ActionServerManageSettings, err)

		return
	}

	stored, err := h.agent.Allowlist(ctx, p)
	if err != nil {
		h.writeAgentRefusal(w, r, p, identity.ActionServerManageSettings, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, egressAllowlistOf(stored))
}

// checkEgressAllowlistRange refuses a rule that starts in an address that no
// rule may open.
func checkEgressAllowlistRange(rule egress.CIDRRule) error {
	if err := egress.CheckAllowedRange(rule.CIDR); err != nil {
		return egressAllowlistInvalid(err)
	}

	return nil
}

// setEgressAllowlist replaces the stored allowlist with a. It maps a refused
// allowlist to the coded error of the route.
func (h *Handler) setEgressAllowlist(
	ctx context.Context, r *http.Request, p identity.Principal, a egress.Allowlist,
) error {
	err := h.agent.SetAllowlist(ctx, p, callerEntry(r, p, identity.ActionServerManageSettings), a)
	if err == nil {
		return nil
	}

	if errors.Is(err, agent.ErrInvalid) || errors.Is(err, egress.ErrRangeTooWide) {
		return egressAllowlistInvalid(err)
	}

	return err
}
