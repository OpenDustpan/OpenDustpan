package issuersvc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"opendustpan.org/opendustpan/pkg/profile"
)

// The names of the steps of the copy check.
const (
	stepDiscovery = "discovery"
	stepJWKS      = "jwks"
	stepActive    = "active-key"
	stepNext      = "next-key"
)

// checkWant says which keys a copy check looks for. The active key is always
// one of them.
type checkWant struct {
	// Next says that the check also looks for the next key.
	Next bool
}

// runCheck makes the copy check with the issuerURL of set: it reads the
// discovery document and the JWKS at that URL through the Fetcher, and looks
// for the kid of the active key, and of the next key when want asks for it. It
// passes only when each step passes. It stores nothing.
//
// The jwks_uri that it reads is built from the issuerURL, and never taken from
// the document, so a copy cannot send the server to another address.
func (s *Service) runCheck(
	ctx context.Context, set *profile.IssuerSettings, keys []profile.Key, want checkWant,
) profile.CopyCheck {
	// A check that cannot read the copy says nothing about the keys that an
	// earlier check found at the same address: the active key keeps signing,
	// as the days of its life allow. Only a JWKS that was read replaces the
	// list. A check of another address says nothing about this one, so the
	// list starts empty.
	check := profile.CopyCheck{At: s.now().UTC(), URL: set.IssuerURL, Steps: nil, Verified: nil, OK: false}
	if set.CopyCheck != nil && set.CopyCheck.URL == set.IssuerURL {
		check.Verified = slices.Clone(set.CopyCheck.Verified)
	}

	disc := profile.NewDiscovery(set.IssuerURL)

	if !s.checkDiscovery(ctx, &check, set.IssuerURL, disc) {
		return check
	}

	jwks, ok := s.checkJWKS(ctx, &check, disc.JWKSURI)
	if !ok {
		return check
	}

	found, err := heldKeys(keys, &jwks)
	if err != nil {
		check.AddStep(stepJWKS, "the server cannot build the JWK of its own key", false)

		return check
	}

	check.Verified = nil

	for i := range keys {
		if keys[i].State.Published() && found[keys[i].ID] {
			check.Verified = append(check.Verified, keys[i].ID)
		}
	}

	slices.Sort(check.Verified)

	active, hasActive := findState(keys, profile.KeyActive)
	check.AddStep(stepActive, string(active.ID), hasActive && found[active.ID])

	if want.Next {
		next, hasNext := findState(keys, profile.KeyNext)
		check.AddStep(stepNext, string(next.ID), hasNext && found[next.ID])
	}

	check.OK = !slices.ContainsFunc(check.Steps, func(st profile.CopyStep) bool { return !st.OK })

	return check
}

// heldKeys says which published keys the copied JWKS holds: the copy has
// exactly one entry with the kid of the key, and that entry has the kty, use,
// alg, n and e of the JWK that the server publishes for the key. A copy with
// the right kid and a stale or wrong key passes nothing, because no cloud
// could verify a token against it.
func heldKeys(keys []profile.Key, copied *profile.JWKS) (map[profile.KeyID]bool, error) {
	want, err := profile.BuildJWKS(keys)
	if err != nil {
		return nil, fmt.Errorf("build the JWKS of the namespace: %w", err)
	}

	held := map[profile.KeyID]bool{}

	for _, w := range want.Keys {
		copies := 0
		equal := false

		for _, c := range copied.Keys {
			if c.Kid != w.Kid {
				continue
			}

			copies++
			equal = c == w
		}

		held[profile.KeyID(w.Kid)] = copies == 1 && equal
	}

	return held, nil
}

// checkDiscovery reads the discovery document at base and checks that it names
// base as the issuer and the JWKS address of base. It adds its step and says
// whether the check goes on.
func (s *Service) checkDiscovery(ctx context.Context, c *profile.CopyCheck, base string, want profile.Discovery) bool {
	raw, err := s.fetch(ctx, base+"/.well-known/openid-configuration")
	if err != nil {
		c.AddStep(stepDiscovery, err.Error(), false)

		return false
	}

	var got profile.Discovery
	if jsonErr := decodeStrict(raw, &got); jsonErr != nil {
		c.AddStep(stepDiscovery, "the document is not a discovery document", false)

		return false
	}

	if got.Issuer != base || got.JWKSURI != want.JWKSURI {
		c.AddStep(stepDiscovery, fmt.Sprintf("issuer must be %s and jwks_uri must be %s", base, want.JWKSURI), false)

		return false
	}

	c.AddStep(stepDiscovery, "read", true)

	return true
}

// checkJWKS reads the JWKS at uri. It adds its step and says whether the check
// goes on.
func (s *Service) checkJWKS(ctx context.Context, c *profile.CopyCheck, uri string) (profile.JWKS, bool) {
	raw, err := s.fetch(ctx, uri)
	if err != nil {
		c.AddStep(stepJWKS, err.Error(), false)

		return profile.JWKS{Keys: nil}, false
	}

	set := profile.JWKS{Keys: nil}
	if jsonErr := decodeStrict(raw, &set); jsonErr != nil {
		c.AddStep(stepJWKS, "the document is not a JWKS", false)

		return profile.JWKS{Keys: nil}, false
	}

	c.AddStep(stepJWKS, "read", true)

	return set, true
}

// The errors of a Fetcher. Their text is safe for an answer: it names no
// address that the rules refused.
var (
	// ErrFetchURL reports a URL that does not open.
	ErrFetchURL = errors.New("the URL does not open")
	// ErrFetchConnection reports a connection that failed or that the egress
	// rules refused. The text does not tell the two apart.
	ErrFetchConnection = errors.New("the connection failed")
	// ErrFetchStatus reports an answer with a status other than 200.
	ErrFetchStatus = errors.New("the answer is not 200")
	// ErrFetchSize reports an answer that is too large or that cannot be read.
	ErrFetchSize = errors.New("the answer is too large or cannot be read")
	// ErrNoFetcher reports a server with no client for outbound requests.
	ErrNoFetcher = errors.New("the server has no client for outbound requests")
	// errTrailing reports data after the JSON value of a document.
	errTrailing = errors.New("trailing data")
)

// Fetcher reads one document at an https URL for the copy check. The adapter
// applies the egress rules, follows no redirect and limits the size of the
// answer.
type Fetcher interface {
	// Fetch answers the body of a 200 answer. ErrFetchURL, ErrFetchConnection,
	// ErrFetchStatus and ErrFetchSize.
	Fetch(ctx context.Context, url string) ([]byte, error)
}

// fetch reads one document through the Fetcher. A server with no Fetcher fails
// closed.
func (s *Service) fetch(ctx context.Context, url string) ([]byte, error) {
	if s.fetcher == nil {
		return nil, ErrNoFetcher
	}

	body, err := s.fetcher.Fetch(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", checkName(url), err)
	}

	return body, nil
}

// checkName names a document of the copy check for an error text: the part of
// the URL after the issuerURL is the same for each namespace.
func checkName(url string) string {
	if strings.Contains(url, "/.well-known/") {
		return "the discovery document"
	}

	return "the JWKS"
}

// decodeStrict decodes one JSON value and refuses trailing data.
func decodeStrict(raw []byte, into any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))

	if err := dec.Decode(into); err != nil {
		return fmt.Errorf("decode: %w", err)
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errTrailing
	}

	return nil
}

func marshal(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode document: %w", err)
	}

	return append(b, '\n'), nil
}
