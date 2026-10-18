// Package sdk is how an author writes a provider in Go, for a tool that a
// declaration cannot describe. The author writes one struct per resource
// type, with tags, and one function per operation:
//
//	type Schedule struct {
//		ID   *string `provider:"id,computed"`
//		Name string  `provider:"name,required,replace"`
//		Team *string `provider:"team,optional"`
//	}
//
//	var schedule = sdk.Resource[Config, Schedule]{
//		Name:   "schedule",
//		Create: func(ctx context.Context, c sdk.Call[Config], plan Schedule) (Schedule, error) { ... },
//		Read:   func(ctx context.Context, c sdk.Call[Config], prior Schedule) (Schedule, error) { ... },
//		Update: func(ctx context.Context, c sdk.Call[Config], prior, plan Schedule) (Schedule, error) { ... },
//		Delete: func(ctx context.Context, c sdk.Call[Config], prior Schedule) error { ... },
//	}
//
// The SDK derives the schema from the struct, converts values between the
// struct and the core, and serves the same lifecycle.Provider that a
// declared provider serves. An author never handles a protocol of OpenTofu
// or Pulumi.
package sdk

import (
	"context"
	"errors"
	"fmt"
	"os"

	"opendustpan.org/opendustpan/providerkit/cliexec"
	"opendustpan.org/opendustpan/providerkit/lifecycle"
	"opendustpan.org/opendustpan/providerkit/schema"
)

// ConfigSchemaOf returns the configuration schema that an SDK provider
// serves for the struct C: SchemaOf, with the file form of each sensitive
// field from lifecycle.WithFileForms, as a declared provider serves it.
// When the user sets the file form, the SDK reads the file into the field
// before it calls Configure. Errors: ErrInvalidModel.
func ConfigSchemaOf[C any]() (schema.Schema, error) {
	base, err := SchemaOf[C]()
	if err != nil {
		return base, err
	}

	s, err := lifecycle.WithFileForms(base)
	if err != nil {
		return base, fmt.Errorf("%w: %w", ErrInvalidModel, err)
	}

	return s, nil
}

// Provider is a provider that an author writes in Go. C is the struct of the
// provider configuration.
type Provider[C any] struct {
	// Name is the provider type name: lowercase letters and digits.
	Name string
	// Version is the semantic version of the release, without a "v". The
	// build sets it.
	Version string
	// Configure checks the configuration before any operation. It is
	// optional.
	Configure func(ctx context.Context, config C) error
	// Resources are the resource types.
	Resources []ResourceType[C]
	// DataSources are the data source types.
	DataSources []DataSourceType[C]
}

// Call is what an operation gets: the configuration, a runner for the
// program of the tool, and the private directory of the session. The runner
// is the same adapter that the CLI engine uses, so a written provider and a
// declared one start programs by one set of rules. The runner does not check
// arguments or variables for secrets: an operation gives a secret to a
// program on the standard input, or as a file from SecretFile.
type Call[C any] struct {
	// Config is the provider configuration.
	Config C
	// Runner runs commands as argument lists, with no shell.
	Runner cliexec.Runner
	// dir is the private directory of the session, of mode 0700. The
	// session removes it when it closes.
	dir string
}

// NewCall returns the Call of a session with the private directory dir. The
// SDK makes one for each session. A test of an operation makes one with a
// directory of the test.
func NewCall[C any](config C, runner cliexec.Runner, dir string) Call[C] {
	return Call[C]{Config: config, Runner: runner, dir: dir}
}

// ErrNoSessionDir reports a Call without the private directory of a session.
var ErrNoSessionDir = errors.New("call has no session directory")

// SecretFile writes value to a new file of mode 0600 in the private directory
// of the session, and returns its path. An operation passes the path to a
// program, for example as "--token-file=<path>", so the secret goes in no
// argument and no variable. The session removes the file when it closes.
// Errors: ErrNoSessionDir, or a failure to write the file, wrapped.
func (c Call[C]) SecretFile(value string) (string, error) {
	if c.dir == "" {
		return "", ErrNoSessionDir
	}

	f, err := os.CreateTemp(c.dir, "secret-*")
	if err != nil {
		return "", fmt.Errorf("make a secret file: %w", err)
	}

	// CreateTemp makes the file with mode 0600.
	path := f.Name()
	if _, err := f.WriteString(value); err != nil {
		return "", errors.Join(fmt.Errorf("write the secret file: %w", err), f.Close(), os.Remove(path))
	}

	if err := f.Close(); err != nil {
		return "", errors.Join(fmt.Errorf("close the secret file: %w", err), os.Remove(path))
	}

	return path, nil
}

// ResourceType is a Resource of any model type. Only Resource implements it.
type ResourceType[C any] interface {
	resourceName() string
}

// DataSourceType is a DataSource of any model type. Only DataSource
// implements it.
type DataSourceType[C any] interface {
	dataSourceName() string
}

// Resource is one resource type. M is the struct of its attributes. An
// operation returns lifecycle.ErrNotFound, wrapped, for an object that is
// gone.
type Resource[C, M any] struct {
	// Name is the type name without the provider prefix, in snake_case.
	Name string
	// Create makes the object from the planned model, with the write-only
	// values. A pointer field that the plan does not know yet is nil. When
	// the object exists and a later step fails, Create returns the model
	// with at least the id, together with the error.
	Create func(ctx context.Context, c Call[C], plan M) (M, error)
	// Read returns the current model. An import calls it with only the id.
	Read func(ctx context.Context, c Call[C], prior M) (M, error)
	// Update changes the object in place. Nil means that every field that
	// the user sets forces a replace.
	Update func(ctx context.Context, c Call[C], prior, plan M) (M, error)
	// Delete removes the object. It returns nil for an object that is gone.
	Delete func(ctx context.Context, c Call[C], prior M) error
}

func (r Resource[C, M]) resourceName() string { return r.Name }

// DataSource is one data source type. M is the struct of its inputs and
// results.
type DataSource[C, M any] struct {
	// Name is the type name without the provider prefix, in snake_case.
	Name string
	// Read fills the results of the model from its inputs.
	Read func(ctx context.Context, c Call[C], in M) (M, error)
}

func (d DataSource[C, M]) dataSourceName() string { return d.Name }
