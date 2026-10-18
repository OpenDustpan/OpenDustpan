// Package sdk is how an author writes a provider in Go, for a tool that a
// declaration cannot describe. The author writes one struct per resource
// type, with tags, and one function per operation:
//
//	type Schedule struct {
//		ID   string `provider:"id,computed"`
//		Name string `provider:"name,required,replace"`
//		Team string `provider:"team,optional"`
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

	"opendustpan.org/opendustpan/providerkit/cliexec"
)

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

// Call is what an operation gets: the configuration, and a runner for the
// program of the tool. The runner is the same adapter that the CLI engine
// uses, so a written provider and a declared one start programs by one set
// of rules.
type Call[C any] struct {
	// Config is the provider configuration.
	Config C
	// Runner runs commands as argument lists, with no shell.
	Runner cliexec.Runner
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
	// Create makes the object from the planned model. Fields that the plan
	// does not know yet are their zero value.
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
