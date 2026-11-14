// Package refusal writes the audit entry of a request that the Authorizer
// refuses. The tool adapters check their routes through Recorder.Authorize, so
// a refusal writes its entry the same way for every tool.
package refusal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/faultlog"
	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// channelHeader names the request header that tells the channel of a request.
const channelHeader = "Dustpan-Channel"

// ErrNoStore reports that New got no audit store. A recorder with no store
// would let a refusal pass with no entry, so New fails closed.
var ErrNoStore = errors.New("refusal recorder needs an audit store")

// errNoServerStore reports that the audit store cannot write the scope of the
// installation.
var errNoServerStore = errors.New("audit store has no installation scope")

// errNoNamespaces reports a Recorder that has no namespace reader and an entry
// for a namespace.
var errNoNamespaces = errors.New("recorder has no namespace reader")

// Namespaces reads a namespace to take its fence. The namespace service
// implements it.
type Namespaces interface {
	Get(ctx context.Context, name namespace.Name) (namespace.Namespace, error)
}

// Bounds of the text that an entry takes from a request. Each value is cut
// after it is cleaned.
const (
	maxSubjectBytes = 255
	maxObjectBytes  = 512
	maxRouteBytes   = 200
)

// Reasons that the middleware writes. A caller of Record in a domain package
// gives its own reason.
const (
	// ReasonPolicy is the reason of a refusal by the Authorizer.
	ReasonPolicy = "denied by policy"
	// ReasonFailed is the reason when the Authorizer returned an error that is
	// not a refusal. The entry never holds the text of that error.
	ReasonFailed = "authorization failed"
)

// unknownScope is the scope in the key of the limiter for a namespace that does
// not exist. A caller can name any namespace, so all such names count as one.
const unknownScope = "@unknown"

// writeTimeout bounds the append of the entry of one refusal, and, apart from
// it, the append of the flush entries that the refusal writes after it. The
// appends run after the request context is done too, so a client that closes
// the connection cannot make an entry get lost.
const writeTimeout = 5 * time.Second

// suppressedSubject is the subject of the entries that count denials which
// no entry of their own carries.
const suppressedSubject = "denial-limiter"

// Recorder asks the Authorizer and writes one audit entry of the outcome
// denied for each refusal, within the bound of its limiter: for one principal,
// action, object kind and namespace it writes the first refusal of each
// window, and counts the others. A Recorder always has a store: New refuses
// a nil store, so no refusal goes unrecorded.
type Recorder struct {
	authz      identity.Authorizer
	store      audit.Store
	namespaces Namespaces
	logger     *slog.Logger
	onFailure  func()
	limiter    *denylimit.Limiter
	surface    string
}

// Denial is what a refusal was about: the action, the object and the reason.
type Denial struct {
	Action identity.Action
	Object identity.Object
	// Reason says why, in a short sentence.
	Reason string
	// ReasonWithin returns the reason inside limit bytes, when the reason has
	// a part that can get shorter without losing its meaning. A reach reason
	// uses it, so that the route and the count of suppressed refusals stay and
	// only the list of actions gets shorter. Nil keeps Reason unchanged apart
	// from the cut.
	ReasonWithin func(limit int) string
	// Window is the window of the limiter for this refusal. Zero means the
	// window of the limiter of the Recorder. A caller uses one window for each
	// action and object kind.
	Window time.Duration
}

// WithLimiter sets the limiter and returns r. The default is a limiter with
// the default bounds of package denylimit. A nil limiter keeps the default.
func (r *Recorder) WithLimiter(l *denylimit.Limiter) *Recorder {
	if l != nil {
		r.limiter = l
	}

	return r
}

// WithSurface names the adapter of r and returns r. The name is part of the
// key of the limiter, so the refusals of two adapters of one tool (the
// TFE-compatible API and the OpenTofu HTTP backend) in one window write one
// entry each. The names are fixed in code, so the keys stay bounded.
func (r *Recorder) WithSurface(name string) *Recorder {
	r.surface = Clean(name, maxSubjectBytes)

	return r
}

