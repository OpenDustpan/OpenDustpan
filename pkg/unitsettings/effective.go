package unitsettings

import (
	"opendustpan.org/opendustpan/pkg/job"
)

// Origin names the level that gave the effective value of a field.
type Origin string

// The origins.
const (
	// OriginUnit is a value in the settings record of the unit.
	OriginUnit Origin = "unit"
	// OriginNamespaceDefault is a value of the defaults of the namespace.
	OriginNamespaceDefault Origin = "namespace-default"
	// OriginBuiltIn is a value that the code of the server gives when no level
	// above sets the field.
	OriginBuiltIn Origin = "built-in-default"
)

// Field is the effective value of one field and its origin.
type Field[T any] struct {
	Value  T
	Origin Origin
}

// Effective is the value of each field that a job uses, and where each value
// comes from. A field with the built-in origin and an empty value is a field
// that nothing sets.
type Effective struct {
	Executor       Field[job.ExecutorKind]
	Pool           Field[string]
	Connection     Field[string]
	Repository     Field[string]
	Ref            Field[string]
	Directory      Field[string]
	Delivery       Field[job.Delivery]
	Files          Field[[]string]
	ReviewRequests Field[bool]
	Steps          Field[[]job.Step]
}

// Context is what Resolve needs from outside this package. The caller reads it
// from the namespace and from the tool of the unit.
type Context struct {
	// DefaultConnection is the ID of the default Git host connection of the
	// namespace, or empty when it has none.
	DefaultConnection string
	// HasJobPool is true when the namespace has a runner pool for jobs. It
	// decides the built-in executor.
	HasJobPool bool
	// DefaultFiles are the default write-back files of the tool of the unit:
	// the default file of the tool.
	DefaultFiles []string
}

// RootDirectory is the effective directory of a code location that sets none.
const RootDirectory = "."

// Resolve returns the effective value of each field of the unit. s is the
// record of the unit, or nil for a unit with no record. d are the defaults of
// the namespace. There is no merge by field across levels: the record gives a
// field when it sets the field, the defaults give the pool and the connection
// when the record does not, and the built-in value gives the rest.
//
// The built-in executor is runner when the namespace has a runner pool for
// jobs, and write-back when it has none.
func Resolve(s *Settings, d NamespaceDefaults, c Context) Effective {
	if s == nil {
		var none Settings

		s = &none
	}

	e := Effective{
		Executor:       Field[job.ExecutorKind]{Value: job.ExecutorWriteBack, Origin: OriginBuiltIn},
		Pool:           pickDefault(s.Pool, d.Pool, ""),
		Connection:     pickDefault(s.CodeLocation.Connection, c.DefaultConnection, ""),
		Repository:     pick(s.CodeLocation.Repository, ""),
		Ref:            pick(s.CodeLocation.Ref, ""),
		Directory:      pick(s.CodeLocation.Directory, RootDirectory),
		Delivery:       Field[job.Delivery]{Value: job.DeliveryReviewRequest, Origin: OriginBuiltIn},
		Files:          Field[[]string]{Value: c.DefaultFiles, Origin: OriginBuiltIn},
		ReviewRequests: Field[bool]{Value: false, Origin: OriginBuiltIn},
		Steps:          Field[[]job.Step]{Value: nil, Origin: OriginBuiltIn},
	}

	if c.HasJobPool {
		e.Executor.Value = job.ExecutorRunner
	}

	if s.Executor != "" {
		e.Executor = Field[job.ExecutorKind]{Value: s.Executor, Origin: OriginUnit}
	}

	if s.WriteBack.Delivery != "" {
		e.Delivery = Field[job.Delivery]{Value: s.WriteBack.Delivery, Origin: OriginUnit}
	}

	if len(s.WriteBack.Files) > 0 {
		e.Files = Field[[]string]{Value: s.WriteBack.Files, Origin: OriginUnit}
	}

	if s.ReviewRequests.Enabled {
		e.ReviewRequests = Field[bool]{Value: true, Origin: OriginUnit}
	}

	if len(s.Steps) > 0 {
		e.Steps = Field[[]job.Step]{Value: s.Steps, Origin: OriginUnit}
	}

	return e
}

// pick gives the unit value when set, and the built-in value otherwise.
func pick(unit, builtIn string) Field[string] {
	if unit != "" {
		return Field[string]{Value: unit, Origin: OriginUnit}
	}

	return Field[string]{Value: builtIn, Origin: OriginBuiltIn}
}

// pickDefault gives the unit value when set, then the namespace default, then
// the built-in value.
func pickDefault(unit, namespaceDefault, builtIn string) Field[string] {
	switch {
	case unit != "":
		return Field[string]{Value: unit, Origin: OriginUnit}
	case namespaceDefault != "":
		return Field[string]{Value: namespaceDefault, Origin: OriginNamespaceDefault}
	default:
		return Field[string]{Value: builtIn, Origin: OriginBuiltIn}
	}
}
