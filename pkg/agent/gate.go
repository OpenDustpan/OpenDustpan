package agent

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

// Errors of the secret gate. SecretAccess.Check wraps ErrSecretsClosed and
// the error of the first stage that is not open, so a caller can test for
// either.
var (
	// ErrSecretsClosed reports a secret read that the gate refuses. A
	// caller answers the masked value instead, never a part of the secret.
	ErrSecretsClosed = errors.New("agents cannot read secret values in plain text")
	// ErrSecretsNoFlag reports a self-hosted server that did not start with
	// the secret access flag.
	ErrSecretsNoFlag = errors.New("the server did not start with the secret access flag")
	// ErrSecretsNotAccepted reports a server that started with the flag in a
	// maintenance window that no site administrator accepted.
	ErrSecretsNotAccepted = errors.New(
		"no administrator accepted secret access in the maintenance window of this start",
	)
	// ErrSecretsNoAccountOptIn reports a hosted account whose administrator
	// did not opt in, or whose opt-in an administrator ended.
	ErrSecretsNoAccountOptIn = errors.New("the account did not opt in to secret access")
	// ErrSecretsNoNamespaceOptIn reports a namespace that did not opt in.
	ErrSecretsNoNamespaceOptIn = errors.New("the namespace did not opt in to secret access")
	// ErrSecretsNoUserOptIn reports a person who did not opt in.
	ErrSecretsNoUserOptIn = errors.New("the person did not opt in to secret access")
	// ErrSecretsNoUseConsent reports an outside agent call with no consent
	// for this one use. The person gives the consent in the console.
	ErrSecretsNoUseConsent = errors.New("the person did not give consent for this use")
)

// SecretAccess holds the state of each stage of the secret gate for one tool
// call that asks for secret values in plain text. The gate is open only when
// every stage that applies is open. The server fills it from the start flag,
// the acceptance record, the settings of the account, the namespace and the
// person, and the consent of the call.
type SecretAccess struct {
	// Hosted is true on the hosted service. A hosted customer cannot set a
	// start flag or maintenance mode: the account opt-in takes the place of
	// the flag and the window acceptance.
	Hosted bool
	// StartFlag is true when this instance started with the secret access
	// flag. Self-hosted only.
	StartFlag bool
	// WindowAccepted is true when the maintenance window that this instance
	// joined at its start has an acceptance (SecretWindow.Accepted).
	// Self-hosted only.
	WindowAccepted bool
	// AccountOptIn is true when the account has an opt-in that an account
	// administrator made with a valid Acceptance, and no later opt-out.
	// Hosted only.
	AccountOptIn bool
	// NamespaceOptIn is true when the namespace of the unit opted in.
	NamespaceOptIn bool
	// UserOptIn is true when the person of the call opted in.
	UserOptIn bool
	// Channel is the channel of the call.
	Channel Channel
	// UseConsent is true when the call carries a valid consent for this one
	// use: this tool, this unit, not used before, not expired. Only
	// ChannelMCP and ChannelProxy calls need it.
	UseConsent bool
}

// Check returns nil when the gate is open for the call, and otherwise an
// error that wraps ErrSecretsClosed and the error of the first stage that is
// closed. The stages are, in order: the start flag and the acceptance of its
// window (self-hosted) or the account opt-in (hosted), the namespace opt-in, the
// person opt-in, and the consent for one use (outside agents only).
func (s SecretAccess) Check() error {
	if err := s.firstClosed(); err != nil {
		return fmt.Errorf("%w: %w", ErrSecretsClosed, err)
	}

	return nil
}

// Exposed reports whether secret values can reach an agent on the
// installation or account at all: the first stage is open. The status API
// answers it, and the console then shows the warning theme and banner to
// every person, not only to the persons who opted in.
func (s SecretAccess) Exposed() bool {
	if s.Hosted {
		return s.AccountOptIn
	}

	return s.StartFlag && s.WindowAccepted
}

func (s SecretAccess) firstClosed() error {
	if !s.Channel.Valid() {
		return fmt.Errorf("%w: unknown channel %q", ErrInvalid, s.Channel)
	}

	if err := s.installationClosed(); err != nil {
		return err
	}

	switch {
	case !s.NamespaceOptIn:
		return ErrSecretsNoNamespaceOptIn
	case !s.UserOptIn:
		return ErrSecretsNoUserOptIn
	case s.Channel != ChannelAgent && !s.UseConsent:
		return ErrSecretsNoUseConsent
	default:
		return nil
	}
}

// installationClosed checks the first stage: the account opt-in on the
// hosted service, the start flag and the acceptance on a self-hosted server.
func (s SecretAccess) installationClosed() error {
	switch {
	case s.Hosted && !s.AccountOptIn:
		return ErrSecretsNoAccountOptIn
	case !s.Hosted && !s.StartFlag:
		return ErrSecretsNoFlag
	case !s.Hosted && !s.WindowAccepted:
		return ErrSecretsNotAccepted
	default:
		return nil
	}
}

// Errors of an acceptance and of a consent.
var (
	// ErrAcceptanceText reports an acceptance whose typed text is not
	// AcceptanceText.
	ErrAcceptanceText = errors.New(`type "I accept" to accept`)
	// ErrMFANotFresh reports an acceptance, an opt-in or a consent approval
	// with no MFA step, or with an MFA step older than MaxMFAAge.
	ErrMFANotFresh = errors.New("this action needs a fresh MFA step")
	// ErrWindowClosed reports an acceptance of a maintenance window that is
	// already accepted or closed.
	ErrWindowClosed = errors.New("the maintenance window is already accepted or closed")
	// ErrWindowNotFound reports a maintenance window ID that the store does
	// not know.
	ErrWindowNotFound = errors.New("maintenance window not found")
	// ErrConsentNotFound reports a consent ID that the store does not know.
	ErrConsentNotFound = errors.New("consent not found")
	// ErrConsentNotUsable reports a consent that is not approved, expired,
	// used, or made for another person, token, tool or unit.
	ErrConsentNotUsable = errors.New("the consent is not valid for this call")
)

