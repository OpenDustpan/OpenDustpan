package gate

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// SchemaAccess is the subject of the schema version of the access schema,
// which holds the facts of an access limit. It is not a decision point.
const SchemaAccess = "access"

// SchemaVersion names a fact schema: <decision point>/<major>.<minor>, for
// example change.planned/1.0, or access/<major>.<minor> for the access
// schema. A new field is a new minor version. A removed or changed field is
// a new major version.
type SchemaVersion struct {
	// Subject is a decision point, or SchemaAccess.
	Subject string
	Major   int
	Minor   int
}

// String returns <subject>/<major>.<minor>.
func (v SchemaVersion) String() string {
	return v.Subject + "/" + strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor)
}

// MarshalText returns the schema version as String gives it. The zero
// version gives the empty text.
func (v SchemaVersion) MarshalText() ([]byte, error) {
	if v.Subject == "" {
		return []byte{}, nil
	}

	return []byte(v.String()), nil
}

// UnmarshalText parses text with ParseSchemaVersion. The empty text gives
// the zero version.
func (v *SchemaVersion) UnmarshalText(text []byte) error {
	if len(text) == 0 {
		*v = SchemaVersion{Subject: "", Major: 0, Minor: 0}

		return nil
	}

	parsed, err := ParseSchemaVersion(string(text))
	if err != nil {
		return err
	}

	*v = parsed

	return nil
}

// ParseSchemaVersion parses s. The subject is a decision point or
// "access". Major and minor are decimal numbers with no sign and no
// leading zero. It returns ErrInvalid for any other form.
func ParseSchemaVersion(s string) (SchemaVersion, error) {
	subject, num, ok := strings.Cut(s, "/")
	if !ok {
		return SchemaVersion{}, fmt.Errorf("%w: schema version %q", ErrInvalid, s)
	}

	if subject != SchemaAccess {
		if _, err := ParseDecisionPoint(subject); err != nil {
			return SchemaVersion{}, fmt.Errorf("schema version %q: %w", s, err)
		}
	}

	majorText, minorText, ok := strings.Cut(num, ".")
	if !ok {
		return SchemaVersion{}, fmt.Errorf("%w: schema version %q", ErrInvalid, s)
	}

	major, okMajor := parseVersionNumber(majorText)
	minor, okMinor := parseVersionNumber(minorText)

	if !okMajor || !okMinor {
		return SchemaVersion{}, fmt.Errorf("%w: schema version %q", ErrInvalid, s)
	}

	return SchemaVersion{Subject: subject, Major: major, Minor: minor}, nil
}

// maxVersionDigits bounds a major or a minor number.
const maxVersionDigits = 6

// parseVersionNumber parses 0 or a decimal number with no leading zero.
func parseVersionNumber(s string) (int, bool) {
	if s == "" || len(s) > maxVersionDigits || (len(s) > 1 && s[0] == '0') {
		return 0, false
	}

	n := 0

	for _, c := range []byte(s) {
		if c < '0' || c > '9' {
			return 0, false
		}

		n = n*10 + int(c-'0') //nolint:mnd // Decimal digits.
	}

	return n, true
}

// Group names one group of facts. Each group has one producer.
type Group string

// The groups of facts.
const (
	GroupPrincipal      Group = "principal"
	GroupSource         Group = "source"
	GroupUnit           Group = "unit"
	GroupOperation      Group = "operation"
	GroupChange         Group = "change"
	GroupPlan           Group = "plan"
	GroupCost           Group = "cost"
	GroupBudget         Group = "budget"
	GroupDrift          Group = "drift"
	GroupResourcePolicy Group = "resourcePolicy"
	GroupExternalChecks Group = "externalChecks"
	GroupQuota          Group = "quota"
	GroupDependents     Group = "dependents"
	GroupPaths          Group = "paths"
	GroupDocument       Group = "document"
	GroupState          Group = "state"
	GroupProfile        Group = "profile"
)

// HasAvailable reports whether g has the flag available. A producer of
// such a group can fail, and then gives available false and the zero value
// of each field. A condition that reads a field of such a group must also
// read the flag.
func (g Group) HasAvailable() bool {
	return g == GroupPlan || g == GroupCost || g == GroupBudget || g == GroupDrift ||
		g == GroupResourcePolicy || g == GroupQuota || g == GroupDependents ||
		g == GroupPaths || g == GroupState
}

