package objectcontext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/identity"
)

// SchemaVersion is the version of the shape of a Document. Each change of the
// shape raises it, so a client sees the change.
const SchemaVersion = 1

// Limits of one read. A section that a limit cuts answers StatusPartial.
const (
	// MaxReadHistory bounds the history items of SectionChanges.
	MaxReadHistory = 10
	// MaxReadJobs bounds the jobs of SectionChanges.
	MaxReadJobs = 10
	// MaxReadResourceChanges bounds the resource changes of SectionChanges.
	MaxReadResourceChanges = 200
	// MaxReadNeighbors bounds the neighbors of SectionDependencies in each
	// direction, by the newest edges.
	MaxReadNeighbors = 50
	// MaxReadAudit bounds the entries of SectionAudit.
	MaxReadAudit = 20
	// MaxReadOutputsBytes bounds the masked outputs of SectionOutputs.
	MaxReadOutputsBytes = 1 << 20
)

// ErrUnavailable reports a section source that has no data for the object,
// for example a unit with no job, or a feature that is not on. The section
// answers StatusUnavailable, and the call does not fail.
var ErrUnavailable = errors.New("no data for this section")

// Request is one call of the read API: the object and the sections that the
// caller asks for. An empty Include asks for the default sections.
type Request struct {
	Object  Ref
	Include []Section
}

// ObjectID names the object of a Document on the wire. Tool and UnitID are
// set for a unit and a resource, Name for a collection and a blueprint, and
// Resource for a resource.
type ObjectID struct {
	Kind      Kind   `json:"kind"`
	Namespace string `json:"namespace"`
	Tool      string `json:"tool,omitempty"`
	UnitID    string `json:"unitID,omitempty"`
	Name      string `json:"name,omitempty"`
	Resource  string `json:"resource,omitempty"`
}

// IDOf returns the wire name of r.
func IDOf(r Ref) ObjectID {
	return ObjectID{
		Kind: r.Kind, Namespace: r.Namespace.String(), Tool: r.Unit.Tool, UnitID: r.Unit.ID, Name: r.Name,
		Resource: r.Resource,
	}
}

// Document is the answer of the read API. Sections holds one Answer for each
// section that the call answers.
type Document struct {
	Sections      map[Section]Answer `json:"sections"`
	Object        ObjectID           `json:"object"`
	SchemaVersion int                `json:"schemaVersion"`
}

// Answer is the envelope of one section: the action that the section needs,
// the object of that action, the status, and the data. Hidden counts the
// objects that the section left out because the caller cannot read them.
// Layers is set for SectionContext only: the status of each object of the
// chain. Data is nil for StatusDenied and StatusUnavailable. Otherwise it is
// one of IdentityData, ContextData, ReadmeData, OutputsData,
// DependenciesData, ChangesData, HealthData and AuditData.
type Answer struct {
	Data   any             `json:"data,omitempty"`
	Action identity.Action `json:"action"`
	Object ObjectRole      `json:"object"`
	Status Status          `json:"status"`
	Layers []LayerStatus   `json:"layers,omitempty"`
	Hidden int             `json:"hidden,omitempty"`
}

// LayerStatus is the status of one object of the context chain: StatusOK when
// the caller holds its read action, else StatusDenied. A denied layer still
// gives its owners and its on-call (Resolve).
type LayerStatus struct {
	Object Kind            `json:"object"`
	Action identity.Action `json:"action"`
	Status Status          `json:"status"`
}

// IdentityData is the data of SectionIdentity. Name is the tool's own name
// of the unit, and DisplayName its display name. For a resource, Type and
// Key name the resource. Each tool keeps its own words: Noun is "stack" for
// the Pulumi CLI and "workspace" for OpenTofu.
type IdentityData struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
	Noun        string `json:"noun"`
	Collection  string `json:"collection,omitempty"`
	Blueprint   string `json:"blueprint,omitempty"`
	Type        string `json:"type,omitempty"`
	Key         string `json:"key,omitempty"`
}

// ContextData is the data of SectionContext: the resolved context with the
// provenance of each value, and the stored entries of each layer that the
// caller can read.
type ContextData struct {
	Entries  []Entry  `json:"entries"`
	Resolved Resolved `json:"resolved"`
}

// ReadmeData is the data of SectionReadme.
type ReadmeData struct {
	Readme string `json:"readme"`
}

