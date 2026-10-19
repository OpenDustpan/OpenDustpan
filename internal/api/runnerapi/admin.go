package runnerapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// poolJSON is the pool in the management API. Durations are whole
// milliseconds, as in the protocol.
type poolJSON struct {
	ID             string             `json:"id"`
	Namespace      string             `json:"namespace"`
	Name           string             `json:"name"`
	Purpose        string             `json:"purpose"`
	Lease          protocol.Duration  `json:"lease"`
	Concurrency    int                `json:"concurrency"`
	Isolation      []string           `json:"isolation"`
	GitCredentials gitCredentialsJSON `json:"gitCredentials"`
	ReadOnly       bool               `json:"readOnly"`
	CreatedAt      time.Time          `json:"createdAt"`
	UpdatedAt      time.Time          `json:"updatedAt"`
}

type gitCredentialsJSON struct {
	Mode   string `json:"mode"`
	Helper string `json:"helper,omitempty"`
}

// poolRequest is the body of a pool create. Lease and Concurrency take the
// defaults of the pool when zero.
type poolRequest struct {
	Name           string             `json:"name"`
	Purpose        string             `json:"purpose"`
	Lease          protocol.Duration  `json:"lease"`
	Concurrency    int                `json:"concurrency"`
	Isolation      []string           `json:"isolation"`
	GitCredentials gitCredentialsJSON `json:"gitCredentials"`
}

