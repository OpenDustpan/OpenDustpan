package profile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Consumers of an open session: the only callers that may open a profile.
const (
	// OpenConsumerAPI is a script or a tool that calls the API.
	OpenConsumerAPI = "api"
	// OpenConsumerConsole is the console.
	OpenConsumerConsole = "console"
	// OpenConsumerPulumi is the Pulumi CLI through the tool-neutral API.
	OpenConsumerPulumi = "pulumi"
)

// openConsumers is the allowlist of the consumer of an open.
func openConsumers() []string {
	return []string{OpenConsumerAPI, OpenConsumerConsole, OpenConsumerPulumi}
}

// OpenInput is one open of a profile.
type OpenInput struct {
	Namespace namespace.Name
	Ref       Ref
	// Paths are the joined paths that the open answers. Empty means all.
	Paths []string
	// Consumer is api, console or pulumi.
	Consumer string
	// Duration is the life of the session. Zero means DefaultSessionDuration.
	Duration time.Duration
}

// Opened is the answer of an open: the session ID and its end. It never
// holds a value.
type Opened struct {
	SessionID string
	ExpiresAt time.Time
}

// SessionData is the answer of a session read: the plain values by joined
// path.
type SessionData struct {
	// Values holds the plain value of each path.
	Values map[string]any
	// Secrets marks the paths that hold a secret.
	Secrets map[string]bool
}

// sessionLeaf is one value of a session payload.
type sessionLeaf struct {
	Value  any  `json:"value"`
	Secret bool `json:"secret,omitempty"`
}

// sessionPayload is the sealed answer of an open.
type sessionPayload struct {
	Values map[string]sessionLeaf `json:"values"`
}

// Open checks profile:open on the namespace, merges the root profile with
// its imports, seals the result with the data key of the root profile and
// stores one session. It answers the session ID and its end, never a value.
// It writes one audit entry of the kind access. A denial and a refusal write
// one entry with the outcome and no value.
func (s *Service) Open(ctx context.Context, a *Actor, in OpenInput) (Opened, error) {
	resolver, err := s.newOpenResolver(a, in)
	if err != nil {
		return Opened{}, err
	}

	if authErr := resolver.authorize(ctx); authErr != nil {
		return Opened{}, authErr
	}

	fence, err := s.fence(ctx, in.Namespace)
	if err != nil {
		return Opened{}, err
	}

	root, err := s.store.Get(ctx, in.Namespace, in.Ref)
	if err != nil {
		return Opened{}, fmt.Errorf("get profile %s: %w", in.Ref, err)
	}

	tree, err := resolver.resolveRoot(ctx, root)
	if err != nil {
		s.recordAccessRefused(ctx, a, in.Namespace, root, audit.OutcomeFailure)

		return Opened{}, err
	}

	values, err := resolver.selectPaths(tree, root)
	if err != nil {
		s.recordAccessRefused(ctx, a, in.Namespace, root, audit.OutcomeFailure)

		return Opened{}, err
	}

	text, err := json.Marshal(sessionPayload{Values: values})
	if err != nil {
		s.recordAccessRefused(ctx, a, in.Namespace, root, audit.OutcomeFailure)

		return Opened{}, fmt.Errorf("encode open of %s: %w", root.Name, err)
	}

	if len(text) > MaxOpenBytes {
		s.recordAccessRefused(ctx, a, in.Namespace, root, audit.OutcomeFailure)

		return Opened{}, fmt.Errorf("%w: open of %s has %d bytes, the limit is %d",
			ErrLimit, root.Name, len(text), MaxOpenBytes)
	}

	now := s.now().UTC()
	expiry := now.Add(resolver.duration)

	sealed, err := s.sealOpen(ctx, in.Namespace, root.ID, text)
	if err != nil {
		s.recordAccessRefused(ctx, a, in.Namespace, root, audit.OutcomeFailure)

		return Opened{}, err
	}

	sess := Session{
		ID: ident.NewID(SessionIDPrefix), Namespace: in.Namespace, Root: root.ID,
		Owner: SessionOwner{Kind: OwnerPrincipal, ID: a.Principal.Subject}, Consumer: in.Consumer, Paths: in.Paths,
		Sealed: sealed, CreatedAt: now, ExpiresAt: expiry,
	}

	entry := s.accessEntry(a, in, root, resolver.reads)

	if err = s.sessions.Put(ctx, sess, fence, []audit.Entry{entry}); err != nil {
		return Opened{}, fmt.Errorf("store open of %s: %w", root.Name, err)
	}

	return Opened{SessionID: sess.ID, ExpiresAt: expiry}, nil
}

// sealOpen seals the answer of an open under the data key of the root
// profile. A seal error fails closed and names no value.
func (s *Service) sealOpen(
	ctx context.Context, ns namespace.Name, root ID, text []byte,
) ([]byte, error) {
	sc, err := scopeOf(ns, root)
	if err != nil {
		return nil, err
	}

	out, err := s.sealer.Encrypt(ctx, sc, [][]byte{text})
	if err != nil {
		return nil, fmt.Errorf("seal open: %w", err)
	}

	if len(out) != 1 {
		return nil, fmt.Errorf("%w: sealer returned %d values for 1 open", ErrInvalid, len(out))
	}

	return out[0], nil
}

