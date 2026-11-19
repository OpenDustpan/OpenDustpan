package forge

import (
	"context"
	"crypto/subtle"
	"errors"
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
	// OldEventSecretEndsAt is the effective end of the window of the old event
	// secret. It is the zero time when no window is open or the end passed.
	OldEventSecretEndsAt time.Time
}

// View returns r without its sealed key and its sealed event secret.
// The window of the old event secret shows as open when its end is after the
// time of the call: View reads the clock of the process, and the methods of
// Service use ViewAt with their own clock.
func (r *Registration) View() RegistrationView {
	return registrationView(r, time.Now())
}

// ViewAt is View for the time now.
func (r *Registration) ViewAt(now time.Time) RegistrationView {
	return registrationView(r, now)
}

// oldSecretOpen reports whether the old event secret of r is usable at now:
// all three old fields are set and now is before the end.
func (r *Registration) oldSecretOpen(now time.Time) bool {
	// Defense in depth: ReceiveEvent and the store check a registration with
	// Validate too. This check stays so that the view and the window never
	// trust a partial set of old fields, whichever caller got the row.
	return r.validateOldEventSecret() == nil && !r.OldEventSecretEndsAt.IsZero() &&
		now.Before(r.OldEventSecretEndsAt)
}

func registrationView(r *Registration, now time.Time) RegistrationView {
	var ends time.Time
	if r.oldSecretOpen(now) {
		ends = r.OldEventSecretEndsAt
	}

	return RegistrationView{
		OldEventSecretEndsAt: ends,
		CreatedAt:            r.CreatedAt,
		UpdatedAt:            r.UpdatedAt,
		Key:                  SetView{ID: r.KeySetID, CreatedAt: r.KeyCreatedAt},
		EventSecret:          SetView{ID: r.EventSecretSetID, CreatedAt: r.EventSecretCreatedAt},
		Kind:                 r.Kind,
		AppID:                r.AppID,
		BaseURL:              r.BaseURL,
		HookID:               r.HookID,
		Version:              r.Version,
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
		OldEventSecretSetID: "", OldSealedEventSecret: nil, OldEventSecretEndsAt: time.Time{},
		Version: 1,
	}

	entry := serverEntry(e, at, "git-host-app:put", string(kind))
	entry.Changes = append(entry.Changes,
		change("appID", in.AppID), change("apiAddress", baseURL),
		// The key and the event secret are recorded as "set": never the value.
		change("privateKey", "set"), change("eventSecret", "set"),
	)

	rotation, writeVersion, err := s.rotate(ctx, &reg, in.EventSecret, ifVersion)
	if err != nil {
		return RegistrationView{}, err
	}

	entry.Changes = append(entry.Changes, change("eventSecretRotation", rotation))

	stored, err := s.store.PutRegistration(ctx, reg, writeVersion, entry)
	if err != nil {
		return RegistrationView{}, fmt.Errorf("put registration: %w", err)
	}

	return registrationView(&stored, at), nil
}

// OldEventSecretWindow is how long the old event secret stays valid after a
// rotation. An operator confirms the rotation earlier with ConfirmRotation.
const OldEventSecretWindow = 7 * 24 * time.Hour

// The values that the entry of a put records for the rotation of the event
// secret.
const (
	rotationNone      = "none"
	rotationStarted   = "started: the former secret stays valid for 7 days"
	rotationKept      = "kept: the same secret, the window is as it was"
	rotationEnded     = "ended: the window had passed"
	rotationAddress   = "ended: the address changed"
	rotationNotOpened = "ended: the former secret did not open"
)

// rotate sets the old event secret fields of reg, which holds the new
// registration, from the stored registration of its kind. The time of the
// rotation is reg.CreatedAt. It returns what the
// entry records and the ifVersion that the write needs. When it computed the
// old fields from a read, the write carries the version of that read, so a
// concurrent put returns ErrVersionConflict and no secret is lost. A caller
// version other than the one that was read is a conflict at once.
func (s *Service) rotate(
	ctx context.Context, reg *Registration, secret []byte, ifVersion int64,
) (string, int64, error) {
	kind, at := reg.Kind, reg.CreatedAt
	stored, err := s.store.GetRegistration(ctx, kind)

	switch {
	case errors.Is(err, ErrNoRegistration):
		// Nothing to rotate. The store answers a version above 0 with
		// ErrNoRegistration.
		return rotationNone, ifVersion, nil
	case err != nil:
		return "", 0, fmt.Errorf("get registration: %w", err)
	case ifVersion > 0 && stored.Version != ifVersion:
		return "", 0, fmt.Errorf("put registration: %w", ErrVersionConflict)
	}

	if stored.AddressDigest != reg.AddressDigest || stored.Digest() != reg.AddressDigest {
		// The old secret is sealed for the old address and opens for no other.
		return rotationAddress, stored.Version, nil
	}

	// A secret that does not open cannot be compared or kept: the window ends.
	current, ok := s.openSealed(ctx, kind, stored.AddressDigest, stored.SealedEventSecret)
	if !ok {
		return rotationNotOpened, stored.Version, nil
	}

	if subtle.ConstantTimeCompare(current, secret) == 1 {
		if !stored.oldSecretOpen(at) {
			return rotationEnded, stored.Version, nil
		}

		reg.OldEventSecretSetID = stored.OldEventSecretSetID
		reg.OldSealedEventSecret = slices.Clone(stored.OldSealedEventSecret)
		reg.OldEventSecretEndsAt = stored.OldEventSecretEndsAt

		return rotationKept, stored.Version, nil
	}

	// A different secret: the secret that was current becomes the old one. A
	// third secret replaces the old one, because two columns hold two secrets.
	reg.OldEventSecretSetID = stored.EventSecretSetID
	reg.OldSealedEventSecret = slices.Clone(stored.SealedEventSecret)
	reg.OldEventSecretEndsAt = at.Add(OldEventSecretWindow)

	return rotationStarted, stored.Version, nil
}

// openSealed opens the sealed event secret of kind that is bound to digest.
// ok is false for a value that does not open.
func (s *Service) openSealed(ctx context.Context, kind Kind, digest string, sealed []byte) ([]byte, bool) {
	plain, err := s.secrets.OpenBound(ctx, EventSecretScope(kind), []byte(digest), sealed)

	return plain, err == nil
}

// ConfirmRotation ends the old event secret at once. It returns
// ErrNoRegistration, ErrVersionConflict and ErrNoRotation when the
// registration holds no old event secret. e is the entry of the caller: the
// service completes it.
func (s *Service) ConfirmRotation(
	ctx context.Context,
	e audit.Entry,
	kind Kind,
	ifVersion int64,
) (RegistrationView, error) {
	if _, ok := s.openers[kind]; !ok || kind != KindGitHub {
		return RegistrationView{}, fmt.Errorf("%w: the kind %q is not supported", ErrInvalid, kind)
	}

	at := s.now().UTC()
	entry := serverEntry(e, at, "git-host-app:confirm-rotation", string(kind))

	stored, err := s.store.ConfirmRotation(ctx, kind, ifVersion, entry)
	if err != nil {
		return RegistrationView{}, fmt.Errorf("confirm rotation: %w", err)
	}

	return registrationView(&stored, at), nil
}

// GetRegistration returns the registration of kind without its secrets, or
// ErrNoRegistration.
func (s *Service) GetRegistration(ctx context.Context, kind Kind) (RegistrationView, error) {
	reg, err := s.store.GetRegistration(ctx, kind)
	if err != nil {
		return RegistrationView{}, fmt.Errorf("get registration: %w", err)
	}

	return registrationView(&reg, s.now()), nil
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
