package dustpan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/setting"
)

// allowlistAuditTimeout bounds the write of a denial entry. The write
// outlives the request, so a canceled request never loses its entry.
const allowlistAuditTimeout = 5 * time.Second

// allowScope is the scope of an allowlist that refused a request.
type allowScope string

// The scopes of an allowlist.
const (
	allowScopeInstallation allowScope = "installation"
	allowScopeNamespace    allowScope = "namespace"
)

// allowDenial is one refusal: the scope that refused and the recorded client
// address.
type allowDenial struct {
	Scope   allowScope
	Address netip.Addr
}

// allowDecision is the answer of the checker: denied, with the scope that
// refused.
type allowDecision struct {
	Scope  allowScope
	Denied bool
	// Unavailable says that the list could not be read because the database
	// does not answer. The request is denied, but no address was refused, so
	// the caller answers 503 and writes no denial entry.
	Unavailable bool
}

// allowChecker holds the parsed lists of one request. The installation list
// is checked first, then the namespace list. A nil namespace list means that
// the namespace did not opt in. The checker fails closed on an unknown
// address.
type allowChecker struct {
	enabled      bool
	installation ipallow.List
	namespace    *ipallow.List
	record       func(context.Context, allowDenial)
	// unavailable is true when the read of the list failed because the
	// database does not answer.
	unavailable bool
}

// newAllowChecker returns a checker for the lists. record writes the denial
// entry; nil records nothing.
func newAllowChecker(
	enabled bool,
	installation ipallow.List,
	perNamespace *ipallow.List,
	record func(context.Context, allowDenial),
) *allowChecker {
	return &allowChecker{
		enabled: enabled, installation: installation, namespace: perNamespace, record: record, unavailable: false,
	}
}

// check answers whether addr is outside an opted-in list. It checks the
// installation list before the namespace list, so a refusal names the first
// scope that refused.
func (c *allowChecker) check(ctx context.Context, addr netip.Addr) allowDecision {
	if c == nil || !c.enabled {
		return allowDecision{Scope: "", Denied: false, Unavailable: false}
	}

	// The list is unknown, so the request is denied (fail closed). No client
	// address was refused, so the decision writes no denial entry.
	if c.unavailable {
		return allowDecision{Scope: "", Denied: true, Unavailable: true}
	}

	steps := []struct {
		scope allowScope
		list  *ipallow.List
	}{
		{scope: allowScopeInstallation, list: &c.installation},
		{scope: allowScopeNamespace, list: c.namespace},
	}

	for _, step := range steps {
		if step.list == nil || step.list.Contains(addr) {
			continue
		}

		if c.record != nil {
			c.record(ctx, allowDenial{Scope: step.scope, Address: addr})
		}

		return allowDecision{Scope: step.scope, Denied: true, Unavailable: false}
	}

	return allowDecision{Scope: "", Denied: false, Unavailable: false}
}

// allowlistGate reads the installation list from the settings resource on
// each request and records a refusal. A nil settings service means the server
// has no settings resource, so the installation list is off.
type allowlistRequestGate struct {
	settings *setting.Service
	audit    audit.ServerStore
	limiter  *denylimit.Limiter
	now      func() time.Time
	logger   *slog.Logger
}

// newAllowlistRequestGate builds the installation list gate of cfg. It
// answers the audit store of the installation scope.
func newAllowlistRequestGate(cfg *settings) (allowlistRequestGate, error) {
	serverAudit, err := serverAuditOf(cfg.namespaceStore)
	if err != nil {
		return allowlistRequestGate{}, err
	}

	return allowlistGateOf(cfg, serverAudit), nil
}

// allowlistGateOf returns the gate of cfg over the audit store of the server
// scope. The gate has its own limiter, never the shared denial limiter.
func allowlistGateOf(cfg *settings, serverAudit audit.ServerStore) allowlistRequestGate {
	return allowlistRequestGate{
		settings: newSettingService(cfg, cfg.namespaceStore),
		audit:    serverAudit,
		limiter:  cfg.allowlistLimiter,
		now:      cfg.now,
		logger:   cfg.logger,
	}
}