// ReadSession checks profile:open again and answers the values of the live
// session id for the principal that opened it, or one path of it. A read
// writes no audit entry. A read after the grant ended answers a denial and
// deletes the session.
func (s *Service) ReadSession(
	ctx context.Context, a *Actor, ns namespace.Name, id string, paths []string,
) (SessionData, error) {
	if a == nil || a.Principal.Subject == "" {
		return SessionData{}, fmt.Errorf("%w: an open session read needs a caller", ErrInvalid)
	}

	if a.Principal.Credential == identity.CredentialJobToken {
		return SessionData{}, s.denySessionRead(ctx, a, ns, id)
	}

	if !validSessionID(id) {
		return SessionData{}, fmt.Errorf("open session %s: %w", id, ErrNotFound)
	}

	for _, p := range paths {
		if err := checkOpenPath(p); err != nil {
			return SessionData{}, err
		}
	}

	owner := SessionOwner{Kind: OwnerPrincipal, ID: a.Principal.Subject}

	if err := s.require(ctx, a, ns, identity.ActionProfileOpen); err != nil {
		if errors.Is(err, identity.ErrDenied) {
			return SessionData{}, s.denySessionRead(ctx, a, ns, id)
		}

		return SessionData{}, err
	}

	sess, err := s.sessions.Get(ctx, ns, id, owner, s.now().UTC())
	if err != nil {
		return SessionData{}, fmt.Errorf("read open session: %w", err)
	}

	return s.openSessionValues(ctx, a, ns, sess, paths)
}

// denySessionRead deletes the session after a refused read and answers the
// denial. A delete that fails leaves the denial as the answer, and no value
// leaves. A denied entry names the root profile when the session still reads.
func (s *Service) denySessionRead(ctx context.Context, a *Actor, ns namespace.Name, id string) error {
	denied := fmt.Errorf("authorize %s: %w", identity.ActionProfileOpen, identity.ErrDenied)

	owner := SessionOwner{Kind: OwnerPrincipal, ID: a.Principal.Subject}

	// The read names the root profile of the entry before the delete: after
	// the delete the session reads as not found.
	sess, getErr := s.sessions.Get(ctx, ns, id, owner, s.now().UTC())

	// Defense in depth: the store deletes only the session of the owner in
	// the namespace, so a refused read cannot remove a session of another
	// owner or namespace.
	// The delete is best effort: the denial is the answer either way.
	if err := s.sessions.Delete(ctx, ns, id, owner); err != nil {
		return denied
	}

	// The entry is best effort: the denial names the root profile only when
	// the session still reads.
	if getErr != nil {
		return denied
	}

	p, err := s.store.Get(ctx, ns, RefByID(sess.Root))
	if err != nil {
		return denied
	}

	s.recordAccessRefused(ctx, a, ns, p, audit.OutcomeDenied)

	return denied
}

// openSessionValues opens the sealed answer of sess and selects paths.
func (s *Service) openSessionValues(
	ctx context.Context, a *Actor, ns namespace.Name, sess Session, paths []string,
) (SessionData, error) {
	sc, err := scopeOf(ns, sess.Root)
	if err != nil {
		return SessionData{}, err
	}

	plain, err := s.sealer.Decrypt(ctx, sc, [][]byte{sess.Sealed})
	if err != nil || len(plain) != 1 {
		return SessionData{}, fmt.Errorf("open session: %w", errOrInvalid(err))
	}

	var payload sessionPayload
	if err = json.Unmarshal(plain[0], &payload); err != nil {
		return SessionData{}, fmt.Errorf("open session: stored answer is not valid: %w", ErrInvalid)
	}

	_ = a

	out := SessionData{Values: map[string]any{}, Secrets: map[string]bool{}}

	if len(paths) == 0 {
		for path, leaf := range payload.Values {
			out.Values[path], out.Secrets[path] = leaf.Value, leaf.Secret
		}

		return out, nil
	}

	for _, p := range paths {
		leaf, ok := payload.Values[p]
		if !ok {
			return SessionData{}, fmt.Errorf("open session: path %q: %w", p, ErrNotFound)
		}

		out.Values[p], out.Secrets[p] = leaf.Value, leaf.Secret
	}

	return out, nil
}

// errOrInvalid answers err, or ErrInvalid when err is nil.
func errOrInvalid(err error) error {
	if err != nil {
		return err
	}

	return ErrInvalid
}

// validSessionID reports whether id can name an open session.
func validSessionID(id string) bool {
	return len(id) > len(SessionIDPrefix) && strings.HasPrefix(id, SessionIDPrefix)
}

// checkOpenPath checks one joined path of an open or a session read.
func checkOpenPath(path string) error {
	if path == "" {
		return fmt.Errorf("%w: a path is not empty", ErrInvalid)
	}

	if strings.ContainsFunc(path, isOpenControl) {
		return fmt.Errorf("%w: path %q holds a control character", ErrInvalid, path)
	}

	if _, err := splitOpenPath(path); err != nil {
		return err
	}

	return nil
}

// isOpenControl reports whether r is a control character.
func isOpenControl(r rune) bool { return r < 0x20 || r == 0x7f }

// splitOpenPath splits a joined path into segments. A "." or a "\" in a key
// is written with a leading "\".
func splitOpenPath(path string) ([]string, error) {
	var (
		segs    []string
		current strings.Builder
	)

	escaped := false

	for _, r := range path {
		switch {
		case escaped:
			if r != '.' && r != '\\' {
				return nil, fmt.Errorf("%w: path %q escapes %q", ErrInvalid, path, r)
			}

			current.WriteRune(r)

			escaped = false
		case r == '\\':
			escaped = true
		case r == '.':
			segs = append(segs, current.String())
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}

	if escaped {
		return nil, fmt.Errorf("%w: path %q ends in an escape", ErrInvalid, path)
	}

	segs = append(segs, current.String())

	if slices.Contains(segs, "") {
		return nil, fmt.Errorf("%w: path %q holds an empty segment", ErrInvalid, path)
	}

	return segs, nil
}
