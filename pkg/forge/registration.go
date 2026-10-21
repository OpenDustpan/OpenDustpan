package forge

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// KeyScope returns the installation scope of the private key of the app of
// kind. The key is sealed under it.
func KeyScope(kind Kind) string {
	return "git-host-" + string(kind) + "-key"
}

// EventSecretScope returns the installation scope of the event secret of the
// app of kind.
func EventSecretScope(kind Kind) string {
	return "git-host-" + string(kind) + "-event-secret"
}

// RegistrationInput is the registration of the installation-level app, as the
// operator sends it. PrivateKey and EventSecret are the secrets themselves: the
// service seals them, and never stores, records or answers them.
type RegistrationInput struct {
	AppID       string
	BaseURL     string
	PrivateKey  []byte
	EventSecret []byte
}

// SetView is what a read answers about a credential: that it is set, with its
// set ID and creation time. It never holds the value.
type SetView struct {
	ID        string
	CreatedAt time.Time
}

// RegistrationView is the registration without its secrets.
type RegistrationView struct {
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Key         SetView
	EventSecret SetView
	Kind        Kind
	AppID       string
	BaseURL     string
	HookID      string
	Version     int64
}

// View returns r without its sealed key and its sealed event secret.
func (r *Registration) View() RegistrationView {
	return registrationView(r)
}

func registrationView(r *Registration) RegistrationView {
	return RegistrationView{
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, Key: SetView{ID: r.KeySetID, CreatedAt: r.KeyCreatedAt},
		EventSecret: SetView{ID: r.EventSecretSetID, CreatedAt: r.EventSecretCreatedAt}, Kind: r.Kind,
		AppID: r.AppID, BaseURL: r.BaseURL, HookID: r.HookID, Version: r.Version,
	}
}

// serverEntry completes the entry e of the caller for a change of the
// installation. The scope is @server and the object ID names the thing that
// changed.
func serverEntry(e audit.Entry, at time.Time, action, objectID string) audit.Entry {
	e.At = at
	e.Namespace = audit.ServerScope
	e.Action = action
	e.Object = identity.ServerObject().String()
	e.ObjectID = objectID
	e.Kind = audit.KindChange
	e.Outcome = audit.OutcomeSuccess
	e.Changes = slices.Clone(e.Changes)

	return e
}

// PutRegistration registers the app of kind, or replaces its registration, for
// the installation. It checks the address with the outbound rules, the app ID,
// the key (the adapter must parse it) and the event secret (at least
// MinEventSecretBytes). It seals both secrets before it stores them. A version
// above 0 is the version that the caller read. It returns ErrInvalid,
// ErrVersionConflict and ErrNoRegistration for a version with no registration.
// e is the entry of the caller: the service completes it.
func (s *Service) PutRegistration(
	ctx context.Context, e audit.Entry, kind Kind, in RegistrationInput, ifVersion int64,
) (RegistrationView, error) {
	baseURL, err := s.validateRegistration(ctx, kind, in)
	if err != nil {
		return RegistrationView{}, err
	}

	// Both secrets are bound to the address digest: they open only for the
	// address, the kind and the app ID that they were sealed for.
	digest := AddressDigest(kind, baseURL, in.AppID)

	sealedKey, err := s.secrets.SealBound(ctx, KeyScope(kind), []byte(digest), in.PrivateKey)
	if err != nil {
		return RegistrationView{}, fmt.Errorf("seal the key of the GitHub App: %w", err)
	}

	sealedEvent, err := s.secrets.SealBound(ctx, EventSecretScope(kind), []byte(digest), in.EventSecret)
	if err != nil {
		return RegistrationView{}, fmt.Errorf("seal the event secret of the GitHub App: %w", err)
	}

	at := s.now().UTC()
	reg := Registration{
		CreatedAt: at, UpdatedAt: at, KeyCreatedAt: at, EventSecretCreatedAt: at, Kind: kind, AppID: in.AppID,
		// A new registration gets this hook ID. The store keeps the hook ID of an
		// existing registration when it replaces the row, and returns the stored row.
		BaseURL: baseURL, HookID: NewHookID(), KeySetID: NewSetID(), EventSecretSetID: NewSetID(),
		AddressDigest: digest, SealedKey: sealedKey, SealedEventSecret: sealedEvent,
		Version: 1,
	}

	entry := serverEntry(e, at, "git-host-app:put", string(kind))
	entry.Changes = append(entry.Changes,
		change("appID", in.AppID), change("apiAddress", baseURL),
		// The key and the event secret are recorded as "set": never the value.
		change("privateKey", "set"), change("eventSecret", "set"),
	)

	stored, err := s.store.PutRegistration(ctx, reg, ifVersion, entry)
	if err != nil {
		return RegistrationView{}, fmt.Errorf("put registration: %w", err)
	}

	return registrationView(&stored), nil
}

