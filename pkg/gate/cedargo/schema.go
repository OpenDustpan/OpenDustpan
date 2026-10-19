package cedargo

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/cedar-policy/cedar-go/x/exp/schema"
	"github.com/cedar-policy/cedar-go/x/exp/schema/validate"

	"opendustpan.org/opendustpan/pkg/gate"
)

// The Cedar names of the engine. Each one is in the namespace OpenDustpan,
// so a policy writes OpenDustpan::Group::"sre".
const (
	namespaceName = "OpenDustpan"
	typePrincipal = namespaceName + "::Principal"
	typeGroup     = namespaceName + "::Group"
	typeUnit      = namespaceName + "::Unit"
	typeObject    = namespaceName + "::Object"
	typeAction    = namespaceName + "::Action"
)

// The schema versions that this engine knows. A new field is a new minor
// version, so a later version of this package knows more than one.
const (
	knownMajor = 1
	knownMinor = 0
)

// maxSchemas bounds the schemas that the engine keeps. A schema is one for
// each decision point, and one for each set of path sets.
const maxSchemas = 64

// groupFields holds the Cedar type of each field of each group of facts, in
// Cedar schema text. The group paths is not in this table, because its type
// depends on the path sets of the scope.
var groupFields = map[gate.Group]string{ //nolint:gochecknoglobals // A constant table.
	gate.GroupPrincipal: `{"kind": String, "credential": String}`,
	gate.GroupSource: `{"kind": String, "ref": String, "protected": Bool, "commit": String, ` +
		`"upstreamUnit": String}`,
	gate.GroupUnit:      `{"tool": String, "collection": String, "labels": Set<String>}`,
	gate.GroupOperation: `{"kind": String}`,
	gate.GroupChange: `{"inputKeys": Set<String>, "secretKeys": Set<String>, ` +
		`"truncatedKeys": Set<String>, "paths": Set<String>}`,
	gate.GroupPlan: `{"available": Bool, "add": Long, "change": Long, "replace": Long, "destroy": Long, ` +
		`"resourceTypes": Set<String>}`,
	gate.GroupCost:   `{"available": Bool, "deltaMonthly": Long, "totalMonthly": Long, "currency": String}`,
	gate.GroupBudget: `{"available": Bool, "level": String, "percentUsed": Long}`,
	gate.GroupDrift:  `{"available": Bool, "resources": Long, "ageSeconds": Long}`,
	gate.GroupResourcePolicy: `{"available": Bool, "mandatoryFailed": Long, "advisoryFailed": Long, ` +
		`"unreadable": Long}`,
	gate.GroupExternalChecks: `{"passed": Set<String>, "failed": Set<String>, "pending": Set<String>, ` +
		`"timedOut": Set<String>}`,
	gate.GroupQuota:      `{"available": Bool, "maxPercentUsed": Long, "exceeded": Set<String>}`,
	gate.GroupDependents: `{"available": Bool, "count": Long, "units": Set<String>}`,
	gate.GroupDocument:   `{"truncated": Set<String>}`,
	gate.GroupState:      `{"available": Bool, "resources": Long, "removed": Set<String>}`,
	gate.GroupProfile: `{"name": String, "access": String, "refProtected": Bool, "jobOpen": Bool, ` +
		`"routingChange": Bool, "importsAdded": Set<String>}`,
}

// accessContext is the Cedar type of the context of an access limit
// request, in the access schema.
const accessContext = `{"principal": {"kind": String, "credential": String}, "authAgeSeconds": Long, ` +
	`"unit": {"collection": String, "labels": Set<String>}}`

// typedSchema is a schema of the port, resolved for the validator.
type typedSchema struct {
	validator *validate.Validator
	// access is true for the access schema.
	access bool
	// point is the decision point of a fact schema.
	point gate.DecisionPoint
}

// schemaCache keeps the resolved schemas by their key.
type schemaCache struct {
	mu      sync.Mutex
	schemas map[string]*typedSchema
}

func newSchemaCache() *schemaCache {
	return &schemaCache{mu: sync.Mutex{}, schemas: map[string]*typedSchema{}}
}