// OutputsData is the data of SectionOutputs: the outputs of the latest state
// item, with secret values masked. Truncated is true when the outputs were
// above MaxReadOutputsBytes and the source cut them.
type OutputsData struct {
	Outputs   map[string]json.RawMessage `json:"outputs"`
	Truncated bool                       `json:"truncated,omitempty"`
}

// DependenciesData is the data of SectionDependencies: the edges one hop
// upstream and one hop downstream, and the count of readers of the outputs
// that are principals and not units. Truncated is true when a direction had
// more than MaxReadNeighbors edges.
type DependenciesData struct {
	Upstream         []graph.Edge `json:"upstream"`
	Downstream       []graph.Edge `json:"downstream"`
	PrincipalReaders int          `json:"principalReaders"`
	Truncated        bool         `json:"truncated,omitempty"`
}

// HistoryItem is one history item in the words of its tool: Noun is
// "update" or "preview" for the Pulumi CLI and "state version" or "plan" for
// OpenTofu. Actor is the principal subject who made it.
type HistoryItem struct {
	At      time.Time `json:"at"`
	ID      string    `json:"id"`
	Noun    string    `json:"noun"`
	Actor   string    `json:"actor"`
	Summary string    `json:"summary,omitempty"`
}

// JobSummary is one job of a unit: its operation, its status and its actor.
type JobSummary struct {
	At        time.Time `json:"at"`
	ID        string    `json:"id"`
	Operation string    `json:"operation"`
	Status    string    `json:"status"`
	Actor     string    `json:"actor"`
}

// ResourceChange is one resource that the newest state item changed, or, for
// a resource, one change of it from blame.
type ResourceChange struct {
	Resource string `json:"resource"`
	Type     string `json:"type"`
	Change   string `json:"change"`
	Item     string `json:"item,omitempty"`
}

// ChangesData is the data of SectionChanges. Truncated is true when a limit
// cut the history items or the resource changes.
type ChangesData struct {
	Items     []HistoryItem    `json:"items"`
	Jobs      []JobSummary     `json:"jobs,omitempty"`
	Resources []ResourceChange `json:"resources,omitempty"`
	Truncated bool             `json:"truncated,omitempty"`
}

// HealthData is the data of SectionHealth. Status is the status of the unit
// in the words of its tool. NewestJob is nil when no job ran or the job
// feature is not on. OpenFindings counts the open unmanaged-resource
// findings that name the unit, by kind; it is nil when the feature is not on.
type HealthData struct {
	LatestItemAt time.Time        `json:"latestItemAt"`
	NewestJob    *JobSummary      `json:"newestJob,omitempty"`
	OpenFindings map[string]int64 `json:"openFindings,omitempty"`
	Status       string           `json:"status"`
	LockedBy     string           `json:"lockedBy,omitempty"`
	Locked       bool             `json:"locked"`
}

// AuditData is the data of SectionAudit: the newest audit entries whose
// object is the unit.
type AuditData struct {
	Entries []audit.Entry `json:"entries"`
}

// Reader is the service port of the read API. The console, the CLI, the
// agent tools and every other client call it, so every client gets the same
// document.
type Reader interface {
	// Read answers the document of req.Object. It returns ErrNotFound for an
	// object that does not exist or whose read action the caller does not
	// hold, so the call does not tell that a hidden object exists. It returns
	// ErrInvalidObject and ErrUnknownSection. A section that the caller may
	// not read answers StatusDenied, and a section with no data answers
	// StatusUnavailable: neither fails the call.
	Read(ctx context.Context, req Request) (Document, error)
}

// InventoryReader is the source port of the facts of an object from the
// inventory and the tool of the unit. The internal wiring implements it over
// the inventory and each tool's own port, so each tool keeps its own words.
type InventoryReader interface {
	// Chain returns the context chain of object, the most specific first,
	// in the order that Resolve reads: the object, then for a resource its
	// unit, then the blueprint and the collection of the unit, then the
	// namespace. It returns ErrNotFound for an object that does not exist.
	Chain(ctx context.Context, object Ref) ([]Ref, error)
	// Identity returns the identity of a unit or a resource.
	Identity(ctx context.Context, object Ref) (IdentityData, error)
	// Outputs returns the masked outputs of the latest state item of unit,
	// cut at maxBytes. It returns ErrUnavailable for a unit with no state.
	Outputs(ctx context.Context, unit graph.Unit, maxBytes int) (OutputsData, error)
	// Health returns the status, the lock and the age of the latest state
	// item of unit. The reader adds the newest job and the open findings.
	Health(ctx context.Context, unit graph.Unit) (HealthData, error)
}

