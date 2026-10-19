package profile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// ErrMissingDependency reports a Config without a required port.
var ErrMissingDependency = errors.New("missing dependency")

// Sealer seals and opens the secrets of a profile under the data key of a
// scope. *secret.Service implements it.
type Sealer interface {
	Encrypt(ctx context.Context, scope secret.Scope, plaintexts [][]byte) ([][]byte, error)
	Decrypt(ctx context.Context, scope secret.Scope, ciphertexts [][]byte) ([][]byte, error)
}

// Config holds the dependencies of a Service.
type Config struct {
	Store      Store
	Namespaces namespace.Store
	Sealer     Sealer
	Audit      audit.Store
	Authorizer identity.Authorizer
	Now        func() time.Time
}

// Service implements the profile use cases. Each method checks the actions
// it needs for the caller, on the namespace of the profile. A grant on one
// profile does not exist yet, so a grant on the namespace is the narrowest
// grant that works.
type Service struct {
	store      Store
	namespaces namespace.Store
	sealer     Sealer
	audit      audit.Store
	authz      identity.Authorizer
	now        func() time.Time
}

// Actor is the caller of a use case. By is the template of the audit entry:
// the caller fills who and where, and the service fills the action, the
// object and the time.
type Actor struct {
	Principal identity.Principal
	By        audit.Entry
}

// Target names one profile.
type Target struct {
	Namespace namespace.Name
	Ref       Ref
}

// NewProfile is the content of a create.
type NewProfile struct {
	Name     Name
	Document []byte
	Reason   string
}

// Change is the content of a save. Base is the number of the revision that
// the caller read.
type Change struct {
	Base     int
	Document []byte
	Reason   string
}

// EntryChange sets one entry on top of Base.
type EntryChange struct {
	Base   int
	Path   string
	Input  EntryInput
	Reason string
}

// EntryRemoval removes one entry on top of Base.
type EntryRemoval struct {
	Base   int
	Path   string
	Reason string
}

// Removal is the content of a delete. A non-zero IfLatest names the
// revision that the caller read.
type Removal struct {
	IfLatest int
	Reason   string
}

// RevisionPage selects revisions: those with a number below Before (0 means
// from the newest), at most Limit.
type RevisionPage struct {
	Before int
	Limit  int
}

// ReadOptions selects what a read answers. Revision 0 is the newest. A
// non-empty Entry limits the answer to the entry at that path. Reveal also
// answers the plain text of each secret: it needs profile:open and writes an
// audit entry of the kind reveal before it answers. If the entry fails, no
// value leaves.
type ReadOptions struct {
	Revision int
	Entry    string
	Reveal   bool
}

// View is a revision read for a caller. Entries never hold a secret value.
// Secrets holds the plain text of each secret, by path, and only when the
// read revealed them.
type View struct {
	Profile  Profile
	Revision Revision
	Entries  []Entry
	Secrets  map[string]string
}

