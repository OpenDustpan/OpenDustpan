package forge

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Errors of the connections that the server keeps. They are separate from the
// sentinels of the port, which an adapter returns.
var (
	// ErrConnectionNotFound reports a connection ID that no connection of the
	// namespace has.
	ErrConnectionNotFound = errors.New("connection to the Git host not found")
	// ErrConnectionExists reports a second connection to an installation that
	// a connection of the namespace already uses.
	ErrConnectionExists = errors.New("the namespace has a connection to this installation already")
	// ErrConnectionLimit reports a namespace that has MaxConnectionsPerNamespace
	// connections.
	ErrConnectionLimit = errors.New("the namespace has the most connections to the Git host")
	// ErrDefaultConnection reports a delete of the default connection while the
	// namespace has other connections. A person makes another connection the
	// default first.
	ErrDefaultConnection = errors.New("the connection is the default connection of the namespace")
	// ErrNoRegistration reports that the operator has not registered the GitHub
	// App of the installation.
	ErrNoRegistration = errors.New("no GitHub App is registered for the installation")
	// ErrInstallationNotAssigned reports that the GitHub App installation is
	// not assigned to the namespace of the connection. The operator assigns an
	// installation to one namespace.
	ErrInstallationNotAssigned = errors.New("the installation is not assigned to the namespace")
	// ErrAssignmentNotFound reports an installation ID that has no assignment.
	ErrAssignmentNotFound = errors.New("the installation has no assignment")
	// ErrRegistrationInUse reports a delete of the registration while an
	// installation is assigned to a namespace.
	ErrRegistrationInUse = errors.New("the GitHub App has assigned installations")
	// ErrVersionConflict reports a change or a delete whose expected version
	// differs from the stored version.
	ErrVersionConflict = errors.New("the object changed after it was read")
	// ErrAddressChanged reports a stored credential whose address digest differs
	// from the address and the parameters now. The server never sends such a
	// credential.
	ErrAddressChanged = errors.New("the address of the GitHub App changed after the credential was stored")
	// ErrKeyNotOpened reports a stored key that does not open with the binding
	// of the address that the registration holds now: the stored bytes or the
	// address changed after the key was sealed.
	ErrKeyNotOpened = errors.New("the stored key of the GitHub App does not open")
	// ErrAddressRefused reports an API address that the outbound address rules
	// refuse when the connection opens.
	ErrAddressRefused = errors.New("the outbound address rules refuse the API address of the GitHub App")
)

// Limits of the connections of a namespace.
const (
	// MaxConnectionsPerNamespace is the most connections that one namespace has.
	MaxConnectionsPerNamespace = 50
	// maxIDDigits is the most digits of an app ID and of an installation ID.
	maxIDDigits  = 20
	connIDPrefix = "gitconn-"
	// idBytes is the length of a random connection ID or hook ID, in bytes. It
	// gives 128 bits.
	idBytes = 16
	// setIDBytes is the length of the random part of a credential set ID.
	setIDBytes = 8
)

var (
	numericIDPattern  = regexp.MustCompile(`^[1-9]\d{0,19}$`)
	connectionPattern = regexp.MustCompile(`^gitconn-[0-9a-f]{32}$`)
	hookIDPattern     = regexp.MustCompile(`^[0-9a-f]{32}$`)
	setIDPattern      = regexp.MustCompile(`^set-[0-9a-f]{16}$`)
)

// ValidNumericID reports whether id is an app ID or an installation ID of a
// GitHub App: 1 to 20 decimal digits.
func ValidNumericID(id string) bool {
	return len(id) <= maxIDDigits && numericIDPattern.MatchString(id)
}

// ValidConnectionID reports whether id has the form of a connection ID.
func ValidConnectionID(id string) bool {
	return connectionPattern.MatchString(id)
}

// ValidHookID reports whether id has the form of a hook ID.
func ValidHookID(id string) bool {
	return hookIDPattern.MatchString(id)
}

// ValidSetID reports whether id has the form of a credential set ID.
func ValidSetID(id string) bool {
	return setIDPattern.MatchString(id)
}

