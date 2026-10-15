// Package audit is the tool-neutral audit log of OpenDustpan (ADR 41
// decision 13): append-only entries that record one action each, with a hash
// chain over the entries of each namespace. A reader lists the entries of a
// namespace newest first and checks the chain of a namespace.
//
// An entry records who acted (subject, identity provider subject, access
// token ID), what happened (action, object, version, value path), when it
// happened (UTC, Unix milliseconds), where it came from (source IP, user
// agent, channel) and how it ended (outcome, request ID). Kind tells a reveal
// (a person asked to see a secret value) apart from a tool operation (a tool
// needed secrets for its own work), so a reader never confuses the two.
//
// The chain covers a change or a deletion of an entry: each entry holds the
// SHA-256 of its own fields and of the previous entry of its namespace. It
// does not stop a person who can write to the database and compute the chain
// again. An anchor outside the database, retention, export to a SIEM and the
// full catalog of events are RFC 22.
package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// Kind names the kind of access that an entry records. Only the values below
// exist, so a reader never confuses a tool that needed secrets with a person
// who asked to see one, or with a person who used an override.
type Kind string

// Kinds known to the audit log.
const (
	// KindToolOperation records that a tool needed secrets for its own work,
	// for example a plan, an apply, an update or a read of remote state.
	KindToolOperation Kind = "tool-operation"
	// KindReveal records that a person asked to see a secret value, through
	// the console, a CLI or the API.
	KindReveal Kind = "reveal"
	// KindOverride records that a person used an override of ADR 44: a forced
	// write, a write without the lock, or an unfenced force-unlock.
	KindOverride Kind = "override"
	// KindChange records an action that changes something, for example a
	// principal or a group of the installation, or a delete of a unit (ADR 60,
	// decision 4).
	KindChange Kind = "change"
	// KindReadAccess records a read of the access-review export of an object
	// (ADR 52, decision 13): who read the authorization map, of which
	// object, as a snapshot or a history.
	KindReadAccess Kind = "read-access"
)

// ServerScope is the scope of the entries of actions that are not in a
// namespace, for example a change of a principal or a group (ADR 60,
// decision 7). It has its own sequence and chain. Its wire form starts with
// '@', which namespace names never contain, so it never equals a namespace.
const ServerScope = namespace.Name("@server")

// Channel names where the request came from, as the client declares it.
type Channel string

// Channels known to the audit log.
const (
	// ChannelAPI is a request from a tool CLI or another API client.
	ChannelAPI Channel = "api"
	// ChannelConsole is a request from the web console.
	ChannelConsole Channel = "console"
	// ChannelStartup is the entry that an instance writes at each start with
	// its settings (ADR 63 decision 10).
	ChannelStartup Channel = "startup"
	// ChannelConfigFile is the entry of a reload of the configuration file
	// that changed a setting (ADR 63 decision 9).
	ChannelConfigFile Channel = "config-file"
)

// Outcome names how the recorded action ended.
type Outcome string

// Outcomes known to the audit log.
const (
	// OutcomeSuccess records an action that happened.
	OutcomeSuccess Outcome = "success"
	// OutcomeFailure records an action that failed.
	OutcomeFailure Outcome = "failure"
	// OutcomeDenied records an action that the server refused.
	OutcomeDenied Outcome = "denied"
)

// MaxUserAgentLen caps the user agent that an entry keeps, in bytes. The
// HTTP adapter truncates longer values before it appends.
const MaxUserAgentLen = 512

// DefaultPageSize is the page size of a list that names none.
const DefaultPageSize = 50

// MaxPageSize caps the page size of a list.
const MaxPageSize = 200

// CheckPageSize bounds the memory of a chain check: Check reads the chain
// oldest first in pages of this many entries, so a namespace with a large
// log never grows the check without a bound. The walk still visits every
// entry, one page at a time.
const CheckPageSize = 100

