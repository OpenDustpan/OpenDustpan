package gate

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
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
	SourceAPI      SourceKind = "api"
	SourceCLI      SourceKind = "cli"
	SourceForge    SourceKind = "forge"
	SourceCatalog  SourceKind = "catalog"
	SourceAgent    SourceKind = "agent"
	SourceSchedule SourceKind = "schedule"
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
// point) carry meaning, and the engine adapter sends only those. Each fact
// always has a value of its type. A fact never holds a secret value.
type Facts struct {
	Schema         SchemaVersion
	Principal      PrincipalFacts
	Source         SourceFacts
	Unit           UnitFacts
	Operation      OperationFacts
	Change         ChangeFacts
	Plan           PlanFacts
	Cost           CostFacts
	Budget         BudgetFacts
	Drift          DriftFacts
	ResourcePolicy ResourcePolicyFacts
	ExternalChecks ExternalCheckFacts
	Quota          QuotaFacts
	Dependents     DependentFacts
	Paths          PathFacts
	Document       DocumentFacts
	State          StateFacts
	Profile        ProfileFacts
}

// PrincipalFacts is the group principal.
type PrincipalFacts struct {
	Kind       PrincipalKind
	Credential CredentialKind
}

// SourceFacts is the group source.
type SourceFacts struct {
	Kind SourceKind
	Ref  string
	// Protected is true for a ref that the source filter lists as a
	// protected branch.
	Protected    bool
	Commit       string
	UpstreamUnit string
}

// UnitFacts is the group unit.
type UnitFacts struct {
	Tool       string
	Collection string
	// Labels holds key=value strings.
	Labels []string
}

// OperationFacts is the group operation.
type OperationFacts struct {
	Kind OperationKind
}

// ChangeFacts is the group change. It holds the keys of the inputs, never
// a value.
type ChangeFacts struct {
	InputKeys     []string
	SecretKeys    []string
	TruncatedKeys []string
	Paths         []string
}

// PlanFacts is the group plan. At state.write, Available is always false.
type PlanFacts struct {
	Available     bool
	Add           int64
	Change        int64
	Replace       int64
	Destroy       int64
	ResourceTypes []string
}

// CostFacts is the group cost, in cents.
type CostFacts struct {
	Available    bool
	DeltaMonthly int64
	TotalMonthly int64
	Currency     string
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
	Available   bool
	Level       BudgetLevel
	PercentUsed int64
}

// DriftFacts is the group drift.
type DriftFacts struct {
	Available  bool
	Resources  int64
	AgeSeconds int64
}

// ResourcePolicyFacts is the group resourcePolicy: the counts of the
// resource policy reports of the plan.
type ResourcePolicyFacts struct {
	Available       bool
	MandatoryFailed int64
	AdvisoryFailed  int64
	Unreadable      int64
}

// ExternalCheckFacts is the group externalChecks: the names of the
// external checks by result.
type ExternalCheckFacts struct {
	Passed   []string
	Failed   []string
	Pending  []string
	TimedOut []string
}

// QuotaFacts is the group quota.
type QuotaFacts struct {
	Available      bool
	MaxPercentUsed int64
	Exceeded       []string
}

// DependentFacts is the group dependents.
type DependentFacts struct {
	Available bool
	Count     int64
	// Units holds unit references.
	Units []string
}

// PathFacts is the group paths. Touches and Only hold one entry for each
// path set that holds for the unit, by path set ID. Go computes them
// before any truncation.
type PathFacts struct {
	Available bool
	Touches   map[ID]bool
	Only      map[ID]bool
}

// DocumentFacts is the group document.
type DocumentFacts struct {
	// Truncated holds the fact path of each set that the server emptied to
	// fit the document into MaxFactDocumentBytes.
	Truncated []string
}

// StateFacts is the group state of state.write. It has values only for a
// tool-neutral operation, where the server holds the current and the new
// state.
type StateFacts struct {
	Available bool
	// Resources is the count of resources in the new state.
	Resources int64
	// Removed holds the resource addresses in the current state and not in
	// the new state.
	Removed []string
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
	Name          string
	Access        ProfileAccess
	RefProtected  bool
	JobOpen       bool
	RoutingChange bool
	ImportsAdded  []string
}

// AccessFacts is the context of an access limit request, in the access
// schema.
type AccessFacts struct {
	Schema     SchemaVersion
	Principal  PrincipalFacts
	AuthAge    time.Duration
	UnitLabels []string
	Collection string
}

// Producer gives one group of facts. Each group has one producer, with a
// conformance suite.
type Producer interface {
	// Group returns the group that the producer fills.
	Group() Group
	// Produce fills the group of the producer in f, for the unit of c at
	// index unit. It reads no other group of f. It returns an error when
	// it cannot give the facts. The caller then sets the group to its zero
	// value with available false for a group with HasAvailable, and never
	// keeps a partial value. For a group with no flag, the evaluation
	// fails with ErrUnavailable. Produce returns ctx.Err() when ctx ends.
	Produce(ctx context.Context, c Change, unit int, f *Facts) error
}