func poolOut(p *runner.Pool) poolJSON {
	isolation := make([]string, 0, len(p.Isolation))
	for _, k := range p.Isolation {
		isolation = append(isolation, string(k))
	}

	mode := string(p.GitCredentials.Mode)
	if mode == "" {
		mode = string(runner.GitCredentialsNone)
	}

	return poolJSON{
		ID: p.ID, Namespace: p.Namespace.String(), Name: p.Name, Purpose: string(p.Purpose),
		Lease: protocol.FromStd(p.EffectiveLease()), Concurrency: p.EffectiveConcurrency(), Isolation: isolation,
		GitCredentials: gitCredentialsJSON{Mode: mode, Helper: p.GitCredentials.Helper},
		ReadOnly:       p.ReadOnly(), CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
}

// resolvePool finds the pool of the path in the namespace of the call. The
// path holds the ID or the name of the pool. A pool of another namespace is
// not found.
func (h *Handler) resolvePool(r *http.Request, c *call) (runner.Pool, error) {
	ctx := r.Context()
	ref := r.PathValue("pool")

	if strings.HasPrefix(ref, runner.PoolIDPrefix) {
		p, err := h.pools.Pool(ctx, ref)
		if errors.Is(err, runner.ErrNotFound) || (err == nil && p.Namespace != c.ns.Name) {
			return runner.Pool{}, errNotFound
		}

		if err != nil {
			return runner.Pool{}, fmt.Errorf("select pool: %w", err)
		}

		return p, nil
	}

	pools, err := h.pools.Pools(ctx, c.ns.Name)
	if err != nil {
		return runner.Pool{}, fmt.Errorf("list pools: %w", err)
	}

	for i := range pools {
		if pools[i].Name == ref {
			return pools[i], nil
		}
	}

	return runner.Pool{}, errNotFound
}

func (h *Handler) listPools(w http.ResponseWriter, r *http.Request, c *call) {
	pools, err := h.pools.Pools(r.Context(), c.ns.Name)
	if err != nil {
		h.writeError(r.Context(), w, ScopeAuthorized, err)

		return
	}

	out := make([]poolJSON, 0, len(pools))
	for i := range pools {
		out = append(out, poolOut(&pools[i]))
	}

	h.writeJSON(r.Context(), w, http.StatusOK, map[string]any{"pools": out})
}

func (h *Handler) getPool(w http.ResponseWriter, r *http.Request, c *call) {
	p, err := h.resolvePool(r, c)
	if err != nil {
		h.writeError(r.Context(), w, ScopeAuthorized, err)

		return
	}

	h.writeJSON(r.Context(), w, http.StatusOK, poolOut(&p))
}

// writable returns the fence of the namespace of the call, or an error when
// this region does not home the namespace.
func (h *Handler) writable(r *http.Request, c *call) (namespace.Fence, error) {
	ns, err := h.namespaces.WritableHere(r.Context(), c.ns.Name)
	if err != nil {
		return namespace.Fence{}, fmt.Errorf("namespace is writable here: %w", err)
	}

	return ns.Fence(), nil
}

// manageEntry returns the audit entry of a management change of the event.
func (h *Handler) manageEntry(
	r *http.Request,
	c *call,
	event runner.Event,
	objectID string,
	ch ...audit.Change,
) audit.Entry {
	rule := runner.AuditRules()[event]
	e := h.entry(r, c.ns.Name, c.principal.Subject, rule, h.now())
	e.TokenID = c.principal.TokenID
	e.PrincipalKind = string(c.principal.Kind)
	e.ObjectID = objectID
	e.Changes = append(
		[]audit.Change{{Key: changeEvent, Old: nil, New: string(event), Layer: "", TakesEffect: ""}},
		ch...,
	)

	return e
}

func change(key, value string) audit.Change {
	return audit.Change{Key: key, Old: nil, New: value, Layer: "", TakesEffect: ""}
}

func (h *Handler) createPool(w http.ResponseWriter, r *http.Request, c *call) {
	ctx := r.Context()

	var req poolRequest
	if err := decodeBody(c.body, &req); err != nil {
		h.writeError(ctx, w, ScopeAuthorized, err)

		return
	}

	isolation := make([]runner.Isolation, 0, len(req.Isolation))
	for _, k := range req.Isolation {
		isolation = append(isolation, runner.Isolation(k))
	}

	now := h.now()
	p := runner.Pool{
		ID:          "",
		Namespace:   c.ns.Name,
		Name:        req.Name,
		Purpose:     runner.Purpose(req.Purpose),
		Lease:       req.Lease.Std(),
		Concurrency: req.Concurrency,
		Isolation:   isolation,
		GitCredentials: runner.GitCredentials{
			Mode:   runner.GitCredentialMode(req.GitCredentials.Mode),
			Helper: req.GitCredentials.Helper,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	fence, err := h.writable(r, c)
	if err != nil {
		h.writeError(ctx, w, ScopeAuthorized, err)

		return
	}

	e := h.manageEntry(r, c, runner.EventPoolCreated, "", change("name", p.Name), change("purpose", req.Purpose))

	stored, err := h.pools.CreatePool(ctx, p, fence, []audit.Entry{e})
	if err != nil {
		h.writeError(ctx, w, ScopeAuthorized, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusCreated, poolOut(&stored))
}

// poolPatch is the body of a pool update. A field that the body leaves out
// keeps its value. A purpose that differs from the pool is refused.
type poolPatch struct {
	Name           *string             `json:"name"`
	Purpose        *string             `json:"purpose"`
	Lease          *protocol.Duration  `json:"lease"`
	Concurrency    *int                `json:"concurrency"`
	Isolation      *[]string           `json:"isolation"`
	GitCredentials *gitCredentialsJSON `json:"gitCredentials"`
}

// apply returns old with the fields of the patch, and the audit changes
// that name each field that the patch changes.
func (pp *poolPatch) apply(old *runner.Pool, at time.Time) (runner.Pool, []audit.Change) {
	next := *old
	next.UpdatedAt = at

	var changes []audit.Change

	note := func(key, before, after string) {
		if before != after {
			changes = append(changes, audit.Change{Key: key, Old: &before, New: after, Layer: "", TakesEffect: ""})
		}
	}

	if pp.Name != nil {
		next.Name = *pp.Name
		note("name", old.Name, next.Name)
	}

	if pp.Purpose != nil {
		next.Purpose = runner.Purpose(*pp.Purpose)
		note("purpose", string(old.Purpose), string(next.Purpose))
	}

	if pp.Lease != nil {
		next.Lease = pp.Lease.Std()
		note("lease", old.EffectiveLease().String(), next.EffectiveLease().String())
	}

	if pp.Concurrency != nil {
		next.Concurrency = *pp.Concurrency
		note("concurrency", strconv.Itoa(old.EffectiveConcurrency()), strconv.Itoa(next.EffectiveConcurrency()))
	}

	if pp.Isolation != nil {
		next.Isolation = make([]runner.Isolation, 0, len(*pp.Isolation))
		for _, k := range *pp.Isolation {
			next.Isolation = append(next.Isolation, runner.Isolation(k))
		}

		note("isolation", joinIsolation(old.Isolation), joinIsolation(next.Isolation))
	}

	if pp.GitCredentials != nil {
		next.GitCredentials = runner.GitCredentials{
			Mode:   runner.GitCredentialMode(pp.GitCredentials.Mode),
			Helper: pp.GitCredentials.Helper,
		}

		note("gitCredentials", gitCredentialsText(old.GitCredentials), gitCredentialsText(next.GitCredentials))
	}

	return next, changes
}

// joinIsolation returns the isolation kinds as a comma-separated list.
func joinIsolation(kinds []runner.Isolation) string {
	names := make([]string, 0, len(kinds))
	for _, k := range kinds {
		names = append(names, string(k))
	}

	return strings.Join(names, ",")
}

// gitCredentialsText returns the mode, and the helper when there is one.
func gitCredentialsText(g runner.GitCredentials) string {
	if g.Helper == "" {
		return string(g.Mode)
	}

	return string(g.Mode) + ":" + g.Helper
}

func (h *Handler) updatePool(w http.ResponseWriter, r *http.Request, c *call) {
	ctx := r.Context()

	var patch poolPatch
	if err := decodeBody(c.body, &patch); err != nil {
		h.writeError(ctx, w, ScopeAuthorized, err)

		return
	}

	old, err := h.resolvePool(r, c)
	if err != nil {
		h.writeError(ctx, w, ScopeAuthorized, err)

		return
	}

	fence, err := h.writable(r, c)
	if err != nil {
		h.writeError(ctx, w, ScopeAuthorized, err)

		return
	}

	next, changes := patch.apply(&old, h.now())
	e := h.manageEntry(r, c, runner.EventPoolChanged, old.ID, changes...)

	stored, err := h.pools.UpdatePool(ctx, next, fence, []audit.Entry{e})
	if err != nil {
		h.writeError(ctx, w, ScopeAuthorized, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, poolOut(&stored))
}

func (h *Handler) deletePool(w http.ResponseWriter, r *http.Request, c *call) {
	ctx := r.Context()

	p, err := h.resolvePool(r, c)
	if err != nil {
		h.writeError(ctx, w, ScopeAuthorized, err)

		return
	}

	fence, err := h.writable(r, c)
	if err != nil {
		h.writeError(ctx, w, ScopeAuthorized, err)

		return
	}

	e := h.manageEntry(r, c, runner.EventPoolDeleted, p.ID, change("name", p.Name))
	if err = h.pools.DeletePool(ctx, p.ID, fence, []audit.Entry{e}); err != nil {
		h.writeError(ctx, w, ScopeAuthorized, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// joinTokenJSON is a join token in the management API. It never holds the
// secret, except in the answer of the create.
type joinTokenJSON struct {
	ID        string     `json:"id"`
	Pool      string     `json:"pool"`
	CreatedBy string     `json:"createdBy"`
	CreatedAt time.Time  `json:"createdAt"`
	ExpiresAt time.Time  `json:"expiresAt"`
	UsedAt    *time.Time `json:"usedAt,omitempty"`
	UsedBy    string     `json:"usedBy,omitempty"`
	RevokedAt *time.Time `json:"revokedAt,omitempty"`
}

func optTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}

	return &t
}

func tokenOut(t *runner.JoinToken) joinTokenJSON {
	return joinTokenJSON{
		ID: t.ID, Pool: t.Pool, CreatedBy: t.CreatedBy, CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt,
		UsedAt: optTime(t.UsedAt), UsedBy: t.UsedBy, RevokedAt: optTime(t.RevokedAt),
	}
}

func (h *Handler) listJoinTokens(w http.ResponseWriter, r *http.Request, c *call) {
	p, err := h.resolvePool(r, c)
	if err != nil {
		h.writeError(r.Context(), w, ScopeAuthorized, err)

		return
	}

	tokens, err := h.pools.JoinTokens(r.Context(), p.ID)
	if err != nil {
		h.writeError(r.Context(), w, ScopeAuthorized, err)

		return
	}

	out := make([]joinTokenJSON, 0, len(tokens))
	for i := range tokens {
		out = append(out, tokenOut(&tokens[i]))
	}

	h.writeJSON(r.Context(), w, http.StatusOK, map[string]any{"joinTokens": out})
}

// createJoinTokenRequest is the body of a join token create. Ttl is the
// lifetime in milliseconds; zero takes one hour.
type createJoinTokenRequest struct {
	TTL protocol.Duration `json:"ttl"`
}

// createJoinTokenResponse holds the join token and its secret. The secret is
// in no other answer.
type createJoinTokenResponse struct {
	joinTokenJSON

	Token   string `json:"token"`
	Command string `json:"command"`
}

func (h *Handler) createJoinToken(w http.ResponseWriter, r *http.Request, c *call) {
	ctx := r.Context()

	var req createJoinTokenRequest
	if len(c.body) > 0 {
		if err := decodeBody(c.body, &req); err != nil {
			h.writeError(ctx, w, ScopeAuthorized, err)

			return
		}
	}

	ttl, err := runner.JoinTTL(req.TTL.Std())
	if err != nil {
		h.writeError(ctx, w, ScopeAuthorized, err)

		return
	}

	p, err := h.resolvePool(r, c)
	if err != nil {
		h.writeError(ctx, w, ScopeAuthorized, err)

		return
	}

	fence, err := h.writable(r, c)
	if err != nil {
		h.writeError(ctx, w, ScopeAuthorized, err)

		return
	}

	secret, err := newSecret(runner.JoinTokenPrefix)
	if err != nil {
		h.writeError(ctx, w, ScopeAuthorized, err)

		return
	}

	now := h.now()
	tok := runner.JoinToken{
		ID: "", Pool: p.ID, SHA256: sha256Hex(secret), CreatedBy: c.principal.Subject, CreatedAt: now,
		ExpiresAt: now.Add(ttl), UsedAt: time.Time{}, UsedBy: "", RevokedAt: time.Time{},
	}
	e := h.manageEntry(
		r,
		c,
		runner.EventJoinTokenCreated,
		p.ID,
		change("expiresAt", tok.ExpiresAt.UTC().Format(time.RFC3339)),
	)

	stored, err := h.pools.CreateJoinToken(ctx, tok, fence, []audit.Entry{e})
	if err != nil {
		h.writeError(ctx, w, ScopeAuthorized, err)

		return
	}

	args := runner.RejoinArgs("dustpan-runner", h.serverURL, p.ID, "<name>")

	h.writeJSON(ctx, w, http.StatusCreated, createJoinTokenResponse{
		joinTokenJSON: tokenOut(&stored), Token: secret, Command: strings.Join(args, " "),
	})
}

func (h *Handler) revokeJoinToken(w http.ResponseWriter, r *http.Request, c *call) {
	ctx := r.Context()

	p, err := h.resolvePool(r, c)
	if err != nil {
		h.writeError(ctx, w, ScopeAuthorized, err)

		return
	}

	id := r.PathValue("id")

	tokens, err := h.pools.JoinTokens(ctx, p.ID)
	if err != nil {
		h.writeError(ctx, w, ScopeAuthorized, err)

		return
	}

	found := false

	for i := range tokens {
		found = found || tokens[i].ID == id
	}

	if !found {
		h.writeError(ctx, w, ScopeAuthorized, errNotFound)

		return
	}

	fence, err := h.writable(r, c)
	if err != nil {
		h.writeError(ctx, w, ScopeAuthorized, err)

		return
	}

	e := h.manageEntry(r, c, runner.EventJoinTokenRevoked, p.ID, change("joinToken", id))
	if err = h.pools.RevokeJoinToken(ctx, id, fence, []audit.Entry{e}); err != nil {
		h.writeError(ctx, w, ScopeAuthorized, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// runnerJSON is a runner in the management API. It holds the ID of the key,
// never the key.
type runnerJSON struct {
	ID         string            `json:"id"`
	Pool       string            `json:"pool"`
	Name       string            `json:"name"`
	Kind       string            `json:"kind"`
	Isolation  string            `json:"isolation"`
	Labels     map[string]string `json:"labels"`
	Release    string            `json:"release"`
	Status     string            `json:"status"`
	Draining   bool              `json:"draining"`
	KeyID      string            `json:"keyID"`
	KeyExpires time.Time         `json:"keyExpires"`
	LastSeenAt time.Time         `json:"lastSeenAt"`
	JoinedAt   time.Time         `json:"joinedAt"`
	RejoinArgs []string          `json:"rejoinCommand,omitempty"`
}

func (h *Handler) runnerOut(rn *runner.Runner, now time.Time) runnerJSON {
	labels := rn.Labels
	if labels == nil {
		labels = map[string]string{}
	}

	out := runnerJSON{
		ID: rn.ID, Pool: rn.Pool, Name: rn.Name, Kind: string(rn.Kind), Isolation: string(rn.Isolation),
		Labels: labels, Release: rn.Release.String(), Status: string(rn.StatusAt(now)), Draining: rn.Draining,
		KeyID: rn.Key.ID(), KeyExpires: rn.Key.CreatedAt.Add(runner.MaxKeyAge), LastSeenAt: rn.LastSeenAt,
		JoinedAt: rn.JoinedAt, RejoinArgs: nil,
	}

	if rn.StatusAt(now) == runner.StatusMustRejoin {
		out.RejoinArgs = runner.RejoinArgs("dustpan-runner", h.serverURL, rn.Pool, rn.Name)
	}

	return out
}

func (h *Handler) listRunners(w http.ResponseWriter, r *http.Request, c *call) {
	p, err := h.resolvePool(r, c)
	if err != nil {
		h.writeError(r.Context(), w, ScopeAuthorized, err)

		return
	}

	runners, err := h.members.Runners(r.Context(), p.ID)
	if err != nil {
		h.writeError(r.Context(), w, ScopeAuthorized, err)

		return
	}

	now := h.now()
	out := make([]runnerJSON, 0, len(runners))

	for i := range runners {
		out = append(out, h.runnerOut(&runners[i], now))
	}

	h.writeJSON(r.Context(), w, http.StatusOK, map[string]any{"runners": out})
}

// resolveRunner finds the runner of the path in the namespace of the call.
func (h *Handler) resolveRunner(r *http.Request, c *call) (runner.Runner, error) {
	rn, err := h.members.Runner(r.Context(), r.PathValue("runner"))
	if errors.Is(err, runner.ErrNotFound) || (err == nil && rn.Namespace != c.ns.Name) {
		return runner.Runner{}, errNotFound
	}

	if err != nil {
		return runner.Runner{}, fmt.Errorf("select runner: %w", err)
	}

	return rn, nil
}

// drainRequest is the optional body of a drain. Without a body, the drain
// starts. {"draining": false} ends it.
type drainRequest struct {
	Draining *bool `json:"draining"`
}

func (h *Handler) drainRunner(w http.ResponseWriter, r *http.Request, c *call) {
	ctx := r.Context()

	var req drainRequest
	if len(c.body) > 0 {
		if err := decodeBody(c.body, &req); err != nil {
			h.writeError(ctx, w, ScopeAuthorized, err)

			return
		}
	}

	draining := req.Draining == nil || *req.Draining

	rn, err := h.resolveRunner(r, c)
	if err != nil {
		h.writeError(ctx, w, ScopeAuthorized, err)

		return
	}

	fence, err := h.writable(r, c)
	if err != nil {
		h.writeError(ctx, w, ScopeAuthorized, err)

		return
	}

	e := h.manageEntry(
		r,
		c,
		runner.EventRunnerDrained,
		rn.Pool,
		change("runner", rn.ID),
		change("draining", strconv.FormatBool(draining)),
	)
	if err = h.members.SetDraining(ctx, rn.ID, draining, fence, []audit.Entry{e}); err != nil {
		h.writeError(ctx, w, ScopeAuthorized, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) retireRunner(w http.ResponseWriter, r *http.Request, c *call) {
	ctx := r.Context()

	rn, err := h.resolveRunner(r, c)
	if err != nil {
		h.writeError(ctx, w, ScopeAuthorized, err)

		return
	}

	fence, err := h.writable(r, c)
	if err != nil {
		h.writeError(ctx, w, ScopeAuthorized, err)

		return
	}

	e := h.manageEntry(r, c, runner.EventRunnerRetired, rn.Pool, change("runner", rn.ID))
	if err = h.members.Retire(ctx, rn.ID, h.now(), fence, []audit.Entry{e}); err != nil {
		h.writeError(ctx, w, ScopeAuthorized, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}
