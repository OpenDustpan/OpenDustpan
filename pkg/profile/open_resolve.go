package profile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// openResolver holds the state of one open: the revisions read, the count of
// revisions, and the marks. Only Service.Open makes one, and it checks the
// actor, the ref, the paths, the consumer, the duration and the fence before
// it reads a revision.
type openResolver struct {
	svc      *Service
	actor    *Actor
	ns       namespace.Name
	ref      Ref
	consumer OpenConsumer
	// asked maps each asked joined path to its segments. Nil means all paths.
	asked    map[string][]string
	duration time.Duration
	// revisions counts the revisions that the open read.
	revisions int
	// visiting is the stack of the profiles of the current import chain, for
	// the cycle check.
	visiting []ID
	names    []string
	cache    map[ID]*profileTree
	// reads names each profile and revision that the open read, for the audit
	// entry.
	reads []profileRead
}

// profileRead names one profile and revision that an open read.
type profileRead struct {
	id   ID
	name Name
	rev  int
}

// mergeLeaf is one leaf of an open tree.
type mergeLeaf struct {
	segments []string
	key      string
	value    any
	secret   bool
	never    bool
	final    bool
	from     ID
	fromName Name
}

// profileTree is the merged values of one profile: its imports in list
// order, then its own values, with each expression of its own tree computed.
type profileTree struct {
	leaves []mergeLeaf
	byKey  map[string]int
}

// newOpenResolver checks the actor, the ref, the paths, the consumer and the
// duration of in before any read.
func (s *Service) newOpenResolver(a *Actor, in OpenInput) (*openResolver, error) {
	if a == nil || a.Principal.Subject == "" {
		return nil, fmt.Errorf("%w: an open needs a caller", ErrInvalid)
	}

	if in.Namespace == "" {
		return nil, fmt.Errorf("%w: an open needs a namespace", ErrInvalid)
	}

	if in.Ref.ID == "" && in.Ref.Name == "" {
		return nil, fmt.Errorf("%w: an open needs a profile", ErrInvalid)
	}

	if len(in.Paths) > MaxSessionPaths {
		return nil, fmt.Errorf("%w: an open asks at most %d paths", ErrInvalid, MaxSessionPaths)
	}

	asked, err := parseAskedPaths(in.Paths)
	if err != nil {
		return nil, err
	}

	if _, err = ParseOpenConsumer(string(in.Consumer)); err != nil {
		return nil, err
	}

	duration := in.Duration
	if duration == 0 {
		duration = DefaultSessionDuration
	}

	if duration < MinSessionDuration || duration > MaxSessionDuration {
		return nil, fmt.Errorf("%w: duration %s is outside %s to %s",
			ErrInvalid, duration, MinSessionDuration, MaxSessionDuration)
	}

	return &openResolver{
		svc: s, actor: a, ns: in.Namespace, ref: in.Ref, consumer: in.Consumer,
		asked: nilIfEmpty(asked), duration: duration,
		revisions: 0, visiting: nil, names: nil, cache: map[ID]*profileTree{}, reads: nil,
	}, nil
}

// nilIfEmpty answers nil for an empty asked map: no paths list means all
// paths.
func nilIfEmpty(asked map[string][]string) map[string][]string {
	if len(asked) == 0 {
		return nil
	}

	return asked
}

// parseAskedPaths splits each asked joined path into segments.
func parseAskedPaths(paths []string) (map[string][]string, error) {
	asked := map[string][]string{}

	for _, p := range paths {
		segs, err := splitOpenPath(p)
		if err != nil {
			return nil, err
		}

		asked[p] = segs
	}

	return asked, nil
}

// authorize checks that the caller may open: a job token never opens, and
// the caller needs profile:open. A refusal writes one denied entry of the
// kind access and no value.
func (r *openResolver) authorize(ctx context.Context) error {
	if r.actor.Principal.Credential == identity.CredentialJobToken {
		err := fmt.Errorf("authorize %s: %w", identity.ActionProfileOpen, identity.ErrDenied)
		r.recordDenied(ctx, audit.OutcomeDenied)

		return err
	}

	if err := r.svc.require(ctx, r.actor, r.ns, identity.ActionProfileOpen); err != nil {
		if errors.Is(err, identity.ErrDenied) {
			r.recordDenied(ctx, audit.OutcomeDenied)
		}

		return err
	}

	return nil
}

// recordDenied writes the denied entry of a refused open. A profile that is
// not found writes no entry: the refusal only.
func (r *openResolver) recordDenied(ctx context.Context, outcome audit.Outcome) {
	p, err := r.svc.store.Get(ctx, r.ns, r.ref)
	if err != nil {
		return
	}

	r.svc.recordAccessRefused(ctx, r.actor, r.ns, p, outcome)
}

// openAccessFixedChanges is the number of fixed changes of an access entry:
// consumer and paths, before one change for each profile and revision.
const openAccessFixedChanges = 2