// checker returns the checker of one request, or nil when the request uses
// no credential and starts no sign-in. When a settings read or the parse of
// the stored list fails, the checker fails closed: it denies with an empty
// installation list.
func (g allowlistRequestGate) checker(ctx context.Context, r *http.Request) *allowChecker {
	if g.settings == nil || !allowlistRequest(r) {
		return nil
	}

	record := func(ctx context.Context, denial allowDenial) {
		allowlistAudit{store: g.audit, limiter: g.limiter, now: g.now, logger: g.logger}.record(ctx, r, denial)
	}

	enabled, err := g.settings.Effective(ctx, setting.InstallationScope(), ipallow.SettingEnabled)
	if err != nil {
		g.logger.WarnContext(ctx, "read the IP allowlist setting", slog.String("setting", ipallow.SettingEnabled))

		return failedReadChecker(err, record)
	}

	if enabled.Value != "true" {
		return newAllowChecker(false, ipallow.List{}, nil, record)
	}

	installation, err := g.settings.Effective(ctx, setting.InstallationScope(), ipallow.SettingInstallation)
	if err != nil {
		g.logger.WarnContext(ctx, "read the IP allowlist setting", slog.String("setting", ipallow.SettingInstallation))

		return failedReadChecker(err, record)
	}

	return g.installationChecker(ctx, installation.Value, record)
}

// failedReadChecker returns the checker for a failed read of an allowlist
// setting. It denies in each case. When the database does not answer, the
// decision also says so, so that the request gets a 503 that the client can
// retry, not a 403 that names an address that no list refused.
//
// Defense in depth: the denial does not rely on the answer of the caller. The
// checker denies even when a caller ignores the unavailable flag.
func failedReadChecker(err error, record func(context.Context, allowDenial)) *allowChecker {
	checker := newAllowChecker(true, ipallow.List{}, nil, record)
	checker.unavailable = errors.Is(err, namespace.ErrUnavailable)

	return checker
}

// installationChecker returns the checker of the stored installation list
// value. A value that does not decode fails closed: the checker denies with an
// empty installation list.
//
// Defense in depth: the settings layer already ignores a stored value that its
// check refuses, and the API refuses to store one. This check stays so that a
// value from another source, for example a restored backup, cannot open the
// list.
func (g allowlistRequestGate) installationChecker(
	ctx context.Context, value string, record func(context.Context, allowDenial),
) *allowChecker {
	list, err := ipallow.DecodeList(value)
	if err != nil {
		g.logger.WarnContext(ctx, "parse the IP allowlist", slog.String("setting", ipallow.SettingInstallation))

		return newAllowChecker(true, ipallow.List{}, nil, record)
	}

	return newAllowChecker(true, list, nil, record)
}

// allowlistRequest reports whether a request uses a credential or starts a
// sign-in. A health probe, a static asset and a public page use neither, so
// the installation list does not stop them.
func allowlistRequest(r *http.Request) bool {
	// A probe answers from the process state and shows no data. It stays
	// outside the list even with a credential, so that a stopped database
	// cannot turn a readiness answer of 503 into a 403.
	if healthProbeRequest(r) {
		return false
	}

	if r.Header.Get("Authorization") != "" {
		return true
	}

	if strings.HasPrefix(r.URL.Path, "/api/dustpan/v1/sign-in/") ||
		strings.HasPrefix(r.URL.Path, "/api/server/sign-in/") {
		return true
	}

	for _, cookie := range r.Cookies() {
		switch cookie.Name {
		case apidesc.SessionCookie, session.AccessCookie, session.RefreshCookie:
			return true
		default:
		}
	}

	return false
}

// healthProbeRequest reports whether r is a request for a health probe: the
// live probe, the ready probe or the ready probe of one component. It reads
// the decoded path and the escaped path, and both must be a clean probe path
// in exact case, so an encoded slash such as /health%2Fready is no probe.
// The metrics route is no probe.
//
// Defense in depth: the escaped-path check decides, because an encoded form
// of the path is the one that differs from the decoded one. The decoded-path
// check stays so that a change of the escaped-path rules cannot let in a path
// that decodes to a non-probe. It does not rely on the check of the route or
// on the handler.
func healthProbeRequest(r *http.Request) bool {
	return healthProbePath(r.URL.Path) && healthProbePath(r.URL.EscapedPath())
}

// healthProbePath reports whether p is a clean health probe path.
func healthProbePath(p string) bool {
	return workerOnlyPath(p) && p != metricsPath
}

