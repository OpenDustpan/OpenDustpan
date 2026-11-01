package localusersvc

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/principal"
)

// Audit actions of the lockout by name.
const (
	ActionNameLocked   = "sign-in.name-locked"
	ActionNameUnlocked = "sign-in.name-unlocked"
)

// ReasonNameLocked is the reason of a refused sign-in or a refused current
// password while the name is locked.
const ReasonNameLocked = "user name locked after failed attempts"

// unknownSource is the sub-key of a request whose source address does not
// parse. Such requests share one count, which is the safe direction.
const unknownSource = "unknown"

// ipv6Prefix is the prefix length of an IPv6 source of a name count.
const ipv6Prefix = 64

// keyLength is the length of the key of the digests of the names.
const keyLength = 32

// NameKeys reads the key of the digests of the user names in the lock
// counts. Every instance on one database reads the same key. Both SQL
// adapters meet it.
type NameKeys interface {
	// NameLockKey returns the key: 32 bytes.
	NameLockKey(ctx context.Context) ([]byte, error)
}

// SourcePrefix returns the sub-key of the count of a name for the source
// address addr: the IPv4 address, or the /64 of an IPv6 address. An address
// that does not parse returns one shared sub-key.
func SourcePrefix(addr string) string {
	ip, err := netip.ParseAddr(addr)
	if err != nil {
		return unknownSource
	}

	ip = ip.Unmap()
	if ip.Is4() {
		return ip.String()
	}

	prefix, err := ip.WithZone("").Prefix(ipv6Prefix)
	if err != nil {
		return unknownSource
	}

	return prefix.String()
}

// nameDigest returns the name of the count of the folded user name: an
// HMAC-SHA-256 under the key of the installation, in hexadecimal. So the
// table of the counts holds no user name, and an unknown name counts as a
// known one does.
func (s *Service) nameDigest(ctx context.Context, folded string) (string, error) {
	key, err := s.keys.NameLockKey(ctx)
	if err != nil {
		return "", fmt.Errorf("%w: read the key of the names: %w", localuser.ErrUnavailable, err)
	}

	// Defense in depth: the store checks the length of the key too. This
	// check stays so that a store of another version never gives a short key.
	if len(key) != keyLength {
		return "", fmt.Errorf("%w: the key of the names has %d bytes", localuser.ErrUnavailable, len(key))
	}

	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(folded))

	return hex.EncodeToString(mac.Sum(nil)), nil
}

// lockKeys are the keys of the counts of one attempt: the digest of the name
// and the source prefix.
type lockKeys struct {
	name string
	sub  string
}

// countFailure adds the failure of an attempt to the count of the name from
// the source prefix and to the count of the name from every source, before
// the compare of the password. It returns an error that wraps
// authgate.ErrNameLocked while a lock holds, and ErrUnavailable when a count
// cannot be written: the caller refuses the attempt in both cases.
func (s *Service) countFailure(ctx context.Context, base audit.Entry, object string, k lockKeys) error {
	rules := []struct {
		rule  authgate.LockRule
		scope string
		sub   string
	}{
		{scope: authgate.ScopePassword, sub: k.sub, rule: authgate.PasswordRule()},
		{scope: authgate.ScopePasswordName, sub: "", rule: authgate.PasswordNameRule()},
	}

	for _, r := range rules {
		lock, err := s.locks.AddNameFailure(ctx, r.scope, k.name, r.sub, r.rule)
		if errors.Is(err, authgate.ErrNameLocked) {
			return err
		}

		if err != nil {
			return fmt.Errorf("%w: count the failure of the name: %w", localuser.ErrUnavailable, err)
		}

		if !lock.LockedUntil.IsZero() {
			// This add set the lock: the name is locked from now on.
			s.recordLock(ctx, base, object, r.scope, lock)
		}
	}

	return nil
}