// NewConnectionID returns a new connection ID from the system random source.
func NewConnectionID() string {
	return connIDPrefix + randomHex(idBytes)
}

// NewHookID returns a new hook ID from the system random source. It holds 128
// bits, so a person cannot guess it.
func NewHookID() string {
	return randomHex(idBytes)
}

// NewSetID returns a new credential set ID.
func NewSetID() string {
	return "set-" + randomHex(setIDBytes)
}

func randomHex(n int) string {
	b := make([]byte, n)

	// crypto/rand.Read does not return an error on any supported platform.
	_, _ = rand.Read(b)

	return hex.EncodeToString(b)
}

// AppScope says who owns the GitHub App of a connection.
type AppScope string

const (
	// AppScopeInstallation is the one GitHub App of the installation of the
	// server. The operator registers it one time. Its address, its app ID and
	// its keys belong to the registration, never to a connection.
	AppScopeInstallation AppScope = "installation"
)

// ConnectionRecord is a stored connection of a namespace. It holds no
// credential and no API address: a connection of the installation-level app
// takes both from the registration at each Open.
type ConnectionRecord struct {
	CreatedAt time.Time
	UpdatedAt time.Time
	Namespace namespace.Name
	ID        string
	Kind      Kind
	Auth      AuthKind
	Scope     AppScope
	// InstallationID is the ID of the GitHub App installation that the operator
	// assigned to the namespace. No later call changes it.
	InstallationID string
	// Version counts the changes of the record. A new record has version 1.
	Version int64
	// Push is true when the connection may write to repositories.
	Push bool
	// Default is true for the connection that a feature uses when it names none.
	// A namespace has at most one default connection.
	Default bool
}

// Validate returns an error that wraps ErrInvalid for a record that a store
// must not keep. A store checks a record again when it reads one, because a
// backup or another version of the server can have written the row.
func (c ConnectionRecord) Validate() error {
	switch {
	case c.Namespace.String() == "":
		return fmt.Errorf("%w: the connection has no namespace", ErrInvalid)
	case !ValidConnectionID(c.ID):
		return fmt.Errorf("%w: the connection ID is not valid", ErrInvalid)
	case c.Kind != KindGitHub:
		return fmt.Errorf("%w: the connection kind %q is not supported", ErrInvalid, c.Kind)
	case c.Auth != AuthApp || c.Scope != AppScopeInstallation:
		return fmt.Errorf("%w: only a connection of the installation-level GitHub App is supported", ErrInvalid)
	case !ValidNumericID(c.InstallationID):
		return fmt.Errorf("%w: the installation ID is 1 to %d digits", ErrInvalid, maxIDDigits)
	}

	return validateVersionAndTimes("connection", c.Version, c.CreatedAt, c.UpdatedAt)
}

// validateVersionAndTimes checks the version and the times that every stored
// object holds. what names the object in the error.
func validateVersionAndTimes(what string, version int64, created, updated time.Time) error {
	switch {
	case version < 1:
		return fmt.Errorf("%w: the version of a %s is 1 or more", ErrInvalid, what)
	case created.IsZero() || updated.Before(created):
		return fmt.Errorf("%w: the %s needs a creation time no later than its update time", ErrInvalid, what)
	}

	return nil
}

// Registration is the GitHub App that the operator registered for the
// installation of the server. The private key and the event secret are
// sealed under installation scopes, and no read answers either of them.
type Registration struct {
	CreatedAt time.Time
	UpdatedAt time.Time
	// KeyCreatedAt and EventSecretCreatedAt are the times when the operator
	// stored the current private key and the current event secret.
	KeyCreatedAt         time.Time
	EventSecretCreatedAt time.Time
	Kind                 Kind
	AppID                string
	// BaseURL is the API address of GitHub, for example https://api.github.com.
	BaseURL string
	// HookID names the registration in the address of the webhook. It does not
	// change when the operator replaces a key or an event secret.
	HookID            string
	KeySetID          string
	EventSecretSetID  string
	AddressDigest     string
	SealedKey         []byte
	SealedEventSecret []byte
	Version           int64
}

