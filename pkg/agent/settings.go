package agent

import (
	"context"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Connection is the model connection of an installation. The credential is
// not in it: the store holds it sealed with the key-encryption key, and no
// read answers it.
type Connection struct {
	// Adapter names the model adapter, for example "anthropic" or
	// "openai-compatible".
	Adapter string
	BaseURL string
	Model   string
	// MaxOutputTokens bounds the answer of one model call. 0 takes the
	// output token limit of the run.
	MaxOutputTokens int64
	ConnectedBy     string
	ConnectedAt     time.Time
}

// InstallationSettings are the agent settings of an installation. The zero
// value is the default: no direct changes and no MCP endpoint.
type InstallationSettings struct {
	// Limits are the limits of the installation. Every field is above 0.
	Limits Limits
	// DirectChanges allows direct changes by agents, with the opt-ins.
	DirectChanges bool
	// MCPEnabled turns on the MCP endpoint and the stdio proxy. An
	// administrator with ActionServerManageAgent turns it on.
	MCPEnabled bool
}

// NamespaceSettings are the agent settings of a namespace. Lower holds the
// limits that the namespace lowers: a field of 0 lowers nothing.
type NamespaceSettings struct {
	Lower Limits
	// DirectOptIn and SecretOptIn are the opt-ins of the namespace, or nil.
	DirectOptIn *Acceptance
	SecretOptIn *Acceptance
}

// PersonOptIns are the opt-ins of one person, or nil for each that is not
// set.
type PersonOptIns struct {
	Direct  *Acceptance
	Secrets *Acceptance
}

// AccountSecretOptIn is the opt-in of a hosted account to the secret hatch.
// An account administrator makes it with an Acceptance on each enablement.
// EndedAt is the time of the opt-out, or the zero time.
type AccountSecretOptIn struct {
	Acceptance Acceptance
	EndedAt    time.Time
}

// Active reports whether o opens the hosted installation stage.
func (o AccountSecretOptIn) Active() bool {
	return o.EndedAt.IsZero() && o.Acceptance.Validate() == nil
}

// SettingsStore is the persistence port for the agent settings. Each write
// appends its audit entry e in its own transaction. A write in a namespace
// checks fence, and returns namespace.ErrFenced for a stale one. An opt-in
// write returns the error of Acceptance.Validate. Each SQL store adapter
// implements it and runs the conformance suite in package agenttest.
//
//nolint:interfacebloat // One port for the agent settings of three scopes, each with a read and a write.
type SettingsStore interface {
	// Connection returns the model connection, or ErrDisabled.
	Connection(ctx context.Context) (Connection, error)
	// PutConnection sets the connection and its sealed credential.
	PutConnection(ctx context.Context, c Connection, sealedCredential []byte, e audit.Entry) error
	// DeleteConnection removes the connection and its credential. A run
	// then stops at its next check. It returns ErrDisabled when no
	// connection is set.
	DeleteConnection(ctx context.Context, e audit.Entry) error

	// Installation returns the settings of the installation, or the
	// defaults when none are set.
	Installation(ctx context.Context) (InstallationSettings, error)
	// PutInstallation sets the settings of the installation. It returns
	// ErrInvalid for limits that fail Limits.Validate.
	PutInstallation(ctx context.Context, s InstallationSettings, e audit.Entry) error

	// Namespace returns the settings of ns, or the zero settings.
	Namespace(ctx context.Context, ns namespace.Name) (NamespaceSettings, error)
	// PutNamespace sets the settings of ns.
	PutNamespace(
		ctx context.Context,
		ns namespace.Name,
		s NamespaceSettings,
		fence namespace.Fence,
		e audit.Entry,
	) error

	// OptIns returns the opt-ins of subject, or the zero opt-ins.
	OptIns(ctx context.Context, subject string) (PersonOptIns, error)
	// PutOptIns sets the opt-ins of subject.
	PutOptIns(ctx context.Context, subject string, o PersonOptIns, e audit.Entry) error

	// AccountSecretOptIn returns the opt-in of the hosted account, and
	// false when there is none.
	AccountSecretOptIn(ctx context.Context) (AccountSecretOptIn, bool, error)
	// PutAccountSecretOptIn sets or ends the opt-in of the hosted account.
	// An opt-out closes the gate at once.
	PutAccountSecretOptIn(ctx context.Context, o AccountSecretOptIn, e audit.Entry) error
}

// WindowStore is the persistence port for the maintenance windows of the
// secret hatch. Each SQL store adapter implements it as a control table and
// runs the conformance suite in package agenttest.
type WindowStore interface {
	// JoinOrOpen records the start of instance with the flag. It joins the
	// open window, or opens a new one when no window is open, and puts the
	// installation in maintenance mode in the same transaction. opened is
	// true for a new window. A second call for one instance and one window
	// changes nothing.
	JoinOrOpen(
		ctx context.Context,
		instance string,
		at time.Time,
		e audit.Entry,
	) (w SecretWindow, opened bool, err error)
	// Accept records a on the open window id and ends the maintenance mode
	// in the same transaction. It returns the error of a.Validate,
	// ErrWindowClosed for a window that is not open, and ErrWindowNotFound
	// for an unknown ID.
	Accept(ctx context.Context, id string, a Acceptance, e audit.Entry) (SecretWindow, error)
	// Close closes the open window id with no acceptance, when an
	// administrator ends the maintenance mode another way. It returns
	// ErrWindowClosed and ErrWindowNotFound.
	Close(ctx context.Context, id string, at time.Time) (SecretWindow, error)
	// ForInstance returns the latest window that instance joined, and false
	// when it joined none.
	ForInstance(ctx context.Context, instance string) (SecretWindow, bool, error)
}

// ConsentStore is the persistence port for the consents of one use. Each
// SQL store adapter implements it and runs the conformance suite in package
// agenttest.
type ConsentStore interface {
	// Create persists c, not approved and not used, and returns it with
	// its ID.
	Create(ctx context.Context, c Consent) (Consent, error)
	// Get returns the consent id, or ErrConsentNotFound.
	Get(ctx context.Context, id string) (Consent, error)
	// Approve records the approval of the consent id by a, and appends e.
	// It returns ErrConsentNotFound, the error of a.Validate, and
	// ErrConsentNotUsable for a consent of another subject, an approved,
	// used or expired one.
	Approve(ctx context.Context, id string, a Acceptance, e audit.Entry) (Consent, error)
	// Use marks the consent u.ID used at u.At when Consent.Usable allows u,
	// in one transaction, so of two concurrent uses at most one succeeds. A
	// refused use changes nothing. It returns ErrConsentNotFound and
	// ErrConsentNotUsable.
	Use(ctx context.Context, u ConsentUse) error
	// SessionRevealed reports whether a consent was used in the MCP session
	// sessionID. The Toolset then refuses propose_change in that session.
	SessionRevealed(ctx context.Context, sessionID string) (bool, error)
}
