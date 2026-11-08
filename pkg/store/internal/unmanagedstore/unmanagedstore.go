// Package unmanagedstore holds the rules of the sweep target and sweep tables
// that the SQLite and PostgreSQL adapters share: the JSON form of the scopes,
// the command and the counts, and the check of a stored row.
//
// A stored row is untrusted when it is read, because a backup, a restore or
// another version of the server can have written it.
package unmanagedstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"opendustpan.org/opendustpan/internal/unmanaged"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
)

// ErrCorrupt reports a stored row that does not pass the checks of a target or
// a sweep. The adapters do not serve such a row.
var ErrCorrupt = errors.New("stored sweep target or sweep is not valid")

// TargetRow is the column representation of a sweep target. Namespace is the
// name that a read joins, Pool the name of the runner pool of the target, and
// EveryMs and TimeoutMs are milliseconds. A zero EveryMs means a target that
// sweeps only on request.
type TargetRow struct {
	Namespace   string
	Name        string
	Pool        string
	ScopesJSON  string
	CommandJSON string
	EveryMs     int64
	TimeoutMs   int64
	Enabled     bool
}

// ScanRow is the column representation of a sweep. Namespace is the name that a
// read joins, Seq is the sequence number of the sweep in its namespace, and
// EndedAtMs is 0 for a sweep that did not end.
type ScanRow struct {
	ID          string
	Namespace   string
	Seq         int64
	Target      string
	Kind        string
	Status      string
	StartedBy   string
	Reason      string
	ScopesJSON  string
	CountsJSON  string
	CreatedAtMs int64
	EndedAtMs   int64
}

// scopeDoc is the stored form of one account scope of a target.
type scopeDoc struct {
	Provider string   `json:"provider"`
	Scope    string   `json:"scope"`
	Regions  []string `json:"regions"`
}

// reportScopeDoc is the stored form of one scope of the header of a report.
type reportScopeDoc struct {
	Provider string   `json:"provider"`
	Scope    string   `json:"scope"`
	Error    string   `json:"error"`
	Regions  []string `json:"regions"`
	Complete bool     `json:"complete"`
}

// countsDoc is the stored form of the counts of a sweep.
type countsDoc struct {
	Records      int64 `json:"records"`
	Managed      int64 `json:"managed"`
	Elsewhere    int64 `json:"elsewhere"`
	NotInAnyUnit int64 `json:"notInAnyUnit"`
	LeftBehind   int64 `json:"leftBehind"`
	ManagedTwice int64 `json:"managedTwice"`
	Resolved     int64 `json:"resolved"`
	Rejected     int64 `json:"rejected"`
	OutOfScope   int64 `json:"outOfScope"`
}

// EncodeScopes returns the JSON of the account scopes of a target. The list
// keeps its order, and an empty list is an empty JSON list.
func EncodeScopes(scopes []unmanaged.TargetScope) (string, error) {
	docs := make([]scopeDoc, 0, len(scopes))

	for _, s := range scopes {
		docs = append(docs, scopeDoc{Provider: s.Provider, Scope: s.Scope, Regions: listOf(s.Regions)})
	}

	return encode("scopes", docs)
}

// EncodeReportScopes returns the JSON of the scopes of the header of a sweep.
// The list keeps its order, and an empty list is an empty JSON list.
func EncodeReportScopes(scopes []unmanaged.ReportScope) (string, error) {
	docs := make([]reportScopeDoc, 0, len(scopes))

	for _, s := range scopes {
		docs = append(docs, reportScopeDoc{
			Provider: s.Provider, Scope: s.Scope, Error: s.Error, Regions: listOf(s.Regions),
			Complete: s.Complete,
		})
	}

	return encode("scopes", docs)
}

// EncodeCommand returns the JSON of the arguments of the command of a target.
func EncodeCommand(command []string) (string, error) {
	return encode("command", listOf(command))
}

// EncodeCounts returns the JSON of the counts of a sweep.
func EncodeCounts(c unmanaged.ScanCounts) (string, error) {
	return encode("counts", countsDoc{
		Records: c.Records, Managed: c.Managed, Elsewhere: c.Elsewhere, NotInAnyUnit: c.NotInAnyUnit,
		LeftBehind: c.LeftBehind, ManagedTwice: c.ManagedTwice, Resolved: c.Resolved, Rejected: c.Rejected,
		OutOfScope: c.OutOfScope,
	})
}

// EncodeTarget returns the row of t, whose pool has the name pool. The caller
// has validated t and has read the pool.
func EncodeTarget(t unmanaged.Target, pool string) (TargetRow, error) {
	scopes, err := EncodeScopes(t.Scopes)
	if err != nil {
		return TargetRow{}, err
	}

	command, err := EncodeCommand(t.Command)
	if err != nil {
		return TargetRow{}, err
	}

	return TargetRow{
		Namespace: t.Namespace.String(), Name: t.Name, Pool: pool, ScopesJSON: scopes, CommandJSON: command,
		EveryMs: t.Every.Milliseconds(), TimeoutMs: t.Timeout.Milliseconds(), Enabled: t.Enabled,
	}, nil
}

// EncodeScan returns the row of s with the sequence number seq. A zero
// CreatedAt or EndedAt is 0.
func EncodeScan(s unmanaged.Scan, seq int64) (ScanRow, error) {
	scopes, err := EncodeReportScopes(s.Scopes)
	if err != nil {
		return ScanRow{}, err
	}

	counts, err := EncodeCounts(s.Counts)
	if err != nil {
		return ScanRow{}, err
	}

	return ScanRow{
		ID: s.ID, Namespace: s.Namespace.String(), Seq: seq, Target: s.Target, Kind: string(s.Kind),
		Status: string(s.Status), StartedBy: s.StartedBy, Reason: s.Reason, ScopesJSON: scopes, CountsJSON: counts,
		CreatedAtMs: millis(s.CreatedAt), EndedAtMs: millis(s.EndedAt),
	}, nil
}

