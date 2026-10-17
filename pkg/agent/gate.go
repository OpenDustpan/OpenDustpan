package agent

import (
	"errors"
	"fmt"
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
	// ErrSecretsNotAccepted reports a server that started with the flag,
	// but no site administrator accepted the risk after this start.
	ErrSecretsNotAccepted = errors.New("no administrator accepted secret access after this start")
	// ErrSecretsNoAccountOptIn reports a hosted account whose administrator
	// did not opt in.
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
	// Hosted is true on the hosted service. The hosted service has no
	// start flag: the account opt-in takes the place of the flag and the
	// acceptance.
	Hosted bool
	// StartFlag is true when this instance started with the secret access
	// flag. Self-hosted only.
	StartFlag bool
	// Accepted is true when a site administrator accepted the risk after the
	// start of this instance. Self-hosted only.
	Accepted bool
	// AccountOptIn is true when an administrator of the account opted in.
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
// closed. The stages are, in order: the start flag and the acceptance
// (self-hosted) or the account opt-in (hosted), the namespace opt-in, the
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

	return s.StartFlag && s.Accepted
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
	case !s.Hosted && !s.Accepted:
		return ErrSecretsNotAccepted
	default:
		return nil
	}
}