// NewService returns a Service. It returns ErrMissingDependency when a port
// is missing. A nil Now uses time.Now.
func NewService(cfg Config) (*Service, error) {
	if cfg.Store == nil || cfg.Namespaces == nil || cfg.Sealer == nil || cfg.Audit == nil || cfg.Authorizer == nil {
		return nil, fmt.Errorf("%w: store, namespaces, sealer, audit and authorizer", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &Service{
		store: cfg.Store, namespaces: cfg.Namespaces, sealer: cfg.Sealer, audit: cfg.Audit, authz: cfg.Authorizer,
		now: now,
	}, nil
}

// Create makes the profile with revision 1. A document with a routing
// change also needs profile:open.
func (s *Service) Create(ctx context.Context, a Actor, ns namespace.Name, in NewProfile) (Profile, Revision, error) {
	if err := s.require(ctx, a, ns, identity.ActionProfileWrite); err != nil {
		return Profile{}, Revision{}, err
	}

	if _, err := ParseName(string(in.Name)); err != nil {
		return Profile{}, Revision{}, err
	}

	if err := audit.ValidateReason(in.Reason); err != nil {
		return Profile{}, Revision{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	doc, err := ParseDocument(in.Document)
	if err != nil {
		return Profile{}, Revision{}, err
	}

	fence, err := s.fence(ctx, ns)
	if err != nil {
		return Profile{}, Revision{}, err
	}

	id := NewID()

	stored, err := s.seal(ctx, a, sealJob{ns: ns, id: id, before: nil, doc: doc})
	if err != nil {
		return Profile{}, Revision{}, err
	}

	at := s.now().UTC()
	p := Profile{
		ID: id, Namespace: ns, Name: in.Name, Latest: 1, CreatedAt: at, CreatedBy: a.Principal.Subject, UpdatedAt: at,
	}
	r := newRevision(id, 1, stored, at)
	r.Author, r.Reason = a.Principal.Subject, in.Reason
	e := s.entry(a, record{
		ns: ns, action: identity.ActionProfileWrite, id: id, name: in.Name, rev: 1, kind: audit.KindChange,
		reason: in.Reason,
	})

	created, err := s.store.Create(ctx, p, r, fence, e)
	if err != nil {
		return Profile{}, Revision{}, fmt.Errorf("create profile %s: %w", in.Name, err)
	}

	return created, r, nil
}

// Get reads the metadata of one profile.
func (s *Service) Get(ctx context.Context, a Actor, t Target) (Profile, error) {
	if err := s.require(ctx, a, t.Namespace, identity.ActionProfileRead); err != nil {
		return Profile{}, err
	}

	p, err := s.store.Get(ctx, t.Namespace, t.Ref)
	if err != nil {
		return Profile{}, fmt.Errorf("get profile %s: %w", t.Ref, err)
	}

	return p, nil
}

// List reads one page of profiles of the namespace in q.
func (s *Service) List(ctx context.Context, a Actor, q Filter) (Page, error) {
	if err := s.require(ctx, a, q.Namespace, identity.ActionProfileRead); err != nil {
		return Page{}, err
	}

	page, err := s.store.List(ctx, q)
	if err != nil {
		return Page{}, fmt.Errorf("list profiles: %w", err)
	}

	return page, nil
}

// Revisions reads the revisions of a profile, newest first.
func (s *Service) Revisions(ctx context.Context, a Actor, t Target, page RevisionPage) ([]Revision, error) {
	p, err := s.Get(ctx, a, t)
	if err != nil {
		return nil, err
	}

	revs, err := s.store.Revisions(ctx, t.Namespace, p.ID, page.Before, page.Limit)
	if err != nil {
		return nil, fmt.Errorf("list revisions of %s: %w", p.Name, err)
	}

	return revs, nil
}

// Read reads one revision of a profile.
func (s *Service) Read(ctx context.Context, a Actor, t Target, o ReadOptions) (View, error) {
	if err := s.require(ctx, a, t.Namespace, identity.ActionProfileRead); err != nil {
		return View{}, err
	}

	if o.Reveal {
		if err := s.require(ctx, a, t.Namespace, identity.ActionProfileOpen); err != nil {
			return View{}, err
		}
	}

	p, err := s.store.Get(ctx, t.Namespace, t.Ref)
	if err != nil {
		return View{}, fmt.Errorf("get profile %s: %w", t.Ref, err)
	}

	r, err := s.store.Revision(ctx, t.Namespace, p.ID, o.Revision)
	if err != nil {
		return View{}, fmt.Errorf("read revision %d of %s: %w", o.Revision, p.Name, err)
	}

	doc, err := ParseDocument(r.Document)
	if err != nil {
		return View{}, fmt.Errorf("stored revision %d of %s: %w", r.Number, p.Name, err)
	}

	v := View{Profile: p, Revision: r, Entries: doc.Entries(), Secrets: nil}

	if o.Entry != "" {
		v.Entries = onlyEntry(v.Entries, o.Entry)
		if len(v.Entries) == 0 {
			return View{}, fmt.Errorf("%w: entry %q of %s", ErrNotFound, o.Entry, p.Name)
		}
	}

	if !o.Reveal {
		return v, nil
	}

	return s.reveal(ctx, a, v, doc, o.Entry)
}

// Save adds a revision on top of the revision that the caller read.
func (s *Service) Save(ctx context.Context, a Actor, t Target, in Change) (Profile, Revision, error) {
	p, prev, err := s.prepare(ctx, a, t, in.Base, in.Reason)
	if err != nil {
		return Profile{}, Revision{}, err
	}

	doc, err := ParseDocument(in.Document)
	if err != nil {
		return Profile{}, Revision{}, err
	}

	return s.commit(ctx, a, p, pending{prev: prev, doc: doc, reason: in.Reason})
}

// SaveEntry sets the entry at a path on top of the revision that the caller
// read. It is a save of the newest revision with that one leaf changed.
func (s *Service) SaveEntry(ctx context.Context, a Actor, t Target, in EntryChange) (Profile, Revision, error) {
	p, prev, err := s.prepare(ctx, a, t, in.Base, in.Reason)
	if err != nil {
		return Profile{}, Revision{}, err
	}

	doc, err := prev.WithEntry(in.Path, in.Input)
	if err != nil {
		return Profile{}, Revision{}, err
	}

	return s.commit(ctx, a, p, pending{prev: prev, doc: doc, reason: in.Reason})
}

// DeleteEntry removes the entry at a path on top of the revision that the
// caller read.
func (s *Service) DeleteEntry(ctx context.Context, a Actor, t Target, in EntryRemoval) (Profile, Revision, error) {
	p, prev, err := s.prepare(ctx, a, t, in.Base, in.Reason)
	if err != nil {
		return Profile{}, Revision{}, err
	}

	doc, err := prev.WithoutEntry(in.Path)
	if err != nil {
		return Profile{}, Revision{}, err
	}

	return s.commit(ctx, a, p, pending{prev: prev, doc: doc, reason: in.Reason})
}

// Delete removes a profile.
func (s *Service) Delete(ctx context.Context, a Actor, t Target, in Removal) error {
	if err := s.require(ctx, a, t.Namespace, identity.ActionProfileManage); err != nil {
		return err
	}

	if err := audit.ValidateReason(in.Reason); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	p, err := s.store.Get(ctx, t.Namespace, t.Ref)
	if err != nil {
		return fmt.Errorf("get profile %s: %w", t.Ref, err)
	}

	fence, err := s.fence(ctx, t.Namespace)
	if err != nil {
		return err
	}

	e := s.entry(a, record{
		ns: t.Namespace, action: identity.ActionProfileManage, id: p.ID, name: p.Name, rev: 0,
		kind: audit.KindChange, reason: in.Reason,
	})

	if err = s.store.Delete(ctx, Anchor{Namespace: t.Namespace, ID: p.ID, Latest: in.IfLatest}, fence, e); err != nil {
		return fmt.Errorf("delete profile %s: %w", p.Name, err)
	}

	return nil
}

// record is what the audit entry of one write names.
type record struct {
	ns     namespace.Name
	action identity.Action
	id     ID
	name   Name
	rev    int
	kind   audit.Kind
	reason string
}

// pending is a document that a save adds on top of prev.
type pending struct {
	prev   *Document
	doc    *Document
	reason string
}

// sealJob is one document that the service seals for a profile. before is
// the revision that the document replaces, or nil.
type sealJob struct {
	ns     namespace.Name
	id     ID
	before *Document
	doc    *Document
}

func (s *Service) require(ctx context.Context, a Actor, ns namespace.Name, action identity.Action) error {
	if err := s.authz.Authorize(ctx, a.Principal, action, identity.NamespaceObject(ns)); err != nil {
		return fmt.Errorf("authorize %s: %w", action, err)
	}

	return nil
}

func (s *Service) fence(ctx context.Context, ns namespace.Name) (namespace.Fence, error) {
	n, err := s.namespaces.GetNamespace(ctx, ns)
	if err != nil {
		return namespace.Fence{}, fmt.Errorf("read namespace %s: %w", ns, err)
	}

	return n.Fence(), nil
}

// entry makes the audit entry of one write. It names the profile and never a
// value.
func (s *Service) entry(a Actor, r record) audit.Entry {
	e := a.By
	e.At = s.now().UTC()
	e.Namespace = r.ns
	e.Subject = a.Principal.Subject
	e.TokenID = a.Principal.TokenID
	e.Action = string(r.action)
	e.Object = identity.NamespaceObject(r.ns).String()
	e.ObjectID = string(r.id)
	e.Kind = r.kind
	e.Outcome = audit.OutcomeSuccess
	e.Reason = r.reason
	e.Changes = []audit.Change{{Key: "profile", Old: nil, New: string(r.name), Layer: "", TakesEffect: ""}}

	if r.rev > 0 {
		e.Version = itoa(r.rev)
	}

	return e
}

// seal checks the routing rule against the revision that the document
// replaces, checks each $sealed value, seals each $secret and returns the
// text to store.
func (s *Service) seal(ctx context.Context, a Actor, job sealJob) ([]byte, error) {
	sc, err := scopeOf(job.ns, job.id)
	if err != nil {
		return nil, err
	}

	if sealErr := s.checkSealed(ctx, sc, job.doc); sealErr != nil {
		return nil, sealErr
	}

	text, err := job.doc.Seal(func(plain [][]byte) ([][]byte, error) {
		out, encErr := s.sealer.Encrypt(ctx, sc, plain)
		if encErr != nil {
			return nil, fmt.Errorf("seal secrets: %w", encErr)
		}

		return out, nil
	})
	if err != nil {
		return nil, err
	}

	after, err := ParseDocument(text)
	if err != nil {
		return nil, err
	}

	if changes := RoutingChanges(job.before, after); len(changes) > 0 {
		if authErr := s.require(ctx, a, job.ns, identity.ActionProfileOpen); authErr != nil {
			if errors.Is(authErr, identity.ErrDenied) {
				return nil, &RoutingError{Changes: changes}
			}

			return nil, authErr
		}
	}

	return text, nil
}

// checkSealed opens each $sealed value of doc under the scope sc, so a value
// that was copied from another profile fails.
func (s *Service) checkSealed(ctx context.Context, sc secret.Scope, doc *Document) error {
	secrets, err := doc.Secrets()
	if err != nil {
		return err
	}

	var sealed [][]byte

	for _, sec := range secrets {
		if sec.Sealed != nil {
			sealed = append(sealed, sec.Sealed)
		}
	}

	if len(sealed) == 0 {
		return nil
	}

	if _, err = s.sealer.Decrypt(ctx, sc, sealed); err != nil {
		if errors.Is(err, secret.ErrDecrypt) {
			return fmt.Errorf("%w: a $sealed value does not belong to this profile: send $secret to set a value",
				ErrInvalid)
		}

		return fmt.Errorf("check sealed values: %w", err)
	}

	return nil
}

// reveal opens the secrets of v, writes the audit entry and answers them.
func (s *Service) reveal(ctx context.Context, a Actor, v View, doc *Document, only string) (View, error) {
	secrets, err := doc.Secrets()
	if err != nil {
		return View{}, err
	}

	var picked []Secret

	for _, sec := range secrets {
		if only == "" || sec.Path == only {
			picked = append(picked, sec)
		}
	}

	plain, err := s.openSecrets(ctx, v, picked)
	if err != nil {
		return View{}, err
	}

	fence, err := s.fence(ctx, v.Profile.Namespace)
	if err != nil {
		return View{}, err
	}

	count := int64(len(picked))
	e := s.entry(a, record{
		ns: v.Profile.Namespace, action: identity.ActionProfileOpen, id: v.Profile.ID, name: v.Profile.Name,
		rev: v.Revision.Number, kind: audit.KindReveal, reason: "",
	})
	e.ValueCount = &count
	e.ValuePath = only

	if _, err = s.audit.Append(ctx, fence, e); err != nil {
		return View{}, fmt.Errorf("record reveal of %s: %w", v.Profile.Name, err)
	}

	v.Secrets = make(map[string]string, len(picked))
	for i, sec := range picked {
		v.Secrets[sec.Path] = string(plain[i])
	}

	return v, nil
}

func (s *Service) openSecrets(ctx context.Context, v View, picked []Secret) ([][]byte, error) {
	if len(picked) == 0 {
		return [][]byte{}, nil
	}

	sealed := make([][]byte, len(picked))
	for i, sec := range picked {
		sealed[i] = sec.Sealed
	}

	sc, err := scopeOf(v.Profile.Namespace, v.Profile.ID)
	if err != nil {
		return nil, err
	}

	plain, err := s.sealer.Decrypt(ctx, sc, sealed)
	if err != nil {
		return nil, fmt.Errorf("open secrets of %s: %w", v.Profile.Name, err)
	}

	return plain, nil
}

// prepare checks the caller and the base of a save, and parses the revision
// that the save builds on.
func (s *Service) prepare(ctx context.Context, a Actor, t Target, base int, reason string) (Profile, *Document, error) {
	if err := s.require(ctx, a, t.Namespace, identity.ActionProfileWrite); err != nil {
		return Profile{}, nil, err
	}

	if base < 1 {
		return Profile{}, nil, fmt.Errorf("%w: name the revision that you read: send If-Match", ErrInvalid)
	}

	if err := audit.ValidateReason(reason); err != nil {
		return Profile{}, nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	p, err := s.store.Get(ctx, t.Namespace, t.Ref)
	if err != nil {
		return Profile{}, nil, fmt.Errorf("get profile %s: %w", t.Ref, err)
	}

	if p.Latest != base {
		return Profile{}, nil, fmt.Errorf("%w: revision %d is not the newest revision of %s (%d): read it again",
			ErrConflict, base, p.Name, p.Latest)
	}

	r, err := s.store.Revision(ctx, t.Namespace, p.ID, base)
	if err != nil {
		return Profile{}, nil, fmt.Errorf("read revision %d of %s: %w", base, p.Name, err)
	}

	prev, err := ParseDocument(r.Document)
	if err != nil {
		return Profile{}, nil, fmt.Errorf("stored revision %d of %s: %w", base, p.Name, err)
	}

	return p, prev, nil
}

// commit seals the document of ch and adds it as the next revision of p.
func (s *Service) commit(ctx context.Context, a Actor, p Profile, ch pending) (Profile, Revision, error) {
	fence, err := s.fence(ctx, p.Namespace)
	if err != nil {
		return Profile{}, Revision{}, err
	}

	stored, err := s.seal(ctx, a, sealJob{ns: p.Namespace, id: p.ID, before: ch.prev, doc: ch.doc})
	if err != nil {
		return Profile{}, Revision{}, err
	}

	at := s.now().UTC()
	number := p.Latest + 1
	r := newRevision(p.ID, number, stored, at)
	r.Author, r.Reason = a.Principal.Subject, ch.reason
	e := s.entry(a, record{
		ns: p.Namespace, action: identity.ActionProfileWrite, id: p.ID, name: p.Name, rev: number,
		kind: audit.KindChange, reason: ch.reason,
	})

	updated, err := s.store.Save(ctx, Anchor{Namespace: p.Namespace, ID: p.ID, Latest: p.Latest}, r, fence, e)
	if err != nil {
		return Profile{}, Revision{}, fmt.Errorf("save profile %s: %w", p.Name, err)
	}

	return updated, r, nil
}

func scopeOf(ns namespace.Name, id ID) (secret.Scope, error) {
	sc, err := secret.NewScope(ns, string(id))
	if err != nil {
		return secret.Scope{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return sc, nil
}

// newRevision makes the revision of a stored text. The caller sets the author
// and the reason.
func newRevision(id ID, number int, text []byte, at time.Time) Revision {
	sum := sha256.Sum256(text)

	return Revision{
		Profile: id, Number: number, Document: text, SHA256: hex.EncodeToString(sum[:]),
		Author: "", CreatedAt: at, Reason: "",
	}
}

func onlyEntry(all []Entry, path string) []Entry {
	for _, e := range all {
		if e.Path == path {
			return []Entry{e}
		}
	}

	return nil
}