// Digest returns the address digest of the values of r. The server stores it
// with the sealed key, and Open recomputes it from the values that the row
// holds now.
func (r *Registration) Digest() string {
	return AddressDigest(r.Kind, r.BaseURL, r.AppID)
}

// AddressDigest returns the hex SHA-256 over the kind, the API address and the
// app ID. The fields have length prefixes, so two lists of values never share
// a digest.
func AddressDigest(kind Kind, baseURL, appID string) string {
	h := sha256.New()

	for _, field := range []string{"git-host-address", string(kind), baseURL, appID} {
		// A hash.Hash never returns an error from Write.
		_, _ = h.Write([]byte(strconv.Itoa(len(field)) + ":" + field))
	}

	return hex.EncodeToString(h.Sum(nil))
}

// Validate returns an error that wraps ErrInvalid for a registration that a
// store must not keep.
func (r *Registration) Validate() error {
	switch {
	case r.Kind != KindGitHub:
		return fmt.Errorf("%w: the registration kind %q is not supported", ErrInvalid, r.Kind)
	case !ValidNumericID(r.AppID):
		return fmt.Errorf("%w: the app ID is 1 to %d digits", ErrInvalid, maxIDDigits)
	case r.BaseURL == "":
		return fmt.Errorf("%w: the API address is empty", ErrInvalid)
	case !ValidHookID(r.HookID):
		return fmt.Errorf("%w: the hook ID is not valid", ErrInvalid)
	case !ValidSetID(r.KeySetID) || !ValidSetID(r.EventSecretSetID):
		return fmt.Errorf("%w: a credential set ID is not valid", ErrInvalid)
	case len(r.SealedKey) == 0 || len(r.SealedEventSecret) == 0:
		return fmt.Errorf("%w: the registration needs a sealed key and a sealed event secret", ErrInvalid)
	case r.AddressDigest != r.Digest():
		return fmt.Errorf("%w: the address digest does not match the address", ErrInvalid)
	}

	return validateVersionAndTimes("registration", r.Version, r.CreatedAt, r.UpdatedAt)
}

// Assignment gives one GitHub App installation to one namespace. The
// installation-to-namespace map is data of the installation: a backup of one
// namespace does not hold it, and only the operator changes it.
type Assignment struct {
	CreatedAt      time.Time
	UpdatedAt      time.Time
	InstallationID string
	Namespace      namespace.Name
	Version        int64
}

// Validate returns an error that wraps ErrInvalid for an assignment that a
// store must not keep.
func (a Assignment) Validate() error {
	switch {
	case !ValidNumericID(a.InstallationID):
		return fmt.Errorf("%w: the installation ID is 1 to %d digits", ErrInvalid, maxIDDigits)
	case a.Namespace.String() == "":
		return fmt.Errorf("%w: the assignment has no namespace", ErrInvalid)
	}

	return validateVersionAndTimes("assignment", a.Version, a.CreatedAt, a.UpdatedAt)
}

// Store keeps the connections of the namespaces, the registration of the
// installation-level GitHub App and the installation-to-namespace map. A SQL
// adapter serves it.
//
// Every write of a namespace carries the fence of the namespace and the audit
// entry of the change. Every write of the installation carries the audit entry
// of the scope @server. The store checks the fence, writes the change and
// appends the entry in one transaction, and it changes nothing when any of
// them fails (fail closed). When the context of a write carries an idempotency
// completion, the same transaction stores the answer of the request.
//
// A write with an ifVersion above 0 changes the object only when its stored
// version equals ifVersion, and returns ErrVersionConflict otherwise. An
// ifVersion of 0 sets no condition. A read returns an object only when it
// passes the Validate of its type: a row that fails is not served.
type Store interface {
	ConnectionStore
	RegistrationStore
	AssignmentStore
}

