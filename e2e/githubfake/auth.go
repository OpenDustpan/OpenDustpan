package githubfake

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// jwtParts is the number of parts of a JWT: the header, the claims and the
// signature.
const jwtParts = 3

// maxJWTLifetime is the longest lifetime of a JWT that GitHub accepts.
const maxJWTLifetime = 10 * time.Minute

// installationTokenLifetime is the lifetime of an installation token.
const installationTokenLifetime = time.Hour

// installationToken is a token that the fake made for the installation.
type installationToken struct {
	value       string
	permissions map[string]string
	// repos limits the token to these repositories, by short name. Nil
	// means every repository of the installation.
	repos   []string
	expires time.Time
	revoked bool
}

// peek returns the time of the clock with no tick. The caller holds the lock.
func (f *Fake) peek() time.Time {
	if f.cfg.Now != nil {
		return f.cfg.Now()
	}

	return Epoch().Add(time.Duration(f.tick) * time.Second)
}

// bearerOf returns the credential of a request, and false when the header
// has none.
func bearerOf(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")

	for _, scheme := range []string{"Bearer ", "token "} {
		if v, ok := strings.CutPrefix(h, scheme); ok && v != "" {
			return v, true
		}
	}

	return "", false
}

// classify names the kind of the credential of r. The caller holds the lock.
func (f *Fake) classify(r *http.Request) BearerKind {
	value, ok := bearerOf(r)
	if !ok {
		return BearerNone
	}

	if f.faults.refuseAll {
		return BearerInvalid
	}

	if f.cfg.UserToken != "" && subtle.ConstantTimeCompare([]byte(value), []byte(f.cfg.UserToken)) == 1 {
		return BearerUser
	}

	if t, found := f.tokens[value]; found {
		if !t.revoked && t.expires.After(f.peek()) {
			return BearerInstallation
		}

		return BearerInvalid
	}

	if f.verifyJWT(value) {
		return BearerApp
	}

	return BearerInvalid
}

// verifyJWT checks the RS256 signature, the issuer and the lifetime of a JWT
// of the app.
func (f *Fake) verifyJWT(token string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != jwtParts {
		return false
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}

	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(&f.key.PublicKey, crypto.SHA256, sum[:], sig) != nil {
		return false
	}

	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}

	var claims struct {
		Issuer  json.Number `json:"iss"`
		Issued  int64       `json:"iat"`
		Expires int64       `json:"exp"`
	}

	if json.Unmarshal(raw, &claims) != nil {
		return false
	}

	lifetime := time.Duration(claims.Expires-claims.Issued) * time.Second

	return claims.Issuer.String() == f.cfg.AppID && lifetime > 0 && lifetime <= maxJWTLifetime
}

// authorize checks the credential of r. It answers 401 for a missing or bad
// credential and 403 for a kind that is not in allowed, and returns false
// then. The caller holds the lock.
func (f *Fake) authorize(w http.ResponseWriter, r *http.Request, allowed ...BearerKind) bool {
	kind := f.classify(r)

	switch {
	case kind == BearerNone || kind == BearerInvalid:
		f.message(w, http.StatusUnauthorized, "Bad credentials")

		return false
	case !slices.Contains(allowed, kind):
		f.message(w, http.StatusForbidden, "Resource not accessible by integration")

		return false
	}

	return true
}

// newToken makes an installation token. The caller holds the lock.
func (f *Fake) newToken(perms map[string]string, repos []string) *installationToken {
	sum := sha256.Sum256([]byte("installation-token-" + strconv.FormatUint(f.next(), 10)))
	t := &installationToken{
		value:       "ghs_" + hex.EncodeToString(sum[:])[:36],
		permissions: perms,
		repos:       repos,
		expires:     f.now().Add(installationTokenLifetime),
		revoked:     false,
	}
	f.tokens[t.value] = t

	return t
}

// bearerToken returns the installation token of r, or nil for another kind
// of credential. The caller holds the lock.
func (f *Fake) bearerToken(r *http.Request) *installationToken {
	value, _ := bearerOf(r)

	return f.tokens[value]
}

// permitted reports whether the credential of r holds the permission name at
// level (read or write) on the repository fullName. A user token holds
// every permission on every repository. The caller holds the lock.
func (f *Fake) permitted(r *http.Request, fullName, name, level string) bool {
	t := f.bearerToken(r)
	if t == nil {
		return true
	}

	if t.repos != nil && !slices.Contains(t.repos, shortName(fullName)) {
		return false
	}

	return levelRank(t.permissions[name]) >= levelRank(level)
}

func shortName(fullName string) string {
	_, name, _ := strings.Cut(fullName, "/")

	return name
}

// The permission levels and their ranks.
const (
	levelNameRead  = "read"
	levelNameWrite = "write"
)

const (
	levelRead  = 1
	levelWrite = 2
)

// levelRank orders the permission levels. An unknown level is zero.
func levelRank(level string) int {
	switch level {
	case levelNameRead:
		return levelRead
	case levelNameWrite:
		return levelWrite
	default:
		return 0
	}
}
