// Package fakecred builds fake credentials for tests at run time.
//
// A test that checks the refusal or the masking of a credential needs a URL
// with a user name and a password. If the source holds that text as one
// literal, a secret scanner reports it. This package joins the parts when the
// test runs, so the source never holds the text. Only test code imports this
// package. The binary does not link it.
package fakecred

import "net/url"

// URL returns scheme://user:password@host/path, built from its parts with
// net/url. The host can hold a port. The path has no leading slash and can be
// empty. An empty password gives "user:@".
func URL(scheme, user, password, host, path string) string {
	u := url.URL{
		Scheme: scheme,
		User:   url.UserPassword(user, password),
		Host:   host,
		Path:   "/" + path,
	}
	return u.String()
}
