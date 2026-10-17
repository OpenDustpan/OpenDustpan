package unmanagedtest_test

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/unmanaged"
	"opendustpan.org/opendustpan/pkg/unmanaged/unmanagedtest"
)

type indexed struct {
	entries []unmanaged.IndexEntry
	seq     int64
	deleted bool
}

// mem is the reference adapter of the three ports. It runs the suite, so
// each case runs before any SQL adapter exists.
type mem struct {
	fences   map[namespace.Name]int64
	pools    map[namespace.Name]map[string]bool
	index    map[graph.Unit]*indexed
	findings map[namespace.Name]map[unmanaged.ResourceKey]*unmanaged.Finding
	seen     map[string]map[unmanaged.ResourceKey]bool
	rules    map[namespace.Name]map[string]unmanaged.IgnoreRule
	targets  map[namespace.Name]map[string]unmanaged.Target
	scans    map[namespace.Name][]*unmanaged.Scan
	audited  []audit.Entry
	next     int
	mu       sync.Mutex
}

func newMem() *mem {
	return &mem{
		fences:   map[namespace.Name]int64{},
		pools:    map[namespace.Name]map[string]bool{},
		index:    map[graph.Unit]*indexed{},
		findings: map[namespace.Name]map[unmanaged.ResourceKey]*unmanaged.Finding{},
		seen:     map[string]map[unmanaged.ResourceKey]bool{},
		rules:    map[namespace.Name]map[string]unmanaged.IgnoreRule{},
		targets:  map[namespace.Name]map[string]unmanaged.Target{},
		scans:    map[namespace.Name][]*unmanaged.Scan{},
		audited:  nil,
		next:     0,
		mu:       sync.Mutex{},
	}
}

func checkKey(k unmanaged.ResourceKey) error {
	n, err := unmanaged.NormalizeKey(k)
	if err != nil {
		return err
	}

	if n != k {
		return fmt.Errorf("%w: not in normal form", unmanaged.ErrInvalidKey)
	}

	return nil
}

// Index.

func (m *mem) ReplaceUnit(
	_ context.Context, unit graph.Unit, seq int64, entries []unmanaged.IndexEntry, fence namespace.Fence,
) error {
	if err := unit.Validate(); err != nil {
		return err
	}

	if len(entries) > unmanaged.MaxIndexEntries {
		return unmanaged.ErrLimit
	}

	for _, e := range entries {
		for _, k := range e.Keys {
			if err := checkKey(k); err != nil {
				return err
			}
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.fenced(fence); err != nil {
		return err
	}

	cur := m.index[unit]
	if cur != nil && cur.seq > seq {
		return nil
	}

	deleted := cur != nil && cur.deleted
	m.index[unit] = &indexed{entries: slices.Clone(entries), seq: seq, deleted: deleted}

	return nil
}

func (m *mem) SetDeleted(_ context.Context, unit graph.Unit, deleted bool, fence namespace.Fence) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.fenced(fence); err != nil {
		return err
	}

	if cur := m.index[unit]; cur != nil {
		cur.deleted = deleted
	}

	return nil
}

func (m *mem) DropUnit(_ context.Context, unit graph.Unit, fence namespace.Fence) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.fenced(fence); err != nil {
		return err
	}

	delete(m.index, unit)

	return nil
}