// ContextReader is the source port of the stored context entries. Store
// implements it.
type ContextReader interface {
	// Entries returns the stored entries of objects, as Store.Entries does.
	Entries(ctx context.Context, objects []Ref) ([]Entry, error)
}

// DependencyReader is the source port of the dependency edges of one unit.
type DependencyReader interface {
	// Neighbors returns the edges one hop upstream and one hop downstream of
	// unit, the newest first, at most limit in each direction. It names
	// every unit, readable or not: the reader filters them.
	Neighbors(ctx context.Context, unit graph.Unit, limit int) (DependenciesData, error)
}

// HistoryReader is the source port of the history items of a unit, and of
// the changes of a resource from blame.
type HistoryReader interface {
	// Changes returns the newest items history items of the unit of
	// object, and the resource changes of its newest state item, at most
	// resources. For a resource, it returns the newest changes of that
	// resource.
	Changes(ctx context.Context, object Ref, items, resources int) (ChangesData, error)
}

// JobReader is the source port of the jobs of a unit.
type JobReader interface {
	// Jobs returns the newest jobs of unit, at most limit, the newest first.
	Jobs(ctx context.Context, unit graph.Unit, limit int) ([]JobSummary, error)
}

// FindingsReader is the source port of the unmanaged-resource findings that
// name a unit.
type FindingsReader interface {
	// OpenCounts returns the count of open findings that name unit, by kind.
	OpenCounts(ctx context.Context, unit graph.Unit) (map[string]int64, error)
}

// AuditReader is the source port of the audit entries of a unit.
type AuditReader interface {
	// Entries returns the newest audit entries whose object is unit, at most
	// limit, the newest first.
	Entries(ctx context.Context, unit graph.Unit, limit int) ([]audit.Entry, error)
}

// Access is the port of the access rules for one caller.
type Access interface {
	// Layers returns, for each object of chain in order, whether the caller
	// holds the read action of the object (ReadAction).
	Layers(ctx context.Context, chain []Ref) ([]bool, error)
	// Holds reports whether the caller holds action on object.
	Holds(ctx context.Context, action identity.Action, object Ref) (bool, error)
	// Readable returns the units of units that the caller can read with
	// unit:read. The adapter filters in one query, never with one check for
	// each unit.
	Readable(ctx context.Context, units []graph.Unit) (map[graph.Unit]bool, error)
}

// Sources holds the ports that the read API reads. Inventory, Context and
// Access are required. A nil feature port makes its sections answer
// StatusUnavailable: Dependencies for SectionDependencies, History for
// SectionChanges, Audit for SectionAudit. Jobs and Findings add to
// SectionChanges and SectionHealth when they are set.
type Sources struct {
	Inventory    InventoryReader
	Context      ContextReader
	Access       Access
	Dependencies DependencyReader
	History      HistoryReader
	Jobs         JobReader
	Findings     FindingsReader
	Audit        AuditReader
}

// Service implements Reader over Sources.
type Service struct {
	src Sources
}

// NewReader returns the read API over src. It returns ErrInvalidObject when
// a required port is nil.
func NewReader(src Sources) (*Service, error) {
	if src.Inventory == nil || src.Context == nil || src.Access == nil {
		return nil, fmt.Errorf("%w: the read API needs the inventory, the context and the access ports",
			ErrInvalidObject)
	}

	return &Service{src: src}, nil
}

// Read implements Reader.
func (s *Service) Read(ctx context.Context, req Request) (Document, error) {
	doc := Document{Sections: map[Section]Answer{}, Object: IDOf(req.Object), SchemaVersion: SchemaVersion}

	if err := req.Object.Validate(); err != nil {
		return doc, err
	}

	specs, err := Plan(req.Object.Kind, req.Include)
	if err != nil {
		return doc, err
	}

	chain, readable, err := s.chain(ctx, req.Object)
	if err != nil {
		return doc, err
	}

	for _, spec := range specs {
		a, err := s.section(ctx, spec, chain, readable)
		if err != nil {
			return doc, fmt.Errorf("section %s: %w", spec.Section, err)
		}

		doc.Sections[spec.Section] = a
	}

	return doc, nil
}