// allowlistAudit writes the denial entries of the installation scope.
type allowlistAudit struct {
	store   audit.ServerStore
	limiter *denylimit.Limiter
	now     func() time.Time
	logger  *slog.Logger
}

// record writes the entry of one denial, within the bound of the limiter. The
// key of the limiter is the scope that refused and the client address, so a
// client outside the list writes one entry for each window and each later
// refusal only adds to the count that the next entry carries. The limiter
// limits the entry only: the caller answers the 403 in each case. A failed
// append writes one WARN line and gives the write back to the limiter.
//
// Defense in depth: the list check does not rely on a rate limit of the
// listener or a proxy to keep the audit log small.
func (a allowlistAudit) record(ctx context.Context, r *http.Request, denial allowDenial) {
	if a.store == nil {
		return
	}

	key := denylimit.Key{
		Principal: denial.Address.String(), Action: allowlistDenyAction,
		Kind: string(denial.Scope), Scope: string(audit.ServerScope),
	}

	decision := denylimit.Decision{Write: true, Suppressed: 0, Overflow: false, Flush: nil}
	if a.limiter != nil {
		decision = a.limiter.Allow(key)
	}

	a.writePendings(ctx, decision.Flush)

	if !decision.Write {
		return
	}

	entry := allowlistDenialEntry(a.now, r, denial)

	switch {
	case decision.Overflow:
		entry = a.overflowEntry(decision.Suppressed)
	case decision.Suppressed > 0:
		entry.Reason = fmt.Sprintf("%s; %d further refusals in the last window were not recorded one by one",
			entry.Reason, decision.Suppressed)
	default:
	}

	if !a.append(ctx, entry, denial.Scope) && a.limiter != nil {
		a.limiter.Release(key, decision)
	}
}

// flush writes the counts that the limiter still holds. The server calls it
// once at shutdown.
func (a allowlistAudit) flush(ctx context.Context) {
	if a.store == nil || a.limiter == nil {
		return
	}

	a.writePendings(ctx, a.limiter.Drain())
}

// writePendings writes the counts of pendings that carry the action of this
// gate, the aggregate mark of a client address, or the overflow mark. It skips
// the count of any other key, because the entry has the label of this gate.
//
// Defense in depth: the gate has its own limiter, so a foreign key does not
// get here; the action filter stays so that a shared limiter cannot give the
// count of another adapter the wrong label. The aggregate clause also stays:
// it holds if the count of keys for each address, or the start of the
// aggregate key, changes. The tests of the gate and its limiter do not rely
// on this filter.
func (a allowlistAudit) writePendings(ctx context.Context, pendings []denylimit.Pending) {
	for _, pending := range pendings {
		ours := pending.Key.Action == allowlistDenyAction || pending.Key.Action == denylimit.AggregateMark

		if !pending.Overflow && !ours {
			continue
		}

		a.append(ctx, a.pendingEntry(pending), allowScope(pending.Key.Kind))
	}
}

// pendingEntry returns the entry that carries a count of refusals that the
// limiter held back.
func (a allowlistAudit) pendingEntry(pending denylimit.Pending) audit.Entry {
	if pending.Overflow {
		return a.overflowEntry(pending.Suppressed)
	}

	scope := allowScope(pending.Key.Kind)
	if pending.Key.Kind == denylimit.AggregateMark {
		scope = ""
	}

	address, err := netip.ParseAddr(pending.Key.Principal)
	if err != nil {
		address = netip.Addr{}
	}

	entry := allowlistDenialEntry(a.now, nil, allowDenial{Scope: scope, Address: address})
	entry.Reason = fmt.Sprintf(
		"%d further refusals in the last window were not recorded one by one",
		pending.Suppressed,
	)

	return entry
}

// overflowEntry returns the entry that stands for the refusals of client
// addresses that the limiter had no room to track.
func (a allowlistAudit) overflowEntry(suppressed int) audit.Entry {
	entry := allowlistDenialEntry(a.now, nil, allowDenial{Scope: "", Address: netip.Addr{}})
	entry.Object = "refusals of untracked client addresses"
	entry.Reason = fmt.Sprintf(
		"%d refusals were not recorded one by one: too many client addresses in one window",
		suppressed,
	)

	return entry
}