func (m *mem) Holders(
	_ context.Context,
	keys []unmanaged.ResourceKey,
) (map[unmanaged.ResourceKey][]unmanaged.Holder, error) {
	if len(keys) == 0 || len(keys) > unmanaged.MaxLookup {
		return nil, fmt.Errorf("%w: batch of %d", unmanaged.ErrInvalidKey, len(keys))
	}

	for _, k := range keys {
		if err := checkKey(k); err != nil {
			return nil, err
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	all := m.allHolders()
	out := map[unmanaged.ResourceKey][]unmanaged.Holder{}

	for _, k := range keys {
		if hs, ok := all[k]; ok {
			out[k] = hs
		}
	}

	return out, nil
}

// contested reports whether the key of holders hs is contested in ns: a
// unit of ns holds it, and two or more live units hold it, or only deleted
// units hold it.
func contested(ns namespace.Name, hs []unmanaged.Holder) bool {
	here, live, deleted := false, map[graph.Unit]bool{}, false

	for _, h := range hs {
		here = here || h.Unit.Namespace == ns
		if h.Deleted {
			deleted = true
		} else {
			live[h.Unit] = true
		}
	}

	return here && (len(live) >= 2 || (len(live) == 0 && deleted))
}

func (m *mem) Contested(
	_ context.Context, ns namespace.Name, after unmanaged.ResourceKey, limit int,
) ([]unmanaged.Contest, error) {
	if after != (unmanaged.ResourceKey{Provider: "", ID: ""}) {
		if err := checkKey(after); err != nil {
			return nil, err
		}
	}

	if limit < 1 || limit > unmanaged.MaxLookup {
		return nil, fmt.Errorf("%w: limit %d", unmanaged.ErrInvalidKey, limit)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	var out []unmanaged.Contest

	for k, hs := range m.allHolders() {
		if after != (unmanaged.ResourceKey{Provider: "", ID: ""}) && k.String() <= after.String() {
			continue
		}

		if contested(ns, hs) {
			out = append(out, unmanaged.Contest{Key: k, Holders: hs})
		}
	}

	slices.SortFunc(out, func(a, b unmanaged.Contest) int { return cmp.Compare(a.Key.String(), b.Key.String()) })

	if len(out) > limit {
		out = out[:limit]
	}

	return out, nil
}

// FindingStore.

func (m *mem) Record(
	_ context.Context, scan unmanaged.Scan, obs []unmanaged.Observation, now time.Time, fence namespace.Fence,
) (unmanaged.ScanCounts, error) {
	var counts unmanaged.ScanCounts

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.fenced(fence); err != nil {
		return counts, err
	}

	s := m.scanOf(scan.Namespace, scan.ID)
	if s == nil {
		return counts, unmanaged.ErrNotFound
	}

	if s.Status != unmanaged.ScanMatching {
		return counts, unmanaged.ErrTransition
	}

	if m.seen[s.ID] == nil {
		m.seen[s.ID] = map[unmanaged.ResourceKey]bool{}
	}

	for _, o := range obs {
		if err := checkKey(o.Key); err != nil {
			return counts, err
		}

		if m.seen[s.ID][o.Key] {
			continue
		}

		m.seen[s.ID][o.Key] = true
		counts.Records++
		m.observe(*s, o, now, &counts)
	}

	return counts, nil
}

func (m *mem) ResolveUnseen(_ context.Context, scan unmanaged.Scan, _ time.Time, fence namespace.Fence) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.fenced(fence); err != nil {
		return 0, err
	}

	s := m.scanOf(scan.Namespace, scan.ID)
	if s == nil {
		return 0, unmanaged.ErrNotFound
	}

	complete := map[[2]string]bool{}

	for _, sc := range s.Scopes {
		if sc.Complete {
			complete[[2]string{sc.Provider, sc.Scope}] = true
		}
	}

	var n int64

	for k, f := range m.findings[s.Namespace] {
		if m.seen[s.ID][k] || f.Status == unmanaged.StatusResolved {
			continue
		}

		if resolvesUnseen(s.Kind, f.Kind, complete[[2]string{k.Provider, unmanaged.Scope(k)}]) {
			m.move(f, unmanaged.EventResolve)
			n++
		}
	}

	return n, nil
}

// resolvesUnseen reports whether a finding of kind k that a scan of kind
// scan did not see resolves. inComplete is true when the scan is complete
// in the scope of the key of the finding.
func resolvesUnseen(scan unmanaged.ScanKind, k unmanaged.Kind, inComplete bool) bool {
	switch scan {
	case unmanaged.ScanCloud:
		return k != unmanaged.KindManagedTwice && inComplete
	case unmanaged.ScanState:
		return k == unmanaged.KindManagedTwice
	default:
		// No other kind of scan exists.
		return false
	}
}

func (m *mem) Get(_ context.Context, ns namespace.Name, id string) (unmanaged.Finding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, f := range m.findings[ns] {
		if f.ID == id {
			return *f, nil
		}
	}

	return unmanaged.Finding{}, unmanaged.ErrNotFound
}

func (m *mem) List(
	_ context.Context, ns namespace.Name, flt unmanaged.Filter, after string, limit int,
) ([]unmanaged.Finding, error) {
	if limit < 1 || limit > unmanaged.MaxPage {
		return nil, fmt.Errorf("%w: limit %d", unmanaged.ErrLimit, limit)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	all := make([]unmanaged.Finding, 0, len(m.findings[ns]))

	for _, f := range m.findings[ns] {
		if selects(flt, *f) {
			all = append(all, *f)
		}
	}

	slices.SortFunc(all, func(a, b unmanaged.Finding) int {
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Key.String(), b.Key.String()))
	})

	if after != "" {
		i := slices.IndexFunc(all, func(f unmanaged.Finding) bool { return f.ID == after })
		if i < 0 {
			return nil, unmanaged.ErrNotFound
		}

		all = all[i+1:]
	}

	if len(all) > limit {
		all = all[:limit]
	}

	return all, nil
}

func selects(flt unmanaged.Filter, f unmanaged.Finding) bool {
	switch {
	case len(flt.Kinds) > 0 && !slices.Contains(flt.Kinds, f.Kind),
		len(flt.Statuses) > 0 && !slices.Contains(flt.Statuses, f.Status),
		flt.Unit != nil && !slices.Contains(f.Units, *flt.Unit),
		flt.Provider != "" && flt.Provider != f.Key.Provider,
		flt.Scope != "" && flt.Scope != unmanaged.Scope(f.Key),
		flt.Type != "" && flt.Type != f.Type:
		return false
	}

	return true
}

// checkChange reports whether c is valid for ev at now: an ignore needs a
// valid ignore.
func checkChange(ev unmanaged.Event, c unmanaged.Change, now time.Time) error {
	if ev != unmanaged.EventIgnore {
		return nil
	}

	if c.Ignore == nil {
		return unmanaged.ErrInvalidReason
	}

	return unmanaged.CheckIgnore(*c.Ignore, now)
}

//nolint:revive // The signature of the unmanaged.FindingStore port.
func (m *mem) Act(
	_ context.Context, ns namespace.Name, id string, from unmanaged.Status, ev unmanaged.Event, c unmanaged.Change,
	fence namespace.Fence, e audit.Entry,
) (unmanaged.Finding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.fenced(fence); err != nil {
		return unmanaged.Finding{}, err
	}

	var f *unmanaged.Finding

	for _, x := range m.findings[ns] {
		if x.ID == id {
			f = x
		}
	}

	if f == nil {
		return unmanaged.Finding{}, unmanaged.ErrNotFound
	}

	if f.Status != from {
		return unmanaged.Finding{}, unmanaged.ErrStatusChanged
	}

	if _, err := unmanaged.Next(f.Status, ev); err != nil {
		return unmanaged.Finding{}, err
	}

	if err := checkChange(ev, c, e.At); err != nil {
		return unmanaged.Finding{}, err
	}

	m.move(f, ev)

	if ev == unmanaged.EventIgnore {
		ig := *c.Ignore
		f.Ignore = &ig
	}

	if ev == unmanaged.EventAct && c.Pending != nil {
		p := *c.Pending
		f.Pending = &p
	}

	m.audited = append(m.audited, e)

	return *f, nil
}

func (m *mem) ExpireIgnores(_ context.Context, ns namespace.Name, now time.Time, fence namespace.Fence) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.fenced(fence); err != nil {
		return 0, err
	}

	var n int64

	for _, f := range m.findings[ns] {
		if f.Status == unmanaged.StatusIgnored && f.Ignore != nil && !f.Ignore.Until.IsZero() &&
			!now.Before(f.Ignore.Until) {
			m.move(f, unmanaged.EventIgnoreExpired)
			n++
		}
	}

	return n, nil
}

