package apidesc

import (
	"log/slog"
	"net/http"
)

// MisuseLog is the report of the server, for a test.
type MisuseLog = misuseLog

// NewMisuseLog returns a report that writes to the logger.
func NewMisuseLog(l *slog.Logger) *MisuseLog {
	m := new(MisuseLog)
	m.logger.Store(l)

	return m
}

// ReportInServer reports in the mode of the server.
func (m *misuseLog) ReportInServer(kind, route, name string) {
	m.report(modeLog, kind, route, name, FixCommand)
}

// QueryOfServer is QueryOf as the server runs it: an undeclared name does not
// panic.
func QueryOfServer(r *http.Request, labels map[string]Label) Query {
	return newQuery(r, labels, modeLog)
}
