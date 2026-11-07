package jobs

import (
	"sync"
	"time"
)

// The defaults of the limits of a log stream.
const (
	// defaultLogMaxLife is the longest life of one stream. At that limit the
	// stream ends with a resume line, and the client opens it again with
	// ?after.
	defaultLogMaxLife = 10 * time.Minute
	// defaultLogWriteTimeout is the longest time that one write or flush may
	// take. A client that does not read ends the stream at this limit.
	defaultLogWriteTimeout = 30 * time.Second
	// defaultLogStreamsPerPrincipal and defaultLogStreamsTotal are the largest
	// numbers of open streams of one principal and of the server.
	defaultLogStreamsPerPrincipal = 8
	defaultLogStreamsTotal        = 256
)

// LogOptions are the limits and the timing of the log streams. A zero field
// takes its default.
type LogOptions struct {
	// Poll is the time between two reads of a running phase.
	Poll time.Duration
	// MaxLife is the longest life of one stream.
	MaxLife time.Duration
	// WriteTimeout is the longest time of one write or flush to the client.
	WriteTimeout time.Duration
	// PerPrincipal is the largest number of open streams of one principal.
	PerPrincipal int
	// Total is the largest number of open streams of the server.
	Total int
	// Stopping tells that the server stops. A stream checks it at each poll
	// and ends with a resume line. Nil means that the server never says so.
	Stopping func() bool
}

// logLimits returns o with the defaults for its zero fields. A nil o gives
// every default.
func logLimits(o *LogOptions) LogOptions {
	var out LogOptions
	if o != nil {
		out = *o
	}

	if out.Poll <= 0 {
		out.Poll = defaultLogPoll
	}

	if out.MaxLife <= 0 {
		out.MaxLife = defaultLogMaxLife
	}

	if out.WriteTimeout <= 0 {
		out.WriteTimeout = defaultLogWriteTimeout
	}

	if out.PerPrincipal <= 0 {
		out.PerPrincipal = defaultLogStreamsPerPrincipal
	}

	if out.Total <= 0 {
		out.Total = defaultLogStreamsTotal
	}

	return out
}

// streamLimiter counts the open log streams of the server and of each
// principal.
type streamLimiter struct {
	mu            sync.Mutex
	total         int
	byPrincipal   map[string]int
	maxTotal      int
	maxPerSubject int
}

func newStreamLimiter(perPrincipal, total int) *streamLimiter {
	return &streamLimiter{
		mu: sync.Mutex{}, total: 0, byPrincipal: map[string]int{}, maxTotal: total, maxPerSubject: perPrincipal,
	}
}

// acquire takes a slot for the principal subject. It returns false when the
// principal or the server is at its limit. The caller calls the returned
// function once to give the slot back.
func (l *streamLimiter) acquire(subject string) (func(), bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.total >= l.maxTotal || l.byPrincipal[subject] >= l.maxPerSubject {
		return nil, false
	}

	l.total++
	l.byPrincipal[subject]++

	var once sync.Once

	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()

			l.total--

			l.byPrincipal[subject]--
			if l.byPrincipal[subject] <= 0 {
				delete(l.byPrincipal, subject)
			}
		})
	}, true
}