// chain returns the context chain of object and the layers that the caller
// can read. It returns ErrNotFound when the caller cannot read the object.
func (s *Service) chain(ctx context.Context, object Ref) ([]Ref, []bool, error) {
	chain, err := s.src.Inventory.Chain(ctx, object)
	if err != nil {
		return nil, nil, err
	}

	if len(chain) == 0 || chain[0] != object {
		return nil, nil, fmt.Errorf("%w: the chain does not start at the object", ErrInvalidObject)
	}

	readable, err := s.src.Access.Layers(ctx, chain)
	if err != nil {
		return nil, nil, err
	}

	if len(readable) != len(chain) || !readable[0] {
		return nil, nil, ErrNotFound
	}

	return chain, readable, nil
}

// section answers one section. An error of a source other than
// ErrUnavailable fails the call.
func (s *Service) section(ctx context.Context, spec SectionSpec, chain []Ref, readable []bool) (Answer, error) {
	a := Answer{Data: nil, Action: spec.Action, Object: spec.Object, Status: StatusOK, Layers: nil, Hidden: 0}
	target := chain[0]

	if spec.Object == RoleTarget || spec.Object == RoleNamespace {
		on := UnitRef(target.Unit)
		if spec.Object == RoleNamespace {
			on = NamespaceRef(target.Namespace)
		}

		ok, err := s.src.Access.Holds(ctx, spec.Action, on)
		if err != nil {
			return a, err
		}

		if !ok {
			a.Status = StatusDenied

			return a, nil
		}
	}

	err := s.fill(ctx, &a, spec.Section, chain, readable)
	if errors.Is(err, ErrUnavailable) {
		a.Data, a.Status, a.Hidden = nil, StatusUnavailable, 0

		return a, nil
	}

	return a, err
}

// fill sets the data and the status of a for section sec.
func (s *Service) fill(ctx context.Context, a *Answer, sec Section, chain []Ref, readable []bool) error {
	target := chain[0]

	switch sec {
	case SectionIdentity:
		return set(a, func() (IdentityData, error) { return s.src.Inventory.Identity(ctx, target) })
	case SectionContext:
		return s.fillContext(ctx, a, chain, readable)
	case SectionReadme:
		return s.fillReadme(ctx, a, target.Unit)
	case SectionOutputs:
		return s.fillOutputs(ctx, a, target.Unit)
	case SectionDependencies:
		return s.fillDependencies(ctx, a, target.Unit)
	case SectionChanges:
		return s.fillChanges(ctx, a, target)
	case SectionHealth:
		return s.fillHealth(ctx, a, target.Unit)
	case SectionAudit:
		if s.src.Audit == nil {
			return ErrUnavailable
		}

		return set(a, func() (AuditData, error) {
			es, err := s.src.Audit.Entries(ctx, target.Unit, MaxReadAudit)

			return AuditData{Entries: es}, err
		})
	default:
		return fmt.Errorf("%w: %q", ErrUnknownSection, sec)
	}
}

// set sets the data of a from read.
func set[T any](a *Answer, read func() (T, error)) error {
	v, err := read()
	if err != nil {
		return err
	}

	a.Data = v

	return nil
}

// fillContext resolves the context of chain. A layer that the caller cannot
// read is denied, gives its owners and on-call only, and makes the section
// partial.
func (s *Service) fillContext(ctx context.Context, a *Answer, chain []Ref, readable []bool) error {
	entries, err := s.src.Context.Entries(ctx, chain)
	if err != nil {
		return err
	}

	layers := make([]Layer, len(chain))

	for i, ref := range chain {
		layers[i] = Layer{API: nil, Output: nil, Object: ref, Hidden: !readable[i]}

		st := LayerStatus{Object: ref.Kind, Action: ReadAction(ref.Kind), Status: StatusOK}
		if !readable[i] {
			st.Status, a.Status = StatusDenied, StatusPartial
			a.Hidden++
		}

		a.Layers = append(a.Layers, st)
	}

	shown := placeEntries(layers, entries)

	r, err := Resolve(layers)
	if err != nil {
		return err
	}

	a.Data = ContextData{Entries: shown, Resolved: r}

	return nil
}

// placeEntries puts each entry of entries in the layer of its object, and
// returns the entries of the layers that are not hidden.
func placeEntries(layers []Layer, entries []Entry) []Entry {
	shown := make([]Entry, 0, len(entries))

	for i := range entries {
		e := &entries[i]

		j := slices.IndexFunc(layers, func(l Layer) bool { return l.Object == e.Object })
		if j < 0 {
			continue
		}

		if e.Source == SourceAPI {
			layers[j].API = e
		} else {
			layers[j].Output = e
		}

		if !layers[j].Hidden {
			shown = append(shown, *e)
		}
	}

	return shown
}