func (m *mem) PutRule(
	_ context.Context,
	r unmanaged.IgnoreRule,
	now time.Time,
	fence namespace.Fence,
	e audit.Entry,
) error {
	if err := r.Validate(now); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.fenced(fence); err != nil {
		return err
	}

	if m.rules[r.Namespace] == nil {
		m.rules[r.Namespace] = map[string]unmanaged.IgnoreRule{}
	}

	switch {
	case m.rules[r.Namespace][r.ID].ID != "":
		return unmanaged.ErrAlreadyExists
	case len(m.rules[r.Namespace]) >= unmanaged.MaxRules:
		return unmanaged.ErrLimit
	}

	m.rules[r.Namespace][r.ID] = r
	m.audited = append(m.audited, e)

	return nil
}

func (m *mem) DeleteRule(_ context.Context, ns namespace.Name, id string, fence namespace.Fence, e audit.Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.fenced(fence); err != nil {
		return err
	}

	if _, ok := m.rules[ns][id]; !ok {
		return unmanaged.ErrNotFound
	}

	delete(m.rules[ns], id)
	m.audited = append(m.audited, e)

	return nil
}

func (m *mem) Rules(_ context.Context, ns namespace.Name) ([]unmanaged.IgnoreRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]unmanaged.IgnoreRule, 0, len(m.rules[ns]))
	for _, id := range slices.Sorted(maps.Keys(m.rules[ns])) {
		out = append(out, m.rules[ns][id])
	}

	return out, nil
}