// GetRegistration returns the registration of kind without its secrets, or
// ErrNoRegistration.
func (s *Service) GetRegistration(ctx context.Context, kind Kind) (RegistrationView, error) {
	reg, err := s.store.GetRegistration(ctx, kind)
	if err != nil {
		return RegistrationView{}, fmt.Errorf("get registration: %w", err)
	}

	return registrationView(&reg), nil
}

// DeleteRegistration removes the registration of kind. It returns
// ErrNoRegistration, ErrRegistrationInUse while an installation is assigned,
// and ErrVersionConflict.
func (s *Service) DeleteRegistration(ctx context.Context, e audit.Entry, kind Kind, ifVersion int64) error {
	entry := serverEntry(e, s.now().UTC(), "git-host-app:delete", string(kind))

	if err := s.store.DeleteRegistration(ctx, kind, ifVersion, entry); err != nil {
		return fmt.Errorf("delete registration: %w", err)
	}

	return nil
}

// AssignInstallation gives the installation to the namespace ns, or moves it
// there. Only the operator calls it. The connections of the old namespace stop
// at their next Open. A version above 0 is the version that the caller read.
func (s *Service) AssignInstallation(
	ctx context.Context, e audit.Entry, installationID string, ns namespace.Name, ifVersion int64,
) (Assignment, error) {
	if !ValidNumericID(installationID) {
		return Assignment{}, fmt.Errorf("%w: the installation ID is 1 to %d digits", ErrInvalid, maxIDDigits)
	}

	at := s.now().UTC()
	a := Assignment{CreatedAt: at, UpdatedAt: at, InstallationID: installationID, Namespace: ns, Version: 1}

	if err := a.Validate(); err != nil {
		return Assignment{}, err
	}

	entry := serverEntry(e, at, "git-host-installation:assign", installationID)
	entry.Changes = append(entry.Changes, change("namespace", ns.String()))

	stored, err := s.store.PutAssignment(ctx, a, ifVersion, entry)
	if err != nil {
		return Assignment{}, fmt.Errorf("assign installation: %w", err)
	}

	return stored, nil
}

// GetAssignment returns the assignment of the installation, or
// ErrAssignmentNotFound.
func (s *Service) GetAssignment(ctx context.Context, installationID string) (Assignment, error) {
	if !ValidNumericID(installationID) {
		return Assignment{}, ErrAssignmentNotFound
	}

	a, err := s.store.GetAssignment(ctx, installationID)
	if err != nil {
		return Assignment{}, fmt.Errorf("get assignment: %w", err)
	}

	return a, nil
}

// ListAssignments returns every assignment, in the order of the installation
// IDs as numbers.
func (s *Service) ListAssignments(ctx context.Context) ([]Assignment, error) {
	list, err := s.store.ListAssignments(ctx)
	if err != nil {
		return nil, fmt.Errorf("list assignments: %w", err)
	}

	return slices.Clone(list), nil
}

// UnassignInstallation removes the assignment of the installation. The
// connections that name it stop at their next Open.
func (s *Service) UnassignInstallation(
	ctx context.Context, e audit.Entry, installationID string, ifVersion int64,
) error {
	if !ValidNumericID(installationID) {
		return ErrAssignmentNotFound
	}

	entry := serverEntry(e, s.now().UTC(), "git-host-installation:unassign", installationID)

	if err := s.store.DeleteAssignment(ctx, installationID, ifVersion, entry); err != nil {
		return fmt.Errorf("unassign installation: %w", err)
	}

	return nil
}

// validateRegistration checks the input of PutRegistration and returns the
// API address in its stored form. It never puts a secret in an error.
func (s *Service) validateRegistration(ctx context.Context, kind Kind, in RegistrationInput) (string, error) {
	opener, ok := s.openers[kind]
	if !ok || kind != KindGitHub {
		return "", fmt.Errorf("%w: the kind %q is not supported", ErrInvalid, kind)
	}

	if !ValidNumericID(in.AppID) {
		return "", fmt.Errorf("%w: the app ID is 1 to %d digits", ErrInvalid, maxIDDigits)
	}

	baseURL := strings.TrimRight(in.BaseURL, "/")
	if err := s.checkURL(baseURL); err != nil {
		return "", fmt.Errorf("%w: the API address: %w", ErrInvalid, err)
	}

	if err := ValidateEventSecret(in.EventSecret); err != nil {
		return "", err
	}

	// The adapter parses the key in Open and makes no call, so this proves that
	// the key has the form that the adapter needs.
	probe := Connection{
		ID: "probe", Kind: kind, Auth: AuthApp, BaseURL: baseURL,
		Params: map[string]string{ParamAppID: in.AppID, ParamInstallationID: "1"}, Push: false,
	}

	cred := Credential{SetID: "probe", Secret: in.PrivateKey, CreatedAt: s.now()}

	_, err := opener.Open(ctx, probe, cred)
	if err != nil {
		return "", fmt.Errorf("%w: the private key: %w", ErrInvalid, err)
	}

	return baseURL, nil
}
