package gatestore

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/gate"
)

// The JSON bodies of the stored objects. The domain types carry no JSON
// names for these fields, and a stored body must not change when a domain
// field moves, so each body is a type of this package.

type gateBody struct {
	DecisionPoint string   `json:"decisionPoint"`
	Condition     string   `json:"condition"`
	Unless        string   `json:"unless"`
	Outcome       string   `json:"outcome"`
	Message       string   `json:"message"`
	Mode          string   `json:"mode"`
	OnError       string   `json:"onError"`
	Final         bool     `json:"final"`
	CheckedEngine string   `json:"checkedEngine"`
	CheckedSchema string   `json:"checkedSchema"`
	Reads         []string `json:"reads"`
}

type accessLimitBody struct {
	Policy string `json:"policy"`
}

type approvalRuleBody struct {
	Approvers        gate.Approvers `json:"approvers"`
	Count            int            `json:"count"`
	AuthorMayApprove bool           `json:"authorMayApprove"`
	FreshAuth        gate.FreshAuth `json:"freshAuthSeconds"`
	TimeoutNanos     time.Duration  `json:"timeoutNanos"`
}

type ownershipRuleBody struct {
	Mode      string         `json:"mode"`
	Paths     []string       `json:"paths"`
	When      string         `json:"when"`
	Approvers gate.Approvers `json:"approvers"`
	Count     int            `json:"count"`
	FreshAuth gate.FreshAuth `json:"freshAuthSeconds"`
	Reads     []string       `json:"reads"`
}

type pathSetBody struct {
	Globs []string `json:"globs"`
}

type approvalDefaultBody struct {
	Count     int            `json:"count"`
	FreshAuth gate.FreshAuth `json:"freshAuthSeconds"`
	Approvers gate.Approvers `json:"approvers"`
	Unless    string         `json:"unless"`
	Reads     []string       `json:"reads"`
}

type carryOverBody struct {
	Value string `json:"value"`
}

// codec converts one kind of object to its row and back.
type codec[T gate.Object] struct {
	kind  string
	limit int
	// encode returns the body of obj and the references that obj makes.
	encode func(obj T) (string, []Ref, error)
	// decode returns the object that a row holds in scope.
	decode func(scope gate.Scope, row Row) (T, error)
}

// codecOf returns the codec of the kind T.
func codecOf[T gate.Object]() codec[T] {
	var zero T

	var c any

	switch any(zero).(type) {
	case gate.Gate:
		c = codec[gate.Gate]{
			kind: KindGate, limit: gate.MaxGatesPerScope, encode: encodeGate, decode: decodeGate,
		}
	case gate.AccessLimit:
		c = codec[gate.AccessLimit]{
			kind: KindAccessLimit, limit: gate.MaxAccessLimitsPerScope, encode: encodeAccessLimit,
			decode: decodeAccessLimit,
		}
	case gate.ApprovalRule:
		c = codec[gate.ApprovalRule]{
			kind: KindApprovalRule, limit: gate.MaxApprovalRulesPerScope, encode: encodeApprovalRule,
			decode: decodeApprovalRule,
		}
	case gate.OwnershipRule:
		c = codec[gate.OwnershipRule]{
			kind: KindOwnershipRule, limit: gate.MaxOwnershipRulesPerScope, encode: encodeOwnershipRule,
			decode: decodeOwnershipRule,
		}
	case gate.PathSet:
		c = codec[gate.PathSet]{
			kind: KindPathSet, limit: gate.MaxPathSetsPerScope, encode: encodePathSet, decode: decodePathSet,
		}
	default:
	}

	typed, ok := c.(codec[T])
	if !ok {
		panic(fmt.Sprintf("gatestore: no codec for %T", zero))
	}

	return typed
}

func marshal(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("encode body: %w", err)
	}

	return string(b), nil
}

func unmarshal(row Row, v any) error {
	if err := json.Unmarshal([]byte(row.Body), v); err != nil {
		return fmt.Errorf("decode %s %q: %w", row.Kind, row.ID, err)
	}

	return nil
}

func idStrings(ids []gate.ID) []string {
	if ids == nil {
		return nil
	}

	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = string(id)
	}

	return out
}

func idsOf(ss []string) []gate.ID {
	if ss == nil {
		return nil
	}

	out := make([]gate.ID, len(ss))
	for i, s := range ss {
		out[i] = gate.ID(s)
	}

	return out
}

// pathSetRefs returns one reference to a path set for each ID of reads.
func pathSetRefs(reads []gate.ID) []Ref {
	var refs []Ref

	for _, id := range reads {
		r := Ref{Kind: RefPathSet, ID: string(id)}
		if !slices.Contains(refs, r) {
			refs = append(refs, r)
		}
	}

	return refs
}

func encodeGate(g gate.Gate) (string, []Ref, error) {
	body, err := marshal(gateBody{
		DecisionPoint: string(g.DecisionPoint), Condition: g.Condition, Unless: g.Unless,
		Outcome: string(g.Outcome), Message: g.Message, Mode: string(g.Mode), OnError: string(g.OnError),
		Final: g.Final, CheckedEngine: g.CheckedWith.Engine, CheckedSchema: textOf(g.CheckedWith.Schema),
		Reads: idStrings(g.Reads),
	})
	if err != nil {
		return "", nil, err
	}

	refs := pathSetRefs(g.Reads)
	if rule, ok := g.Outcome.Rule(); ok {
		refs = append(refs, Ref{Kind: RefApprovalRule, ID: string(rule)})
	}

	return body, refs, nil
}