// ScanStore.

func (m *mem) CreateTarget(_ context.Context, t unmanaged.Target, fence namespace.Fence, e audit.Entry) error {
	if err := t.Validate(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.fenced(fence); err != nil {
		return err
	}

	switch {
	case !m.pools[t.Namespace][t.Pool]:
		return fmt.Errorf("%w: the pool is not in the namespace", unmanaged.ErrInvalidTarget)
	case m.targets[t.Namespace][t.Name].Name != "":
		return unmanaged.ErrAlreadyExists
	case len(m.targets[t.Namespace]) >= unmanaged.MaxTargets:
		return unmanaged.ErrLimit
	}

	if m.targets[t.Namespace] == nil {
		m.targets[t.Namespace] = map[string]unmanaged.Target{}
	}

	m.targets[t.Namespace][t.Name] = t
	m.audited = append(m.audited, e)

	return nil
}

func (m *mem) UpdateTarget(_ context.Context, t unmanaged.Target, fence namespace.Fence, e audit.Entry) error {
	if err := t.Validate(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.fenced(fence); err != nil {
		return err
	}

	if _, ok := m.targets[t.Namespace][t.Name]; !ok {
		return unmanaged.ErrNotFound
	}

	if !m.pools[t.Namespace][t.Pool] {
		return fmt.Errorf("%w: the pool is not in the namespace", unmanaged.ErrInvalidTarget)
	}

	m.targets[t.Namespace][t.Name] = t
	m.audited = append(m.audited, e)

	return nil
}

func (m *mem) DeleteTarget(
	_ context.Context,
	ns namespace.Name,
	name string,
	fence namespace.Fence,
	e audit.Entry,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.fenced(fence); err != nil {
		return err
	}

	if _, ok := m.targets[ns][name]; !ok {
		return unmanaged.ErrNotFound
	}

	delete(m.targets[ns], name)

	for _, s := range m.scans[ns] {
		if s.Target == name && !s.Status.Terminal() {
			if to, err := unmanaged.NextScan(s.Kind, s.Status, unmanaged.ScanEventCancel); err == nil {
				s.Status = to
			}
		}
	}

	m.audited = append(m.audited, e)

	return nil
}

func (m *mem) Targets(_ context.Context, ns namespace.Name) ([]unmanaged.Target, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]unmanaged.Target, 0, len(m.targets[ns]))
	for _, n := range slices.Sorted(maps.Keys(m.targets[ns])) {
		out = append(out, m.targets[ns][n])
	}

	return out, nil
}

func (m *mem) StartScan(
	_ context.Context,
	s unmanaged.Scan,
	fence namespace.Fence,
	e audit.Entry,
) (unmanaged.Scan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.fenced(fence); err != nil {
		return unmanaged.Scan{}, err
	}

	if s.Kind == unmanaged.ScanCloud {
		if _, ok := m.targets[s.Namespace][s.Target]; !ok {
			return unmanaged.Scan{}, unmanaged.ErrNotFound
		}
	}

	for _, x := range m.scans[s.Namespace] {
		if x.Target == s.Target && x.Kind == s.Kind && !x.Status.Terminal() {
			return unmanaged.Scan{}, unmanaged.ErrScanBusy
		}
	}

	s.ID, s.Status = m.id("scn"), unmanaged.ScanQueued
	stored := s
	m.scans[s.Namespace] = append(m.scans[s.Namespace], &stored)
	m.audited = append(m.audited, e)

	return s, nil
}

