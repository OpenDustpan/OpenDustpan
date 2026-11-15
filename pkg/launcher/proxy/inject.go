package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// ErrCredential is the error for a credential that the job proxy cannot use.
// Its text is fixed: it never holds a name or a value of a credential.
var ErrCredential = errors.New("the credential is not available or is not valid")

// CredentialKind is the kind of a credential.
type CredentialKind int

// The kinds of a credential.
const (
	// KindHeader sets one header to Value.
	KindHeader CredentialKind = iota + 1
	// KindBasic sets Authorization to Basic with User and Password.
	KindBasic
	// KindQuery sets one query parameter to Value.
	KindQuery
	// KindClientCertificate is the client certificate for the TLS to the host.
	KindClientCertificate
)

// Credential is a credential that the job proxy adds to a request. The job
// proxy never logs it and never puts it in a record.
type Credential struct {
	Kind CredentialKind
	// Name is the header name or the query parameter name.
	Name     string
	Value    string
	User     string
	Password string
	// Certificate is the client certificate of KindClientCertificate.
	Certificate *tls.Certificate
}

// Credentials gives the credentials of the proxy rules by the name that the
// proxy rule holds.
type Credentials interface {
	Credential(name string) (Credential, error)
}

// Trust gives the CA pool of a proxy rule whose server trust names a CA
// export.
type Trust interface {
	Pool(name string) (*x509.CertPool, error)
}

// hopByHop lists the headers that name one connection. The job proxy never
// passes them on.
func hopByHop() []string {
	return []string{
		"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate",
		"Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
	}
}

// stripHopByHop removes the hop-by-hop headers, and each header that the
// Connection header names, from h.
func stripHopByHop(h http.Header) {
	for _, v := range h.Values("Connection") {
		for name := range strings.SplitSeq(v, ",") {
			if name = strings.TrimSpace(name); name != "" {
				h.Del(name)
			}
		}
	}

	for _, name := range hopByHop() {
		h.Del(name)
	}
}

// validHeaderName reports whether s is an HTTP token.
func validHeaderName(s string) bool {
	if s == "" {
		return false
	}

	for i := range len(s) {
		c := s[i]

		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0
		if !ok {
			return false
		}
	}

	return true
}

// validHeaderValue reports whether s can be a header value: no control
// character other than the tab, so no CR, no LF and no NUL.
func validHeaderValue(s string) bool {
	for i := range len(s) {
		if c := s[i]; c < 0x20 && c != '\t' || c == 0x7f {
			return false
		}
	}

	return true
}

// validate checks the fields of the credential for its kind, and returns
// ErrCredential for a credential that could split a header or fill none.
func (c Credential) validate() error {
	var ok bool

	switch c.Kind {
	case KindHeader:
		ok = c.headerValid()
	case KindBasic:
		ok = c.basicValid()
	case KindQuery:
		ok = c.Name != "" && c.Value != "" && validQueryText(c.Name)
	case KindClientCertificate:
		ok = c.Certificate != nil
	default:
		ok = false
	}

	if !ok {
		return ErrCredential
	}

	return nil
}

// headerValid reports whether the fields fit a header credential.
func (c Credential) headerValid() bool {
	return validHeaderName(c.Name) && c.Value != "" && validHeaderValue(c.Value)
}

// basicValid reports whether the fields fit a basic credential.
func (c Credential) basicValid() bool {
	return c.Password != "" && !strings.Contains(c.User, ":") &&
		validHeaderValue(c.User) && validHeaderValue(c.Password)
}

// noCredential returns the zero credential, which fails validate.
func noCredential() Credential {
	return Credential{Kind: 0, Name: "", Value: "", User: "", Password: "", Certificate: nil}
}

// validQueryText reports whether s has no control character.
func validQueryText(s string) bool {
	return validHeaderValue(s) && !strings.ContainsAny(s, "&=#")
}

// secrets returns the values that the response scan looks for.
func (c Credential) secrets() []string {
	// A client certificate has no value that a response could echo.
	if c.Kind == KindBasic {
		return []string{c.Password, c.User + ":" + c.Password}
	}

	if c.Kind == KindHeader || c.Kind == KindQuery {
		return []string{c.Value}
	}

	return nil
}

// strippedHeaders lists the headers that the job proxy deletes before it adds
// a credential, besides the header that the credential sets.
func strippedHeaders() []string {
	return []string{"Authorization", "Proxy-Authorization", "Cookie"}
}

// apply deletes the headers that could carry another credential, then adds
// the credential to h and to the query of u. Defense in depth: the client of
// the job is not trusted, so its own Authorization, Cookie and
// Proxy-Authorization never pass, whatever the credential is.
func (c Credential) apply(h http.Header, rawQuery string) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}

	for _, name := range strippedHeaders() {
		h.Del(name)
	}

	// A client certificate belongs to the TLS to the host, not to the request,
	// so it changes neither the headers nor the query.
	switch c.Kind {
	case KindHeader:
		h.Del(c.Name)
		h.Set(c.Name, c.Value)
	case KindBasic:
		h.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(c.User+":"+c.Password)))
	case KindQuery:
		return setQueryParam(rawQuery, c.Name, c.Value), nil
	case KindClientCertificate:
		return rawQuery, nil
	default:
		return "", ErrCredential
	}

	return rawQuery, nil
}

// setQueryParam removes each parameter named name from rawQuery, by the text
// of the name before the "=" (decoded and not), and adds name=value. It keeps
// the other parameters as they are, byte for byte.
func setQueryParam(rawQuery, name, value string) string {
	var kept []string

	if rawQuery != "" {
		for part := range strings.SplitSeq(rawQuery, "&") {
			key, _, _ := strings.Cut(part, "=")
			if dec, err := url.QueryUnescape(key); err == nil && dec == name || key == name {
				continue
			}

			if part != "" {
				kept = append(kept, part)
			}
		}
	}

	kept = append(kept, url.QueryEscape(name)+"="+url.QueryEscape(value))

	return strings.Join(kept, "&")
}