// recordLock writes the audit entry of a new lock of a name, best effort: the
// lock holds in the store whether the entry is written or not.
func (s *Service) recordLock(ctx context.Context, base audit.Entry, object, scope string, lock authgate.NameLock) {
	e := s.entry(base, ActionNameLocked, object, audit.OutcomeSuccess, "")
	e.Changes = []audit.Change{
		{Key: "scope", Old: nil, New: scope, Layer: "", TakesEffect: ""},
		{Key: "lockedUntil", Old: nil, New: lock.LockedUntil.UTC().Format(time.RFC3339), Layer: "", TakesEffect: ""},
		{Key: "level", Old: nil, New: strconv.Itoa(lock.Level), Layer: "", TakesEffect: ""},
	}

	if _, err := s.audit.AppendServer(ctx, e); err != nil {
		return
	}
}

// ClearLocks clears the counts of a full sign-in: the count of the name from
// its source prefix and the count of the name from every source. The HTTP
// adapter calls it after the commit of the session. A failed clear leaves
// the counts, which is the safe direction, and returns the error.
func (s *Service) ClearLocks(ctx context.Context, in SignedIn) error {
	if in.locks.name == "" {
		return nil
	}

	if err := s.locks.ClearNameFailures(ctx, authgate.ScopePassword, in.locks.name, in.locks.sub); err != nil {
		return fmt.Errorf("clear the count of the source: %w", err)
	}

	if err := s.locks.ClearNameFailures(ctx, authgate.ScopePasswordName, in.locks.name, ""); err != nil {
		return fmt.Errorf("clear the count of the name: %w", err)
	}

	return nil
}

// Lock is the state of one count of the name of a local user.
type Lock struct {
	LockedUntil time.Time
	Scope       string
	Source      string
	Failures    int
	Level       int
}

// Locks returns the state of each count of the name of the local user of the
// person principalID, in both scopes of the password. It returns
// localuser.ErrNotFound for a person with no local user.
func (s *Service) Locks(ctx context.Context, principalID string) ([]Lock, error) {
	u, err := s.store.UserByPrincipal(ctx, principalID)
	if err != nil {
		return nil, fmt.Errorf("read the local user: %w", err)
	}

	name, err := s.nameDigest(ctx, u.UserName)
	if err != nil {
		return nil, err
	}

	var out []Lock

	for _, scope := range []string{authgate.ScopePassword, authgate.ScopePasswordName} {
		locks, listErr := s.locks.NameLocks(ctx, scope, name)
		if listErr != nil {
			return nil, fmt.Errorf("%w: list the counts of the name: %w", localuser.ErrUnavailable, listErr)
		}

		for _, l := range locks {
			out = append(out, Lock{
				LockedUntil: l.LockedUntil, Scope: scope, Source: l.Sub, Failures: l.Failures, Level: l.Level,
			})
		}
	}

	return out, nil
}

// Unlock ends every count and lock of the name of the local user of the
// person principalID, in both scopes, and writes the audit entry with the
// reason. It returns how many counts it cleared, localuser.ErrReasonRequired
// for no reason, and localuser.ErrNotFound for a person with no local user.
func (s *Service) Unlock(ctx context.Context, a Actor, principalID, reason string) (int64, error) {
	object := principal.Ref(principalID)

	if err := validReason(reason); err != nil {
		return 0, s.refuse(ctx, a.Entry, on(ActionNameUnlocked, object), "no reason", err)
	}

	u, err := s.store.UserByPrincipal(ctx, principalID)
	if err != nil {
		return 0, s.refuse(ctx, a.Entry, on(ActionNameUnlocked, object), "no local user", err)
	}

	object = UserObject(u.ID)

	name, err := s.nameDigest(ctx, u.UserName)
	if err != nil {
		return 0, s.refuse(ctx, a.Entry, on(ActionNameUnlocked, object), "key cannot be read", err)
	}

	// The clear of both counts and the entry of the unlock commit in one
	// transaction of the store: a failed append clears nothing.
	e := s.entry(a.Entry, ActionNameUnlocked, object, audit.OutcomeSuccess, reason)

	total, err := s.locks.ClearName(ctx, []string{authgate.ScopePassword, authgate.ScopePasswordName}, name, e)
	if err != nil {
		return 0, s.refuse(ctx, a.Entry, on(ActionNameUnlocked, object), "counts cannot be cleared",
			fmt.Errorf("%w: clear the counts of the name: %w", localuser.ErrUnavailable, err))
	}

	return total, nil
}
