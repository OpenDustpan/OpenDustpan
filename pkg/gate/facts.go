package gate

import (
	"encoding/json"
	"fmt"
	"os"

	cedar "github.com/cedar-policy/cedar-go"
	"github.com/cedar-policy/cedar-go/types"
)

// Subject names the unit of a decision.
type Subject struct {
	Tool       string `json:"tool"`
	Namespace  string `json:"namespace"`
	Collection string `json:"collection"`
	// Name is the name of the unit in its namespace, for example "prod/net".
	Name string `json:"name"`
}

// Ref returns the unit reference "<tool>:<namespace>/<name>".
func (s Subject) Ref() string { return s.Tool + ":" + s.Namespace + "/" + s.Name }

// Chain returns the scope chain of s, widest first.
func (s Subject) Chain() []Scope {
	return []Scope{
		{Level: LevelInstallation},
		{Level: LevelNamespace, Name: s.Namespace},
		{Level: LevelCollection, Name: s.Namespace + "/" + s.Collection},
		{Level: LevelUnit, Name: s.Namespace + "/" + s.Name},
	}
}

// Principal is the person or the identity that acts.
type Principal struct {
	ID     string   `json:"id"`
	Groups []string `json:"groups,omitempty"`
}

// Facts is the fact document v0 of PointChangePlanned. Values are computed
// before the decision point. A gate reads them and never computes them.
type Facts struct {
	Operation string     `json:"operation"`
	Unit      UnitFact   `json:"unit"`
	Plan      PlanCounts `json:"plan"`
	Cost      CostFact   `json:"cost"`
	Change    ChangeFact `json:"change"`
}

// UnitFact holds the facts of the unit. Labels hold "key=value" strings:
// a Cedar record cannot have keys that the schema does not name.
type UnitFact struct {
	Collection string   `json:"collection"`
	Labels     []string `json:"labels"`
}

// PlanCounts holds the counts of the plan summary.
type PlanCounts struct {
	Create  int64 `json:"create"`
	Update  int64 `json:"update"`
	Delete  int64 `json:"delete"`
	Replace int64 `json:"replace"`
}

// CostFact holds the cost fact. DeltaMonthly is in cents. When Available is
// false, DeltaMonthly is 0.
type CostFact struct {
	DeltaMonthly int64 `json:"deltaMonthly"`
	Available    bool  `json:"available"`
}

// ChangeFact holds the paths of the files that the change touches, and the
// keys of the inputs that it sets.
type ChangeFact struct {
	Paths     []string `json:"paths"`
	InputKeys []string `json:"inputKeys"`
}

// context converts the facts to a Cedar record through the JSON form of
// Cedar values. Set order is not significant in Cedar.
func contextRecord(doc any) (types.Record, error) {
	raw, err := json.Marshal(doc)
	if err != nil {
		return types.Record{}, fmt.Errorf("marshal facts: %w", err)
	}

	var rec types.Record

	err = rec.UnmarshalJSON(raw)
	if err != nil {
		return types.Record{}, fmt.Errorf("facts as Cedar record: %w", err)
	}

	return rec, nil
}

// Entity types and actions of the spike.
const (
	typeUser   = "User"
	typeGroup  = "Group"
	typeUnit   = "Unit"
	typeAction = "Action"
)

// entities builds the principal with its groups as parents, and the unit.
func entities(sub Subject, who Principal) types.EntityMap {
	parents := make([]types.EntityUID, 0, len(who.Groups))
	em := types.EntityMap{}

	for _, g := range who.Groups {
		uid := types.NewEntityUID(typeGroup, types.String(g))
		parents = append(parents, uid)
		em[uid] = types.Entity{UID: uid}
	}

	user := types.NewEntityUID(typeUser, types.String(who.ID))
	em[user] = types.Entity{UID: user, Parents: types.NewEntityUIDSet(parents...)}

	unit := types.NewEntityUID(typeUnit, types.String(sub.Ref()))
	em[unit] = types.Entity{UID: unit, Attributes: types.NewRecord(types.RecordMap{
		"tool":       types.String(sub.Tool),
		"namespace":  types.String(sub.Namespace),
		"collection": types.String(sub.Collection),
	})}

	return em
}

func request(sub Subject, who Principal, action string, ctx types.Record) cedar.Request {
	return cedar.Request{
		Principal: types.NewEntityUID(typeUser, types.String(who.ID)),
		Action:    types.NewEntityUID(typeAction, types.String(action)),
		Resource:  types.NewEntityUID(typeUnit, types.String(sub.Ref())),
		Context:   ctx,
	}
}

// CostFile is the JSON form of a cost that a client pushes for a unit.
type CostFile struct {
	Unit string `json:"unit"`
	// Currency is the currency of the amount; the spike reads only USD.
	Currency string `json:"currency"`
	// DeltaMonthlyCents is the change in monthly cost, in cents.
	DeltaMonthlyCents int64 `json:"deltaMonthlyCents"`
}

// ReadCostFile reads a cost fact from a JSON file.
func ReadCostFile(path string) (CostFile, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // path comes from the caller of the spike harness
	if err != nil {
		return CostFile{}, fmt.Errorf("read cost file: %w", err)
	}

	var cf CostFile

	err = json.Unmarshal(raw, &cf)
	if err != nil {
		return CostFile{}, fmt.Errorf("parse cost file: %w", err)
	}

	if cf.Unit == "" || cf.Currency != "USD" {
		return CostFile{}, fmt.Errorf("%w: cost file needs a unit and currency USD", ErrInvalid)
	}

	return cf, nil
}

// CostFacts stores the cost facts that clients pushed, by unit reference.
type CostFacts struct{ byUnit map[string]CostFile }

// NewCostFacts returns an empty store.
func NewCostFacts() *CostFacts { return &CostFacts{byUnit: map[string]CostFile{}} }

// Push stores cf and replaces the earlier cost of the same unit.
func (c *CostFacts) Push(cf CostFile) { c.byUnit[cf.Unit] = cf }

// For returns the cost fact of sub. A unit with no pushed cost has
// Available false.
func (c *CostFacts) For(sub Subject) CostFact {
	cf, ok := c.byUnit[sub.Ref()]
	if !ok {
		return CostFact{}
	}

	return CostFact{DeltaMonthly: cf.DeltaMonthlyCents, Available: true}
}

func cedarEmptyContext() types.Record { return types.NewRecord(types.RecordMap{}) }