// GroupsOf returns the groups of the fact document of point, in a fixed
// order.
func GroupsOf(point DecisionPoint) []Group {
	switch point {
	case PointChangePropose:
		return []Group{
			GroupPrincipal, GroupSource, GroupUnit, GroupOperation, GroupChange,
			GroupQuota, GroupPaths, GroupDocument,
		}
	case PointChangePlanned, PointChangeApply:
		return []Group{
			GroupPrincipal, GroupSource, GroupUnit, GroupOperation, GroupChange,
			GroupPlan, GroupCost, GroupBudget, GroupDrift, GroupResourcePolicy,
			GroupExternalChecks, GroupQuota, GroupDependents, GroupPaths, GroupDocument,
		}
	case PointStateWrite:
		return []Group{
			GroupPrincipal, GroupSource, GroupUnit, GroupOperation, GroupPlan,
			GroupDocument, GroupState,
		}
	case PointProfileSave, PointProfileOpen:
		return []Group{GroupProfile}
	default:
		return nil
	}
}

// PrincipalKind is the kind of the principal of a change or a call.
type PrincipalKind string

// The principal kinds.
const (
	PrincipalPerson  PrincipalKind = "person"
	PrincipalService PrincipalKind = "service"
	PrincipalAgent   PrincipalKind = "agent"
)

// CredentialKind is the kind of the credential of a change or a call. A
// token that a trust rule issued is CredentialTrustToken, so a gate can
// tell CI with workload identity from an access token of a person.
type CredentialKind string

// The credential kinds.
const (
	CredentialSession     CredentialKind = "session"
	CredentialAccessToken CredentialKind = "access-token"
	CredentialJobToken    CredentialKind = "job-token"
	CredentialTrustToken  CredentialKind = "trust-token"
)

// SourceKind is where a change comes from.
type SourceKind string

// The source kinds.
const (
	SourceAPI         SourceKind = "api"
	SourceCLI         SourceKind = "cli"
	SourceForge       SourceKind = "forge"
	SourceSelfService SourceKind = "self-service"
	SourceAgent       SourceKind = "agent"
	SourceSchedule    SourceKind = "schedule"
)

// OperationKind is the operation of a change. The kinds after
// OperationImport occur at state.write only.
type OperationKind string

// The operation kinds.
const (
	OperationApply         OperationKind = "apply"
	OperationDestroy       OperationKind = "destroy"
	OperationRefresh       OperationKind = "refresh"
	OperationImport        OperationKind = "import"
	OperationLock          OperationKind = "lock"
	OperationRollback      OperationKind = "rollback"
	OperationForceWrite    OperationKind = "force-write"
	OperationStateImport   OperationKind = "state-import"
	OperationRename        OperationKind = "rename"
	OperationDelete        OperationKind = "delete"
	OperationUnlock        OperationKind = "unlock"
	OperationUnlockedWrite OperationKind = "unlocked-write"
)

// Facts is the fact document of one decision point for one unit: the
// Cedar context of its request. Only the groups of GroupsOf(Schema
// point) carry meaning: the engine adapter sends only those, and the JSON
// form holds only those, with "schema" and one member for each group. Each
// fact always has a value of its type, so a set with no member is the empty
// list in JSON, never null. A fact never holds a secret value.
type Facts struct {
	Schema         SchemaVersion       `json:"schema"`
	Principal      PrincipalFacts      `json:"principal"`
	Source         SourceFacts         `json:"source"`
	Unit           UnitFacts           `json:"unit"`
	Operation      OperationFacts      `json:"operation"`
	Change         ChangeFacts         `json:"change"`
	Plan           PlanFacts           `json:"plan"`
	Cost           CostFacts           `json:"cost"`
	Budget         BudgetFacts         `json:"budget"`
	Drift          DriftFacts          `json:"drift"`
	ResourcePolicy ResourcePolicyFacts `json:"resourcePolicy"`
	ExternalChecks ExternalCheckFacts  `json:"externalChecks"`
	Quota          QuotaFacts          `json:"quota"`
	Dependents     DependentFacts      `json:"dependents"`
	Paths          PathFacts           `json:"paths"`
	Document       DocumentFacts       `json:"document"`
	State          StateFacts          `json:"state"`
	Profile        ProfileFacts        `json:"profile"`
}

// MarshalJSON returns the schema and the groups of the decision point of
// the schema, with an empty list for each set with no member and an empty
// object for each path record with no member.
//
//nolint:gocritic // A value receiver, so encoding/json also calls it for a Facts that is not addressable.
func (f Facts) MarshalJSON() ([]byte, error) {
	groups := factGroups(&f)
	doc := map[string]any{"schema": f.Schema}

	for _, g := range GroupsOf(DecisionPoint(f.Schema.Subject)) {
		doc[string(g)] = groups[g]
	}

	b, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("fact document: %w", err)
	}

	return b, nil
}

