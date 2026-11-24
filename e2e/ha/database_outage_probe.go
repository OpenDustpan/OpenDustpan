// Package ha holds the fixtures of the database outage probes of the
// end-to-end suite: the proxy that stops and starts the database, the
// instances that the probe calls during the stop, and the retry report of the
// CLI matrix.
package ha

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

const (
	probePoll       = 100 * time.Millisecond
	probeWait       = 90 * time.Second
	probeRetryParts = 2
)

var (
	errNoProxy        = errors.New("database outage probe needs a database proxy")
	errNoInstances    = errors.New("database outage probe needs at least one instance")
	errNoMatrix       = errors.New("database outage probe needs at least one CLI version")
	errNoRetry        = errors.New("no retry window measured")
	errBadShortWindow = errors.New("short outage window is above half the smallest retry window")
	errNotUnavailable = errors.New("write did not answer 503")
	errRetryAfter     = errors.New("write answered with the wrong Retry-After")
	errMissingPart    = errors.New("the server does not have a part that the outage probe needs")
)

// The server parts that the outage probe needs. The probe skips when one is
// missing, and the skip names the part.
const (
	// PartWrite503 answers each write with 503 and Retry-After while the
	// database is down.
	PartWrite503 = "503 on write during the outage"
	// PartAtomicStateLocks refuses a workspace lock with 503 while the
	// database is down, so that no lock stays without its state write.
	PartAtomicStateLocks = "atomic state locks"
)

// missingPartError is the error of a capability probe that names the missing
// server part.
type missingPartError struct {
	part string
	err  error
}

func (e *missingPartError) Error() string {
	return fmt.Sprintf("%v: missing part %s: %v", errMissingPart, e.part, e.err)
}

func (e *missingPartError) Unwrap() []error {
	return []error{errMissingPart, e.err}
}

// MissingPart returns the error of a capability probe that names the missing
// server part.
func MissingPart(part string, err error) error {
	return &missingPartError{part: part, err: err}
}

// MissingPartOf returns the part that err names, and true, when err is a
// missing part error. It returns false for each other error.
func MissingPartOf(err error) (string, bool) {
	var missing *missingPartError
	if !errors.As(err, &missing) {
		return "", false
	}

	return missing.part, true
}

// DatabaseProxy stops and starts the database that the instances use.
type DatabaseProxy interface {
	Stop(tb testing.TB)
	Start(tb testing.TB)
}

// InstanceRequest is one request sent to one instance behind the proxy.
type InstanceRequest struct {
	Method string
	Path   string
	Header http.Header
	Body   string
}

// InstanceResponse is the answer of one instance behind the proxy.
type InstanceResponse struct {
	Status int
	Header http.Header
	Body   string
}

// Instance is one server instance that the probe can call directly.
type Instance interface {
	Name() string
	Send(ctx context.Context, req InstanceRequest) (InstanceResponse, error)
}

// CLIVersion is one CLI version of the suite matrix.
type CLIVersion struct {
	Tool    string
	Version string
}

// DatabaseOutageProbe stops the database in a test installation and checks
// what every instance answers during the stop.
type DatabaseOutageProbe struct {
	proxy     DatabaseProxy
	instances []Instance
	matrix    []CLIVersion
	poll      time.Duration
	wait      time.Duration
}

// NewDatabaseOutageProbe checks the fixture inputs and returns the probe. It
// refuses an empty matrix, an empty instance list and a missing proxy.
func NewDatabaseOutageProbe(
	proxy DatabaseProxy, instances []Instance, matrix []CLIVersion,
) (*DatabaseOutageProbe, error) {
	if proxy == nil {
		return nil, errNoProxy
	}

	if len(instances) == 0 {
		return nil, errNoInstances
	}

	if len(matrix) == 0 {
		return nil, errNoMatrix
	}

	return &DatabaseOutageProbe{
		proxy:     proxy,
		instances: append([]Instance(nil), instances...),
		matrix:    append([]CLIVersion(nil), matrix...),
		poll:      probePoll,
		wait:      probeWait,
	}, nil
}

// Matrix returns the CLI versions of the suite matrix.
func (p *DatabaseOutageProbe) Matrix() []CLIVersion {
	return append([]CLIVersion(nil), p.matrix...)
}

// Instances returns the instances of the probe.
func (p *DatabaseOutageProbe) Instances() []Instance {
	return append([]Instance(nil), p.instances...)
}

// Stop stops the database and checks that a write cannot reach it. The caller
// restores the database with Restore, also when the step fails.
func (p *DatabaseOutageProbe) Stop(
	ctx context.Context, tb testing.TB, write InstanceRequest, retryAfter string,
) {
	tb.Helper()

	p.proxy.Stop(tb)

	ctx, cancel := context.WithTimeout(ctx, p.wait)
	defer cancel()

	if err := p.CheckWrites(ctx, write, retryAfter); err != nil {
		tb.Fatalf("check that the database stopped: %v", err)
	}
}

