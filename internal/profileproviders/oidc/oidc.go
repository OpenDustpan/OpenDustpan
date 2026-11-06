// Package oidc is the provider adapter login/oidc. It gets an OIDC token from
// the issuer of the namespace for the audience that the block names, and
// gives the token as a secret value. It makes no exchange: the tool or the
// remote system that reads the value checks the token itself.
package oidc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
	"opendustpan.org/opendustpan/pkg/profile/provider"
)

// Adapter is the name of the adapter. The use of a block is "login/oidc".
const Adapter = "oidc"

// The inputs and the outputs of a block.
const (
	InputAudience      = "audience"
	InputTokenLifetime = "tokenLifetime"
	OutputToken        = "token"
	OutputIssuer       = "issuer"
	OutputAudience     = "audience"
	OutputExpiresAt    = "expiresAt"
)

// DefaultLifetime is the lifetime of a token of a block that sets no
// tokenLifetime: the lifetime of a token in a file for a tool.
const DefaultLifetime = profile.FileTokenLifetime

// expirySkew is how much later than the requested lifetime the expiry of a
// token from the issuer may be.
const expirySkew = time.Minute

// Provider is the adapter login/oidc.
type Provider struct {
	now func() time.Time
}

// New returns the adapter. now is its clock; nil means time.Now.
func New(now func() time.Time) *Provider {
	if now == nil {
		now = time.Now
	}

	return &Provider{now: now}
}

// Adapter implements provider.Provider.
func (*Provider) Adapter() string { return Adapter }

// Kind implements provider.Provider.
func (*Provider) Kind() provider.Kind { return provider.KindLogin }

// Spec implements provider.Provider.
func (*Provider) Spec() provider.Spec {
	return provider.Spec{
		Inputs: []provider.Field{
			{
				Name: InputAudience, Type: provider.TypeString, Required: true, Secret: false,
				Description: "The aud claim of the token: the value that the relying party expects.",
			},
			{
				Name: InputTokenLifetime, Type: provider.TypeDuration, Required: false, Secret: false,
				Description: fmt.Sprintf("How long the token lives, from %s to %s. The default is %s.",
					profile.MinTokenLifetime, profile.MaxTokenLifetime, DefaultLifetime),
			},
		},
		Outputs: []provider.Field{
			{
				Name: OutputToken, Type: provider.TypeString, Required: true, Secret: true,
				Description: "The signed token, a compact JWT.",
			},
			{
				Name: OutputIssuer, Type: provider.TypeString, Required: true, Secret: false,
				Description: "The iss claim of the token: the issuer URL of the namespace.",
			},
			{
				Name: OutputAudience, Type: provider.TypeString, Required: true, Secret: false,
				Description: "The aud claim of the token.",
			},
			{
				Name: OutputExpiresAt, Type: provider.TypeString, Required: true, Secret: false,
				Description: "When the token expires, in RFC 3339 form.",
			},
		},
		MintsTokens: true,
	}
}

// Check implements provider.Provider. It calls no network.
func (p *Provider) Check(in provider.Value) error {
	_, _, err := p.inputs(in)

	return err
}