// append writes entry and reports whether it was stored.
func (a allowlistAudit) append(ctx context.Context, entry audit.Entry, scope allowScope) bool {
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), allowlistAuditTimeout)
	defer cancel()

	if _, err := a.store.AppendServer(writeCtx, entry); err != nil {
		a.logger.WarnContext(ctx, "append the IP allowlist denial", slog.String("scope", string(scope)))

		return false
	}

	return true
}

// allowlistDenyAction is the audit action of a refusal by an IP allowlist.
const allowlistDenyAction = "installation:deny-ip-allowlist"

// allowlistDenialEntry returns the security entry of one refusal. The entry
// holds the scope that refused and the recorded client address, never the
// list or a credential.
func allowlistDenialEntry(now func() time.Time, r *http.Request, denial allowDenial) audit.Entry {
	at := time.Now()
	if now != nil {
		at = now()
	}

	userAgent, channel := "", audit.NormalizeChannel("")

	var requestID string

	if r != nil {
		userAgent = audit.TruncateUserAgent(apidesc.Header(r, "User-Agent"))
		channel = sessionauth.AuditChannel(r, "Dustpan-Channel")
		requestID = requestid.FromContext(r.Context())
	}

	return audit.Entry{
		At: at.UTC(), Namespace: audit.ServerScope, Subject: "anonymous", IDPSubject: "", TokenID: "",
		Action: allowlistDenyAction, Object: string(denial.Scope), Version: "", ValuePath: "",
		ValueCount: nil, Changes: nil, SourceIP: denial.Address.String(), UserAgent: userAgent, Channel: channel,
		Kind: audit.KindSecurity, Outcome: audit.OutcomeDenied, RequestID: requestID, ObjectID: "", PrevHash: "",
		Hash: "", Sequence: 0, FenceVersion: 0, Reason: "the client address is outside an IP allowlist",
		PrincipalKind: "", JobID: "", Origin: nil,
	}
}

// requestCaller finds the caller of a request after the installation list
// lets the client address in.
type requestCaller struct {
	authn identity.Authenticator
	allow allowlistRequestGate
}

// admission is the result of admit: the caller of the request, or a refusal
// that admit already answered.
type admission struct {
	caller   identity.Principal
	signedIn bool
	refused  bool
}

// admit checks the installation list before the credential is used, so a
// refused request writes no sign-in entry and reads no token. A refused
// request gets its 403 here. Otherwise admit answers the caller of the
// request and warns about a token that expires soon. A health probe path is
// outside the list and has no authentication: admit answers no caller for it
// and never reads a token.
func (c requestCaller) admit(
	w http.ResponseWriter, r *http.Request, addr netip.Addr, logger *slog.Logger,
) admission {
	ctx := r.Context()

	// A probe path is outside the list and also has no authentication. A
	// token from an address that the list refuses must not reach the token
	// store, where a successful check would update its last-used time.
	if healthProbeRequest(r) {
		var none identity.Principal

		return admission{caller: none, signedIn: false, refused: false}
	}

	if checker := c.allow.checker(ctx, r); checker != nil {
		if decision := checker.check(ctx, addr); decision.Denied {
			writeAllowlistDenial(ctx, w, logger, decision)

			var none identity.Principal

			return admission{caller: none, signedIn: false, refused: true}
		}
	}

	caller, signedIn := callerForRequest(ctx, c.authn, r)
	if signedIn {
		warnTokenExpiry(w, caller, time.Now())
	}

	return admission{caller: caller, signedIn: signedIn, refused: false}
}

// writeAllowlistDenial writes the answer of a denial. A refusal gets a 403
// with the code of the catalog and a message that names the scope that
// refused. The scope is one of the fixed values of allowScope, never text of
// the request. A stopped database gets a 503 that the client can retry.
func writeAllowlistDenial(ctx context.Context, w http.ResponseWriter, logger *slog.Logger, decision allowDecision) {
	writer := problem.Writer{Logger: logger, KeepDenied: nil, Classify: nil}

	// A database that does not answer is no refusal of an address: answer the
	// 503 of every other route, so the client tries again.
	if decision.Unavailable {
		writer.WriteProblem(ctx, w, problem.StoreUnavailable())

		return
	}

	writer.WriteProblem(ctx, w, problem.New(
		http.StatusForbidden, errcode.AccessIPNotAllowed,
		"the address is outside the IP allowlist of the "+string(decision.Scope),
	))
}
