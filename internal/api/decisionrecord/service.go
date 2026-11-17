package decisionrecord

import (
	"context"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ErrMissingDependency reports a Service or a Handler without a required dependency.
var ErrMissingDependency = errors.New("missing dependency")

// Service reads the stored decision records of one scope at a time. It checks
// the ID and the scope before a value exists, and it checks the scope of the
// loaded record again after the load, so a record of another scope that a
// broken store lets through never reaches the caller.
type Service struct {
	// records is required: the reads never serve without it.
	records Records
}

// NewService returns a Service over records. It returns ErrMissingDependency
// for a nil port.
func NewService(records Records) (*Service, error) {
	if records == nil {
		return nil, fmt.Errorf("%w: records", ErrMissingDependency)
	}

	return &Service{records: records}, nil
}

// Read returns the stored record id of scope. It returns an error that wraps
// gate.ErrInvalid for a non-gdr- ID and for a scope that holds no decision
// record, gate.ErrNotFound for an unknown ID and for a record of another
// scope, and gate.ErrUnavailable when the store cannot answer.
func (s *Service) Read(ctx context.Context, scope gate.Scope, id string) (gate.Record, error) {
	// Defense in depth: the handler checks the ID of the path before it calls
	// the service, and the port takes it as data, never as SQL. This check
	// stays so that a caller of the service cannot ask for a record with an
	// ID that is not a record ID.
	parsed, err := gate.ParseRecordID(id)
	if err != nil {
		return gate.Record{}, err
	}

	ns, err := readNamespace(scope)
	if err != nil {
		return gate.Record{}, err
	}

	rec, err := s.records.Get(ctx, ns, parsed)
	if err != nil {
		return gate.Record{}, err
	}

	// Defense in depth: the port selects the record by the namespace and the
	// ID, so a record of another scope cannot come back. This check stays so
	// that a store with a bug, or a new port implementation, cannot serve a
	// record of another scope either.
	if rec.Namespace != ns {
		return gate.Record{}, fmt.Errorf("%w: the record belongs to another scope", gate.ErrNotFound)
	}

	return rec, nil
}

// readNamespace maps the scope of the path to the store namespace of its
// records. It returns gate.ErrInvalid for a scope that holds no decision
// record of a route: a collection, a unit or a blueprint scope, a namespace
// scope whose name is not a namespace name (for example the server scope
// "@server" written as a namespace), and the zero scope.
func readNamespace(scope gate.Scope) (namespace.Name, error) {
	if scope.IsZero() {
		return "", fmt.Errorf("%w: the zero scope names no scope", gate.ErrInvalid)
	}

	switch scope.Level() {
	case gate.LevelInstallation:
		// A record of an access limit on an installation object is data of the
		// installation: its namespace in the store is the server scope.
		return audit.ServerScope, nil
	case gate.LevelNamespace:
		ns := scope.Namespace()

		if _, err := namespace.ParseName(ns.String()); err != nil {
			return "", fmt.Errorf("%w: the scope namespace %q is not a valid name", gate.ErrInvalid, ns)
		}

		if ns == audit.ServerScope {
			return "", fmt.Errorf("%w: the server scope is not a namespace scope", gate.ErrInvalid)
		}

		return ns, nil
	default:
		return "", fmt.Errorf("%w: a scope of kind %q holds no decision record of a route", gate.ErrInvalid, scope.Level())
	}
}