// Sentinel errors returned by this package and by Store implementations.
var (
	// ErrInvalidEntry reports an entry that fails validation.
	ErrInvalidEntry = errors.New("invalid audit entry")
	// ErrNotFound reports that a namespace in a fence does not exist.
	ErrNotFound = errors.New("audit namespace not found")
)

// Entry is one append-only record of an action in a namespace. Namespace and
// Sequence order the entries of one namespace: the first entry of a
// namespace has sequence 1. Subject names the principal that acted.
// IDPSubject names the subject at the identity provider and stays empty
// until an identity provider exists. TokenID names the stored access token
// that acted (ADR 53), and stays empty for a bootstrap admin token. Action is the tool-neutral action, for
// example "unit:decrypt". Object is a unit reference
// ("<tool>:<namespace>/<name>") or a namespace. Version and ValuePath name
// the state version and the value path of a state read and stay empty
// otherwise. ValueCount holds the count of secret values of a decrypt call
// and stays nil for every other entry. Changes holds the keys of the entry
// with their old and new values, for example the settings of a
// ChannelStartup or ChannelConfigFile entry (ADR 63), and stays nil for an
// entry without them. At is the time of the action in UTC. SourceIP is the
// client address (internal/clientip): the TCP peer, or the address that a
// trusted proxy forwarded. UserAgent is the client user agent, capped at
// MaxUserAgentLen bytes. Channel is where the request came from.
// RequestID is the X-Request-Id of the request (ADR 32). ObjectID is the
// stable ID of the unit that Object names (ADR 19, ADR 47), so the history of
// one unit can be found across a rename or a reuse of its name; it is empty
// when the object is not a unit or the ID is not known. PrevHash holds the
// hash of the previous entry of the namespace and is empty for the first
// entry. Hash holds the SHA-256 of this entry (see ComputeHash).
// FenceVersion is the failover version of the namespace when the store
// appended the entry, and 0 in ServerScope. The store sets it in the
// transaction of the append, and the hash covers it, so a verifier can see
// where a forced failover forked the chain of a namespace.
type Entry struct {
	At           time.Time
	Namespace    namespace.Name
	Subject      string
	IDPSubject   string
	TokenID      string
	Action       string
	Object       string
	Version      string
	ValuePath    string
	ValueCount   *int64
	Changes      []Change
	SourceIP     string
	UserAgent    string
	Channel      Channel
	Kind         Kind
	Outcome      Outcome
	RequestID    string
	ObjectID     string
	PrevHash     string
	Hash         string
	Sequence     int64
	FenceVersion int64
}

// AtMs returns the time of the entry in Unix milliseconds, as stored.
func (e Entry) AtMs() int64 {
	return e.At.UnixMilli()
}

// Validate reports whether the entry holds usable values. It does not check
// Sequence, PrevHash or Hash: the store assigns them on append.
func (e Entry) Validate() error {
	if err := e.validateWho(); err != nil {
		return err
	}

	if err := e.validateWhat(); err != nil {
		return err
	}

	return e.validateHow()
}

// validateWho checks the namespace, subject and time of the entry.
func (e Entry) validateWho() error {
	switch {
	case e.Namespace.String() == "":
		return fmt.Errorf("%w: missing namespace", ErrInvalidEntry)
	case e.Subject == "":
		return fmt.Errorf("%w: missing subject", ErrInvalidEntry)
	case e.At.IsZero():
		return fmt.Errorf("%w: missing time", ErrInvalidEntry)
	default:
		return nil
	}
}

// validateWhat checks the action, object and user agent of the entry.
func (e Entry) validateWhat() error {
	switch {
	case e.Action == "":
		return fmt.Errorf("%w: missing action", ErrInvalidEntry)
	case e.Object == "":
		return fmt.Errorf("%w: missing object", ErrInvalidEntry)
	case len(e.UserAgent) > MaxUserAgentLen:
		return fmt.Errorf("%w: user agent longer than %d bytes", ErrInvalidEntry, MaxUserAgentLen)
	default:
		return nil
	}
}

