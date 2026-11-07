// Package fakecred builds fake credentials for tests at run time.
//
// A test that checks the refusal or the masking of a credential needs a URL
// with a user name and a password. If the source holds that text as one
// literal, a secret scanner reports it. This package joins the parts when the
// test runs, so the source never holds the text. Only test code imports this
// package. The binary does not link it.
package fakecred

import (
	"net/url"
	"strings"
)

// URL returns scheme://user:password@host followed by path, built from its
// parts with net/url. The host can hold a port. An empty path gives no slash
// after the host. A path that does not start with a slash gets one. A path
// that starts with a slash keeps it, and the slash is not doubled. An empty
// password gives "user:@".
func URL(scheme, user, password, host, path string) string {
	if path != "" {
		path = "/" + strings.TrimPrefix(path, "/")
	}
	u := url.URL{
		Scheme: scheme,
		User:   url.UserPassword(user, password),
		Host:   host,
		Path:   path,
	}
	return u.String()
}
