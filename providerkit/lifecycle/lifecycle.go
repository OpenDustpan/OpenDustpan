// Package lifecycle is the core of a provider: the port that every engine
// implements and that both protocol adapters consume. An engine is the HTTP
// engine, the CLI engine, or a provider that an author writes with the SDK.
// A protocol adapter is the OpenTofu plugin server or the Pulumi plugin
// server.
//
// The port knows nothing about either tool. It speaks in schemas and values.
// The adapters translate the calls of their tool to these calls, and the
// shared planner in this package decides create, update, replace or delete
// for both of them.
package lifecycle

import (
	"context"
	"errors"

	"opendustpan.org/providerkit/schema"
)

// The errors of the port. An engine wraps one of them with
// fmt.Errorf("...: %w", err), so an adapter can map it to the diagnostic of
// its tool.
var (
	// ErrNotFound: the object does not exist. Read returns it when an object
	// in state is gone, and the adapter then removes it from state. That is
	// how drift from a deleted object shows in the next plan.
	ErrNotFound = errors.New("object not found")
	// ErrUnknownType: the provider has no resource or data source of the
	// name.
	ErrUnknownType = errors.New("unknown resource or data source type")
	// ErrInvalidConfig: the configuration breaks the schema, for example a
	// required attribute is null or a computed attribute is set.
	ErrInvalidConfig = errors.New("invalid configuration")
	// ErrRejected: the remote system refused the change, for example with a
	// validation error or a conflict. The wrapped text is the reason that
	// the remote system gave, with sensitive values masked.
	ErrRejected = errors.New("change rejected")
	// ErrUnavailable: the remote system did not answer, or answered with a
	// failure that the engine retried until its retry budget ran out.
	ErrUnavailable = errors.New("remote system unavailable")
)

// Info names a provider.
type Info struct {
	// Name is the provider type name: lowercase letters and digits, for
	// example "ipam". It is the same for OpenTofu and Pulumi.
	Name string
	// Version is the semantic version of the release, without a "v".
	Version string
}

// Provider is the port that both protocol adapters consume. An adapter calls
// the schema methods before Configure, and calls Configure once for each
// provider configuration in a run.
type Provider interface {
	// Info returns the name and version of the provider.
	Info() Info
	// ConfigSchema returns the schema of the provider configuration. Its
	// sensitive attributes are the only place where a credential enters a
	// provider.
	ConfigSchema() schema.Schema
	// ResourceSchemas returns the schema of each resource type, by name.
	ResourceSchemas() map[string]schema.Schema
	// DataSourceSchemas returns the schema of each data source type, by
	// name.
	DataSourceSchemas() map[string]schema.Schema
	// Configure checks the configuration and returns a session that holds
	// it. Errors: ErrInvalidConfig, ErrUnavailable when a check against the
	// remote system fails.
	Configure(ctx context.Context, config schema.Values) (Session, error)
}

// Session is a configured provider: the resource and data source types,
// bound to one provider configuration. An adapter looks up a type by name and
// reports ErrUnknownType when the map has no entry. It calls Close when the
// tool stops the plugin. Both tools apply objects in parallel, so the types
// of a session must be safe for concurrent use.
type Session struct {
	// Resources holds each resource type, by name. It has the same names as
	// ResourceSchemas.
	Resources map[string]Resource
	// DataSources holds each data source type, by name. It has the same
	// names as DataSourceSchemas.
	DataSources map[string]DataSource
	// Close releases what the session holds, for example temporary files
	// with credentials. It is nil when there is nothing to release. It is
	// safe to call more than once.
	Close func() error
}

// Resource is the life cycle of one resource type. Each method gets the
// values that the planner or the prior state gives, and returns the values
// that the remote system now has. A returned value set holds every attribute
// of the schema, with no unknown value. A write-only attribute can hold the
// value that the engine sent: the adapter passes every returned value set
// through ForTool, so the tool never gets it.
type Resource interface {
	// Create makes the object. Planned is Change.Planned: the
	// configuration, with the write-only values, and unknown values for
	// what the remote system sets.
	//
	// When the remote system made the object and a later step failed, for
	// example a second call or a read, Create returns the values that it
	// has, with at least the id, together with the error. The adapter
	// keeps that object in state (see Created and Partial), so that it is
	// not orphaned. Errors: ErrRejected, ErrUnavailable.
	Create(ctx context.Context, planned schema.Values) (schema.Values, error)
	// Read returns the current values of the object. Prior holds at least
	// the id. An import calls Read with only the id. Errors: ErrNotFound,
	// ErrUnavailable.
	Read(ctx context.Context, prior schema.Values) (schema.Values, error)
	// Update changes the object in place. The planner calls it only when
	// no changed attribute forces a replace. Planned holds the write-only
	// values, as for Create. When the change was partly applied, Update
	// returns the values that the object now has, with at least the id,
	// together with the error. The adapter keeps Partial of them, and of
	// prior when they are nil, in state. Errors: ErrNotFound,
	// ErrRejected, ErrUnavailable.
	Update(ctx context.Context, prior, planned schema.Values) (schema.Values, error)
	// Delete removes the object. It returns nil when the object is already
	// gone, so that a retried destroy ends. Errors: ErrRejected,
	// ErrUnavailable.
	Delete(ctx context.Context, prior schema.Values) error
}

// Created reports whether a Create that returned an error left an object:
// got holds a known id that is not empty. The adapter then gives its tool
// Partial(s, Change.Planned, got) together with the error:
//   - The OpenTofu adapter returns those values as the new state with the
//     error. OpenTofu keeps an object of a failed create as tainted and
//     replaces it in the next apply.
//   - The Pulumi adapter returns a partial failure with the id and those
//     values. The Pulumi CLI keeps the object in state and applies the
//     configuration again in the next update.
//
// Without an id, a failed Create made no object that the adapter can name,
// and the adapter reports the error alone.
//
// A failed Update always has an object: the adapter gives its tool
// Partial(s, prior, got) with the error, also when got is nil or has no id.
// It never gives null for a failed update, because OpenTofu then removes
// the object from state, and the next plan creates a second object.
func Created(got schema.Values) bool {
	id, ok := got[schema.IDAttribute].AsString()

	return ok && id != ""
}

// Partial returns the values that an adapter gives its tool after a failed
// Create or Update: got, with each attribute that got leaves null or missing
// taken from base, and passed through ForTool. Base is the prior state for
// an Update, and Change.Planned for a Create. An unknown value of base gives
// null. A null in got means "not reported": got never clears an attribute.
//
// Without it, a failed update that returns only the id would give the tool
// null for every other attribute. The next plan would then show a change of
// each required attribute, and a replace when one of them forces a replace.
func Partial(s schema.Schema, base, got schema.Values) schema.Values {
	out := make(schema.Values, len(s.Attributes))

	for _, a := range s.Attributes {
		v, ok := got[a.Name]
		if !ok || v.IsNull() || !v.WhollyKnown() {
			v = base.Get(a)
		}

		if !v.WhollyKnown() {
			v = schema.NullOf(a.Type)
		}

		out[a.Name] = v
	}

	return ForTool(s, out)
}

// DataSource reads values from the remote system. A fact, for example the
// person on call or a free address of a range, comes in through a data
// source.
type DataSource interface {
	// Read returns the values for the configuration. Errors: ErrNotFound
	// when the configuration selects nothing, ErrUnavailable.
	Read(ctx context.Context, config schema.Values) (schema.Values, error)
}