// accessEntry makes the audit entry of one open: the kind access, the action
// profile:open, the consumer, each profile and revision that the open read,
// the asked paths, and no value.
func (s *Service) accessEntry(a *Actor, in OpenInput, root Profile, reads []profileRead) audit.Entry {
	e := s.entry(a, record{
		ns: in.Namespace, action: identity.ActionProfileOpen, id: root.ID, name: root.Name, rev: root.Latest,
		kind: audit.KindAccess, reason: "",
	})

	changes := make([]audit.Change, 0, len(reads)+openAccessFixedChanges)
	changes = append(changes, audit.Change{Key: "consumer", Old: nil, New: string(in.Consumer), Layer: "", TakesEffect: ""})

	paths := "all"
	if len(in.Paths) > 0 {
		if text, err := json.Marshal(in.Paths); err == nil {
			paths = string(text)
		}
	}

	changes = append(changes, audit.Change{Key: "paths", Old: nil, New: paths, Layer: "", TakesEffect: ""})

	// One change names every profile and revision that the open read: the
	// keys of an entry are unique.
	read := make([]string, 0, len(reads))
	for _, r := range reads {
		read = append(read, string(r.name)+"@"+itoa(r.rev))
	}

	changes = append(changes, audit.Change{
		Key: "profiles", Old: nil, New: strings.Join(read, ","), Layer: "", TakesEffect: "",
	})

	e.Changes = changes

	return e
}

// accessRefusedEntry makes the audit entry of a denied or refused open: the
// kind access with the outcome and no value.
func (s *Service) accessRefusedEntry(a *Actor, ns namespace.Name, root Profile, outcome audit.Outcome) audit.Entry {
	e := s.entry(a, record{
		ns: ns, action: identity.ActionProfileOpen, id: root.ID, name: root.Name, rev: 0,
		kind: audit.KindAccess, reason: "",
	})
	e.Outcome = outcome

	return e
}

// recordAccessRefused writes the audit entry of a denied or refused open:
// the kind access with the outcome and no value.
func (s *Service) recordAccessRefused(
	ctx context.Context, a *Actor, ns namespace.Name, root Profile, outcome audit.Outcome,
) {
	fence, err := s.fence(ctx, ns)
	if err != nil {
		return
	}

	e := s.accessRefusedEntry(a, ns, root, outcome)

	// A failed entry write is not the answer: the denial or the refusal is.
	_, _ = s.audit.Append(ctx, fence, e) //nolint:errcheck // A failed entry write is not the answer.
}

// resolveRoot resolves the root profile and its imports.
func (r *openResolver) resolveRoot(ctx context.Context, root Profile) (*profileTree, error) {
	rev, err := r.svc.store.Revision(ctx, r.ns, root.ID, At{Number: 0})
	if err != nil {
		return nil, fmt.Errorf("read revision of %s: %w", root.Name, err)
	}

	doc, err := ParseDocument(rev.Document)
	if err != nil {
		return nil, fmt.Errorf("stored revision %d of %s: %w", rev.Number, root.Name, err)
	}

	return r.resolveDoc(ctx, root, rev.Number, doc, 0)
}

// resolve resolves the profile id at the given import depth and answers its
// tree. It reads each revision once: a second reach reads the cache.
func (r *openResolver) resolve(ctx context.Context, id ID, depth int) (*profileTree, error) {
	if tree, ok := r.cache[id]; ok {
		return tree, nil
	}

	if depth > MaxOpenDepth {
		return nil, fmt.Errorf("%w: imports are deeper than %d levels", ErrLimit, MaxOpenDepth)
	}

	if slices.Contains(r.visiting, id) {
		return nil, fmt.Errorf("%w: %s", ErrImportCycle, strings.Join(append(r.names, r.nameOf(id)), " -> "))
	}

	p, err := r.svc.store.Get(ctx, r.ns, RefByID(id))
	if err != nil {
		return nil, fmt.Errorf("get profile %s: %w", id, err)
	}

	rev, err := r.svc.store.Revision(ctx, r.ns, id, At{Number: 0})
	if err != nil {
		return nil, fmt.Errorf("read revision of %s: %w", p.Name, err)
	}

	doc, err := ParseDocument(rev.Document)
	if err != nil {
		return nil, fmt.Errorf("stored revision %d of %s: %w", rev.Number, p.Name, err)
	}

	return r.resolveDoc(ctx, p, rev.Number, doc, depth)
}

// nameOf names id for the cycle error: the name of the chain when known,
// else the ID.
func (r *openResolver) nameOf(id ID) string {
	for n, seen := range r.visiting {
		if seen == id && n < len(r.names) {
			return r.names[n]
		}
	}

	return string(id)
}

