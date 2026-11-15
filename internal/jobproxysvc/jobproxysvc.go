// Package jobproxysvc is the service of the proxy rule sets of package
// jobproxy. It saves the set of a namespace and the set of a unit through the
// RuleStore port, adds the time, the fence and the digests to the audit entry
// of each save, and joins the sets of the scopes of a unit for a read.
package jobproxysvc

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/jobproxy"
	"opendustpan.org/opendustpan/pkg/jobproxy/rulestore"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Scope labels of a joined entry.
const (
	// ScopeNamespace labels an entry of the proxy rule set of the namespace.
	ScopeNamespace = "namespace"
	// ScopeUnit labels an entry of the proxy rule set of the unit.
	ScopeUnit = "unit"
)

// MaxPatterns is the most path patterns that one proxy rule set holds, in all
// of its proxy rules together.
const MaxPatterns = 200

// maxRetries is the number of times that a save with no If-Match reads the
// version again after a concurrent save. The last write wins.
const maxRetries = 3

// ErrOperation reports an operation that is not plan, apply, destroy or
// refresh.
var ErrOperation = errors.New("the operation must be plan, apply, destroy or refresh")

// Namespaces gives the namespace of a read, and the namespace of a write
// with its current fence. *namespace.Service meets it.
type Namespaces interface {
	// Get returns the namespace called name, or namespace.ErrNotFound.
	Get(ctx context.Context, name namespace.Name) (namespace.Namespace, error)
	// WritableHere returns the namespace called name when this installation
	// may write to it. It returns namespace.ErrNotFound for an unknown
	// namespace.
	WritableHere(ctx context.Context, name namespace.Name) (namespace.Namespace, error)
}

// Units finds a unit by its stable ID. inventory.Reader meets it.
type Units interface {
	// GetUnit returns the live unit with the stable ID unitID, or
	// inventory.ErrUnitNotFound.
	GetUnit(ctx context.Context, unitID string) (inventory.Entry, error)
}

// Config holds the dependencies of a Service. Store, Namespaces and Units are
// required.
type Config struct {
	Store      rulestore.RuleStore
	Namespaces Namespaces
	Units      Units
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
}

// Service saves and reads proxy rule sets. It checks no grant: the route
// declares the action of each call.
type Service struct {
	store      rulestore.RuleStore
	namespaces Namespaces
	units      Units
	now        func() time.Time
}

