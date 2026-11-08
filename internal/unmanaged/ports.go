package unmanaged

import (
	"context"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Bounds of the ports.
const (
	// MaxLookup is the most keys that one call to Index.Holders names. The
	// match reads a report in batches of this size.
	MaxLookup = 1000
	// MaxObservations is the most observations that one call to
	// FindingStore.Record takes.
	MaxObservations = 1000
	// MaxIndexEntries bounds the entries of one unit in the index. A unit
	// with more resources indexes the first ones by resource key and marks
	// itself as truncated in the facts of the inventory.
	MaxIndexEntries = 100_000
)

// IndexEntry is one managed resource in the latest state of a unit. Keys are
// its normalized resource keys; a resource can have more than one, for
// example an ARN and a second ID form. A data source is never an entry.
type IndexEntry struct {
	Unit     graph.Unit
	Resource string
	Type     string
	Keys     []ResourceKey
}

// StateResource is the masked, tool-neutral view of one resource of a state
// item that the adapter of each tool gives to KeysOf. Provider is the cloud
// that the adapter of the tool detects from the type of the resource, for
// example "aws" for the Pulumi type "aws:s3/bucket:Bucket" and for the
// OpenTofu type "aws_s3_bucket". Attributes holds the plain string
// attributes only. A secret value is never in it.
type StateResource struct {
	Attributes map[string]string
	Resource   string
	Type       string
	Provider   string
	ID         string
	DataSource bool
}

// KeysOf returns the resource keys of r, by the key rules of each provider,
// with no key twice. It returns no key for a data source, for a provider with
// no rule, or when the attribute of the rule is missing or invalid. The
// rules:
//
//   - aws: the attribute "arn".
//   - azure: the ID of the resource, which is the Azure resource ID in both
//     tools.
//   - gcp: the attribute "full_resource_name" or "fullResourceName", where a
//     provider gives it.
//
// A team whose resources need another rule gives the rule to the adapter of
// its tool. The rules of other providers are an open question.
func KeysOf(r StateResource) []ResourceKey {
	if r.DataSource {
		return nil
	}

	var candidates []ResourceKey

	switch r.Provider {
	case ProviderAWS:
		candidates = append(candidates, ResourceKey{Provider: ProviderAWS, ID: r.Attributes["arn"]})
	case ProviderAzure:
		candidates = append(candidates, ResourceKey{Provider: ProviderAzure, ID: r.ID})
	case ProviderGCP:
		for _, a := range []string{"full_resource_name", "fullResourceName"} {
			candidates = append(candidates, ResourceKey{Provider: ProviderGCP, ID: r.Attributes[a]})
		}
	default:
		// The server has no key rule for another provider.
	}

	var out []ResourceKey

	for _, c := range candidates {
		k, err := NormalizeKey(c)
		if err != nil {
			continue
		}

		dup := false
		for _, o := range out {
			dup = dup || o == k
		}

		if !dup {
			out = append(out, k)
		}
	}

	return out
}

// Contest is one key of a namespace that the state check reads: a key that
// two or more distinct live units hold, or that only deleted units hold.
// Holders holds every holder of the key in every namespace.
type Contest struct {
	Key     ResourceKey
	Holders []Holder
}

// Index is the persistence port of the resource index: a projection of the
// managed resources in the latest state item of each unit. After each new
// latest state item, a durable operation reads the masked state (the server
// never reads plain secret values for it) and replaces the entries of the
// unit. The operation has one start key for each unit, so state items that
// arrive while it waits or runs coalesce: one run reads the latest item and
// replaces the entries once. Every write checks the fence of the namespace of
// the unit. Every adapter runs the suite in unmanagedtest.
//
// The reads see the units of one namespace and of the live namespaces linked
// with it (Links), never any other. So a namespace cannot learn which keys the
// units of another namespace hold, and cannot change the findings of a
// namespace that did not link it.
type Index interface {
	// ReplaceUnit replaces the entries of unit with entries, from the state
	// item with order seq. When the stored entries come from a higher seq,
	// it changes nothing and returns nil. An empty entries deletes the
	// entries of the unit. A SQL adapter writes the difference: it deletes
	// the entries that are gone, inserts the new ones and leaves the others,
	// so a state item that changes one resource of 100,000 writes one row.
	// It returns ErrInvalidKey for an invalid key, graph.ErrInvalidUnit,
	// ErrLimit above MaxIndexEntries, and namespace.ErrFenced.
	ReplaceUnit(ctx context.Context, unit graph.Unit, seq int64, entries []IndexEntry, fence namespace.Fence) error
	// SetDeleted marks the entries of unit as deleted or live. The soft
	// delete and the restore of the unit call it in their own transaction.
	// A unit with no entry is not an error. It returns namespace.ErrFenced.
	SetDeleted(ctx context.Context, unit graph.Unit, deleted bool, fence namespace.Fence) error
	// DropUnit deletes the entries of unit at its purge. The findings that
	// name the unit stay: they are namespace data. It returns
	// namespace.ErrFenced.
	DropUnit(ctx context.Context, unit graph.Unit, fence namespace.Fence) error
	// Holders returns the holders of each key of keys in namespace ns and in
	// the namespaces linked with ns, ordered by unit. A holder of any other
	// namespace is never in the answer. A key with no such holder is not in
	// the map. keys holds 1 to MaxLookup normalized keys. It returns
	// ErrInvalidKey.
	Holders(ctx context.Context, ns namespace.Name, keys []ResourceKey) (map[ResourceKey][]Holder, error)
	// Contested returns the contests of namespace ns: each key that a unit
	// of ns holds, and that another distinct live unit of ns or of a
	// namespace linked with ns also holds, or that only deleted units hold.
	// Holders of a contest are of ns and of the linked namespaces only. It
	// orders them by ResourceKey.String, starts after the key after (the zero
	// key for the first page), and returns at most limit, from 1 to
	// MaxLookup. It returns ErrInvalidKey for an invalid after.
	Contested(ctx context.Context, ns namespace.Name, after ResourceKey, limit int) ([]Contest, error)
}

// Links is the persistence port of the links between namespaces. A link lets
// the classification of each of two namespaces read the units of the other:
// a resource that units of both hold is managed twice in both. Two teams that
// share one cloud account ask an installation administrator for a link, who
// needs ActionServerLinkNamespaces. A link is installation data: its writes
// append their audit entry in the same transaction, and carry no namespace
// fence. Every adapter runs the suite in unmanagedtest.
//
// A link holds the stable IDs of its two namespaces, never their names. Each
// method resolves a name to the ID of the namespace that holds the name now.
// So a namespace that is created later under the name of a purged namespace
// has a new ID and gets no link of the purged one.
//
// A link classifies only while both of its namespaces are live. While one of
// them is soft deleted, the link stays stored and classifies nothing: Linked
// leaves it out, and Index.Holders and Index.Contested answer no holder of
// the deleted namespace. The link stays only so that a restore of the
// namespace in its recovery window brings it back.
type Links interface {
	// Link links a and b, in both directions, and appends e. A link that
	// exists is not an error. It returns ErrInvalidLink for a namespace
	// linked with itself and ErrNotFound for an unknown or deleted
	// namespace.
	Link(ctx context.Context, a, b namespace.Name, e audit.Entry) error
	// Unlink removes the link of a and b and appends e. It returns
	// ErrNotFound when no link exists. Then Index.Holders and
	// Index.Contested of each namespace leave out the units of the other at
	// once. The findings that the link made change at the next sweep that
	// observes their keys: the next state check of each namespace resolves a
	// finding of managed twice that the link made, and a finding of not in
	// any unit that the link resolved stays resolved until the next cloud
	// sweep of its scope, up to MaxEvery later.
	Unlink(ctx context.Context, a, b namespace.Name, e audit.Entry) error
	// Linked returns the live namespaces linked with ns, ordered by name. It
	// returns none for a deleted ns.
	Linked(ctx context.Context, ns namespace.Name) ([]namespace.Name, error)
	// SetNamespaceDeleted marks the namespace ns as deleted or live for its
	// links. The soft delete and the restore of the namespace call it in
	// their own transaction. The links of a deleted namespace stop to
	// classify at once, and a restore brings them back. It returns
	// ErrNotFound for an unknown namespace.
	SetNamespaceDeleted(ctx context.Context, ns namespace.Name, deleted bool) error
	// DropNamespace deletes every link of ns and appends e, also when ns has
	// no link. The purge of the namespace calls it in its own transaction,
	// before it deletes the namespace. It returns ErrNotFound for an unknown
	// namespace.
	DropNamespace(ctx context.Context, ns namespace.Name, e audit.Entry) error
}

// Observation is the class of one key in one sweep. Record is the record of a
// cloud sweep, nil for a state check; the match sets its Scope to the scope
// that RecordScope gives, and the finding takes it. A state check gives the
// scope of the key (Scope). Holders are the holders of the key that
// Index.Holders gives: of the namespace of the sweep and of the namespaces
// linked with it. The store names in the finding only those of the namespace
// of the sweep, and sets Finding.Elsewhere for the others.
type Observation struct {
	Record  *Record
	Key     ResourceKey
	Class   Class
	Holders []Holder
}

// Filter selects findings. An empty field selects any value.
type Filter struct {
	Unit     *graph.Unit
	Provider string
	Scope    string
	Type     string
	Kinds    []Kind
	Statuses []Status
}

// Change is what an action writes on a finding beside its status. Ignore is
// set for EventIgnore. Pending is set for EventAct. The store clears Ignore
// on the events that leave StatusIgnored and Pending on the events that leave
// StatusPending.
type Change struct {
	Ignore  *Ignore
	Pending *Pending
}

// FindingStore is the persistence port of findings and ignore rules. Every
// write checks the fence of the namespace in the same transaction. A person's
// action appends its audit entry in the same transaction. The writes of a
// sweep append no entry for each finding: the sweep appends one entry with its
// counts when it ends. Every adapter runs the suite in unmanagedtest.
//
//nolint:interfacebloat // A persistence port whose methods share one transaction scope: the fence, the state table and the findings of a namespace.
type FindingStore interface {
	// Record writes the observations of sweep. The sweep must exist and have
	// the status ScanMatching. For each observation:
	//   - A class with a finding and no finding for the key: a new open
	//     finding, then each ignore rule of the namespace that matches it
	//     at now applies EventIgnore with the reason of the rule.
	//   - The same kind as the finding: EventObserve.
	//   - Another kind: EventReclassify, and the finding takes the new kind.
	//   - ClassManaged: EventResolve on the finding of the key, if any.
	//   - ClassElsewhere: EventResolve on the finding of the key, if any.
	//   - ClassNone: nothing.
	// The evidence becomes EvidenceScan when the observation has a record.
	// A second call with the same sweep and key changes nothing. Each
	// observation has a transition in the state table. It returns the counts
	// of the batch, ErrInvalidKey, ErrNotFound for an unknown sweep,
	// ErrTransition for a sweep that is not matching, and
	// namespace.ErrFenced.
	Record(ctx context.Context, scan Scan, obs []Observation, now time.Time, fence namespace.Fence) (ScanCounts, error)
	// ResolveUnseen applies EventResolve, at the end of the match of sweep,
	// to each finding of the namespace that the sweep did not observe:
	//   - for a cloud sweep, a finding of the kind not in any unit or left
	//     behind whose scope is a complete scope of the sweep;
	//   - for a state check, a finding of the kind managed twice. A finding
	//     left behind does not resolve here: after the purge of its unit no
	//     unit holds the key, and only a sweep can tell that the resource is
	//     gone. The match of a state check observes the key of each open
	//     finding again through Index.Holders, so a key that one live unit
	//     now holds resolves through Record.
	// A cloud sweep resolves a finding only when a complete scope of the
	// sweep covers it (Covers): the same provider and account scope, and a
	// region of the scope when the scope lists regions.
	// It returns the count of resolved findings and namespace.ErrFenced.
	ResolveUnseen(ctx context.Context, scan Scan, now time.Time, fence namespace.Fence) (int64, error)
	// Get returns the finding id of namespace ns, or ErrNotFound.
	Get(ctx context.Context, ns namespace.Name, id string) (Finding, error)
	// List returns the findings of ns that f selects, ordered by kind, then
	// key. It starts after the finding whose ID is after, or at the first
	// finding when after is empty, and returns at most limit, from 1 to
	// MaxPage. It returns ErrNotFound for an unknown after.
	List(ctx context.Context, ns namespace.Name, f Filter, after string, limit int) ([]Finding, error)
	// Act applies ev with c to the finding id, when its status is still
	// from, and appends e. It returns the new finding, ErrNotFound,
	// ErrStatusChanged when the status is not from, ErrTransition,
	// ErrInvalidReason for an invalid ignore, and namespace.ErrFenced.
	Act(ctx context.Context, ns namespace.Name, id string, from Status, ev Event, c Change,
		fence namespace.Fence, e audit.Entry) (Finding, error)
	// ExpireIgnores applies EventIgnoreExpired to each ignored finding of
	// ns whose ignore ended at or before now. It returns the count, and
	// namespace.ErrFenced.
	ExpireIgnores(ctx context.Context, ns namespace.Name, now time.Time, fence namespace.Fence) (int64, error)
	// ExpirePending applies EventPendingExpired to each pending finding of
	// ns whose wait ended at or before now (Pending.Until). It returns the
	// count, and namespace.ErrFenced.
	ExpirePending(ctx context.Context, ns namespace.Name, now time.Time, fence namespace.Fence) (int64, error)
	// PutRule creates the rule r and appends e. It returns ErrInvalidRule,
	// ErrInvalidReason, ErrAlreadyExists for a taken ID, ErrLimit above
	// MaxRules, and namespace.ErrFenced. A rule applies to findings that
	// are new after it; a person ignores the open ones with Act.
	PutRule(ctx context.Context, r IgnoreRule, now time.Time, fence namespace.Fence, e audit.Entry) error
	// DeleteRule deletes the rule id of ns and appends e. The findings that
	// it ignored stay ignored until their own end. It returns ErrNotFound
	// and namespace.ErrFenced.
	DeleteRule(ctx context.Context, ns namespace.Name, id string, fence namespace.Fence, e audit.Entry) error
	// Rules returns the rules of ns ordered by ID.
	Rules(ctx context.Context, ns namespace.Name) ([]IgnoreRule, error)
}

// ScanUpdate is what an event of a sweep writes beside its status.
type ScanUpdate struct {
	Scopes []ReportScope
	Counts *ScanCounts
	Reason string
}

// ScanAdvance is one advance of a sweep: the sweep, the event and the update
// of the event.
type ScanAdvance struct {
	Namespace namespace.Name
	ID        string
	Event     ScanEvent
	Update    ScanUpdate
}

// ScanStore is the persistence port of sweep targets and sweeps. Every write
// checks the fence of the namespace in the same transaction. Every adapter
// runs the suite in unmanagedtest.
type ScanStore interface {
	// CreateTarget creates t and appends e. It returns ErrInvalidTarget,
	// also for a pool of another namespace or a pool whose purpose is jobs,
	// not sweeps. The purpose of a pool is set when the pool is made and
	// never changes, so a target never ends up on a pool that applies
	// changes. It returns ErrAlreadyExists, ErrLimit above MaxTargets, and
	// namespace.ErrFenced.
	CreateTarget(ctx context.Context, t Target, fence namespace.Fence, e audit.Entry) error
	// UpdateTarget replaces the target of the same name and appends e. A
	// sweep that runs keeps the command and the scopes that it started with.
	// It returns ErrInvalidTarget, also for a pool as CreateTarget refuses
	// it, ErrNotFound and namespace.ErrFenced.
	UpdateTarget(ctx context.Context, t Target, fence namespace.Fence, e audit.Entry) error
	// DeleteTarget deletes the target name of ns, cancels its sweep that did
	// not end, and appends e. The findings of its sweeps stay. It returns
	// ErrNotFound and namespace.ErrFenced.
	DeleteTarget(ctx context.Context, ns namespace.Name, name string, fence namespace.Fence, e audit.Entry) error
	// Targets returns the targets of ns ordered by name.
	Targets(ctx context.Context, ns namespace.Name) ([]Target, error)
	// StartScan stores s with the status ScanQueued and appends e. For a
	// cloud sweep, it starts the durable operation that a runner of the pool
	// of the target claims, in the same transaction. It returns
	// ErrScanBusy when the target has a sweep that did not end, ErrNotFound
	// for an unknown target, and namespace.ErrFenced.
	StartScan(ctx context.Context, s Scan, fence namespace.Fence, e audit.Entry) (Scan, error)
	// AdvanceScan applies a.Event with a.Update to the sweep a.ID of
	// a.Namespace, by NextScan. At an end status, it appends e with the
	// counts of the sweep. It returns the new sweep, ErrNotFound,
	// ErrTransition and namespace.ErrFenced.
	AdvanceScan(ctx context.Context, a ScanAdvance, fence namespace.Fence, e audit.Entry) (Scan, error)
	// Scan returns the sweep id of ns, or ErrNotFound.
	Scan(ctx context.Context, ns namespace.Name, id string) (Scan, error)
	// Scans returns the sweeps of ns for target, newest first, or of every
	// target when target is empty. It starts after the sweep ID after and
	// returns at most limit, from 1 to MaxPage.
	Scans(ctx context.Context, ns namespace.Name, target, after string, limit int) ([]Scan, error)
}