// get returns the resolved form of s, and builds it when the cache has not.
// It returns gate.ErrInvalid for a schema that the engine cannot use.
func (c *schemaCache) get(s gate.Schema, accessActions []string) (*typedSchema, error) {
	if s.Version.Major != knownMajor || s.Version.Minor != knownMinor {
		return nil, fmt.Errorf("%w: the engine does not know the schema version %s", gate.ErrInvalid, s.Version)
	}

	sets := slices.Clone(s.PathSets)
	slices.Sort(sets)

	sets = slices.Compact(sets)

	key := s.Version.String() + "|" + joinIDs(sets)

	c.mu.Lock()
	defer c.mu.Unlock()

	if ts, ok := c.schemas[key]; ok {
		return ts, nil
	}

	built, err := schemaText(s.Version, sets, accessActions)
	if err != nil {
		return nil, err
	}

	var parsed schema.Schema

	if err = parsed.UnmarshalCedar([]byte(built.text)); err != nil {
		return nil, fmt.Errorf("%w: schema %s: %w", gate.ErrInvalid, s.Version, err)
	}

	resolved, err := parsed.Resolve()
	if err != nil {
		return nil, fmt.Errorf("%w: schema %s: %w", gate.ErrInvalid, s.Version, err)
	}

	if len(c.schemas) >= maxSchemas {
		clear(c.schemas)
	}

	ts := &typedSchema{validator: validate.New(resolved), access: built.access, point: built.point}
	c.schemas[key] = ts

	return ts, nil
}

func joinIDs(ids []gate.ID) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = string(id)
	}

	return strings.Join(parts, ",")
}

// schemaSource is the text of a Cedar schema, and what the schema is for.
type schemaSource struct {
	text string
	// access is true for the access schema.
	access bool
	// point is the decision point of a fact schema.
	point gate.DecisionPoint
}

// schemaText returns the Cedar schema of version, with one boolean for each
// path set in paths.touches and paths.only. It returns gate.ErrInvalid for
// a subject that is no decision point and is not the access schema, and for
// an access schema when the engine holds no access action.
func schemaText(version gate.SchemaVersion, pathSets []gate.ID, accessActions []string) (schemaSource, error) {
	if version.Subject == gate.SchemaAccess {
		if len(accessActions) == 0 {
			return schemaSource{}, fmt.Errorf(
				"%w: the engine holds no access action for the access schema", gate.ErrInvalid,
			)
		}

		return schemaSource{text: accessSchemaText(accessActions), access: true, point: ""}, nil
	}

	point, err := gate.ParseDecisionPoint(version.Subject)
	if err != nil {
		return schemaSource{}, fmt.Errorf("schema %s: %w", version, err)
	}

	var ctx strings.Builder

	ctx.WriteString("{")

	for i, g := range gate.GroupsOf(point) {
		if i > 0 {
			ctx.WriteString(", ")
		}

		ctx.WriteString(strconv.Quote(string(g)))
		ctx.WriteString(": ")

		if g == gate.GroupPaths {
			ctx.WriteString(pathsType(pathSets))

			continue
		}

		ctx.WriteString(groupFields[g])
	}

	ctx.WriteString("}")

	text := "namespace " + namespaceName + " {\n" +
		"entity Group;\nentity Principal in [Group];\nentity Unit;\n" +
		"action " + strconv.Quote(string(point)) +
		" appliesTo { principal: Principal, resource: Unit, context: " + ctx.String() + " };\n}\n"

	return schemaSource{text: text, access: false, point: point}, nil
}

// pathsType returns the Cedar type of the group paths.
func pathsType(pathSets []gate.ID) string {
	var record strings.Builder

	record.WriteString("{")

	for i, id := range pathSets {
		if i > 0 {
			record.WriteString(", ")
		}

		record.WriteString(strconv.Quote(string(id)) + ": Bool")
	}

	record.WriteString("}")

	return `{"available": Bool, "touches": ` + record.String() + `, "only": ` + record.String() + `}`
}

// accessSchemaText returns the Cedar schema of the access schema, with one
// action for each name.
func accessSchemaText(actions []string) string {
	var b strings.Builder

	b.WriteString("namespace " + namespaceName + " {\nentity Group;\nentity Principal in [Group];\nentity Object;\n")

	for _, a := range actions {
		b.WriteString("action " + strconv.Quote(a) + " appliesTo { principal: Principal, resource: Object, context: " +
			accessContext + " };\n")
	}

	b.WriteString("}\n")

	return b.String()
}