// New returns a Recorder. OnFailure runs when an append fails. Nil means no
// counter. It returns ErrNoStore when store is nil.
func New(
	authz identity.Authorizer,
	store audit.Store,
	namespaces Namespaces,
	logger *slog.Logger,
	onFailure func(),
) (*Recorder, error) {
	// Defense in depth: every adapter and NewServer check for a nil store
	// too; this check stays so that a new caller cannot build a recorder
	// that drops refusals.
	if store == nil {
		return nil, ErrNoStore
	}

	return &Recorder{
		authz:      authz,
		store:      store,
		namespaces: namespaces,
		logger:     logger,
		onFailure:  onFailure,
		surface:    "",
		limiter: denylimit.New(
			denylimit.Config{Window: 0, MaxKeys: 0, MaxFlush: 0, Now: nil, MaxKeysPerPrincipal: 0},
		),
	}, nil
}

// Authorize asks the Authorizer whether p may do action on obj, and returns
// its answer. A refusal of an authenticated principal writes an entry before
// Authorize returns, unless the limiter holds it back. The entry records the
// principal, the action, the object, the route and the reason, never the
// request body or a header value. An error of the Authorizer that is not a
// refusal gets an entry too, with the reason "authorization failed". A failed
// append logs and counts the failure. It does not change the answer: the
// error is still returned, and a refusal never turns into a success.
func (r *Recorder) Authorize(
	ctx context.Context, req *http.Request, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	err := r.authz.Authorize(ctx, p, action, obj)
	if err == nil {
		return nil
	}

	reason := ReasonFor(err)

	r.Record(ctx, req, p, Denial{Action: action, Object: obj, Reason: reason, ReasonWithin: nil, Window: 0})

	return err
}

// Decide asks the Authorizer whether p may do action on obj, and returns its
// answer. It writes no entry. The caller must record each refusal that it
// returns to the client with Record, and give the entry the reason that
// ReasonFor returns, as Authorize does.
func (r *Recorder) Decide(
	ctx context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	return r.authz.Authorize(ctx, p, action, obj)
}

// ReasonFor returns the reason of the entry for a refusal of the Authorizer.
// A request that the Authorizer could not decide is refused too. It gets an
// entry with a fixed reason, never the text of the error.
func ReasonFor(err error) string {
	if errors.Is(err, identity.ErrDenied) {
		return ReasonPolicy
	}

	return ReasonFailed
}

// Record writes the entry of a refusal that the caller decided, for example a
// domain package that refuses a lock release by a principal that is not the
// holder. Reason says why, in a short sentence. The limiter, the cleaning of
// the text and the handling of a failed append are the same as for Authorize.
// Record never returns an error: the caller returns its own refusal.
func (r *Recorder) Record(ctx context.Context, req *http.Request, p identity.Principal, d Denial) {
	action, obj := d.Action, d.Object

	// A guest has no subject. The adapters authenticate a caller before they
	// check its routes, so a guest reaches here only through a bug, and it
	// writes nothing.
	if p.Subject == "" {
		return
	}

	// The writes outlive the request: a canceled request context must not lose
	// the entry of the refusal. The timeout bounds them.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	defer cancel()

	// An object with no namespace (the installation, a person) lands in the
	// installation scope.
	scope := obj.Namespace()
	if obj.IsServer() || scope == "" {
		scope = audit.ServerScope
	}

	target := r.resolve(ctx, scope)
	subject := Clean(p.Subject, maxSubjectBytes)

	decision := r.limiter.AllowWindow(denylimit.Key{
		Principal: subject,
		Action:    string(action),
		Kind:      r.keyKind(obj),
		Scope:     target.key,
	}, d.Window)

	// The counts of earlier windows go out after the entry of this refusal,
	// so a slow store cannot use up the time of that entry.
	defer r.flushAfter(ctx, decision.Flush)

	if !decision.Write {
		return
	}

	if decision.Overflow {
		r.write(ctx, overflowEntry(decision.Suppressed))

		return
	}

	entry := deniedEntry(ctx, req, p, d, scope)
	entry.Reason = reasonOf(req, d, decision.Suppressed)

	r.writeTarget(ctx, target, entry)
}

