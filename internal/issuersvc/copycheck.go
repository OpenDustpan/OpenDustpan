package issuersvc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"

	"opendustpan.org/opendustpan/internal/egress/egresshttp"
	"opendustpan.org/opendustpan/pkg/profile"
)

// The names of the steps of the copy check.
const (
	stepDiscovery = "discovery"
	stepJWKS      = "jwks"
	stepActive    = "active-key"
	stepNext      = "next-key"
)

// runCheck makes the copy check with the issuerURL of set:
// it reads the discovery document and the JWKS at that URL through the egress
// guard, and looks for the kid of the active key, and of the next key when
// needNext is set. It passes only when each step passes. It stores nothing.
//
// The jwks_uri that it reads is built from the issuerURL, and never taken from
// the document, so a copy cannot send the server to another address.
func (s *Service) runCheck(
	ctx context.Context, set profile.IssuerSettings, keys []profile.Key, needNext bool,
) profile.CopyCheck {
	check := profile.CopyCheck{At: s.now().UTC(), Steps: nil, Verified: nil, OK: false}
	add := func(name, detail string, ok bool) {
		check.Steps = append(check.Steps, profile.CopyStep{Name: name, Detail: detail, OK: ok})
	}

	base := set.IssuerURL
	disc := profile.NewDiscovery(base)

	raw, err := s.fetch(ctx, base+"/.well-known/openid-configuration")
	if err != nil {
		add(stepDiscovery, err.Error(), false)

		return check
	}

	var got profile.Discovery
	if jsonErr := decodeStrict(raw, &got); jsonErr != nil {
		add(stepDiscovery, "the document is not a discovery document", false)

		return check
	}

	if got.Issuer != base || got.JWKSURI != disc.JWKSURI {
		add(stepDiscovery, fmt.Sprintf("issuer must be %s and jwks_uri must be %s", base, disc.JWKSURI), false)

		return check
	}

	add(stepDiscovery, "read", true)

	raw, err = s.fetch(ctx, disc.JWKSURI)
	if err != nil {
		add(stepJWKS, err.Error(), false)

		return check
	}

	var set2 profile.JWKS
	if jsonErr := decodeStrict(raw, &set2); jsonErr != nil {
		add(stepJWKS, "the document is not a JWKS", false)

		return check
	}

	add(stepJWKS, "read", true)

	found := map[profile.KeyID]bool{}
	for _, k := range set2.Keys {
		found[profile.KeyID(k.Kid)] = true
	}

	for _, k := range keys {
		if k.State.Published() && found[k.ID] {
			check.Verified = append(check.Verified, k.ID)
		}
	}

	slices.Sort(check.Verified)

	active, hasActive := findState(keys, profile.KeyActive)
	add(stepActive, string(active.ID), hasActive && found[active.ID])

	if needNext {
		next, hasNext := findState(keys, profile.KeyNext)
		add(stepNext, string(next.ID), hasNext && found[next.ID])
	}

	check.OK = !slices.ContainsFunc(check.Steps, func(st profile.CopyStep) bool { return !st.OK })

	return check
}

// errNoClient is the failure of a copy check on a server with no guarded
// HTTP client.
var errNoClient = errors.New("the server has no client for outbound requests")

// fetch reads one document through the guarded client. The text of its error
// is safe for an answer: it names no address that the rules refused.
func (s *Service) fetch(ctx context.Context, url string) ([]byte, error) {
	if s.http == nil {
		return nil, errNoClient
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, errors.New("the URL does not open")
	}

	req.Header.Set("Accept", "application/json")

	resp, err := s.http.Do(req)
	if err != nil {
		// The text does not tell a refused address from an unreachable one.
		return nil, errors.New("the connection failed")
	}
	defer s.closeBody(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the answer is %d, want 200", resp.StatusCode)
	}

	body, err := egresshttp.ReadBody(resp)
	if err != nil {
		return nil, errors.New("the answer is too large or cannot be read")
	}

	return body, nil
}

// decodeStrict decodes one JSON value and refuses trailing data.
func decodeStrict(raw []byte, into any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))

	if err := dec.Decode(into); err != nil {
		return fmt.Errorf("decode: %w", err)
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("trailing data")
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