func textOf(v gate.SchemaVersion) string {
	b, err := v.MarshalText()
	if err != nil {
		return ""
	}

	return string(b)
}

func decodeGate(scope gate.Scope, row Row) (gate.Gate, error) {
	var b gateBody
	if err := unmarshal(row, &b); err != nil {
		return gate.Gate{}, err
	}

	var schema gate.SchemaVersion
	if err := schema.UnmarshalText([]byte(b.CheckedSchema)); err != nil {
		return gate.Gate{}, fmt.Errorf("decode gate %q: %w", row.ID, err)
	}

	return gate.Gate{
		Scope: scope, ID: gate.ID(row.ID), Version: row.Version,
		DecisionPoint: gate.DecisionPoint(b.DecisionPoint), Condition: b.Condition, Unless: b.Unless,
		Outcome: gate.Outcome(b.Outcome), Message: b.Message, Mode: gate.Mode(b.Mode),
		OnError: gate.OnError(b.OnError), Final: b.Final,
		CheckedWith: gate.CheckedWith{Engine: b.CheckedEngine, Schema: schema},
		State:       "", Reads: idsOf(b.Reads),
	}, nil
}

func encodeAccessLimit(l gate.AccessLimit) (string, []Ref, error) {
	body, err := marshal(accessLimitBody{Policy: l.Policy})

	return body, nil, err
}

func decodeAccessLimit(scope gate.Scope, row Row) (gate.AccessLimit, error) {
	var b accessLimitBody
	if err := unmarshal(row, &b); err != nil {
		return gate.AccessLimit{}, err
	}

	return gate.AccessLimit{Scope: scope, ID: gate.ID(row.ID), Version: row.Version, Policy: b.Policy}, nil
}

func encodeApprovalRule(r gate.ApprovalRule) (string, []Ref, error) {
	body, err := marshal(approvalRuleBody{
		Approvers: r.Approvers, Count: r.Count, AuthorMayApprove: r.AuthorMayApprove, FreshAuth: r.FreshAuth,
		TimeoutNanos: r.Timeout,
	})

	return body, nil, err
}

func decodeApprovalRule(scope gate.Scope, row Row) (gate.ApprovalRule, error) {
	var b approvalRuleBody
	if err := unmarshal(row, &b); err != nil {
		return gate.ApprovalRule{}, err
	}

	return gate.ApprovalRule{
		Scope: scope, ID: gate.ID(row.ID), Version: row.Version, Approvers: b.Approvers, Count: b.Count,
		AuthorMayApprove: b.AuthorMayApprove, FreshAuth: b.FreshAuth, Timeout: b.TimeoutNanos,
	}, nil
}

func encodeOwnershipRule(r gate.OwnershipRule) (string, []Ref, error) {
	body, err := marshal(ownershipRuleBody{
		Mode: string(r.Mode), Paths: r.Paths, When: r.When, Approvers: r.Approvers, Count: r.Count,
		FreshAuth: r.FreshAuth, Reads: idStrings(r.Reads),
	})
	if err != nil {
		return "", nil, err
	}

	return body, pathSetRefs(r.Reads), nil
}

func decodeOwnershipRule(scope gate.Scope, row Row) (gate.OwnershipRule, error) {
	var b ownershipRuleBody
	if err := unmarshal(row, &b); err != nil {
		return gate.OwnershipRule{}, err
	}

	return gate.OwnershipRule{
		Scope: scope, ID: gate.ID(row.ID), Version: row.Version, Mode: gate.Mode(b.Mode), Paths: b.Paths,
		When: b.When, Approvers: b.Approvers, Count: b.Count, FreshAuth: b.FreshAuth, Reads: idsOf(b.Reads),
	}, nil
}

func encodePathSet(p gate.PathSet) (string, []Ref, error) {
	body, err := marshal(pathSetBody{Globs: p.Globs})

	return body, nil, err
}

func decodePathSet(scope gate.Scope, row Row) (gate.PathSet, error) {
	var b pathSetBody
	if err := unmarshal(row, &b); err != nil {
		return gate.PathSet{}, err
	}

	return gate.PathSet{Scope: scope, ID: gate.ID(row.ID), Version: row.Version, Globs: b.Globs}, nil
}

func encodeApprovalDefault(d gate.ApprovalDefault) (string, []Ref, error) {
	body, err := marshal(approvalDefaultBody{
		Count: d.Count, FreshAuth: d.FreshAuth, Approvers: d.Approvers, Unless: d.Unless, Reads: idStrings(d.Reads),
	})
	if err != nil {
		return "", nil, err
	}

	return body, pathSetRefs(d.Reads), nil
}

func decodeApprovalDefault(scope gate.Scope, row Row) (gate.ApprovalDefault, error) {
	var b approvalDefaultBody
	if err := unmarshal(row, &b); err != nil {
		return gate.ApprovalDefault{}, err
	}

	return gate.ApprovalDefault{
		Scope: scope, Version: row.Version, Count: b.Count, FreshAuth: b.FreshAuth, Approvers: b.Approvers,
		Unless: b.Unless, Reads: idsOf(b.Reads),
	}, nil
}

func encodeCarryOver(value gate.CarryOver) (string, error) {
	return marshal(carryOverBody{Value: string(value)})
}

func decodeCarryOver(scope gate.Scope, row Row) (gate.CarryOverSetting, error) {
	var b carryOverBody
	if err := unmarshal(row, &b); err != nil {
		return gate.CarryOverSetting{}, err
	}

	return gate.CarryOverSetting{Scope: scope, Version: row.Version, Value: gate.CarryOver(b.Value)}, nil
}