func (s *Service) fillReadme(ctx context.Context, a *Answer, unit graph.Unit) error {
	entries, err := s.src.Context.Entries(ctx, []Ref{UnitRef(unit)})
	if err != nil {
		return err
	}

	for i := range entries {
		if entries[i].Source == SourceOutput && entries[i].Readme != "" {
			a.Data = ReadmeData{Readme: entries[i].Readme}

			return nil
		}
	}

	return ErrUnavailable
}

func (s *Service) fillOutputs(ctx context.Context, a *Answer, unit graph.Unit) error {
	out, err := s.src.Inventory.Outputs(ctx, unit, MaxReadOutputsBytes)
	if err != nil {
		return err
	}

	if out.Truncated {
		a.Status = StatusPartial
	}

	a.Data = out

	return nil
}

// fillDependencies keeps the edges whose other unit the caller can read, and
// counts the distinct units of the others.
func (s *Service) fillDependencies(ctx context.Context, a *Answer, unit graph.Unit) error {
	if s.src.Dependencies == nil {
		return ErrUnavailable
	}

	d, err := s.src.Dependencies.Neighbors(ctx, unit, MaxReadNeighbors)
	if err != nil {
		return err
	}

	others := make([]graph.Unit, 0, len(d.Upstream)+len(d.Downstream))
	for i := range d.Upstream {
		others = append(others, d.Upstream[i].Source)
	}

	for i := range d.Downstream {
		others = append(others, d.Downstream[i].Dependent)
	}

	ok := map[graph.Unit]bool{}

	if len(others) > 0 {
		if ok, err = s.src.Access.Readable(ctx, others); err != nil {
			return err
		}
	}

	hidden := map[graph.Unit]bool{}
	d.Upstream = keepEdges(d.Upstream, func(e *graph.Edge) graph.Unit { return e.Source }, ok, hidden)
	d.Downstream = keepEdges(d.Downstream, func(e *graph.Edge) graph.Unit { return e.Dependent }, ok, hidden)

	a.Hidden = len(hidden)
	if a.Hidden > 0 || d.Truncated {
		a.Status = StatusPartial
	}

	a.Data = d

	return nil
}

// keepEdges returns the edges of es whose other unit is readable, and adds
// the other units that are not to hidden.
func keepEdges(
	es []graph.Edge,
	other func(*graph.Edge) graph.Unit,
	readable, hidden map[graph.Unit]bool,
) []graph.Edge {
	out := make([]graph.Edge, 0, len(es))

	for i := range es {
		if u := other(&es[i]); readable[u] {
			out = append(out, es[i])
		} else {
			hidden[u] = true
		}
	}

	return out
}

func (s *Service) fillChanges(ctx context.Context, a *Answer, target Ref) error {
	if s.src.History == nil {
		return ErrUnavailable
	}

	c, err := s.src.History.Changes(ctx, target, MaxReadHistory, MaxReadResourceChanges)
	if err != nil {
		return err
	}

	if s.src.Jobs != nil && target.Kind == KindUnit {
		jobs, err := s.src.Jobs.Jobs(ctx, target.Unit, MaxReadJobs)
		if err != nil && !errors.Is(err, ErrUnavailable) {
			return err
		}

		c.Jobs = jobs
	}

	if c.Truncated {
		a.Status = StatusPartial
	}

	a.Data = c

	return nil
}

func (s *Service) fillHealth(ctx context.Context, a *Answer, unit graph.Unit) error {
	h, err := s.src.Inventory.Health(ctx, unit)
	if err != nil {
		return err
	}

	if s.src.Jobs != nil {
		jobs, err := s.src.Jobs.Jobs(ctx, unit, 1)
		if err != nil && !errors.Is(err, ErrUnavailable) {
			return err
		}

		if len(jobs) > 0 {
			h.NewestJob = &jobs[0]
		}
	}

	if s.src.Findings != nil {
		counts, err := s.src.Findings.OpenCounts(ctx, unit)
		if err != nil && !errors.Is(err, ErrUnavailable) {
			return err
		}

		h.OpenFindings = counts
	}

	a.Data = h

	return nil
}
