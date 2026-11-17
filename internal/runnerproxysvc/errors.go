package runnerproxysvc

import "errors"

// The errors of the package.
var (
	// ErrConfig is the error for a configuration that NewServer or
	// NewCoordinator refuses.
	ErrConfig = errors.New("the job proxy configuration is not valid")
	// ErrPlacement is the error for a claim that cannot have a job proxy.
	ErrPlacement = errors.New("the job proxy placement is not valid")
	// ErrStart is the error for a job proxy that did not start.
	ErrStart = errors.New("the job proxy did not start")
	// ErrLost is the error for a job proxy that stopped during a claim.
	ErrLost = errors.New("the job proxy stopped")
	// ErrRoute is the error for a sandbox route that the coordinator cannot
	// set or remove.
	ErrRoute = errors.New("the job proxy route failed")
	// ErrClose is the error for a job proxy that did not stop.
	ErrClose = errors.New("the job proxy did not stop")
	// ErrLog is the error for a blocked line that the job log did not take.
	ErrLog = errors.New("the job proxy did not write the blocked line")
)