// Flush writes the counts that the limiter still holds, as entries of their
// own, and empties the limiter. The server calls it once at shutdown, with a
// context that has a short timeout. Flush returns after the writes ended or
// ctx is done.
func (r *Recorder) Flush(ctx context.Context) {
	r.writeAll(ctx, flushEntries(r.limiter.Drain()))
}

// flushAfter writes the entries that carry the counts of pending, with a
// timeout of its own. It does not use the deadline of ctx, so the entry of
// the refusal and the flush entries each get the full writeTimeout.
func (r *Recorder) flushAfter(ctx context.Context, pending []denylimit.Pending) {
	if len(pending) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	defer cancel()

	r.writeAll(ctx, flushEntries(pending))
}

// writeAll appends each entry, and stops when ctx is done.
func (r *Recorder) writeAll(ctx context.Context, entries []audit.Entry) {
	for i := range entries {
		if ctx.Err() != nil {
			return
		}

		r.write(ctx, entries[i])
	}
}

// target is the scope of one refusal: the key for the limiter, and the
// namespace when the scope is one that exists.
type target struct {
	key string
	// ns is set when the scope is a namespace that exists.
	ns *namespace.Namespace
	// unknown is true when the namespace does not exist.
	unknown bool
}

// resolve reads the namespace of scope once. The key of a namespace that does
// not exist is one fixed value, so the names that a caller makes up do not make
// keys.
func (r *Recorder) resolve(ctx context.Context, scope namespace.Name) target {
	t := target{key: scope.String(), ns: nil, unknown: false}

	if scope == audit.ServerScope || r.namespaces == nil {
		return t
	}

	ns, err := r.namespaces.Get(ctx, scope)

	switch {
	case errors.Is(err, namespace.ErrNotFound):
		t.key, t.unknown = unknownScope, true
	case err == nil:
		t.ns = &ns
	default:
		// Another error: the append reads the namespace again and reports it.
		// The raw scope is request text, so the key holds a cleaned, bounded copy.
		t.key = Clean(scope.String(), maxSubjectBytes)
	}

	return t
}

// writeTarget appends entry in the scope that resolve found.
func (r *Recorder) writeTarget(ctx context.Context, t target, entry audit.Entry) {
	switch {
	case t.unknown:
		r.write(ctx, unknownEntry(entry))
	case t.ns != nil:
		if _, err := r.store.Append(ctx, t.ns.Fence(), entry); err != nil {
			r.fail(ctx, fmt.Errorf("append entry: %w", err))
		}
	default:
		r.write(ctx, entry)
	}
}

// unknownEntry moves entry to the installation scope and says that the
// namespace it named does not exist.
func unknownEntry(entry audit.Entry) audit.Entry {
	entry.Reason = Clean(entry.Reason+" (namespace "+Clean(entry.Namespace.String(), maxSubjectBytes)+
		" does not exist)", audit.MaxReasonLen)
	entry.Namespace = audit.ServerScope

	return entry
}

// write appends entry in its scope, and logs and counts a failure.
func (r *Recorder) write(ctx context.Context, entry audit.Entry) {
	if err := r.append(ctx, entry); err != nil {
		r.fail(ctx, err)
	}
}

// fail logs and counts an append that failed.
func (r *Recorder) fail(ctx context.Context, err error) {
	faultlog.LogFault(ctx, r.logger, "audit append failed for a refused request", faultlog.Cause(err), "refusal.audit")

	if r.onFailure != nil {
		r.onFailure()
	}
}