// factGroups returns each group of f by its name, with each nil set and map
// made empty.
func factGroups(f *Facts) map[Group]any {
	u, c, p, e := f.Unit, f.Change, f.Plan, f.ExternalChecks
	u.Labels = set(u.Labels)
	c.InputKeys, c.SecretKeys, c.TruncatedKeys, c.Paths = set(c.InputKeys), set(c.SecretKeys),
		set(c.TruncatedKeys), set(c.Paths)
	p.ResourceTypes = set(p.ResourceTypes)
	e.Passed, e.Failed, e.Pending, e.TimedOut = set(e.Passed), set(e.Failed), set(e.Pending), set(e.TimedOut)

	q, d, paths, st, prof := f.Quota, f.Dependents, f.Paths, f.State, f.Profile
	q.Exceeded = set(q.Exceeded)
	d.Units = set(d.Units)
	paths.Touches, paths.Only = record(paths.Touches), record(paths.Only)
	st.Removed = set(st.Removed)
	prof.ImportsAdded = set(prof.ImportsAdded)

	return map[Group]any{
		GroupPrincipal: f.Principal, GroupSource: f.Source, GroupUnit: u, GroupOperation: f.Operation,
		GroupChange: c, GroupPlan: p, GroupCost: f.Cost, GroupBudget: f.Budget, GroupDrift: f.Drift,
		GroupResourcePolicy: f.ResourcePolicy, GroupExternalChecks: e, GroupQuota: q, GroupDependents: d,
		GroupPaths: paths, GroupDocument: DocumentFacts{Truncated: set(f.Document.Truncated)}, GroupState: st,
		GroupProfile: prof,
	}
}

// set returns s, or the empty set for nil.
func set(s []string) []string {
	if s == nil {
		return []string{}
	}

	return s
}

// record returns m, or the empty record for nil.
func record(m map[ID]bool) map[ID]bool {
	if m == nil {
		return map[ID]bool{}
	}

	return m
}

// PrincipalFacts is the group principal.
type PrincipalFacts struct {
	Kind       PrincipalKind  `json:"kind"`
	Credential CredentialKind `json:"credential"`
}

// SourceFacts is the group source.
type SourceFacts struct {
	Kind SourceKind `json:"kind"`
	Ref  string     `json:"ref"`
	// Protected is true for a ref that the source filter lists as a
	// protected branch.
	Protected    bool   `json:"protected"`
	Commit       string `json:"commit"`
	UpstreamUnit string `json:"upstreamUnit"`
}

// UnitFacts is the group unit.
type UnitFacts struct {
	Tool string `json:"tool"`
	// Collection is the collection of the unit, or empty for a unit in no
	// collection.
	Collection string `json:"collection"`
	// Labels holds key=value strings.
	Labels []string `json:"labels"`
}

// OperationFacts is the group operation.
type OperationFacts struct {
	Kind OperationKind `json:"kind"`
}

// ChangeFacts is the group change. It holds the keys of the inputs, never
// a value.
type ChangeFacts struct {
	InputKeys     []string `json:"inputKeys"`
	SecretKeys    []string `json:"secretKeys"`
	TruncatedKeys []string `json:"truncatedKeys"`
	Paths         []string `json:"paths"`
}

// PlanFacts is the group plan. At state.write, Available is always false.
type PlanFacts struct {
	Available     bool     `json:"available"`
	Add           int64    `json:"add"`
	Change        int64    `json:"change"`
	Replace       int64    `json:"replace"`
	Destroy       int64    `json:"destroy"`
	ResourceTypes []string `json:"resourceTypes"`
}

// CostFacts is the group cost, in cents.
type CostFacts struct {
	Available    bool   `json:"available"`
	DeltaMonthly int64  `json:"deltaMonthly"`
	TotalMonthly int64  `json:"totalMonthly"`
	Currency     string `json:"currency"`
}

// BudgetLevel is the level of a budget.
type BudgetLevel string

// The budget levels.
const (
	BudgetOK       BudgetLevel = "ok"
	BudgetWarning  BudgetLevel = "warning"
	BudgetExceeded BudgetLevel = "exceeded"
)

// BudgetFacts is the group budget.
type BudgetFacts struct {
	Available   bool        `json:"available"`
	Level       BudgetLevel `json:"level"`
	PercentUsed int64       `json:"percentUsed"`
}

