package casauth

import "net/url"

// SetServerURLForTest replaces the CAS server URL of m without the checks of
// New, so that a test can send a URL that New refuses to the later checks
// alone.
func SetServerURLForTest(m *Method, u *url.URL) {
	m.server = u
}