// ConnectionStore keeps the connections of the namespaces. See Store for the
// rules that every write follows.
type ConnectionStore interface {
	// CreateConnection stores c with version 1. The first connection of a
	// namespace is the default, whatever c.Default says, and a later one is
	// not. It returns ErrInvalid, ErrConnectionExists for a connection of the
	// namespace to the same installation, ErrConnectionLimit, and
	// namespace.ErrFenced. It returns the stored record.
	CreateConnection(
		ctx context.Context,
		c ConnectionRecord,
		fence namespace.Fence,
		e audit.Entry,
	) (ConnectionRecord, error)
	// GetConnection returns the connection id of ns, or ErrConnectionNotFound.
	GetConnection(ctx context.Context, ns namespace.Name, id string) (ConnectionRecord, error)
	// ListConnections returns the connections of ns, ordered by creation
	// time, then ID. A namespace with none gives an empty list.
	ListConnections(ctx context.Context, ns namespace.Name) ([]ConnectionRecord, error)
	// DeleteConnection removes the connection id of ns. It returns
	// ErrConnectionNotFound, ErrDefaultConnection for the default connection
	// while another connection exists, ErrVersionConflict and
	// namespace.ErrFenced.
	DeleteConnection(
		ctx context.Context, ns namespace.Name, id string, ifVersion int64, fence namespace.Fence, e audit.Entry,
	) error
	// MakeDefault makes the connection id the default of ns, and removes the
	// flag of the old default in the same transaction. A connection that is the
	// default already changes nothing and returns without an audit entry. It
	// returns ErrConnectionNotFound, ErrVersionConflict and namespace.ErrFenced.
	MakeDefault(
		ctx context.Context, ns namespace.Name, id string, ifVersion int64, fence namespace.Fence, e audit.Entry,
	) (ConnectionRecord, error)
}

// RegistrationStore keeps the registration of the installation-level GitHub
// App. See Store for the rules that every write follows.
type RegistrationStore interface {
	// GetRegistration returns the registration of kind, or ErrNoRegistration.
	GetRegistration(ctx context.Context, kind Kind) (Registration, error)
	// PutRegistration stores r. It creates the registration with version 1, or
	// it replaces the stored one, keeps its hook ID and gives it the next
	// version. It returns ErrInvalid and ErrVersionConflict. An ifVersion above
	// 0 for a missing registration returns ErrNoRegistration.
	PutRegistration(ctx context.Context, r Registration, ifVersion int64, e audit.Entry) (Registration, error)
	// DeleteRegistration removes the registration of kind. It returns
	// ErrNoRegistration, ErrRegistrationInUse while an installation is
	// assigned, and ErrVersionConflict.
	DeleteRegistration(ctx context.Context, kind Kind, ifVersion int64, e audit.Entry) error
}

// AssignmentStore keeps the installation-to-namespace map. See Store for the
// rules that every write follows.
type AssignmentStore interface {
	// GetAssignment returns the assignment of an installation ID, or
	// ErrAssignmentNotFound. The service reads it at each Open.
	GetAssignment(ctx context.Context, installationID string) (Assignment, error)
	// ListAssignments returns every assignment, ordered by installation ID
	// as a number.
	ListAssignments(ctx context.Context) ([]Assignment, error)
	// PutAssignment stores a. It creates the assignment with version 1, or it
	// moves the installation to another namespace and gives it the next version.
	// It returns ErrInvalid, ErrVersionConflict, and namespace.ErrNotFound for
	// a namespace that does not exist. An ifVersion above 0 for a missing
	// assignment returns ErrAssignmentNotFound.
	PutAssignment(ctx context.Context, a Assignment, ifVersion int64, e audit.Entry) (Assignment, error)
	// DeleteAssignment removes the assignment of an installation ID. It returns
	// ErrAssignmentNotFound and ErrVersionConflict.
	DeleteAssignment(ctx context.Context, installationID string, ifVersion int64, e audit.Entry) error
}

// Consumers returns every consumer of the port, in a fixed order.
func Consumers() []Consumer {
	return slices.Clone([]Consumer{
		ConsumerWriteBack, ConsumerWriteBackReview, ConsumerFetch, ConsumerRunnerGit, ConsumerSelfService,
		ConsumerCauseDiff,
	})
}
