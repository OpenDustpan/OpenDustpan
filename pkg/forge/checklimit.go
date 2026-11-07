package forge

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrRateLimited reports a Check that the service refused because the
// connection, its installation or the app of all installations of its kind was
// checked too often. The Check made no call to the Git host.
var ErrRateLimited = errors.New("the connection was checked too often")

// RateLimitedError is an ErrRateLimited with the time to wait.
type RateLimitedError struct {
	// After is the time after which a retry can pass.
	After time.Duration
}

// Error returns the text of ErrRateLimited.
func (e *RateLimitedError) Error() string {
	return ErrRateLimited.Error()
}

// Unwrap returns ErrRateLimited, so that errors.Is finds it.
func (e *RateLimitedError) Unwrap() error {
	return ErrRateLimited
}

// RateLimitWait returns the time to wait that err holds, and whether err holds
// a RateLimitedError.
func RateLimitWait(err error) (time.Duration, bool) {
	if r, ok := errors.AsType[*RateLimitedError](err); ok {
		return r.After, true
	}

	return 0, false
}

// CheckLimits bounds the Check calls. A Check calls the Git host and signs
// with the key of the installation, so a caller must not use it to spend the
// rate limit of the GitHub App.
type CheckLimits struct {
	// PerConnection is the shortest time between two Checks of one connection.
	PerConnection time.Duration
	// PerInstallation is the largest number of Checks of one installation in
	// Window.
	PerInstallation int
	// PerApp is the largest number of Checks of all installations of one
	// kind of Git host in Window. All namespaces share one app of a kind, so
	// this bounds the calls that the app signs, whatever the number of
	// installations.
	PerApp int
	// Window is the time that PerInstallation and PerApp count in.
	Window time.Duration
}

// Validate returns an error that wraps ErrInvalid unless each limit is
// positive. A zero or negative value would turn the limiter off, or make the
// first Check read an empty list.
func (l CheckLimits) Validate() error {
	switch {
	case l.PerConnection <= 0:
		return fmt.Errorf("%w: the check interval of a connection must be positive", ErrInvalid)
	case l.PerInstallation <= 0:
		return fmt.Errorf("%w: the number of checks of an installation must be positive", ErrInvalid)
	case l.PerApp <= 0:
		return fmt.Errorf("%w: the number of checks of an app must be positive", ErrInvalid)
	case l.Window <= 0:
		return fmt.Errorf("%w: the window of the checks of an installation must be positive", ErrInvalid)
	default:
		return nil
	}
}

// Default limits of Check: one for each connection in 10 seconds, 30 for each
// installation in one minute, and 40 for all installations of one app in one
// minute. A Check of a GitHub App signs two calls with the key of the app, so
// 40 Checks in a minute are 4,800 such calls in an hour, below the limit of
// 5,000 calls in an hour that GitHub sets for an app.
const (
	DefaultCheckInterval        = 10 * time.Second
	DefaultCheckPerInstallation = 30
	DefaultCheckPerApp          = 40
	DefaultCheckWindow          = time.Minute
)

// maxLimiterKeys bounds the memory of the limiter. When the limiter holds more
// keys after it dropped the old ones, it refuses the new key (fail closed).
const maxLimiterKeys = 10_000

// DefaultCheckLimits returns the default limits of Check.
func DefaultCheckLimits() CheckLimits {
	return CheckLimits{
		PerConnection: DefaultCheckInterval, PerInstallation: DefaultCheckPerInstallation, PerApp: DefaultCheckPerApp,
		Window: DefaultCheckWindow,
	}
}

// checkLimiter holds the times of the admitted Checks. It lives in memory: a
// restart or another member of a federation starts with an empty limiter,
// which is a bound on a burst and not a quota.
type checkLimiter struct {
	limits         CheckLimits
	mu             sync.Mutex
	byConnection   map[string]time.Time
	byInstallation map[string][]time.Time
	byApp          map[string][]time.Time
}

func newCheckLimiter(limits CheckLimits) *checkLimiter {
	return &checkLimiter{
		limits: limits, mu: sync.Mutex{}, byConnection: map[string]time.Time{},
		byInstallation: map[string][]time.Time{}, byApp: map[string][]time.Time{},
	}
}

// admit records a Check of the connection, the installation and the app at
// now, or returns the time to wait. A refused Check is not recorded. app names
// the app that all installations of one kind of Git host share.
func (l *checkLimiter) admit(connection, installation, app string, now time.Time) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Defense in depth: NewService refuses limits that are not positive. This
	// check stays so that a limiter built another way fails closed: it never
	// reads an empty list and never runs with a limit that is off.
	if l.limits.Validate() != nil {
		return time.Second, false
	}

	l.prune(now)

	if last, ok := l.byConnection[connection]; ok {
		if wait := l.limits.PerConnection - now.Sub(last); wait > 0 {
			return wait, false
		}
	}

	recent := l.byInstallation[installation]
	if len(recent) >= l.limits.PerInstallation {
		return max(recent[0].Add(l.limits.Window).Sub(now), time.Second), false
	}

	appRecent := l.byApp[app]
	if len(appRecent) >= l.limits.PerApp {
		return max(appRecent[0].Add(l.limits.Window).Sub(now), time.Second), false
	}

	_, known := l.byConnection[connection]
	if !known && len(l.byConnection) >= maxLimiterKeys {
		return l.limits.PerConnection, false
	}

	l.byConnection[connection] = now
	l.byInstallation[installation] = append(recent, now)
	l.byApp[app] = append(appRecent, now)

	return 0, true
}

// prune drops the times that no limit counts any more.
func (l *checkLimiter) prune(now time.Time) {
	for key, last := range l.byConnection {
		if now.Sub(last) >= l.limits.PerConnection {
			delete(l.byConnection, key)
		}
	}

	l.pruneWindow(l.byInstallation, now)
	l.pruneWindow(l.byApp, now)
}

// pruneWindow drops from times the times that left the window.
func (l *checkLimiter) pruneWindow(times map[string][]time.Time, now time.Time) {
	for key, list := range times {
		keep := list[:0]

		for _, at := range list {
			if now.Sub(at) < l.limits.Window {
				keep = append(keep, at)
			}
		}

		if len(keep) == 0 {
			delete(times, key)
		} else {
			times[key] = keep
		}
	}
}