// resolveDoc merges the imports of doc in list order, then the own values of
// p, and computes each expression of the own tree.
func (r *openResolver) resolveDoc(
	ctx context.Context, p Profile, rev int, doc *Document, depth int,
) (*profileTree, error) {
	r.revisions++
	if r.revisions > MaxOpenRevisions {
		return nil, fmt.Errorf("%w: an open reads at most %d revisions", ErrLimit, MaxOpenRevisions)
	}

	if block := providerBlockOf(doc); block != "" {
		return nil, fmt.Errorf("%w: profile %q holds the provider block %q, which the open cannot run yet",
			ErrProvider, p.Name, block)
	}

	if hasChecks(doc) {
		return nil, fmt.Errorf("%w: profile %q holds checks, which the open cannot run yet", ErrInvalid, p.Name)
	}

	r.visiting = append(r.visiting, p.ID)
	r.names = append(r.names, string(p.Name))

	defer func() {
		r.visiting = r.visiting[:len(r.visiting)-1]
		r.names = r.names[:len(r.names)-1]
	}()

	merged := newProfileTree()

	for _, item := range importsOf(doc) {
		base, _, _ := strings.Cut(item, "@")

		name, err := ParseName(base)
		if err != nil {
			return nil, fmt.Errorf("%w: imports: %q is not a profile name", ErrInvalid, item)
		}

		imp, err := r.svc.store.Get(ctx, r.ns, RefByName(name))
		if err != nil {
			return nil, fmt.Errorf("get import %s of %s: %w", name, p.Name, err)
		}

		tree, err := r.resolve(ctx, imp.ID, depth+1)
		if err != nil {
			return nil, err
		}

		if err := merged.merge(tree, p); err != nil {
			return nil, err
		}
	}

	own, templates, err := r.ownLeaves(ctx, p, doc)
	if err != nil {
		return nil, err
	}

	if err := merged.merge(own, p); err != nil {
		return nil, err
	}

	if err := r.computeTemplates(ctx, p, merged, templates); err != nil {
		return nil, err
	}

	r.cache[p.ID] = merged
	r.reads = append(r.reads, profileRead{id: p.ID, name: p.Name, rev: rev})

	return merged, nil
}

// providerBlockOf answers the first provider block of doc, or "".
func providerBlockOf(doc *Document) string {
	blocks := mapGet(doc.root, topProviders)
	if blocks == nil || blocks.Kind != yaml.MappingNode || len(blocks.Content) == 0 {
		return ""
	}

	return blocks.Content[0].Value
}

// hasChecks reports whether doc holds a rule in checks.
func hasChecks(doc *Document) bool {
	checks := mapGet(doc.root, topChecks)

	return checks != nil && checks.Kind == yaml.SequenceNode && len(checks.Content) > 0
}

// importsOf answers the import items of doc in list order.
func importsOf(doc *Document) []string {
	imports := mapGet(doc.root, topImports)
	if imports == nil {
		return nil
	}

	out := make([]string, 0, len(imports.Content))

	for _, item := range imports.Content {
		if name := importName(item); name != "" {
			out = append(out, name)
		}
	}

	return out
}

// ownLeaves splits the values of doc into literal leaves and template
// leaves: a plain string that holds "${". A $secret entry is literal: the
// stored document holds $sealed values, which the open decrypts.
func (r *openResolver) ownLeaves(
	ctx context.Context, p Profile, doc *Document,
) (*profileTree, []templateLeaf, error) {
	plain, err := r.decryptSecrets(ctx, p, doc)
	if err != nil {
		return nil, nil, err
	}

	own, templates := newProfileTree(), []templateLeaf{}

	for _, l := range doc.leaves {
		leaf := mergeLeaf{
			segments: splitJoined(l.entry.Path), key: l.entry.Path, value: nil, secret: l.entry.Secret,
			never: l.entry.NeverStore, final: l.entry.Final, from: p.ID, fromName: p.Name,
		}

		if l.entry.Secret {
			leaf.value = plain[l.entry.Path]
			own.set(leaf)

			continue
		}

		text, isText := l.entry.Value.(string)
		if isText && strings.Contains(text, "${") {
			templates = append(templates, templateLeaf{leaf: leaf, source: text})

			continue
		}

		leaf.value = l.entry.Value
		own.set(leaf)
	}

	return own, templates, nil
}

// decryptSecrets decrypts the sealed secrets of doc under the data key of p
// and answers them by path.
func (r *openResolver) decryptSecrets(
	ctx context.Context, p Profile, doc *Document,
) (map[string]string, error) {
	plain := map[string]string{}

	secrets, err := doc.Secrets()
	if err != nil {
		return nil, err
	}

	if len(secrets) == 0 {
		return plain, nil
	}

	sealed := make([][]byte, len(secrets))
	for i, sec := range secrets {
		sealed[i] = sec.Sealed
	}

	sc, err := scopeOf(r.ns, p.ID)
	if err != nil {
		return nil, err
	}

	opened, err := r.svc.sealer.Decrypt(ctx, sc, sealed)
	if err != nil {
		return nil, fmt.Errorf("open secrets of %s: %w", p.Name, err)
	}

	for i, sec := range secrets {
		plain[sec.Path] = string(opened[i])
	}

	return plain, nil
}

// splitJoined splits a joined path of an Entry back into segments.
func splitJoined(path string) []string {
	segs, err := splitOpenPath(path)
	if err != nil {
		return strings.Split(path, ".")
	}

	return segs
}

// templateLeaf is one plain value that holds "${".
type templateLeaf struct {
	leaf   mergeLeaf
	source string
}