//nolint:revive // The signature of the unmanaged.ScanStore port.
func (m *mem) AdvanceScan(
	_ context.Context, ns namespace.Name, id string, ev unmanaged.ScanEvent, u unmanaged.ScanUpdate,
	fence namespace.Fence, e audit.Entry,
) (unmanaged.Scan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.fenced(fence); err != nil {
		return unmanaged.Scan{}, err
	}

	s := m.scanOf(ns, id)
	if s == nil {
		return unmanaged.Scan{}, unmanaged.ErrNotFound
	}

	to, err := unmanaged.NextScan(s.Kind, s.Status, ev)
	if err != nil {
		return unmanaged.Scan{}, err
	}

	s.Status = to

	if u.Scopes != nil {
		s.Scopes = slices.Clone(u.Scopes)
	}

	if u.Counts != nil {
		s.Counts = *u.Counts
	}

	if u.Reason != "" {
		s.Reason = u.Reason
	}

	if to.Terminal() {
		s.EndedAt = e.At
		m.audited = append(m.audited, e)
	}

	return *s, nil
}

func (m *mem) Scan(_ context.Context, ns namespace.Name, id string) (unmanaged.Scan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if s := m.scanOf(ns, id); s != nil {
		return *s, nil
	}

	return unmanaged.Scan{}, unmanaged.ErrNotFound
}

