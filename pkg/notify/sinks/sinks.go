// Package sinks implements the use cases of notification sinks for the HTTP
// adapter: create, replace, delete, read and test. It adds the fence, the
// time, the IDs, the sealing of the secrets and the audit entry to the calls
// of notify.SinkStore. It needs neither the outbox nor the dispatcher: the
// test of a sink sends one fixed message through the notify.Sender of the
// sink kind.
package sinks

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
)

// ErrMissingDependency reports a Config without a required port.
var ErrMissingDependency = errors.New("missing dependency")

// ErrAccessToken reports an access token that the platform of the sink does
// not take, that the platform needs and the request lacks, or that is not a
// valid header value. It also matches notify.ErrInvalid. The error never holds
// the token.
var ErrAccessToken = errors.New("the access token is not valid for this notification destination")

// maxChatItems is the most new secret items of one change of a chat sink: the
// URL and the access token.
const maxChatItems = 2

// The fixed rules of ErrAccessToken, for a person.
const (
	accessRuleNone    = "This platform takes no access token: leave out accessToken."
	accessRuleNeeded  = "This platform needs an access token: send accessToken."
	accessRuleInvalid = "The access token has 1 to 4096 printable ASCII characters and no space."
	accessRuleKind    = "Only a chat destination takes an access token."
)

// accessTokenError is the error of a refused access token. It matches
// ErrAccessToken and notify.ErrInvalid, and it holds a fixed rule.
type accessTokenError struct{ rule string }

func (e accessTokenError) Error() string {
	return notify.ErrInvalid.Error() + ": " + ErrAccessToken.Error()
}

func (accessTokenError) Is(target error) bool {
	return target == ErrAccessToken || target == notify.ErrInvalid
}

// AccessTokenRule returns the fixed rule that err carries when err matches
// ErrAccessToken, and an empty string otherwise. The rule never holds the
// token.
func AccessTokenRule(err error) string {
	if te, ok := errors.AsType[accessTokenError](err); ok {
		return te.rule
	}

	return ""
}

// ErrNoSender reports a test of a sink whose kind has no sender on this
// server.
var ErrNoSender = errors.New("this server cannot send to this kind of notification destination")

// SendTimeout bounds the send of a test message.
const SendTimeout = 10 * time.Second

// signingKeyBytes is the size of the signing key of a webhook sink, before
// its hex form.
const signingKeyBytes = 32

// Namespaces reads the namespace that a write fences. namespace.Service
// implements it. WritableHere returns namespace.ErrNotHome for a namespace
// that another installation owns.
type Namespaces interface {
	Get(ctx context.Context, name namespace.Name) (namespace.Namespace, error)
	WritableHere(ctx context.Context, name namespace.Name) (namespace.Namespace, error)
}

// Config holds the dependencies of a Service.
type Config struct {
	Store      notify.SinkStore
	Sealer     notify.SinkSealer
	Namespaces Namespaces
	// Senders send the test message, one for each sink kind that this
	// server can send. A kind with no sender cannot be tested.
	Senders []notify.Sender
	// Ports are the ports that a sink may send to. The zero value allows the
	// port 443 only.
	Ports egress.Ports
	// Counter holds the shared count of the limits of the test route. Required:
	// the limits hold for all instances of one installation only when they
	// count in the database that the instances share.
	Counter authgate.FailureCounter
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
}

// Service implements the use cases of sinks.
type Service struct {
	store      notify.SinkStore
	sealer     notify.SinkSealer
	namespaces Namespaces
	senders    map[notify.SinkKind]notify.Sender
	ports      egress.Ports
	limiter    *testLimiter
	now        func() time.Time
}

