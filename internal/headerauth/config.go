package headerauth

import (
	"crypto/sha256"
	"fmt"
	"net/netip"
	"net/textproto"
	"net/url"
	"strings"

	"opendustpan.org/opendustpan/pkg/egress"
)

// checkProxies refuses a proxy range that is not valid, has host bits, or
// holds every address.
func checkProxies(list []netip.Prefix) error {
	for _, p := range list {
		if !p.IsValid() || p.Bits() == 0 || p.Masked() != p {
			return fmt.Errorf("%w: a trusted proxy is not a valid address or CIDR range", ErrConfig)
		}
	}

	return nil
}

// emptyJWT reports whether j holds no setting.
func emptyJWT(j JWTConfig) bool {
	return j == JWTConfig{
		Preset: PresetCustom, Issuer: "", Audience: "", KeyURL: "", Region: "", SignerARN: "", GroupsClaim: "",
	}
}

// canonicalHeader returns the canonical form of the header name h, or an
// error when h is not a header name of the allowlist.
func canonicalHeader(h, what string) (string, error) {
	if !headerNamePattern.MatchString(h) {
		return "", fmt.Errorf("%w: the %s header name is not valid", ErrConfig, what)
	}

	return textproto.CanonicalMIMEHeaderKey(h), nil
}

// configurePlain checks and keeps the settings of the plain mode.
func configurePlain(m *Method, cfg Config) error {
	var err error

	if m.userHeader, err = canonicalHeader(cfg.UserHeader, "user"); err != nil {
		return err
	}

	if m.secretHeader, err = canonicalHeader(cfg.SecretHeader, "secret"); err != nil {
		return err
	}

	if cfg.GroupsHeader != "" {
		if m.groupsHeader, err = canonicalHeader(cfg.GroupsHeader, "groups"); err != nil {
			return err
		}
	}

	if m.userHeader == m.secretHeader || m.userHeader == m.groupsHeader || m.secretHeader == m.groupsHeader {
		return fmt.Errorf("%w: each header of the provider must have its own name", ErrConfig)
	}

	secret := cfg.Secret.Reveal()
	if len(secret) < MinSecretBytes || len(secret) > MaxSecretBytes || !printableASCII(secret) {
		return fmt.Errorf("%w: the shared secret must have %d to %d printable ASCII bytes",
			ErrConfig, MinSecretBytes, MaxSecretBytes)
	}

	if !emptyJWT(cfg.JWT) {
		return fmt.Errorf("%w: the plain mode has no JWT settings", ErrConfig)
	}

	m.secretSum = sha256.Sum256([]byte(secret))

	return nil
}

// configureJWT checks and keeps the settings of the jwt mode, with the
// values of the preset.
func configureJWT(m *Method, cfg Config) error {
	if err := checkJWTSettings(cfg); err != nil {
		return err
	}

	keyURL, err := applyPreset(m, cfg)
	if err != nil {
		return err
	}

	if m.userHeader, err = canonicalHeader(m.userHeader, "user"); err != nil {
		return err
	}

	if m.issuer == "" || (m.preset != PresetAWSALB && m.audience == "") {
		return fmt.Errorf("%w: the jwt mode needs an issuer and an audience", ErrConfig)
	}

	if err := checkKeyURL(keyURL); err != nil {
		return err
	}

	timeout := cfg.KeyTimeout
	if timeout <= 0 {
		timeout = egress.Timeout
	}

	m.keys = newKeyCache(cfg.HTTPClient, keyURL, timeout, m.now)

	return nil
}

// checkJWTSettings refuses the settings that the jwt mode cannot have.
func checkJWTSettings(cfg Config) error {
	if cfg.HTTPClient == nil {
		return fmt.Errorf("%w: the jwt mode needs an HTTP client", ErrConfig)
	}

	if cfg.GroupsHeader != "" || cfg.SecretHeader != "" || cfg.Secret.Reveal() != "" {
		return fmt.Errorf("%w: the jwt mode reads groups from a claim and has no shared secret", ErrConfig)
	}

	if cfg.JWT.GroupsClaim != "" && !claimNamePattern.MatchString(cfg.JWT.GroupsClaim) {
		return fmt.Errorf("%w: the groups claim name is not valid", ErrConfig)
	}

	return nil
}

// applyPreset fills the user header and the issuer of the preset into m,
// and returns the key URL.
func applyPreset(m *Method, cfg Config) (string, error) {
	keyURL := cfg.JWT.KeyURL

	switch cfg.JWT.Preset {
	case PresetGoogleIAP:
		m.userHeader = orDefault(cfg.UserHeader, GoogleIAPHeader)
		m.issuer = orDefault(cfg.JWT.Issuer, GoogleIAPIssuer)

		return orDefault(keyURL, GoogleIAPKeyURL), nil
	case PresetAWSALB:
		m.userHeader = orDefault(cfg.UserHeader, AWSALBHeader)

		return albKeyBase(cfg.JWT)
	case PresetCustom:
		if cfg.JWT.SignerARN != "" || cfg.JWT.Region != "" {
			return "", fmt.Errorf("%w: a custom issuer has no signer and no region", ErrConfig)
		}

		return keyURL, nil
	default:
		return "", fmt.Errorf("%w: the preset is not known", ErrConfig)
	}
}

// albKeyBase returns the base of the key URL of the AWS ALB: the KeyURL of
// j, or the URL of the region of j.
func albKeyBase(j JWTConfig) (string, error) {
	keyURL := j.KeyURL

	if keyURL == "" && j.Region != "" {
		if !regionPattern.MatchString(j.Region) {
			return "", fmt.Errorf("%w: the AWS region is not valid", ErrConfig)
		}

		keyURL = "https://public-keys.auth.elb." + j.Region + ".amazonaws.com/"
	}

	if j.SignerARN == "" || j.Audience != "" || !strings.HasSuffix(keyURL, "/") {
		return "", fmt.Errorf(
			"%w: the AWS ALB needs the ARN of the load balancer and a region, and has no audience", ErrConfig,
		)
	}

	return keyURL, nil
}

// checkKeyURL refuses a key URL that is not an absolute https URL with a
// host, or that has user information, a query or a fragment. The egress
// client checks the URL and the address again on each request.
func checkKeyURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" ||
		u.Fragment != "" || len(raw) > egress.MaxURLBytes {
		return fmt.Errorf("%w: the key URL must be an https URL with no user, query or fragment", ErrConfig)
	}

	return nil
}

// orDefault returns v, or def when v is empty.
func orDefault(v, def string) string {
	if v == "" {
		return def
	}

	return v
}
