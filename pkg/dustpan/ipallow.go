package dustpan

import (
	"context"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/ipallow"
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
}

// newAllowChecker returns a checker for the lists. record writes the denial
// entry; nil records nothing.
func newAllowChecker(
	enabled bool,
	installation ipallow.List,
	namespace *ipallow.List,
	record func(context.Context, allowDenial),
) *allowChecker {
	return &allowChecker{enabled: enabled, installation: installation, namespace: namespace, record: record}
}

// check answers whether addr is outside an opted-in list. It checks the
// installation list before the namespace list, so a refusal names the first
// scope that refused.
func (c *allowChecker) check(ctx context.Context, addr netip.Addr) allowDecision {
	if c == nil || !c.enabled {
		return allowDecision{Scope: "", Denied: false}
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

		return allowDecision{Scope: step.scope, Denied: true}
	}

	return allowDecision{Scope: "", Denied: false}
}

// allowlistGate reads the installation list from the settings resource on
// each request and records a refusal. A nil settings service means the server
// has no settings resource, so the installation list is off.
type allowlistRequestGate struct {
	settings *setting.Service
	audit    audit.ServerStore
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

	return allowlistRequestGate{
		settings: newSettingService(cfg, cfg.namespaceStore),
		audit:    serverAudit,
		now:      cfg.now,
		logger:   cfg.logger,
	}, nil
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
		allowlistAudit{store: g.audit, now: g.now, logger: g.logger}.record(ctx, r, denial)
	}

	enabled, err := g.settings.Effective(ctx, setting.InstallationScope(), ipallow.SettingEnabled)
	if err != nil {
		g.logger.WarnContext(ctx, "read the IP allowlist setting", slog.String("setting", ipallow.SettingEnabled))

		return newAllowChecker(true, ipallow.List{}, nil, record)
	}

	if enabled.Value != "true" {
		return newAllowChecker(false, ipallow.List{}, nil, record)
	}

	installation, err := g.settings.Effective(ctx, setting.InstallationScope(), ipallow.SettingInstallation)
	if err != nil {
		g.logger.WarnContext(ctx, "read the IP allowlist setting", slog.String("setting", ipallow.SettingInstallation))

		return newAllowChecker(true, ipallow.List{}, nil, record)
	}

	list, err := ipallow.DecodeList(installation.Value)
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

// allowlistAudit writes the denial entries of the installation scope.
type allowlistAudit struct {
	store  audit.ServerStore
	now    func() time.Time
	logger *slog.Logger
}

// record writes one denial entry. It never changes the answer: a failed
// append writes one WARN line and the request stays refused.
func (a allowlistAudit) record(ctx context.Context, r *http.Request, denial allowDenial) {
	if a.store == nil {
		return
	}

	entry := allowlistDenialEntry(a.now, r, denial)

	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), allowlistAuditTimeout)
	defer cancel()

	if _, err := a.store.AppendServer(writeCtx, entry); err != nil {
		a.logger.WarnContext(ctx, "append the IP allowlist denial", slog.String("scope", string(denial.Scope)))
	}
}

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
		Action: "installation:deny-ip-allowlist", Object: string(denial.Scope), Version: "", ValuePath: "",
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
// request and warns about a token that expires soon.
func (c requestCaller) admit(
	w http.ResponseWriter, r *http.Request, addr netip.Addr, logger *slog.Logger,
) admission {
	ctx := r.Context()

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

// writeAllowlistDenial writes the 403 of a refusal: the code of the catalog
// and a message that names the scope that refused. The scope is one of the
// fixed values of allowScope, never text of the request.
func writeAllowlistDenial(ctx context.Context, w http.ResponseWriter, logger *slog.Logger, decision allowDecision) {
	problem.Writer{Logger: logger, KeepDenied: nil, Classify: nil}.WriteProblem(ctx, w, problem.New(
		http.StatusForbidden, errcode.AccessIPNotAllowed,
		"the address is outside the IP allowlist of the "+string(decision.Scope),
	))
}