// validateHow checks the channel, kind, outcome and changes of the entry.
func (e Entry) validateHow() error {
	if err := validateChanges(e.Changes); err != nil {
		return err
	}

	switch {
	case !knownChannel(e.Channel):
		return fmt.Errorf("%w: unknown channel %q", ErrInvalidEntry, e.Channel)
	case e.Kind != KindToolOperation && e.Kind != KindReveal && e.Kind != KindOverride && e.Kind != KindChange &&
		e.Kind != KindReadAccess:
		return fmt.Errorf("%w: unknown kind %q", ErrInvalidEntry, e.Kind)
	case e.Outcome != OutcomeSuccess && e.Outcome != OutcomeFailure && e.Outcome != OutcomeDenied:
		return fmt.Errorf("%w: unknown outcome %q", ErrInvalidEntry, e.Outcome)
	default:
		return nil
	}
}

// knownChannel reports whether c is one of the channels of this package.
func knownChannel(c Channel) bool {
	switch c {
	case ChannelAPI, ChannelConsole, ChannelStartup, ChannelConfigFile:
		return true
	default:
		return false
	}
}

// NormalizeChannel maps a client-declared channel value to a Channel.
// Unknown values are ChannelAPI: a client that declares nothing usable is an
// API client.
func NormalizeChannel(raw string) Channel {
	if Channel(strings.ToLower(strings.TrimSpace(raw))) == ChannelConsole {
		return ChannelConsole
	}

	return ChannelAPI
}

// hashFieldSeparator splits the fields of the hash encoding. It is a fixed
// part of the documented encoding, not a magic number.
const hashFieldSeparator = 0x1F

// ComputeHash returns the hex-encoded SHA-256 over the fixed encoding of the
// fields of e and prevHash. The encoding joins every field below with a
// unit separator (0x1F), in this order: namespace, sequence (decimal),
// subject, identity provider subject, token ID, action, object, version,
// value path, time (Unix milliseconds, decimal), source IP, user agent,
// channel, kind, outcome, request ID, previous hash. Numeric fields use
// decimal text. After the previous hash, the encoding appends the optional
// fields that e sets, each as "<name>=<value>" with the same separator, in
// this fixed order: value_count (the decimal count), then object_id (ADR 47,
// which its own change adds), then changes (the canonical JSON of
// EncodeChanges), then fence_version (the decimal failover version, when
// it is above 0). An optional field that e does not set adds nothing, so an
// entry written before the field keeps its hash. The hash takes no key:
// any installation can compute it again from the stored fields. The separator never
// appears in a validated field except the user agent and free-form names,
// where it is kept as is: the encoding is fixed and documented, not
// ambiguous-free, and its only use is the chain check of the same code.
func ComputeHash(e Entry, prevHash string) string {
	return hashWithChanges(e, prevHash, EncodeChanges(e.Changes))
}

// ComputeStoredHash answers the hash of a stored entry: the encoding of
// ComputeHash with changesText, the changes text as the store holds it,
// in place of the encoding of e.Changes. The chain check uses it, so a
// changed text breaks the hash, also when it decodes to the same changes or
// does not decode at all.
func ComputeStoredHash(e Entry, prevHash, changesText string) string {
	return hashWithChanges(e, prevHash, changesText)
}

