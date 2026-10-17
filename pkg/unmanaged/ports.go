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
// unit. Every write checks the fence of the namespace of the unit. Every
// adapter runs the suite in unmanagedtest.
type Index interface {
	// ReplaceUnit replaces the entries of unit with entries, from the state
	// item with order seq. When the stored entries come from a higher seq,
	// it changes nothing and returns nil. An empty entries deletes the
	// entries of the unit. It returns ErrInvalidKey for an invalid key,
	// graph.ErrInvalidUnit, ErrLimit above MaxIndexEntries, and
	// namespace.ErrFenced.
	ReplaceUnit(ctx context.Context, unit graph.Unit, seq int64, entries []IndexEntry, fence namespace.Fence) error
	// SetDeleted marks the entries of unit as deleted or live. The soft
	// delete and the restore of the unit call it in their own transaction.
	// A unit with no entry is not an error. It returns namespace.ErrFenced.
	SetDeleted(ctx context.Context, unit graph.Unit, deleted bool, fence namespace.Fence) error
	// DropUnit deletes the entries of unit at its purge. The findings that
	// name the unit stay: they are namespace data. It returns
	// namespace.ErrFenced.
	DropUnit(ctx context.Context, unit graph.Unit, fence namespace.Fence) error
	// Holders returns the holders of each key of keys, in every namespace,
	// ordered by unit. A key with no holder is not in the map. keys holds 1
	// to MaxLookup normalized keys. It returns ErrInvalidKey.
	Holders(ctx context.Context, keys []ResourceKey) (map[ResourceKey][]Holder, error)
	// Contested returns the contests of namespace ns: each key that a unit
	// of ns holds, and that another distinct live unit of any namespace also
	// holds, or that only deleted units hold. It orders them by
	// ResourceKey.String, starts after the key after (the zero key for the
	// first page), and returns at most limit, from 1 to MaxLookup. It
	// returns ErrInvalidKey for an invalid after.
	Contested(ctx context.Context, ns namespace.Name, after ResourceKey, limit int) ([]Contest, error)
}

// Observation is the class of one key in one scan. Record is the record of a
// cloud scan, nil for a state check. Holders are the holders of the key in
// every namespace; the store names in the finding only those of the
// namespace of the scan.
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
// scan append no entry for each finding: the scan appends one entry with its
// counts when it ends. Every adapter runs the suite in unmanagedtest.
//
//nolint:interfacebloat // A persistence port whose methods share one transaction scope: the fence, the state table and the findings of a namespace.
type FindingStore interface {
	// Record writes the observations of scan. The scan must exist and have
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
	// A second call with the same scan and key changes nothing. Each
	// observation has a transition in the state table. It returns the counts
	// of the batch, ErrInvalidKey, ErrNotFound for an unknown scan,
	// ErrTransition for a scan that is not matching, and
	// namespace.ErrFenced.
	Record(ctx context.Context, scan Scan, obs []Observation, now time.Time, fence namespace.Fence) (ScanCounts, error)
	// ResolveUnseen applies EventResolve, at the end of the match of scan,
	// to each finding of the namespace that the scan did not observe:
	//   - for a cloud scan, a finding of the kind not in any unit or left
	//     behind whose scope is a complete scope of the scan;
	//   - for a state check, a finding of the kind managed twice. A finding
	//     left behind does not resolve here: after the purge of its unit no
	//     unit holds the key, and only a scan can tell that the resource is
	//     gone. The match of a state check observes the key of each open
	//     finding again through Index.Holders, so a key that one live unit
	//     now holds resolves through Record.
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

// ScanUpdate is what an event of a scan writes beside its status.
type ScanUpdate struct {
	Scopes []ReportScope
	Counts *ScanCounts
	Reason string
}

// ScanStore is the persistence port of scan targets and scans. Every write
// checks the fence of the namespace in the same transaction. Every adapter
// runs the suite in unmanagedtest.
type ScanStore interface {
	// CreateTarget creates t and appends e. It returns ErrInvalidTarget,
	// also for a pool of another namespace or a pool that runs jobs, not
	// scans. It returns ErrAlreadyExists, ErrLimit above MaxTargets, and
	// namespace.ErrFenced.
	CreateTarget(ctx context.Context, t Target, fence namespace.Fence, e audit.Entry) error
	// UpdateTarget replaces the target of the same name and appends e. A
	// scan that runs keeps the command and the scopes that it started with.
	// It returns ErrInvalidTarget, ErrNotFound and namespace.ErrFenced.
	UpdateTarget(ctx context.Context, t Target, fence namespace.Fence, e audit.Entry) error
	// DeleteTarget deletes the target name of ns, cancels its scan that did
	// not end, and appends e. The findings of its scans stay. It returns
	// ErrNotFound and namespace.ErrFenced.
	DeleteTarget(ctx context.Context, ns namespace.Name, name string, fence namespace.Fence, e audit.Entry) error
	// Targets returns the targets of ns ordered by name.
	Targets(ctx context.Context, ns namespace.Name) ([]Target, error)
	// StartScan stores s with the status ScanQueued and appends e. For a
	// cloud scan, it starts the durable operation that a runner of the pool
	// of the target claims, in the same transaction. It returns
	// ErrScanBusy when the target has a scan that did not end, ErrNotFound
	// for an unknown target, and namespace.ErrFenced.
	StartScan(ctx context.Context, s Scan, fence namespace.Fence, e audit.Entry) (Scan, error)
	// AdvanceScan applies ev with u to the scan id of ns, by NextScan. At
	// an end status, it appends e with the counts of the scan. It returns
	// the new scan, ErrNotFound, ErrTransition and namespace.ErrFenced.
	AdvanceScan(ctx context.Context, ns namespace.Name, id string, ev ScanEvent, u ScanUpdate,
		fence namespace.Fence, e audit.Entry) (Scan, error)
	// Scan returns the scan id of ns, or ErrNotFound.
	Scan(ctx context.Context, ns namespace.Name, id string) (Scan, error)
	// Scans returns the scans of ns for target, newest first, or of every
	// target when target is empty. It starts after the scan ID after and
	// returns at most limit, from 1 to MaxPage.
	Scans(ctx context.Context, ns namespace.Name, target, after string, limit int) ([]Scan, error)
}
