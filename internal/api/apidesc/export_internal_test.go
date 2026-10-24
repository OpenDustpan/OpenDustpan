package apidesc

import "net/http"

// ReportInServer reports in the mode of the server.
func (m *Reporter) ReportInServer(kind, route, name string) {
	m.report(modeLog, kind, route, name, FixCommand)
}

// QueryOfServer is QueryOf as the server runs it: an undeclared name does not
// panic.
func QueryOfServer(r *http.Request, labels map[string]Label) Query {
	return newQuery(r, labels, modeLog)
}
