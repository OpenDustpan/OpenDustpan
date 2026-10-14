// Package instance names one server process. Each start makes a new ID, and
// the server logs it once and uses it as the subject of the audit entries
// that the process writes on its own: the start entry and the entry of a
// reload of the configuration file (ADR 63 decisions 9 and 10).
//
// The ID lives here, and only here, so the instance registry of ADR 48 can
// take it over: the registry row can hold this ID.
package instance

import "opendustpan.org/opendustpan/internal/ident"

// Prefix starts every instance ID.
const Prefix = "ins-"

// NewID answers a new instance ID: Prefix and 20 random lowercase base32
// characters, as the other opaque IDs. It never uses the hostname: a
// hostname changes, and two instances can have the same one.
func NewID() string {
	return ident.NewID(Prefix)
}