// Target returns the target of a stored row that a read of the namespace ns
// joined. It returns ErrCorrupt for a row that fails the checks of a target.
func Target(ns namespace.Name, r TargetRow) (unmanaged.Target, error) {
	// Defense in depth: the query matched the namespace. This check stays so
	// that a change of the query cannot serve a target of another namespace.
	if r.Namespace != ns.String() {
		return unmanaged.Target{}, fmt.Errorf("%w: target %q is of namespace %q", ErrCorrupt, r.Name, r.Namespace)
	}

	var scopes []scopeDoc
	if err := decode("scopes", r.ScopesJSON, &scopes); err != nil {
		return unmanaged.Target{}, err
	}

	var command []string
	if err := decode("command", r.CommandJSON, &command); err != nil {
		return unmanaged.Target{}, err
	}

	t := unmanaged.Target{
		Namespace: ns, Name: r.Name, Pool: r.Pool, Scopes: make([]unmanaged.TargetScope, 0, len(scopes)),
		Command: command, Every: time.Duration(r.EveryMs) * time.Millisecond,
		Timeout: time.Duration(r.TimeoutMs) * time.Millisecond, Enabled: r.Enabled,
	}

	for _, s := range scopes {
		t.Scopes = append(t.Scopes, unmanaged.TargetScope{Provider: s.Provider, Scope: s.Scope, Regions: s.Regions})
	}

	if err := t.Validate(); err != nil {
		return unmanaged.Target{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}

	return t, nil
}

// Scan returns the sweep of a stored row that a read of the namespace ns
// joined. It returns ErrCorrupt for a row that fails the checks of a sweep.
func Scan(ns namespace.Name, r ScanRow) (unmanaged.Scan, error) {
	// Defense in depth: the query matched the namespace. This check stays so
	// that a change of the query cannot serve a sweep of another namespace.
	if r.Namespace != ns.String() {
		return unmanaged.Scan{}, fmt.Errorf("%w: sweep %q is of namespace %q", ErrCorrupt, r.ID, r.Namespace)
	}

	if r.ID == "" || r.Seq < 1 || !knownKind(r.Kind) || !knownStatus(r.Status) {
		return unmanaged.Scan{}, fmt.Errorf("%w: sweep %q is %s, %s, at %d",
			ErrCorrupt, r.ID, r.Kind, r.Status, r.Seq)
	}

	var scopes []reportScopeDoc
	if err := decode("scopes", r.ScopesJSON, &scopes); err != nil {
		return unmanaged.Scan{}, err
	}

	var counts countsDoc
	if err := decode("counts", r.CountsJSON, &counts); err != nil {
		return unmanaged.Scan{}, err
	}

	s := unmanaged.Scan{
		CreatedAt: timeOf(r.CreatedAtMs), EndedAt: timeOf(r.EndedAtMs), ID: r.ID, Namespace: ns,
		Target: r.Target, Kind: unmanaged.ScanKind(r.Kind), Status: unmanaged.ScanStatus(r.Status),
		StartedBy: r.StartedBy, Reason: r.Reason, Scopes: make([]unmanaged.ReportScope, 0, len(scopes)),
		Counts: unmanaged.ScanCounts{
			Records: counts.Records, Managed: counts.Managed, Elsewhere: counts.Elsewhere,
			NotInAnyUnit: counts.NotInAnyUnit, LeftBehind: counts.LeftBehind, ManagedTwice: counts.ManagedTwice,
			Resolved: counts.Resolved, Rejected: counts.Rejected, OutOfScope: counts.OutOfScope,
		},
	}

	for _, sc := range scopes {
		s.Scopes = append(s.Scopes, unmanaged.ReportScope{
			Provider: sc.Provider, Scope: sc.Scope, Error: sc.Error, Regions: sc.Regions, Complete: sc.Complete,
		})
	}

	return s, nil
}

// knownKind reports whether k is a kind of a sweep.
func knownKind(k string) bool {
	return k == string(unmanaged.ScanCloud) || k == string(unmanaged.ScanState)
}

// knownStatus reports whether s is a status of a sweep.
func knownStatus(s string) bool {
	switch unmanaged.ScanStatus(s) {
	case unmanaged.ScanQueued, unmanaged.ScanDispatched, unmanaged.ScanRunning, unmanaged.ScanMatching,
		unmanaged.ScanSucceeded, unmanaged.ScanFailed, unmanaged.ScanCanceled, unmanaged.ScanExpired:
		return true
	default:
		return false
	}
}

// encode returns the JSON of v, which the decoders of this package read back.
func encode(name string, v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("encode %s: %w", name, err)
	}

	return string(b), nil
}

// decode reads the JSON that encode wrote into v. It refuses a field that it
// does not know and text after the document.
func decode(name, s string, v any) error {
	dec := json.NewDecoder(bytes.NewReader([]byte(s)))
	dec.DisallowUnknownFields()

	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrCorrupt, name, err)
	}

	// A second decode must reach the end of the text. dec.More does not say
	// this: it is false before a stray closing brace.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: %s: text after the document", ErrCorrupt, name)
	}

	return nil
}

// listOf returns a list that is never nil, so that it encodes as a JSON list
// and not as null.
func listOf[T any](list []T) []T {
	if list == nil {
		return []T{}
	}

	return list
}

// millis returns the Unix milliseconds of t, or 0 for the zero time.
func millis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}

	return codec.Millis(t)
}

// timeOf decodes Unix milliseconds that millis wrote.
func timeOf(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}

	return codec.FromMillis(ms)
}