// New returns a Service. It returns an error when cfg lacks a required
// dependency.
func New(cfg Config) (*Service, error) {
	switch {
	case cfg.Store == nil:
		return nil, errors.New("job proxy service: the rule store is missing")
	case cfg.Namespaces == nil:
		return nil, errors.New("job proxy service: the namespace service is missing")
	case cfg.Units == nil:
		return nil, errors.New("job proxy service: the unit reader is missing")
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &Service{store: cfg.Store, namespaces: cfg.Namespaces, units: cfg.Units, now: now}, nil
}

// Set is a proxy rule set as it is stored. A scope with no set gives the zero
// Document, Digest and Version.
type Set struct {
	Scope    rulestore.Scope
	Document []byte
	// Digest is the lowercase hex SHA-256 of Document.
	Digest string
	// Version is the count of saves of the scope.
	Version int64
	// UpdatedAt is the time of the last save, in Unix milliseconds.
	UpdatedAt int64
}

// PutRequest is one save of a proxy rule set.
type PutRequest struct {
	Scope    rulestore.Scope
	Document []byte
	// IfMatch is the digest that the writer read. When it is not empty and it
	// is not the stored digest, the save fails with rulestore.ErrConflict and
	// changes nothing. When it is empty, the last write wins.
	IfMatch string
	// Entry is the audit entry of the caller. The service sets the time, the
	// namespace, the unit ID and the digest change.
	Entry audit.Entry
}

// Entry is one proxy rule of a joined set, with the scope that holds it.
type Entry struct {
	// Scope is ScopeNamespace or ScopeUnit.
	Scope string
	Rule  jobproxy.Rule
}

// Unit returns the live unit unitID of the namespace ns. It returns
// inventory.ErrUnitNotFound for a unit that does not exist and for a unit of
// another namespace.
func (s *Service) Unit(ctx context.Context, ns namespace.Name, unitID string) (inventory.Entry, error) {
	e, err := s.units.GetUnit(ctx, unitID)
	if err != nil {
		return inventory.Entry{}, fmt.Errorf("find unit %q: %w", unitID, err)
	}

	// Defense in depth: the inventory finds a unit by its ID alone. A unit of
	// another namespace must not answer under this namespace.
	if e.Namespace != ns || e.UnitID != unitID {
		return inventory.Entry{}, fmt.Errorf("find unit %q: %w", unitID, inventory.ErrUnitNotFound)
	}

	return e, nil
}

// checkScope checks that the namespace and, for a unit scope, the unit exist.
func (s *Service) checkScope(ctx context.Context, scope rulestore.Scope) error {
	if err := rulestore.ValidScope(scope); err != nil {
		return fmt.Errorf("proxy rule set: %w", err)
	}

	if _, err := s.namespaces.Get(ctx, scope.Namespace); err != nil {
		return fmt.Errorf("find namespace %q: %w", scope.Namespace, err)
	}

	if scope.UnitID == "" {
		return nil
	}

	if _, err := s.Unit(ctx, scope.Namespace, scope.UnitID); err != nil {
		return err
	}

	return nil
}

// Get returns the proxy rule set of scope. A scope that exists and holds no
// set gives an empty Set. It returns namespace.ErrNotFound or
// inventory.ErrUnitNotFound for a scope that does not exist.
func (s *Service) Get(ctx context.Context, scope rulestore.Scope) (Set, error) {
	if err := s.checkScope(ctx, scope); err != nil {
		return Set{}, err
	}

	return s.read(ctx, scope)
}

// read returns the stored set of scope, or the empty set.
func (s *Service) read(ctx context.Context, scope rulestore.Scope) (Set, error) {
	stored, err := s.store.Get(ctx, scope)
	if errors.Is(err, rulestore.ErrNotFound) {
		return Set{Scope: scope, Document: nil, Digest: "", Version: 0, UpdatedAt: 0}, nil
	}

	if err != nil {
		return Set{}, fmt.Errorf("read proxy rule set: %w", err)
	}

	return setOf(stored), nil
}

// setOf converts a stored set.
func setOf(st rulestore.Stored) Set {
	return Set{
		Scope: st.Scope, Document: st.Document, Digest: st.Digest, Version: st.Version, UpdatedAt: st.UpdatedAt,
	}
}

// checkDocument checks doc with jobproxy.Parse and the limit of path patterns.
func checkDocument(doc []byte) error {
	set, err := jobproxy.Parse(doc)
	if err != nil {
		return fmt.Errorf("proxy rule set: %w", err)
	}

	total := 0

	for i := range set.Rules {
		total += len(set.Rules[i].Paths)
		if total > MaxPatterns {
			return fmt.Errorf("%w: proxy rule %d (%s): the proxy rule set has more than %d path patterns",
				jobproxy.ErrInvalid, i+1, set.Rules[i].ID, MaxPatterns)
		}
	}

	return nil
}

// Put saves req.Document as the proxy rule set of req.Scope and returns the
// stored set. It returns jobproxy.ErrInvalid for a document that fails the
// checks, rulestore.ErrConflict when req.IfMatch is not the stored digest,
// and namespace.ErrNotFound or inventory.ErrUnitNotFound for a scope that does
// not exist. The audit entry of the save holds the old and the new digest, and
// the store appends it in the transaction of the save, so a save with no
// audit entry does not happen.
func (s *Service) Put(ctx context.Context, req PutRequest) (Set, error) {
	// Defense in depth: the handler checks the document too; this check stays
	// so that a new caller cannot store a document that it did not check.
	if err := checkDocument(req.Document); err != nil {
		return Set{}, err
	}

	if err := s.checkScope(ctx, req.Scope); err != nil {
		return Set{}, err
	}

	writable, err := s.namespaces.WritableHere(ctx, req.Scope.Namespace)
	if err != nil {
		return Set{}, fmt.Errorf("save proxy rule set in namespace %q: %w", req.Scope.Namespace, err)
	}

	for range maxRetries {
		current, readErr := s.read(ctx, req.Scope)
		if readErr != nil {
			return Set{}, readErr
		}

		if req.IfMatch != "" && req.IfMatch != current.Digest {
			return Set{}, fmt.Errorf("save proxy rule set: %w", rulestore.ErrConflict)
		}

		stored, putErr := s.store.Put(ctx, rulestore.Change{
			Scope: req.Scope, Document: slices.Clone(req.Document), Expected: current.Version,
		}, writable.Fence(), s.entryOf(req, current.Digest))

		switch {
		case putErr == nil:
			return setOf(stored), nil
		case errors.Is(putErr, rulestore.ErrConflict) && req.IfMatch == "":
			// Another save came between the read and the write. With no
			// If-Match, the last write wins: read the version again.
			continue
		default:
			return Set{}, fmt.Errorf("save proxy rule set: %w", putErr)
		}
	}

	return Set{}, fmt.Errorf("save proxy rule set: %w", rulestore.ErrConflict)
}

// entryOf returns the audit entry of a save with the old and the new digest.
func (s *Service) entryOf(req PutRequest, oldDigest string) audit.Entry {
	e := req.Entry
	e.At = s.now().UTC()
	e.Namespace = req.Scope.Namespace
	e.ObjectID = req.Scope.UnitID
	e.Changes = append(slices.Clone(req.Entry.Changes), audit.Change{
		Key: "digest", Old: &oldDigest, New: rulestore.DocumentDigest(req.Document), Layer: "", TakesEffect: "",
	})

	return e
}

// parsed returns the proxy rules of the stored set of scope, in their order.
// A scope with no set gives none.
func (s *Service) parsed(ctx context.Context, scope rulestore.Scope) ([]jobproxy.Rule, error) {
	set, err := s.read(ctx, scope)
	if err != nil {
		return nil, err
	}

	if set.Version == 0 {
		return nil, nil
	}

	// Defense in depth: the store parses the document when it reads it. This
	// check stays so that a store that does not cannot give a bad document to a
	// reader.
	rules, err := jobproxy.Parse(set.Document)
	if err != nil {
		return nil, fmt.Errorf("stored proxy rule set: %w", err)
	}

	return rules.Rules, nil
}

// Joined returns the proxy rules of the namespace set, then the proxy rules of
// the unit set, each with its scope. Two scopes can use one ID: the scope tells
// the entries apart. A unit with no set in both scopes has no proxy rules.
func (s *Service) Joined(ctx context.Context, ns namespace.Name, unitID string) ([]Entry, error) {
	scopes := []struct {
		label string
		scope rulestore.Scope
	}{
		{ScopeNamespace, rulestore.Scope{Namespace: ns, UnitID: ""}},
		{ScopeUnit, rulestore.Scope{Namespace: ns, UnitID: unitID}},
	}

	if err := s.checkScope(ctx, scopes[1].scope); err != nil {
		return nil, err
	}

	out := []Entry{}

	for _, sc := range scopes {
		rules, err := s.parsed(ctx, sc.scope)
		if err != nil {
			return nil, err
		}

		for i := range rules {
			out = append(out, Entry{Scope: sc.label, Rule: rules[i]})
		}
	}

	return out, nil
}

// Rendered returns the joined proxy rules that allow op, with the path
// patterns as they are stored. It renders no template and holds no secret
// value. It returns ErrOperation for an operation that is not known.
func (s *Service) Rendered(
	ctx context.Context, ns namespace.Name, unitID string, op jobproxy.Operation,
) ([]Entry, error) {
	if !op.Valid() {
		return nil, ErrOperation
	}

	all, err := s.Joined(ctx, ns, unitID)
	if err != nil {
		return nil, err
	}

	out := []Entry{}

	for i := range all {
		if slices.Contains(all[i].Rule.Operations, op) {
			out = append(out, all[i])
		}
	}

	return out, nil
}