// ProbeCapabilities stops the database and checks the server parts that the
// probe needs: write must answer 503 with retryAfter on every instance
// (PartWrite503), and lock must answer the same (PartAtomicStateLocks). A
// wrong answer gives an error that names the missing part. The caller
// restores the database with Restore, also when the probe fails.
func (p *DatabaseOutageProbe) ProbeCapabilities(
	ctx context.Context, tb testing.TB, write, lock InstanceRequest, retryAfter string,
) error {
	tb.Helper()

	p.proxy.Stop(tb)

	ctx, cancel := context.WithTimeout(ctx, p.wait)
	defer cancel()

	checks := []struct {
		part string
		req  InstanceRequest
	}{
		{part: PartWrite503, req: write},
		{part: PartAtomicStateLocks, req: lock},
	}

	for _, check := range checks {
		err := p.CheckWrites(ctx, check.req, retryAfter)
		if errors.Is(err, errNotUnavailable) || errors.Is(err, errRetryAfter) {
			return MissingPart(check.part, err)
		}

		if err != nil {
			return fmt.Errorf("probe part %s: %w", check.part, err)
		}
	}

	return nil
}

// Restore starts the database again and waits until every instance answers
// 200 on GET /health/ready. It fails with a blocked database restart when an
// instance does not turn ready.
func (p *DatabaseOutageProbe) Restore(ctx context.Context, tb testing.TB) {
	tb.Helper()
	p.proxy.Start(tb)

	if err := p.WaitReady(ctx, http.StatusOK); err != nil {
		tb.Fatalf("blocked database restart: %v", err)
	}
}

// WaitReady waits until every instance answers want on GET /health/ready.
func (p *DatabaseOutageProbe) WaitReady(ctx context.Context, want int) error {
	ctx, cancel := context.WithTimeout(ctx, p.wait)
	defer cancel()

	for _, instance := range p.instances {
		for {
			resp, sendErr := instance.Send(ctx, InstanceRequest{
				Method: http.MethodGet,
				Path:   "/health/ready",
				Header: nil,
				Body:   "",
			})

			if sendErr == nil && resp.Status == want {
				break
			}

			select {
			case <-ctx.Done():
				if sendErr != nil {
					return fmt.Errorf("instance %s: GET /health/ready: %w, want %d: %w",
						instance.Name(), sendErr, want, ctx.Err())
				}

				return fmt.Errorf("instance %s: GET /health/ready answered %d with %q, want %d: %w",
					instance.Name(), resp.Status, resp.Body, want, ctx.Err())
			case <-time.After(p.poll):
			}
		}
	}

	return nil
}

// CheckWrites sends one write to every instance and checks that each answers
// 503 with the expected Retry-After, never 500.
func (p *DatabaseOutageProbe) CheckWrites(
	ctx context.Context, write InstanceRequest, retryAfter string,
) error {
	ctx, cancel := context.WithTimeout(ctx, p.wait)
	defer cancel()

	for _, instance := range p.instances {
		resp, err := instance.Send(ctx, write)
		if err != nil {
			return fmt.Errorf("instance %s: %s %s: %w", instance.Name(), write.Method, write.Path, err)
		}

		if resp.Status != http.StatusServiceUnavailable {
			return fmt.Errorf("%w: instance %s: %s %s answered %d with %q, want 503",
				errNotUnavailable, instance.Name(), write.Method, write.Path, resp.Status, resp.Body)
		}

		if got := resp.Header.Get("Retry-After"); got != retryAfter {
			return fmt.Errorf("%w: instance %s: %s %s: Retry-After = %q, want %q",
				errRetryAfter, instance.Name(), write.Method, write.Path, got, retryAfter)
		}
	}

	return nil
}

// ShortWindow returns half of the smallest retry window measured in the long outage.
func (p *DatabaseOutageProbe) ShortWindow(report *RetryReport) (time.Duration, error) {
	if report == nil {
		return 0, errNoRetry
	}

	smallest := report.Shortest()
	if smallest <= 0 {
		return 0, errNoRetry
	}

	window := smallest / probeRetryParts
	if err := p.CheckShortWindow(window, report); err != nil {
		return 0, err
	}

	return window, nil
}

// CheckShortWindow refuses a stop above half of the smallest retry window.
func (p *DatabaseOutageProbe) CheckShortWindow(window time.Duration, report *RetryReport) error {
	if window <= 0 {
		return errNoRetry
	}

	if report == nil {
		return errNoRetry
	}

	smallest := report.Shortest()
	if smallest <= 0 {
		return errNoRetry
	}

	if window*probeRetryParts > smallest {
		return fmt.Errorf("%w: stop %s, smallest retry %s", errBadShortWindow, window, smallest)
	}

	return nil
}