func (m *mem) Scans(_ context.Context, ns namespace.Name, target, after string, limit int) ([]unmanaged.Scan, error) {
	if limit < 1 || limit > unmanaged.MaxPage {
		return nil, fmt.Errorf("%w: limit %d", unmanaged.ErrLimit, limit)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	var out []unmanaged.Scan

	started := after == ""

	for _, s := range slices.Backward(m.scans[ns]) {
		if !started {
			started = s.ID == after

			continue
		}

		if target == "" || s.Target == target {
			out = append(out, *s)
		}

		if len(out) == limit {
			break
		}
	}

	return out, nil
}

func (m *mem) fenced(f namespace.Fence) error {
	if v, ok := m.fences[f.Namespace]; !ok || v != f.FailoverVersion {
		return namespace.ErrFenced
	}

	return nil
}

func (m *mem) id(prefix string) string {
	m.next++

	return fmt.Sprintf("%s-%d", prefix, m.next)
}

func (m *mem) allHolders() map[unmanaged.ResourceKey][]unmanaged.Holder {
	out := map[unmanaged.ResourceKey][]unmanaged.Holder{}

	for u, ix := range m.index {
		for _, e := range ix.entries {
			for _, k := range e.Keys {
				out[k] = append(
					out[k],
					unmanaged.Holder{Unit: u, Resource: e.Resource, Type: e.Type, Deleted: ix.deleted},
				)
			}
		}
	}

	for k := range out {
		slices.SortFunc(out[k], func(a, b unmanaged.Holder) int {
			return cmp.Or(cmp.Compare(a.Unit.String(), b.Unit.String()), cmp.Compare(a.Resource, b.Resource))
		})
	}

	return out
}

func (m *mem) scanOf(ns namespace.Name, id string) *unmanaged.Scan {
	for _, s := range m.scans[ns] {
		if s.ID == id {
			return s
		}
	}

	return nil
}

func (m *mem) observe(s unmanaged.Scan, o unmanaged.Observation, now time.Time, counts *unmanaged.ScanCounts) {
	byKey := m.findings[s.Namespace]
	if byKey == nil {
		byKey = map[unmanaged.ResourceKey]*unmanaged.Finding{}
		m.findings[s.Namespace] = byKey
	}

	f := byKey[o.Key]

	kind, isFinding := o.Class.Finding()
	if !isFinding {
		m.observeOther(f, o.Class, counts)

		return
	}

	countKind(counts, kind)

	if f == nil {
		byKey[o.Key] = m.newFinding(s, o, kind, now)

		return
	}

	ev := unmanaged.EventObserve
	if f.Kind != kind {
		ev = unmanaged.EventReclassify
	}

	m.move(f, ev)
	f.Kind = kind
	m.fill(f, s, o, now)
}

// observeOther counts an observation of class c that is not a finding, and
// resolves the finding f of its key, if any, unless c is ClassNone.
func (m *mem) observeOther(f *unmanaged.Finding, c unmanaged.Class, counts *unmanaged.ScanCounts) {
	if c == unmanaged.ClassManaged {
		counts.Managed++
	}

	if c == unmanaged.ClassElsewhere {
		counts.Elsewhere++
	}

	if f != nil && f.Status != unmanaged.StatusResolved && c != unmanaged.ClassNone {
		m.move(f, unmanaged.EventResolve)
		counts.Resolved++
	}
}

// newFinding returns a new open finding of kind for observation o, ignored
// by the first ignore rule of the namespace that matches it at now.
func (m *mem) newFinding(
	s unmanaged.Scan, o unmanaged.Observation, kind unmanaged.Kind, now time.Time,
) *unmanaged.Finding {
	f := &unmanaged.Finding{
		ID: m.id("fnd"), Namespace: s.Namespace, Key: o.Key, Kind: kind, Status: unmanaged.StatusOpen,
		FirstSeen: now, Evidence: unmanaged.EvidenceState,
		LastSeen:  time.Time{},
		Ignore:    nil,
		Pending:   nil,
		Tags:      nil,
		Type:      "",
		Region:    "",
		Name:      "",
		Scan:      "",
		Units:     nil,
		Elsewhere: false,
	}
	m.fill(f, s, o, now)

	for _, id := range slices.Sorted(maps.Keys(m.rules[s.Namespace])) {
		r := m.rules[s.Namespace][id]
		if r.Matches(*f, now) {
			f.Status = unmanaged.StatusIgnored
			f.Ignore = &unmanaged.Ignore{Reason: r.Reason, Until: r.Until, By: r.By, Rule: r.ID}

			break
		}
	}

	return f
}

func (m *mem) fill(f *unmanaged.Finding, s unmanaged.Scan, o unmanaged.Observation, now time.Time) {
	f.LastSeen, f.Scan, f.Units, f.Elsewhere = now, s.ID, nil, false

	for _, h := range o.Holders {
		if h.Unit.Namespace != s.Namespace {
			f.Elsewhere = true
		} else if !slices.Contains(f.Units, h.Unit) && len(f.Units) < unmanaged.MaxFindingUnits {
			f.Units = append(f.Units, h.Unit)
		}
	}

	if o.Record != nil {
		f.Evidence, f.Type, f.Region, f.Name, f.Tags = unmanaged.EvidenceScan, o.Record.Type, o.Record.Region,
			o.Record.Name, o.Record.Tags
	} else if len(o.Holders) > 0 && f.Type == "" {
		f.Type = o.Holders[0].Type
	}
}

// move applies ev, which the caller knows the table allows, and clears what
// the old status held.
func (m *mem) move(f *unmanaged.Finding, ev unmanaged.Event) {
	to, err := unmanaged.Next(f.Status, ev)
	if err != nil {
		return
	}

	if f.Status == unmanaged.StatusIgnored && to != unmanaged.StatusIgnored {
		f.Ignore = nil
	}

	if f.Status == unmanaged.StatusPending && to != unmanaged.StatusPending {
		f.Pending = nil
	}

	f.Status = to
}

// countKind counts an observation of a finding of kind k.
func countKind(counts *unmanaged.ScanCounts, k unmanaged.Kind) {
	switch k {
	case unmanaged.KindNotInAnyUnit:
		counts.NotInAnyUnit++
	case unmanaged.KindLeftBehind:
		counts.LeftBehind++
	case unmanaged.KindManagedTwice:
		counts.ManagedTwice++
	default:
		// Class.Finding returns only the kinds above.
	}
}

func TestMem(t *testing.T) {
	t.Parallel()

	unmanagedtest.Run(t, func(_ *testing.T) unmanagedtest.Harness {
		m := newMem()

		return unmanagedtest.Harness{
			Index: m, Findings: m, Scans: m,
			Namespace: func(_ *testing.T, name string) namespace.Fence {
				m.mu.Lock()
				defer m.mu.Unlock()

				ns := namespace.Name(name)
				m.fences[ns] = 1
				m.pools[ns] = map[string]bool{unmanagedtest.Pool: true}

				return namespace.Fence{Namespace: ns, FailoverVersion: 1}
			},
		}
	})
}