// AcceptanceText is the text that a person types to accept the risk of the
// secret hatch, on each enablement.
const AcceptanceText = "I accept"

// MaxMFAAge bounds the age of the MFA step of an acceptance, an opt-in and a
// consent approval.
const MaxMFAAge = 5 * time.Minute

// ConsentTTL is the time that a consent for one use waits for its approval
// and its use, from its create.
const ConsentTTL = 5 * time.Minute

// Acceptance is one "I accept" of a person with a console session: the
// acceptance of a maintenance window, a hosted account opt-in, a namespace
// opt-in or a person opt-in. Text is what the person typed. MFAAt is the
// time of the MFA step of the session. An access token can never accept: the
// route needs a console session.
type Acceptance struct {
	Subject string
	Text    string
	MFAAt   time.Time
	At      time.Time
}

// Validate returns ErrInvalid for an acceptance with no subject or time,
// ErrAcceptanceText for another text, and ErrMFANotFresh for an MFA step
// that is missing, after At, or older than MaxMFAAge at At.
func (a Acceptance) Validate() error {
	switch {
	case a.Subject == "" || a.At.IsZero():
		return fmt.Errorf("%w: an acceptance needs a subject and a time", ErrInvalid)
	case a.Text != AcceptanceText:
		return ErrAcceptanceText
	case a.MFAAt.IsZero() || a.MFAAt.After(a.At) || a.At.Sub(a.MFAAt) > MaxMFAAge:
		return ErrMFANotFresh
	default:
		return nil
	}
}

// SecretWindow is one maintenance window of a self-hosted installation for
// the secret hatch. The first instance that starts with the flag outside a
// window opens one and puts the installation in maintenance mode. Each
// instance that starts with the flag while the window is open joins it. One
// acceptance covers every instance of the window and ends the maintenance
// mode. A start with the flag after that opens a new window. An
// administrator can also end the maintenance mode with no acceptance: the
// window then closes, and the hatch of its instances stays shut.
type SecretWindow struct {
	ID       string
	OpenedAt time.Time
	// Instances are the instances that started with the flag in the
	// window.
	Instances []string
	// Acceptance is the acceptance of the window, or nil.
	Acceptance *Acceptance
	// ClosedAt is the end of a window with no acceptance, or the zero time.
	ClosedAt time.Time
}

// Open reports whether an instance that starts with the flag joins w.
func (w SecretWindow) Open() bool {
	return w.Acceptance == nil && w.ClosedAt.IsZero()
}

// Accepted reports whether w has an acceptance.
func (w SecretWindow) Accepted() bool {
	return w.Acceptance != nil
}

// Covers reports whether the hatch is open for instance by w: instance
// started with the flag in w, and w is accepted.
func (w SecretWindow) Covers(instance string) bool {
	if !w.Accepted() {
		return false
	}

	return slices.Contains(w.Instances, instance)
}

// Consent is the consent of a person for one use of secret values by an
// outside agent: one call of one tool on one unit, with one token, in one
// MCP session. The person approves it in the console with a fresh MFA step.
// The store marks it used at its first use. A session that used a consent
// cannot propose a change.
type Consent struct {
	ID         string
	Subject    string
	TokenID    string
	SessionID  string
	Tool       ToolName
	Unit       string
	CreatedAt  time.Time
	ApprovedAt time.Time
	UsedAt     time.Time
}

// ConsentUse is one use of a consent: the call of Tool on Unit by Subject
// with the token TokenID in the MCP session SessionID, at At.
type ConsentUse struct {
	ID        string
	Subject   string
	TokenID   string
	SessionID string
	Tool      ToolName
	Unit      string
	At        time.Time
}

// Usable returns nil when c is valid for u, and ErrConsentNotUsable
// otherwise: not approved, used, older than ConsentTTL at u.At, or made for
// another subject, token, session, tool or unit.
func (c Consent) Usable(u ConsentUse) error {
	switch {
	case c.ApprovedAt.IsZero(), !c.UsedAt.IsZero(), u.At.Sub(c.CreatedAt) > ConsentTTL:
		return ErrConsentNotUsable
	case c.Subject != u.Subject || c.TokenID != u.TokenID || c.SessionID != u.SessionID || c.Tool != u.Tool ||
		c.Unit != u.Unit:
		return ErrConsentNotUsable
	default:
		return nil
	}
}

// The names of the stages of the secret gate, as SecretStage answers them.
const (
	StageFlag      = "flag"
	StageWindow    = "window-acceptance"
	StageAccount   = "account-opt-in"
	StageNamespace = "namespace-opt-in"
	StagePerson    = "person-opt-in"
	StageConsent   = "use-consent"
)

// SecretStage returns the name of the closed stage that err of
// SecretAccess.Check names, for the tool result and the audit entry, or ""
// for an error of no stage.
func SecretStage(err error) string {
	stages := []struct {
		err  error
		name string
	}{
		{ErrSecretsNoFlag, StageFlag},
		{ErrSecretsNotAccepted, StageWindow},
		{ErrSecretsNoAccountOptIn, StageAccount},
		{ErrSecretsNoNamespaceOptIn, StageNamespace},
		{ErrSecretsNoUserOptIn, StagePerson},
		{ErrSecretsNoUseConsent, StageConsent},
	}

	for _, s := range stages {
		if errors.Is(err, s.err) {
			return s.name
		}
	}

	return ""
}