// Open implements provider.Provider. It asks the token source for one token
// and returns it with the secret mark.
func (p *Provider) Open(ctx context.Context, r provider.Request) (provider.Result, error) {
	// Defense in depth: the runtime runs Check before Open. This check stays
	// so that a caller that skips the runtime cannot get a token for an
	// input that Check refuses.
	aud, lifetime, err := p.inputs(r.Inputs)
	if err != nil {
		return provider.Result{}, err
	}

	if r.Tokens == nil {
		return provider.Result{}, fmt.Errorf("%w: login/oidc: the open has no token source",
			profile.ErrMissingDependency)
	}

	tok, err := r.Tokens.Token(ctx, aud, lifetime)
	if err != nil {
		return provider.Result{}, translate(ctx, err)
	}

	now := p.now()

	// Defense in depth: the issuer signs only what the request asks. This
	// check stays so that a token for another audience or with another
	// lifetime never leaves as the value of this block.
	if tok.Value == "" || tok.Audience != aud || !tok.ExpiresAt.After(now) ||
		tok.ExpiresAt.After(now.Add(lifetime+expirySkew)) {
		return provider.Result{}, fmt.Errorf(
			"%w: login/oidc: the issuer answered a token that does not match the request",
			provider.ErrUnavailable,
		)
	}

	return provider.Result{
		Value: provider.Value{
			OutputToken:     {Value: tok.Value, Secret: true},
			OutputIssuer:    {Value: tok.Issuer, Secret: false},
			OutputAudience:  {Value: tok.Audience, Secret: false},
			OutputExpiresAt: {Value: tok.ExpiresAt.UTC().Format(time.RFC3339), Secret: false},
		},
		Expires:  tok.ExpiresAt,
		Identity: tok.Subject,
	}, nil
}

// inputs validates in and returns the audience and the lifetime.
func (p *Provider) inputs(in provider.Value) (string, time.Duration, error) {
	spec := p.Spec()
	if err := provider.CheckInputs(&spec, in); err != nil {
		return "", 0, fmt.Errorf("login/oidc: %w", err)
	}

	aud, ok := in[InputAudience].Value.(string)
	if !ok {
		return "", 0, fmt.Errorf("%w: login/oidc: the input audience is not a string", profile.ErrInvalid)
	}

	// Defense in depth: the issuer checks the audience too, with the
	// addresses of this server. This check stays so that a bad audience
	// fails at save, with no call to the issuer.
	if err := profile.CheckAudience(aud); err != nil {
		return "", 0, fmt.Errorf("login/oidc: the input audience: %w", err)
	}

	lifetime := DefaultLifetime

	if leaf, set := in[InputTokenLifetime]; set {
		s, isString := leaf.Value.(string)
		if !isString {
			return "", 0, fmt.Errorf("%w: login/oidc: the input tokenLifetime is not a duration", profile.ErrInvalid)
		}

		d, err := time.ParseDuration(s)
		if err != nil {
			return "", 0, fmt.Errorf("%w: login/oidc: the input tokenLifetime is not a duration", profile.ErrInvalid)
		}

		lifetime = d
	}

	// Defense in depth: the issuer refuses another lifetime too. This check
	// stays so that the block fails at save.
	if lifetime < profile.MinTokenLifetime || lifetime > profile.MaxTokenLifetime {
		return "", 0, fmt.Errorf("%w: login/oidc: the input tokenLifetime is from %s to %s",
			profile.ErrInvalid, profile.MinTokenLifetime, profile.MaxTokenLifetime)
	}

	return aud, lifetime, nil
}

// translate maps an error of the token source to an error of the port. The
// message keeps only the text of a known domain error of the issuer, which
// holds no token.
func translate(ctx context.Context, err error) error {
	switch {
	case ctx.Err() != nil:
		return fmt.Errorf("%w: login/oidc: the issuer did not answer before the deadline", provider.ErrUnavailable)
	case errors.Is(err, profile.ErrInvalid), errors.Is(err, profile.ErrSubjectTooLong):
		return fmt.Errorf("%w: login/oidc: the issuer refused the request: %w", profile.ErrInvalid, err)
	case errors.Is(err, profile.ErrIssuerNotConfigured), errors.Is(err, profile.ErrIssuerKeyMissing),
		errors.Is(err, profile.ErrIssuerKeyExpired), errors.Is(err, namespace.ErrFenced),
		errors.Is(err, profile.ErrMissingDependency):
		return fmt.Errorf("%w: login/oidc: the issuer cannot sign: %w", provider.ErrUnavailable, err)
	default:
		// Fail closed: an unknown error keeps no text, because it can come
		// from any layer under the issuer.
		return fmt.Errorf("%w: login/oidc: the issuer failed", provider.ErrUnavailable)
	}
}