// append writes entry in its namespace, or in the installation scope. A
// namespace that does not exist (a caller can name any) falls back to the
// installation scope, so the refusal is not lost.
func (r *Recorder) append(ctx context.Context, entry audit.Entry) error {
	if entry.Namespace == audit.ServerScope {
		return r.appendServer(ctx, entry)
	}

	if r.namespaces == nil {
		return errNoNamespaces
	}

	ns, err := r.namespaces.Get(ctx, entry.Namespace)
	if errors.Is(err, namespace.ErrNotFound) {
		return r.appendServer(ctx, unknownEntry(entry))
	}

	if err != nil {
		return fmt.Errorf("read namespace: %w", err)
	}

	if _, err := r.store.Append(ctx, ns.Fence(), entry); err != nil {
		return fmt.Errorf("append entry: %w", err)
	}

	return nil
}

// appendServer writes entry in the installation scope.
func (r *Recorder) appendServer(ctx context.Context, entry audit.Entry) error {
	ss, ok := r.store.(audit.ServerStore)
	if !ok {
		return errNoServerStore
	}

	if _, err := ss.AppendServer(ctx, entry); err != nil {
		return fmt.Errorf("append installation entry: %w", err)
	}

	return nil
}

// keyKind returns the kind in the key of the limiter: the kind of obj, after
// the surface of r when it has one.
func (r *Recorder) keyKind(obj identity.Object) string {
	if r.surface == "" {
		return objectKind(obj)
	}

	return r.surface + "/" + objectKind(obj)
}

// objectKind names the kind of obj for the key of the limiter.
func objectKind(obj identity.Object) string {
	switch {
	case obj.IsServer():
		return "server"
	case obj.IsUnit():
		// The tool is in the kind: a stack and a workspace in one namespace
		// are different objects. The set of tools is fixed, so the keys stay
		// bounded; Clean cuts the text in any case.
		return "unit:" + Clean(obj.Tool(), maxSubjectBytes)
	case obj.IsCollection():
		return "collection"
	case obj.IsPrincipal():
		return "principal"
	case obj.IsCaller():
		return "caller"
	default:
		return "namespace"
	}
}

// reasonOf returns the reason text: the cleaned reason and route, and the
// number of refusals that the limiter held back before this entry. The route
// and the count come first in the budget, so a long reason gets shorter and
// they stay.
func reasonOf(req *http.Request, d Denial, suppressed int) string {
	tail := ""

	if route := routeOf(req); route != "" {
		tail += " on " + route
	}

	if suppressed > 0 {
		tail += fmt.Sprintf("; %d further refusals since the last entry", suppressed)
	}

	limit := max(0, audit.MaxReasonLen-len(tail))

	var reason string
	if d.ReasonWithin != nil {
		reason = Clean(d.ReasonWithin(limit), limit)
	} else {
		reason = Clean(d.Reason, limit)
	}

	return Clean(reason+tail, audit.MaxReasonLen)
}

// routeOf returns the route of req: the matched pattern when the mux set one,
// else the method and the path. It never holds the query or a header value.
func routeOf(req *http.Request) string {
	if req == nil {
		return ""
	}

	if req.Pattern != "" {
		return Clean(req.Pattern, maxRouteBytes)
	}

	if req.URL == nil {
		return Clean(req.Method, maxRouteBytes)
	}

	return Clean(req.Method+" "+req.URL.Path, maxRouteBytes)
}

// flushEntries returns the entries that carry the counts of pending.
func flushEntries(pending []denylimit.Pending) []audit.Entry {
	out := make([]audit.Entry, 0, len(pending))
	for _, p := range pending {
		out = append(out, flushEntry(p))
	}

	return out
}

