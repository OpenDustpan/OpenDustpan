// Package admittest holds a fake worker check for the tests of the routes
// that start a durable operation.
package admittest

import (
	"context"
	"fmt"
	"sync/atomic"

	"opendustpan.org/opendustpan/internal/api/admit"
)

// Source is an admit.Source that answers with Err and counts its calls.
type Source struct {
	// Err is the answer of AdmitStart. Nil admits the start.
	Err   error
	calls atomic.Int64
}

// NoWorker returns a Source that refuses every start, as an api process does
// when no worker claimed work for 2 lease periods.
func NoWorker() *Source {
	return &Source{Err: fmt.Errorf("%w for more than 2 lease periods", admit.ErrNoWorker), calls: atomic.Int64{}}
}

// Worker returns a Source that admits every start.
func Worker() *Source {
	return &Source{Err: nil, calls: atomic.Int64{}}
}

// AdmitStart implements admit.Source.
func (s *Source) AdmitStart(context.Context) error {
	s.calls.Add(1)

	return s.Err
}

// Calls returns how many times a route asked the Source.
func (s *Source) Calls() int { return int(s.calls.Load()) }
