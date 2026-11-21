// Package profile models profiles: named sets of config, secrets and
// credentials in one namespace. A save makes a numbered revision that never
// changes. Each secret of a document is sealed with the data key of the
// profile before it reaches the store.
//
// The package is tool-neutral. It imports no tool package, no transport and
// no store adapter. This version holds the create, read, list, save and
// delete of profiles, the document rules for entries, the sealing of secrets,
// the rule for a routing change, the open of a profile and its sessions. The
// provider blocks, the drafts and the rotation are not in it: an open refuses
// a profile that holds a provider block.
package profile

import (
	"errors"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Sentinel errors returned by this package and by Store implementations.
// A port wraps them with fmt.Errorf("...: %w", err), and an adapter turns
// each driver error into one of them.
var (
	// ErrInvalid reports a bad name, ID, field or document.
	ErrInvalid = errors.New("invalid profile")
	// ErrNotFound reports an unknown profile or revision.
	ErrNotFound = errors.New("profile not found")
	// ErrConflict reports a name in use, or a write on a stale read.
	ErrConflict = errors.New("profile conflict")
	// ErrSchemaVersion reports a document with a higher schemaVersion than
	// the server reads.
	ErrSchemaVersion = errors.New("profile document schema version is too high")
	// ErrLimit reports a write above a limit. The message names the limit.
	ErrLimit = errors.New("profile limit reached")
	// ErrProvider reports a profile that holds a provider block: the open
	// cannot run it yet. The message names the block.
	ErrProvider = errors.New("profile needs a provider")
	// ErrNeverStore reports an open that asks a value marked never store.
	// The message names the path.
	ErrNeverStore = errors.New("profile value is never stored")
	// ErrFinal reports an open that replaces or removes a value that an
	// earlier profile marked final. The message names the path and both
	// profiles.
	ErrFinal = errors.New("profile value is final")
	// ErrReference reports an expression that reads a name other than
	// values. The message names the profile and the path.
	ErrReference = errors.New("profile expression reads an unknown name")
	// ErrImportCycle reports imports that make a cycle. The message names
	// the cycle.
	ErrImportCycle = errors.New("profile imports make a cycle")
	// ErrFreshRequired reports a reveal with no check of a fresh
	// authentication. The read fails closed: no value leaves.
	ErrFreshRequired = errors.New("a reveal needs a fresh authentication")
)

// RoutingError reports a change that moves where secrets go and that the
// caller may not make, because the caller lacks profile:open. Changes names
// each block, import or path that the change touches. It wraps
// identity.ErrDenied.
type RoutingError struct {
	Changes []string
}

// Error implements error.
func (e *RoutingError) Error() string {
	return "this change moves where secrets go (" + strings.Join(e.Changes, ", ") +
		") and needs profile:open"
}

// Unwrap returns identity.ErrDenied.
func (e *RoutingError) Unwrap() error { return identity.ErrDenied }

// Limits of this version.
const (
	// MaxProfiles is the largest number of live profiles in a namespace.
	MaxProfiles      = 1000 // MaxDocumentBytes is the largest document text.
	MaxDocumentBytes = 256 << 10
	// MaxLeaves is the largest number of leaves in values.
	MaxLeaves = 4096
	// MaxDepth is the deepest nesting of values.
	MaxDepth = 32
	// MaxProviderBlocks is the largest number of provider blocks.
	MaxProviderBlocks = 64
	// MaxRotatorBlocks is the largest number of rotator blocks.
	MaxRotatorBlocks = 16
	// MaxImports is the largest number of imports.
	MaxImports = 32
	// MaxDescription is the longest description, in characters.
	MaxDescription = 1000
	// SchemaVersion is the document schema version that the server reads.
	SchemaVersion = 1
)

// Limits of an open.
const (
	// MaxOpenRevisions is the largest number of profile revisions that one
	// open reads.
	MaxOpenRevisions = 64
	// MaxOpenDepth is the deepest import chain that one open follows.
	MaxOpenDepth = 16
	// MaxOpenBytes is the largest result of one open, in bytes.
	MaxOpenBytes = 4 << 20
	// MaxOpenPaths is the largest paths list of one open.
	MaxOpenPaths = 1024
)

// Profile is the metadata of one profile. The newest revision is Latest.
type Profile struct {
	ID        ID
	Namespace namespace.Name
	Name      Name
	// Latest is the number of the newest revision.
	Latest    int
	CreatedAt time.Time
	CreatedBy string
	UpdatedAt time.Time
}

// ETag returns the entity tag of the profile. It names the newest revision.
func (p Profile) ETag() string {
	return `"` + string(p.ID) + "." + itoa(p.Latest) + `"`
}

// Revision is one saved document. It never changes. Document is the stored
// text: each secret in it is a sealed value.
type Revision struct {
	Profile  ID
	Number   int
	Document []byte
	// SHA256 is the hex SHA-256 of Document.
	SHA256    string
	Author    string
	CreatedAt time.Time
	// Reason is an optional note of at most 500 characters.
	Reason string
}

// Filter selects one page of profiles of a namespace, ordered by name.
type Filter struct {
	Namespace namespace.Name
	// Folder limits the list to the profiles of one folder. Empty means all.
	Folder string
	// After is the last name of the previous page. Empty starts at the top.
	After Name
	// Limit is the page size, 1 or more.
	Limit int
}

// Page is one page of profiles. Next is the value of After for the next
// page, and empty on the last page.
type Page struct {
	Profiles []Profile
	Next     Name
}