// flushEntry returns the entry that carries the count of refusals of one key
// that the limiter held back in a window that ended. The count of a namespace
// that does not exist, and the count of the aggregate key of a principal, go in
// the installation scope.
func flushEntry(pending denylimit.Pending) audit.Entry {
	if pending.Overflow {
		return overflowEntry(pending.Suppressed)
	}

	scope := namespace.Name(pending.Key.Scope)
	if pending.Key.Scope == unknownScope || pending.Key.Scope == denylimit.AggregateMark {
		scope = audit.ServerScope
	}

	e := baseEntry(scope, pending.Key.Principal, pending.Key.Action)
	e.Object = pending.Key.Kind
	e.Reason = fmt.Sprintf("%d further refusals in the last window were not recorded one by one", pending.Suppressed)

	return e
}

// overflowEntry returns the entry that stands for the refusals of principals
// that the limiter had no room to track.
func overflowEntry(suppressed int) audit.Entry {
	e := baseEntry(audit.ServerScope, suppressedSubject, "denials:suppressed")
	e.Object = "denials of untracked principals"
	e.Reason = fmt.Sprintf("%d refusals were not recorded one by one: too many principals in one window", suppressed)

	return e
}

// baseEntry returns a denied entry with no request data.
func baseEntry(scope namespace.Name, subject, action string) audit.Entry {
	return audit.Entry{
		At:            time.Now().UTC(),
		Namespace:     scope,
		Subject:       subject,
		IDPSubject:    "",
		TokenID:       "",
		Action:        action,
		Object:        "",
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "",
		UserAgent:     "",
		Channel:       audit.NormalizeChannel(""),
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeDenied,
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

// deniedEntry returns the entry of one refusal, with the outcome denied. The
// text of the principal and the object is cleaned and bounded.
func deniedEntry(
	ctx context.Context, req *http.Request, p identity.Principal, d Denial, scope namespace.Name,
) audit.Entry {
	action, obj := d.Action, d.Object

	// A caller of Record may have no request. The entry then has no address (clientip.String accepts nil),
	// no user agent and the default channel. A request that the session
	// cookie signed in is the console, whatever its channel header says, as
	// in every other audit entry of a request.
	userAgent, channel := "", audit.NormalizeChannel("")
	if req != nil {
		userAgent, channel = apidesc.Header(req, "User-Agent"), sessionauth.AuditChannel(req, channelHeader)
	}

	return audit.Entry{
		At:            time.Now().UTC(),
		Namespace:     scope,
		Subject:       Clean(p.Subject, maxSubjectBytes),
		IDPSubject:    "",
		TokenID:       Clean(p.TokenID, maxSubjectBytes),
		Action:        string(action),
		Object:        Clean(obj.String(), maxObjectBytes),
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      clientip.String(req),
		UserAgent:     Clean(audit.TruncateUserAgent(userAgent), audit.MaxUserAgentLen),
		Channel:       channel,
		Kind:          kindOf(action),
		Outcome:       audit.OutcomeDenied,
		RequestID:     requestid.FromContext(ctx),
		ObjectID:      Clean(obj.UnitID(), maxSubjectBytes),
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: principalKind(p.Kind),
		JobID:         Clean(p.JobID, audit.MaxJobIDLen),
	}
}

// kindOf returns the audit kind of the entry for the refused action.
func kindOf(action identity.Action) audit.Kind {
	switch {
	case action == identity.ActionUnitReadSecrets:
		return audit.KindReveal
	case action == identity.ActionUnitWriteForced, action == identity.ActionUnitWriteUnlocked:
		return audit.KindOverride
	case strings.HasSuffix(string(action), ":read"):
		return audit.KindToolOperation
	default:
		return audit.KindChange
	}
}

// principalKind returns the kind of the principal for the denied entry. The
// audit log refuses an entry with a kind that it does not know, so an unknown
// kind is recorded as no kind. The denial is still written.
//
// Defense in depth: audit.Entry.Validate refuses an unknown kind too; this
// allowlist stays so that a principal from an authenticator outside this
// repository cannot make the denial entry fail to append.
func principalKind(k identity.Kind) string {
	if k.Known() {
		return string(k)
	}

	return ""
}
