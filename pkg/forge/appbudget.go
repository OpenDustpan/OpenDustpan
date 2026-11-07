package forge

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrAppBudget reports that the key of an app signed as many calls as its
// budget allows in the window. An adapter wraps it together with an
// UnavailableError that holds the time to wait, so that errors.Is finds
// ErrUnavailable too.
var ErrAppBudget = errors.New("the app used its budget of signed calls")

// AppBudget bounds the calls that the key of an app signs. All connections of
// one app share it, whatever their installation and their namespace, because
// the host counts these calls against the app. An adapter takes one call from
// the budget before each request that it signs with the key of the app, and
// makes no request when the budget is spent.
//
// The budget lives in the memory of one server, as the limits of Check do.
type AppBudget struct {
	limit  int
	window time.Duration
	mu     sync.Mutex
	byApp  map[string][]time.Time
}

// NewAppBudget returns a budget of limit signed calls of each app in window.
// It returns an error that wraps ErrInvalid unless both are positive: a zero
// or negative value would turn the budget off.
func NewAppBudget(limit int, window time.Duration) (*AppBudget, error) {
	if limit <= 0 || window <= 0 {
		return nil, fmt.Errorf("%w: the budget of the signed calls of an app must be positive", ErrInvalid)
	}

	return &AppBudget{limit: limit, window: window, mu: sync.Mutex{}, byApp: map[string][]time.Time{}}, nil
}

// Spend records one signed call of app at now, or returns the time to wait
// and false. A refused call is not recorded.
//
// Defense in depth: NewAppBudget refuses limits that are not positive. A nil
// budget, or one with such limits, refuses each call, so that an adapter that
// got no budget fails closed and does not run without a limit.
func (b *AppBudget) Spend(app string, now time.Time) (time.Duration, bool) {
	if b == nil || b.limit <= 0 || b.window <= 0 {
		return time.Second, false
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	for key, list := range b.byApp {
		keep := list[:0]

		for _, at := range list {
			if now.Sub(at) < b.window {
				keep = append(keep, at)
			}
		}

		if len(keep) == 0 {
			delete(b.byApp, key)
		} else {
			b.byApp[key] = keep
		}
	}

	recent := b.byApp[app]
	if len(recent) >= b.limit {
		return max(recent[0].Add(b.window).Sub(now), time.Second), false
	}

	if _, known := b.byApp[app]; !known && len(b.byApp) >= maxLimiterKeys {
		return b.window, false
	}

	b.byApp[app] = append(recent, now)

	return 0, true
}