// DriftFacts is the group drift.
type DriftFacts struct {
	Available  bool  `json:"available"`
	Resources  int64 `json:"resources"`
	AgeSeconds int64 `json:"ageSeconds"`
}

// ResourcePolicyFacts is the group resourcePolicy: the counts of the
// resource policy reports of the plan.
type ResourcePolicyFacts struct {
	Available       bool  `json:"available"`
	MandatoryFailed int64 `json:"mandatoryFailed"`
	AdvisoryFailed  int64 `json:"advisoryFailed"`
	Unreadable      int64 `json:"unreadable"`
}

// ExternalCheckFacts is the group externalChecks: the names of the
// external checks by result.
type ExternalCheckFacts struct {
	Passed   []string `json:"passed"`
	Failed   []string `json:"failed"`
	Pending  []string `json:"pending"`
	TimedOut []string `json:"timedOut"`
}

// QuotaFacts is the group quota.
type QuotaFacts struct {
	Available      bool     `json:"available"`
	MaxPercentUsed int64    `json:"maxPercentUsed"`
	Exceeded       []string `json:"exceeded"`
}

// DependentFacts is the group dependents.
type DependentFacts struct {
	Available bool  `json:"available"`
	Count     int64 `json:"count"`
	// Units holds unit references.
	Units []string `json:"units"`
}

// PathFacts is the group paths. Touches and Only hold one entry for each
// path set that holds for the unit, by path set ID. Go computes them
// before any truncation.
type PathFacts struct {
	Available bool        `json:"available"`
	Touches   map[ID]bool `json:"touches"`
	Only      map[ID]bool `json:"only"`
}

// DocumentFacts is the group document.
type DocumentFacts struct {
	// Truncated holds the fact path of each set that the server emptied to
	// fit the document into MaxFactDocumentBytes.
	Truncated []string `json:"truncated"`
}

// StateFacts is the group state of state.write. It has values only for a
// tool-neutral operation, where the server holds the current and the new
// state.
type StateFacts struct {
	Available bool `json:"available"`
	// Resources is the count of resources in the new state.
	Resources int64 `json:"resources"`
	// Removed holds the resource addresses in the current state and not in
	// the new state.
	Removed []string `json:"removed"`
}

// ProfileAccess is the access of a profile open or save.
type ProfileAccess string

// The profile accesses.
const (
	ProfileRead  ProfileAccess = "read"
	ProfileOpen  ProfileAccess = "open"
	ProfileWrite ProfileAccess = "write"
)

// ProfileFacts is the group profile of profile.save and profile.open.
type ProfileFacts struct {
	Name          string        `json:"name"`
	Access        ProfileAccess `json:"access"`
	RefProtected  bool          `json:"refProtected"`
	JobOpen       bool          `json:"jobOpen"`
	RoutingChange bool          `json:"routingChange"`
	ImportsAdded  []string      `json:"importsAdded"`
}

// AccessFacts is the context of an access limit request, in the access
// schema.
type AccessFacts struct {
	Schema    SchemaVersion  `json:"schema"`
	Principal PrincipalFacts `json:"principal"`
	// AuthAgeSeconds is the time since the last authentication of the
	// subject, in whole seconds.
	AuthAgeSeconds int64           `json:"authAgeSeconds"`
	Unit           AccessUnitFacts `json:"unit"`
}

// AccessUnitFacts is the unit of the object of a call, in the access
// schema. Both are empty for an object that is not a unit or in a unit.
type AccessUnitFacts struct {
	// Collection is the collection of the unit, or empty for a unit in no
	// collection.
	Collection string `json:"collection"`
	// Labels holds key=value strings.
	Labels []string `json:"labels"`
}

// Producer gives one group of facts. Each group has one producer, with a
// conformance suite.
type Producer interface {
	// Group returns the group that the producer fills.
	Group() Group
	// Produce fills the group of the producer in f, for the unit of c at
	// index unit. The caller gives each producer its own fresh zero *Facts,
	// and copies back only the group of the producer, so the order of the
	// producers does not change the document and no producer reads the
	// group of another. Produce returns an error when it cannot give the
	// facts. The caller then sets the group to its zero value with
	// available false for a group with HasAvailable, and never keeps a
	// partial value. For a group with no flag, the evaluation fails with
	// ErrUnavailable. Produce returns ctx.Err() when ctx ends.
	Produce(ctx context.Context, c Change, unit int, f *Facts) error
}
