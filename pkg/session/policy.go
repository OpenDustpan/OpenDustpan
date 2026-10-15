package session

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
)

// Bounds of the limits of the session policy.
const (
	// MinIdle is the shortest idle limit. A use records the last use of a
	// session at most once each minute, so a much shorter limit could end
	// a session that is in use.
	MinIdle = 5 * time.Minute
	// MaxLengthBound is the longest maximum length: 7 days.
	MaxLengthBound = 7 * 24 * time.Hour
)

// PolicyRef is the audit object of the session policy.
const PolicyRef = "session-policy"

// ErrMissingDependency reports a PolicyConfig without a required port.
var ErrMissingDependency = errors.New("missing dependency")

// Validate reports whether l holds usable limits: an idle limit of MinIdle
// to the maximum length, a maximum length of MinIdle to MaxLengthBound,
// both whole seconds. It returns ErrInvalid otherwise.
func (l Limits) Validate() error {
	switch {
	case l.MaxLength < MinIdle || l.MaxLength > MaxLengthBound:
		return fmt.Errorf("%w: the maximum length %s is not %s to %s", ErrInvalid, l.MaxLength, MinIdle,
			MaxLengthBound)
	case l.Idle < MinIdle || l.Idle > l.MaxLength:
		return fmt.Errorf("%w: the idle limit %s is not %s to the maximum length %s", ErrInvalid, l.Idle, MinIdle,
			l.MaxLength)
	case l.Idle%time.Second != 0 || l.MaxLength%time.Second != 0:
		return fmt.Errorf("%w: a limit is not a whole count of seconds", ErrInvalid)
	default:
		return nil
	}
}

// Policy is the administrator setting of the console sessions of the
// installation: the idle limit and the maximum length of a session.
// UpdatedAt is the time of the last change, or zero for the seeded
// defaults.
type Policy struct {
	UpdatedAt time.Time
	Limits    Limits
}

// DefaultPolicy returns the policy of a new installation: the limits of
// DefaultLimits.
func DefaultPolicy() Policy {
	return Policy{UpdatedAt: time.Time{}, Limits: DefaultLimits()}
}

// PolicyStore keeps the session policy of the installation. The policy has
// no namespace, so no write carries a namespace fence.
type PolicyStore interface {
	// GetSessionPolicy returns the policy of the installation: the seeded
	// DefaultPolicy until a change.
	GetSessionPolicy(ctx context.Context) (Policy, error)
	// UpdateSessionPolicy reads the policy, calls update with it, and
	// writes the policy and the audit entry that update returns, in one
	// transaction that holds the lock of the audit scope @server: two
	// changes run one after the other, and each one sees the result of the
	// one before. An error of update ends the transaction with no change,
	// and UpdateSessionPolicy returns it as it is. It returns
	// audit.ErrInvalidEntry, and changes nothing, when the entry fails
	// validation or names another scope. It returns the stored policy.
	UpdateSessionPolicy(ctx context.Context, update PolicyUpdate) (Policy, error)
}

// PolicyUpdate returns the new policy and the audit entry of a change, from
// the policy that the store holds. An error refuses the change.
type PolicyUpdate func(old Policy) (Policy, audit.Entry, error)

// PolicyChange names the limits that a change sets. A nil field keeps its
// value.
type PolicyChange struct {
	Idle      *time.Duration
	MaxLength *time.Duration
}

// applyTo returns l with the fields of c that are set.
func (c PolicyChange) applyTo(l Limits) Limits {
	if c.Idle != nil {
		l.Idle = *c.Idle
	}

	if c.MaxLength != nil {
		l.MaxLength = *c.MaxLength
	}

	return l
}

// PolicyConfig holds the dependencies of a Policies service. Store is
// required. A nil Now uses time.Now.
type PolicyConfig struct {
	Store PolicyStore
	Now   func() time.Time
}

// Policies reads and changes the session policy.
type Policies struct {
	store PolicyStore
	now   func() time.Time
}

// NewPolicies returns a Policies service. It returns ErrMissingDependency
// when cfg has no store.
func NewPolicies(cfg PolicyConfig) (*Policies, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("%w: session policy store", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &Policies{store: cfg.Store, now: now}, nil
}

// Now returns the time of the clock of the service, in UTC, to the
// millisecond that a store keeps.
func (s *Policies) Now() time.Time {
	return s.now().UTC().Truncate(time.Millisecond)
}

// Policy returns the session policy of the installation.
func (s *Policies) Policy(ctx context.Context) (Policy, error) {
	p, err := s.store.GetSessionPolicy(ctx)
	if err != nil {
		return Policy{}, fmt.Errorf("read session policy: %w", err)
	}

	return p, nil
}

// SetPolicy applies c to the policy and returns the stored policy. The
// store reads and writes the policy in one transaction, so two changes
// cannot lose one of them. e is the audit entry of the caller: SetPolicy
// completes it with the time, the scope @server, the object PolicyRef and
// the old and new value of each limit that changed. A new limit applies to
// the next check of a session. It returns ErrInvalid when the new limits
// fail validation.
func (s *Policies) SetPolicy(ctx context.Context, e audit.Entry, c PolicyChange) (Policy, error) {
	p, err := s.store.UpdateSessionPolicy(ctx, func(old Policy) (Policy, audit.Entry, error) {
		p := Policy{UpdatedAt: s.Now(), Limits: c.applyTo(old.Limits)}

		if err := p.Limits.Validate(); err != nil {
			return Policy{}, audit.Entry{}, err
		}

		return p, policyEntry(e, p.UpdatedAt, old.Limits, p.Limits), nil
	})

	if errors.Is(err, ErrInvalid) {
		return Policy{}, err
	}

	if err != nil {
		return Policy{}, fmt.Errorf("set session policy: %w", err)
	}

	return p, nil
}

// policyEntry returns e as the audit entry of a change of the policy from
// old to updated at the time at.
func policyEntry(e audit.Entry, at time.Time, old, updated Limits) audit.Entry {
	e.At = at
	e.Namespace = audit.ServerScope
	e.Kind = audit.KindChange
	e.Outcome = audit.OutcomeSuccess
	e.Object = PolicyRef
	e.Changes = nil

	for _, c := range []struct {
		key        string
		old, value time.Duration
	}{
		{key: "idleSeconds", old: old.Idle, value: updated.Idle},
		{key: "maxLengthSeconds", old: old.MaxLength, value: updated.MaxLength},
	} {
		if c.old == c.value {
			continue
		}

		before := seconds(c.old)
		e.Changes = append(e.Changes, audit.Change{
			Key: c.key, Old: &before, New: seconds(c.value), Layer: "", TakesEffect: "",
		})
	}

	return e
}

// seconds returns d as a decimal count of whole seconds.
func seconds(d time.Duration) string {
	return strconv.FormatInt(int64(d/time.Second), 10)
}
