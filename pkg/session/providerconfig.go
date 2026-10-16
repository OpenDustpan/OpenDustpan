package session

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
)

// Errors of the sign-in providers that the API manages.
var (
	// ErrProviderNotFound reports a provider ID with no stored provider.
	ErrProviderNotFound = errors.New("sign-in provider not found")
	// ErrProviderExists reports a provider ID that is already in use.
	ErrProviderExists = errors.New("sign-in provider already exists")
	// ErrProviderChanged reports a change of a provider whose stored
	// version is not the version that the change read.
	ErrProviderChanged = errors.New("sign-in provider changed since it was read")
	// ErrProviderInUse reports a delete of a provider ID that a principal
	// has in its subject.
	ErrProviderInUse = errors.New("sign-in provider has principals")
	// ErrInvalidProvider reports a provider that fails validation.
	ErrInvalidProvider = errors.New("invalid sign-in provider")
)

// Limits of a stored provider.
const (
	maxDisplayName  = 64
	maxClientID     = 256
	maxIssuer       = 512
	maxScopes       = 16
	maxScope        = 64
	maxSealedSecret = 4096
)

// providerIDPattern is the form of a provider ID: a lowercase letter, then
// up to 31 lowercase letters, digits and hyphens.
var providerIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// ValidProviderID reports whether id has the form of a provider ID.
func ValidProviderID(id string) bool {
	return providerIDPattern.MatchString(id)
}

// SubjectPrefix returns the prefix of the subject of each person who signs
// in with the provider id: the subject is "<provider ID>:<sub>".
func SubjectPrefix(id string) string {
	return id + ":"
}

// ProviderConfig is a sign-in provider that the API manages. SealedSecret
// is the client secret, sealed in the installation scope of the provider,
// never the secret itself. Version counts the changes of the provider. A
// provider that is not Enabled refuses each sign-in.
type ProviderConfig struct {
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ID           string
	Issuer       string
	DisplayName  string
	ClientID     string
	SealedSecret []byte
	Scopes       []string
	Version      int64
	AutoCreate   bool
	Enabled      bool
}

// Validate returns ErrInvalidProvider for a provider that a store must
// not keep.
func (p ProviderConfig) Validate() error {
	if err := p.validateNames(); err != nil {
		return err
	}

	switch {
	case len(p.SealedSecret) == 0 || len(p.SealedSecret) > maxSealedSecret:
		return fmt.Errorf("%w: the sealed client secret must have 1 to %d bytes", ErrInvalidProvider, maxSealedSecret)
	case p.Version < 1:
		return fmt.Errorf("%w: the version must be 1 or more", ErrInvalidProvider)
	case p.CreatedAt.IsZero() || p.UpdatedAt.Before(p.CreatedAt):
		return fmt.Errorf("%w: the provider needs a creation time no later than its update time", ErrInvalidProvider)
	default:
		return validScopes(p.Scopes)
	}
}

// validateNames checks the ID, the issuer, the display name and the
// client ID of p.
func (p ProviderConfig) validateNames() error {
	switch {
	case !ValidProviderID(p.ID):
		return fmt.Errorf("%w: the provider ID %q does not match %s", ErrInvalidProvider, p.ID, providerIDPattern)
	case !validIssuer(p.Issuer):
		return fmt.Errorf("%w: the issuer must be an absolute http or https URL of at most %d bytes",
			ErrInvalidProvider, maxIssuer)
	case p.DisplayName == "" || len(p.DisplayName) > maxDisplayName || hasControl(p.DisplayName):
		return fmt.Errorf("%w: the display name must have 1 to %d bytes and no control character",
			ErrInvalidProvider, maxDisplayName)
	case p.ClientID == "" || len(p.ClientID) > maxClientID || hasControl(p.ClientID):
		return fmt.Errorf("%w: the client ID must have 1 to %d bytes and no control character",
			ErrInvalidProvider, maxClientID)
	default:
		return nil
	}
}

// validIssuer reports whether issuer is an absolute http or https URL with
// a host. The sign-in client checks the stricter rule for the scheme when
// it uses the issuer.
func validIssuer(issuer string) bool {
	if issuer == "" || len(issuer) > maxIssuer {
		return false
	}

	u, err := url.Parse(issuer)

	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" &&
		u.RawQuery == "" && u.Fragment == ""
}

// validScopes refuses more than maxScopes scopes, an empty scope, a scope
// over maxScope bytes, and a scope with a space or a control character.
func validScopes(scopes []string) error {
	if len(scopes) > maxScopes {
		return fmt.Errorf("%w: at most %d scopes", ErrInvalidProvider, maxScopes)
	}

	for _, s := range scopes {
		if s == "" || len(s) > maxScope || strings.ContainsAny(s, " ") || hasControl(s) {
			return fmt.Errorf("%w: each scope must have 1 to %d bytes and no space", ErrInvalidProvider, maxScope)
		}
	}

	return nil
}

// hasControl reports whether s has a control character.
func hasControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// ProviderConfigStore keeps the sign-in providers that the API manages.
// Each change appends its audit entry e in the scope @server in the same
// transaction, so the change and its entry commit together or not at all.
type ProviderConfigStore interface {
	// CreateProviderConfig stores p. It returns ErrProviderExists when the
	// ID is in use and ErrInvalidProvider for a provider that fails
	// Validate.
	CreateProviderConfig(ctx context.Context, p ProviderConfig, e audit.Entry) error
	// GetProviderConfig returns the provider id, or ErrProviderNotFound.
	GetProviderConfig(ctx context.Context, id string) (ProviderConfig, error)
	// ListProviderConfigs returns every stored provider, ordered by ID. It
	// returns an empty list when the store holds none.
	ListProviderConfigs(ctx context.Context) ([]ProviderConfig, error)
	// UpdateProviderConfig replaces the provider p.ID with p when its
	// stored version is p.Version-1. It returns ErrProviderNotFound for an
	// unknown ID, ErrProviderChanged for another stored version, and
	// ErrInvalidProvider for a provider that fails Validate. The creation
	// time stays.
	UpdateProviderConfig(ctx context.Context, p ProviderConfig, e audit.Entry) error
	// DeleteProviderConfig deletes the provider id. It returns
	// ErrProviderNotFound for an unknown ID, and ErrProviderInUse while a
	// principal, also a deleted one, has a subject that starts with
	// SubjectPrefix(id).
	DeleteProviderConfig(ctx context.Context, id string, e audit.Entry) error
}