// hashWithChanges answers the hash of e and prevHash with changesText as
// the value of the optional field changes.
func hashWithChanges(e Entry, prevHash, changesText string) string {
	var sb strings.Builder

	write := func(s string) {
		sb.WriteString(s)
		sb.WriteByte(hashFieldSeparator)
	}

	write(e.Namespace.String())
	write(strconv.FormatInt(e.Sequence, 10))
	write(e.Subject)
	write(e.IDPSubject)
	write(e.TokenID)
	write(e.Action)
	write(e.Object)
	write(e.Version)
	write(e.ValuePath)
	write(strconv.FormatInt(e.AtMs(), 10))
	write(e.SourceIP)
	write(e.UserAgent)
	write(string(e.Channel))
	write(string(e.Kind))
	write(string(e.Outcome))
	write(e.RequestID)
	write(prevHash)

	if e.ValueCount != nil {
		write("value_count=" + strconv.FormatInt(*e.ValueCount, 10))
	}

	if e.ObjectID != "" {
		write("object_id=" + e.ObjectID)
	}

	if changesText != "" {
		write("changes=" + changesText)
	}

	if e.FenceVersion > 0 {
		write("fence_version=" + strconv.FormatInt(e.FenceVersion, 10))
	}

	sum := sha256.Sum256([]byte(sb.String()))

	return hex.EncodeToString(sum[:])
}

// Page selects one page of a list. Number is one-based; Size caps the
// entries. The zero value lists the first DefaultPageSize entries.
type Page struct {
	Number int
	Size   int
}

// Normalized returns the page with defaults applied: number below one reads
// the first page, size below one reads DefaultPageSize entries, size above
// MaxPageSize reads MaxPageSize entries.
func (p Page) Normalized() Page {
	out := p
	if out.Number < 1 {
		out.Number = 1
	}

	if out.Size < 1 {
		out.Size = DefaultPageSize
	}

	if out.Size > MaxPageSize {
		out.Size = MaxPageSize
	}

	return out
}

// Offset returns the offset of the page in entries.
func (p Page) Offset() int64 {
	return int64(p.Normalized().Number-1) * int64(p.Normalized().Size)
}

// Listing is one page of an audit list: the entries newest first and the
// total count of entries of the namespace.
type Listing struct {
	Entries []Entry
	Total   int64
}

// CheckResult reports the chain check of a namespace. Valid is true when
// every chained entry matches its stored hash and links to its
// predecessor. When Valid is false, BadSequence holds the sequence of the
// first entry that does not match: a changed field, a broken link or a gap
// after a deleted entry. Checked counts the entries the check read.
// Unchained counts the entries without a hash: the chain was off when they
// were appended. The check cannot prove such an entry
// unchanged.
type CheckResult struct {
	Checked     int64
	BadSequence int64
	Unchained   int64
	Valid       bool
}

// Store is the persistence port for audit entries. Append reads the last
// hash of the namespace and writes the entry in one transaction with the
// fence check, so the appends of one namespace form one sequence. List
// answers one page newest first. Check walks the chain oldest first and
// reports the first entry that does not match. No port updates or deletes
// an entry.
type Store interface {
	// Append writes e in the namespace of fence and returns the stored
	// entry with its sequence, previous hash and hash. It returns
	// ErrInvalidEntry if e fails validation or names another namespace,
	// namespace.ErrNotFound if the fence names a missing namespace, and
	// namespace.ErrFenced if the fence carries a stale failover version.
	Append(ctx context.Context, fence namespace.Fence, e Entry) (Entry, error)
	// List answers one page of the entries of ns, newest first, with the
	// total count. A namespace with no entries reads as an empty list. ns
	// can be ServerScope.
	List(ctx context.Context, ns namespace.Name, page Page) (Listing, error)
	// Check walks the chain of ns oldest first in pages of CheckPageSize
	// entries and reports the first entry that does not match. A namespace
	// with no entries checks as valid. ns can be ServerScope.
	Check(ctx context.Context, ns namespace.Name) (CheckResult, error)
}

// ServerStore appends the entries of ServerScope. It is a port of its own,
// so a store that keeps only namespace entries still meets Store. List and
// Check of Store read the scope.
type ServerStore interface {
	// AppendServer writes e in ServerScope and returns the stored entry with
	// its sequence, previous hash and hash. The scope has no fence: it
	// describes the installation. The appends of the scope form one
	// sequence, also when they run at the same time. It returns
	// ErrInvalidEntry if e fails validation or names another scope.
	AppendServer(ctx context.Context, e Entry) (Entry, error)
}
