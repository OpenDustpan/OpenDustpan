package job

import "fmt"

// SourceKind tells where the start of a job came from. The job keeps it, so
// a policy and a reader know whether a person, a schedule or another feature
// of the server asked for the work. The server sets it from the caller of
// Service.Start: a client of the REST API can never choose it.
type SourceKind string

// The source kinds of a job. The names equal those of the gate facts.
const (
	// SourceAPI is a start through the REST API, by a person or a token. It is
	// the default.
	SourceAPI SourceKind = "api"
	// SourceAgent is a start by an agent for the user who asked it.
	SourceAgent SourceKind = "agent"
	// SourceCatalog is a start that a Catalog request makes.
	SourceCatalog SourceKind = "catalog"
	// SourceForge is a start that an event of a forge makes, for example a
	// push to a review request.
	SourceForge SourceKind = "forge"
	// SourceSchedule is a start that a schedule of the server makes, for
	// example a drift check.
	SourceSchedule SourceKind = "schedule"
	// SourceTrigger is a start that a run trigger makes after another unit.
	SourceTrigger SourceKind = "trigger"
)

// SourceKinds lists the source kinds, in the order of the table above.
func SourceKinds() []SourceKind {
	return []SourceKind{SourceAPI, SourceAgent, SourceCatalog, SourceForge, SourceSchedule, SourceTrigger}
}

// Valid reports whether k is one of the source kinds of this package.
func (k SourceKind) Valid() bool {
	switch k {
	case SourceAPI, SourceAgent, SourceCatalog, SourceForge, SourceSchedule, SourceTrigger:
		return true
	default:
		return false
	}
}

// ParseSourceKind returns the source kind s, or an error that wraps
// ErrInvalid. It does not change s to make it valid.
func ParseSourceKind(s string) (SourceKind, error) {
	if k := SourceKind(s); k.Valid() {
		return k, nil
	}

	return "", fmt.Errorf("%w: %q is not a source kind", ErrInvalid, s)
}