// NewService returns a Service. It returns ErrMissingDependency when the
// store, the sealer or the namespace port is missing.
func NewService(cfg Config) (*Service, error) {
	if cfg.Store == nil || cfg.Namespaces == nil || cfg.Sealer.Namespaces == nil || cfg.Counter == nil {
		return nil, fmt.Errorf("%w: store, sealer, namespaces and counter", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	senders := make(map[notify.SinkKind]notify.Sender, len(cfg.Senders))
	for _, s := range cfg.Senders {
		senders[s.Kind()] = s
	}

	return &Service{
		store: cfg.Store, sealer: cfg.Sealer, namespaces: cfg.Namespaces, senders: senders, ports: cfg.Ports,
		limiter: newTestLimiter(cfg.Counter), now: now,
	}, nil
}

// Caller is the principal who calls a use case. By is the template of the
// audit entry: the caller fills where the request came from, and the service
// fills the action, the object and the time.
type Caller struct {
	Principal identity.Principal
	By        audit.Entry
}

// New describes a sink to make. For a chat sink, URL is the incoming webhook
// URL, Platform names the chat platform, and Target stays empty: the service
// derives it from the URL. For a webhook sink, Target is the https URL of the
// receiver: the service seals it as a secret item and keeps only its host as
// the target of the sink. For an email sink, Target is the address.
//
// AccessToken is the access token of a chat sink for a platform that needs
// one (see chat.AuthRenderer), and empty for every other sink. The service
// refuses a token for a platform that takes none, and no token for a platform
// that needs one.
type New struct {
	Kind        notify.SinkKind
	Platform    string
	Name        string
	Target      string
	URL         string
	AccessToken string
}

// Created is a sink that Create made. SigningKey is the signing key of a
// webhook sink. It is set once, here, and no other call answers it.
type Created struct {
	Sink       notify.Sink
	SigningKey string
}

// Create makes a sink in ns. It returns notify.ErrInvalid, namespace.ErrNotFound
// or namespace.ErrFenced.
func (s *Service) Create(ctx context.Context, c *Caller, ns namespace.Name, in New) (Created, error) {
	sink := notify.Sink{
		ID: ident.NewID(notify.SinkIDPrefix), Scope: ns, Kind: in.Kind, Name: in.Name, Platform: in.Platform,
		Target: in.Target, SecretRefs: nil, CreatedBy: c.Principal.Subject, Version: 0,
	}

	secrets, err := s.newSecrets(&sink, in)
	if err != nil {
		return Created{}, err
	}

	if portErr := s.checkPort(&sink, secrets); portErr != nil {
		return Created{}, portErr
	}

	// Defense in depth: the store validates the sink again, and the handler checks
	// the body. This check stays so that a refused sink costs no read of the
	// namespace and no sealing, and so that a store that skips its check cannot
	// take an invalid sink from this service.
	if err = sink.ValidateWrite(); err != nil {
		return Created{}, fmt.Errorf("create sink: %w", err)
	}

	sealed, err := s.sealer.Seal(ctx, sink, secrets)
	if err != nil {
		return Created{}, fmt.Errorf("create sink: %w", err)
	}

	fence, err := s.fence(ctx, ns)
	if err != nil {
		return Created{}, err
	}

	if err = s.store.PutSink(ctx, sink, sealed, fence, s.entry(c, ns, sink.ID, "create")); err != nil {
		return Created{}, fmt.Errorf("create sink: %w", err)
	}

	out := Created{Sink: sink, SigningKey: ""}
	out.Sink.Version = 1

	// A webhook sink has its URL first and its signing key second.
	if sink.Kind == notify.SinkWebhook && len(secrets) == 2 {
		out.SigningKey = string(secrets[1].Value)
	}

	return out, nil
}

// NewSigningKey returns a new signing key for a webhook sink: 32 bytes from
// the system random source, as 64 lowercase hex digits. A receiver uses the
// text as the key of notify.Verify.
func NewSigningKey() ([]byte, error) {
	var b [signingKeyBytes]byte

	if _, err := rand.Read(b[:]); err != nil {
		return nil, fmt.Errorf("make a signing key: %w", err)
	}

	return []byte(hex.EncodeToString(b[:])), nil
}

// Change describes a replace. Version is the version that the caller read.
// Name is the new name. Target, when set, is the new address of an email
// sink, or the new https URL of a webhook sink: the service stores that URL
// as a new secret item under a new ref, and the signing keys stay. URL, when
// set, is the new incoming webhook URL of a chat sink: the service stores it
// as a new secret item under a new ref, and the access token stays.
// AccessToken, when set, is the new access token of a chat sink for a platform that needs
// one: the service stores it as a new secret item under a new ref.
type Change struct {
	Version     int64
	Name        string
	Target      *string
	URL         *string
	AccessToken *string
}

// Replace changes the sink id in ns. It returns notify.ErrNotFound,
// notify.ErrInvalid, notify.ErrConflict, namespace.ErrNotFound or
// namespace.ErrFenced.
func (s *Service) Replace(
	ctx context.Context,
	c *Caller,
	ns namespace.Name,
	id string,
	ch Change,
) (notify.Sink, error) {
	old, err := s.Get(ctx, ns, id)
	if err != nil {
		return notify.Sink{}, err
	}

	next, secrets, err := s.changed(old, ch)
	if err != nil {
		return notify.Sink{}, err
	}

	// Defense in depth: the store validates the sink again. This check stays so
	// that a refused sink costs no sealing, and so that a store that skips its
	// check cannot take an invalid sink from this service.
	if err = next.ValidateWrite(); err != nil {
		return notify.Sink{}, fmt.Errorf("replace sink: %w", err)
	}

	sealed, err := s.sealer.Seal(ctx, next, secrets)
	if err != nil {
		return notify.Sink{}, fmt.Errorf("replace sink: %w", err)
	}

	fence, err := s.fence(ctx, ns)
	if err != nil {
		return notify.Sink{}, err
	}

	if err = s.store.PutSink(ctx, next, sealed, fence, s.entry(c, ns, id, "update")); err != nil {
		return notify.Sink{}, fmt.Errorf("replace sink: %w", err)
	}

	next.Version++

	return next, nil
}

// Delete removes the sink id in ns. It returns notify.ErrNotFound.
func (s *Service) Delete(ctx context.Context, c *Caller, ns namespace.Name, id string) error {
	fence, err := s.fence(ctx, ns)
	if err != nil {
		return err
	}

	if err = s.store.DeleteSink(ctx, id, fence, s.entry(c, ns, id, "delete")); err != nil {
		return fmt.Errorf("delete sink: %w", err)
	}

	return nil
}

// List returns the sinks of ns, ordered by ID. A sink holds no secret.
func (s *Service) List(ctx context.Context, ns namespace.Name) ([]notify.Sink, error) {
	if _, err := s.namespaces.Get(ctx, ns); err != nil {
		return nil, fmt.Errorf("read namespace %s: %w", ns, err)
	}

	sinks, err := s.store.Sinks(ctx, ns)
	if err != nil {
		return nil, fmt.Errorf("list sinks: %w", err)
	}

	return sinks, nil
}

// Get returns the sink id of ns, or notify.ErrNotFound.
func (s *Service) Get(ctx context.Context, ns namespace.Name, id string) (notify.Sink, error) {
	sinks, err := s.List(ctx, ns)
	if err != nil {
		return notify.Sink{}, err
	}

	for i := range sinks {
		if sinks[i].ID == id {
			return sinks[i], nil
		}
	}

	return notify.Sink{}, fmt.Errorf("get sink %s: %w", id, notify.ErrNotFound)
}

// changed returns the sink that applies ch to old, and the new secret items
// that the change makes. It returns notify.ErrInvalid for a field that the
// kind of the sink does not take, and for an address on a port that the
// operator did not allow.
func (s *Service) changed(old notify.Sink, ch Change) (notify.Sink, []notify.SinkSecret, error) {
	next := old
	next.Version, next.Name = ch.Version, ch.Name

	next.SecretRefs = slices.Clone(old.SecretRefs)

	if err := refuseField(old.Kind, ch); err != nil {
		return notify.Sink{}, nil, err
	}

	switch {
	case ch.Target != nil && old.Kind == notify.SinkWebhook:
		return s.withNewURL(next, *ch.Target, notify.WebhookHost, old.SecretRefs[min(1, len(old.SecretRefs)):])
	case ch.Target != nil:
		next.Target = *ch.Target
	case old.Kind == notify.SinkChat && (ch.URL != nil || ch.AccessToken != nil):
		return s.changedChat(next, old, ch)
	default:
		// A change of the name has no new URL.
	}

	// A change of the name or of an email address has no new URL, so no new
	// item meets the port rule: the address that the owner approved stays.
	return next, nil, nil
}

// refuseField returns notify.ErrInvalid for a field of ch that the kind of the
// sink does not take.
func refuseField(kind notify.SinkKind, ch Change) error {
	switch {
	case ch.Target != nil && kind == notify.SinkChat:
		return fmt.Errorf("%w: the target of a chat sink follows its URL: send the url", notify.ErrInvalid)
	case ch.URL != nil && kind != notify.SinkChat:
		return fmt.Errorf("%w: only a chat sink takes a url", notify.ErrInvalid)
	case ch.AccessToken != nil && kind != notify.SinkChat:
		return accessTokenError{rule: accessRuleKind}
	default:
		return nil
	}
}

// changedChat applies a new URL and a new access token of ch to the chat sink
// next, which is old with the new name and version. A new URL keeps the access
// token of old, and a new access token keeps the URL of old. It returns the
// new items.
func (s *Service) changedChat(next, old notify.Sink, ch Change) (notify.Sink, []notify.SinkSecret, error) {
	secrets := make([]notify.SinkSecret, 0, maxChatItems)

	if ch.URL != nil {
		var err error

		next, secrets, err = s.withNewURL(next, *ch.URL, notify.ChatHost, old.SecretRefs[min(1, len(old.SecretRefs)):])
		if err != nil {
			return notify.Sink{}, nil, err
		}
	}

	if ch.AccessToken == nil {
		return next, secrets, nil
	}

	token, err := s.accessToken(next.Platform, *ch.AccessToken, true)
	if err != nil {
		return notify.Sink{}, nil, err
	}

	ref := ident.NewID("itm-")
	next.SecretRefs = []string{next.SecretRefs[0], ref}

	return next, append(secrets, notify.SinkSecret{Ref: ref, Value: token}), nil
}

// takesAccessToken reports whether the chat sender of this server needs an
// access token for platform. A server whose chat sender does not say, for
// example a test double, takes none: it fails closed.
func (s *Service) takesAccessToken(platform string) bool {
	t, ok := s.senders[notify.SinkChat].(interface{ TakesAuthSecret(platform string) bool })

	return ok && t.TakesAuthSecret(platform)
}

// accessToken checks the access token raw for platform and returns its bytes.
// given tells that the request sent an access token, so that an empty one is
// an invalid token and not a missing one. It returns an error that matches
// ErrAccessToken for a platform that takes no token, for a platform that needs
// one and got none, and for a token that is not a valid header value.
//
// Defense in depth: the sender checks the token again before each send, with
// the same function. This check stays so that a refused token is a refused
// write and not a sink that fails at the first message.
func (s *Service) accessToken(platform, raw string, given bool) ([]byte, error) {
	takes := s.takesAccessToken(platform)

	switch {
	case !takes && given:
		return nil, accessTokenError{rule: accessRuleNone}
	case takes && !given:
		return nil, accessTokenError{rule: accessRuleNeeded}
	case !takes:
		return nil, nil
	case !notify.ValidAccessToken([]byte(raw)):
		return nil, accessTokenError{rule: accessRuleInvalid}
	default:
		return []byte(raw), nil
	}
}

// withNewURL returns next with the URL raw as its new first secret item, then
// the refs keys of the items that stay, and the host of the URL as the target.
// hostOf reads the host. A webhook sink keeps its signing keys, so a receiver
// keeps its key when the URL changes. A chat sink keeps its access token. It
// returns the new item, and notify.ErrInvalid when the port rule or hostOf
// refuses the URL.
func (s *Service) withNewURL(
	next notify.Sink, raw string, hostOf func(string) (string, error), keys []string,
) (notify.Sink, []notify.SinkSecret, error) {
	host, err := hostOf(raw)
	if err != nil {
		return notify.Sink{}, nil, fmt.Errorf("%w: %w", notify.ErrInvalid, err)
	}

	if next.Kind == notify.SinkChat {
		if hostErr := s.checkPlatformHost(next, host); hostErr != nil {
			return notify.Sink{}, nil, hostErr
		}
	}

	ref := ident.NewID("itm-")
	next.Target, next.SecretRefs = host, slices.Concat([]string{ref}, keys)

	secrets := []notify.SinkSecret{{Ref: ref, Value: []byte(raw)}}

	if portErr := s.checkPort(&next, secrets); portErr != nil {
		return notify.Sink{}, nil, portErr
	}

	return next, secrets, nil
}

// newSecrets returns the new secret items of a new sink, and sets its target
// and its refs. A chat sink has its URL. A webhook sink has its URL, then a
// new signing key. An email sink has none. The caller owns the bytes of the
// values.
func (s *Service) newSecrets(sink *notify.Sink, in New) ([]notify.SinkSecret, error) {
	// Each kind takes the fields it needs and refuses the others, so a field
	// that a kind ignores never hides a mistake of the caller.
	if sink.Kind != notify.SinkChat && in.URL != "" {
		return nil, fmt.Errorf("%w: only a chat sink takes a url", notify.ErrInvalid)
	}

	if sink.Kind == notify.SinkChat && in.Target != "" {
		return nil, fmt.Errorf("%w: a chat sink takes a url, not a target", notify.ErrInvalid)
	}

	if sink.Kind != notify.SinkChat && in.AccessToken != "" {
		return nil, accessTokenError{rule: accessRuleKind}
	}

	switch sink.Kind {
	case notify.SinkWebhook:
		return webhookSecrets(sink, in.Target)
	case notify.SinkChat:
		return s.chatSecrets(sink, in.URL, in.AccessToken)
	case notify.SinkEmail:
		return nil, nil
	default:
		return nil, fmt.Errorf("%w: unknown sink kind %q", notify.ErrInvalid, sink.Kind)
	}
}

// webhookSecrets sets the target and the refs of a new webhook sink and
// returns its URL item and a new signing key.
func webhookSecrets(sink *notify.Sink, raw string) ([]notify.SinkSecret, error) {
	host, err := notify.WebhookHost(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", notify.ErrInvalid, err)
	}

	key, err := NewSigningKey()
	if err != nil {
		return nil, err
	}

	sink.Target = host
	sink.SecretRefs = []string{ident.NewID("itm-"), ident.NewID("itm-")}

	return []notify.SinkSecret{
		{Ref: sink.SecretRefs[0], Value: []byte(raw)},
		{Ref: sink.SecretRefs[1], Value: key},
	}, nil
}

// chatSecrets sets the target and the refs of a new chat sink and returns its
// URL item, and its access token item for a platform that needs one.
func (s *Service) chatSecrets(sink *notify.Sink, raw, token string) ([]notify.SinkSecret, error) {
	host, err := notify.ChatHost(raw)
	if err != nil {
		if !errors.Is(err, notify.ErrInvalid) {
			err = fmt.Errorf("%w: %w", notify.ErrInvalid, err)
		}

		return nil, err
	}

	if hostErr := s.checkPlatformHost(*sink, host); hostErr != nil {
		return nil, hostErr
	}

	tokenValue, err := s.accessToken(sink.Platform, token, token != "")
	if err != nil {
		return nil, err
	}

	sink.Target = host
	sink.SecretRefs = []string{ident.NewID("itm-")}
	secrets := []notify.SinkSecret{{Ref: sink.SecretRefs[0], Value: []byte(raw)}}

	if tokenValue != nil {
		sink.SecretRefs = append(sink.SecretRefs, ident.NewID("itm-"))
		secrets = append(secrets, notify.SinkSecret{Ref: sink.SecretRefs[1], Value: tokenValue})
	}

	return secrets, nil
}

// ErrPlatformHost reports a chat URL whose host the platform of the sink does
// not use for its webhooks. It also matches notify.ErrInvalid.
var ErrPlatformHost = errors.New("the URL host is not a webhook host of the platform")

// platformHostError is the error of a refused host. It matches
// notify.ErrInvalid and ErrPlatformHost through Is, so that ErrInvalid is in
// the chain and in the text only once.
type platformHostError struct{ rule string }

func (e platformHostError) Error() string {
	return notify.ErrInvalid.Error() + ": " + ErrPlatformHost.Error()
}

func (platformHostError) Is(target error) bool {
	return target == ErrPlatformHost || target == notify.ErrInvalid
}

// PlatformHostRule returns the host rule that err carries when err matches
// ErrPlatformHost, and an empty string otherwise. The rule is a fixed
// sentence of the platform and never holds the host of the URL.
func PlatformHostRule(err error) string {
	if pe, ok := errors.AsType[platformHostError](err); ok {
		return pe.rule
	}

	return ""
}

// checkPlatformHost returns ErrPlatformHost, which matches notify.ErrInvalid, when the sender of chat sinks
// says that host is not a webhook host of the platform of sink, so a person
// who pastes the wrong URL learns it when the sink is saved and not at the
// first message. A sender that has no such check, or no adapter for the
// platform, accepts the host. Defense in depth: the sender checks the host
// again before each send.
func (s *Service) checkPlatformHost(sink notify.Sink, host string) error {
	checker, ok := s.senders[notify.SinkChat].(interface {
		CheckHost(platform, host string) error
	})
	if !ok {
		return nil
	}

	if err := checker.CheckHost(sink.Platform, host); err != nil {
		var rule interface{ HostRule() string }
		if !errors.As(err, &rule) {
			return platformHostError{rule: ""}
		}

		return platformHostError{rule: rule.HostRule()}
	}

	return nil
}

// checkPort returns notify.ErrInvalid when the sink would send to a port that
// the operator did not allow. secrets are the new secret items of the write:
// the first one of a chat sink or of a webhook sink is its URL. An email sink
// has no port, and a sink with no new URL keeps the address that the owner
// approved.
//
// Defense in depth: the sender checks the port again before it sends, and
// Service.Test checks it before the test. This check stays so that a refused
// port is a refused write and not a sink that fails at the first send. The
// store cannot hold this check: the ports are a setting of the operator.
func (s *Service) checkPort(sink *notify.Sink, secrets []notify.SinkSecret) error {
	if sink.Kind == notify.SinkEmail || len(secrets) == 0 || len(sink.SecretRefs) == 0 ||
		secrets[0].Ref != sink.SecretRefs[0] {
		return nil
	}

	if err := egress.CheckPort(string(secrets[0].Value), s.ports); err != nil {
		return fmt.Errorf("%w: %w", notify.ErrInvalid, err)
	}

	return nil
}

// fence returns the fence of ns. It returns namespace.ErrNotHome when the
// primary installation of the namespace is another installation, so that no
// write and no test of a sink runs on an installation that is not the primary
// installation of the namespace.
func (s *Service) fence(ctx context.Context, ns namespace.Name) (namespace.Fence, error) {
	n, err := s.namespaces.WritableHere(ctx, ns)
	if err != nil {
		return namespace.Fence{}, fmt.Errorf("read namespace %s: %w", ns, err)
	}

	return n.Fence(), nil
}

// entry makes the audit entry of one write. It names the sink by ID and
// holds no name, URL or secret.
func (s *Service) entry(c *Caller, ns namespace.Name, id, verb string) audit.Entry {
	return s.entryWith(c, ns, id, verb, audit.OutcomeSuccess)
}

func (s *Service) entryWith(c *Caller, ns namespace.Name, id, verb string, outcome audit.Outcome) audit.Entry {
	e := c.By
	e.At = s.now().UTC()
	e.Namespace = ns
	e.Subject = c.Principal.Subject
	e.TokenID = c.Principal.TokenID
	e.Action = "sink:" + verb
	e.Object = identity.NamespaceObject(ns).String()
	e.ObjectID = id
	e.Kind = audit.KindChange
	e.Outcome = outcome
	e.PrincipalKind = auditKind(c.Principal)

	return e
}

// auditKind returns the audit principal kind of p. The audit log refuses a
// kind that it does not know, so an unknown kind is recorded as no kind.
func auditKind(p identity.Principal) string {
	switch p.Kind {
	case identity.KindPerson:
		return audit.PrincipalKindPerson
	case identity.KindService:
		return audit.PrincipalKindService
	case identity.KindWorkload:
		return audit.PrincipalKindWorkload
	default:
		return ""
	}
}
