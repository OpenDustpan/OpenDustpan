package gate

import (
	"fmt"
	"strings"

	"opendustpan.org/opendustpan/internal/ident"
)

// maxIDLength is the longest ID: one first character and 62 more.
const maxIDLength = 63

// builtinPrefix starts the ID of each gate that the server ships. Only the
// server makes such an ID.
const builtinPrefix = "builtin/"

// The approval rule IDs that name a requirement of another source, never
// an approval rule.
const (
	reservedDefault = "default"
	reservedOwners  = "owners"
)

// ID is the ID of a gate, an access limit, an approval rule, an ownership
// rule or a path set, unique in its scope. It matches
// [a-z0-9][a-z0-9-]{0,62}. A gate that the server ships has an ID of the
// form builtin/<ID>.
type ID string

// ParseID returns s when it matches the ID grammar, or ErrInvalid. It
// refuses the prefix "builtin/", so a request can never name a gate ID
// that only the server makes.
func ParseID(s string) (ID, error) {
	if !validID(s) {
		return "", fmt.Errorf("%w: ID %q must match [a-z0-9][a-z0-9-]{0,62}", ErrInvalid, s)
	}

	return ID(s), nil
}

// ParseRuleID returns s as the ID of an approval rule, or ErrInvalid. It
// also refuses "default" and "owners", which name the requirements of
// approval defaults and ownership rules.
func ParseRuleID(s string) (ID, error) {
	id, err := ParseID(s)
	if err != nil {
		return "", err
	}

	if s == reservedDefault || s == reservedOwners {
		return "", fmt.Errorf("%w: the approval rule ID %q is reserved", ErrInvalid, s)
	}

	return id, nil
}

// ParseGateID returns s as a gate ID, or ErrInvalid. It takes an ID of the
// grammar and an ID of a built-in gate. Use it to read an ID that the
// server stored. Use ParseID for an ID that a request names.
func ParseGateID(s string) (ID, error) {
	if name, ok := strings.CutPrefix(s, builtinPrefix); ok {
		if !validID(name) {
			return "", fmt.Errorf("%w: built-in gate ID %q", ErrInvalid, s)
		}

		return ID(s), nil
	}

	return ParseID(s)
}

// BuiltinID returns the gate ID builtin/<name>. name must already pass
// ParseID.
func BuiltinID(name ID) ID {
	return ID(builtinPrefix + string(name))
}

// IsBuiltin reports whether id is the ID of a gate that the server ships.
func (id ID) IsBuiltin() bool {
	return strings.HasPrefix(string(id), builtinPrefix)
}

// validID reports whether s matches [a-z0-9][a-z0-9-]{0,62}.
func validID(s string) bool {
	if s == "" || len(s) > maxIDLength || s[0] == '-' {
		return false
	}

	for _, c := range []byte(s) {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}

	return true
}

// PolicyID is the Cedar policy ID of a gate or an access limit: the scope
// key, "/", and the ID. Two scopes can each hold the ID, so the decision
// record names each gate by its policy ID.
type PolicyID struct {
	Scope Scope
	ID    ID
}

// String returns <scope key>/<ID>.
func (p PolicyID) String() string {
	return p.Scope.Key() + "/" + string(p.ID)
}

// ParsePolicyID parses s as <scope key>/<ID>. A built-in gate ID is valid
// only at the installation scope. It returns ErrInvalid for any other
// form.
func ParsePolicyID(s string) (PolicyID, error) {
	scope, rest, err := cutScopeKey(s)
	if err != nil {
		return PolicyID{}, err
	}

	rest, ok := strings.CutPrefix(rest, "/")
	if !ok {
		return PolicyID{}, fmt.Errorf("%w: policy ID %q has no gate ID", ErrInvalid, s)
	}

	id, err := ParseGateID(rest)
	if err != nil {
		return PolicyID{}, fmt.Errorf("policy ID %q: %w", s, err)
	}

	if id.IsBuiltin() && scope.Level() != LevelInstallation {
		return PolicyID{}, fmt.Errorf("%w: policy ID %q: a built-in gate is at the installation scope", ErrInvalid, s)
	}

	return PolicyID{Scope: scope, ID: id}, nil
}

// RequirementName names one approval requirement of a change: the ID of an
// approval rule, "default", "default/<scope key>" or "owners/<ID>".
type RequirementName string

// The prefixes of the requirement names that come from no approval rule.
const (
	defaultScopedPrefix = "default/"
	ownersPrefix        = "owners/"
)

// RuleRequirement returns the requirement name of the approval rule id.
func RuleRequirement(id ID) RequirementName {
	return RequirementName(id)
}

// DefaultRequirement returns "default", the requirement of the approval
// defaults that name no approvers, merged into one.
func DefaultRequirement() RequirementName {
	return reservedDefault
}

// ScopedDefaultRequirement returns "default/<scope key>", the requirement
// of the approval default of scope that names approvers.
func ScopedDefaultRequirement(scope Scope) RequirementName {
	return RequirementName(defaultScopedPrefix + scope.Key())
}

// OwnersRequirement returns "owners/<id>", the requirement of the
// ownership rule id.
func OwnersRequirement(id ID) RequirementName {
	return RequirementName(ownersPrefix + string(id))
}

// ParseRequirementName returns s when it is a requirement name, or
// ErrInvalid.
func ParseRequirementName(s string) (RequirementName, error) {
	if s == reservedDefault {
		return DefaultRequirement(), nil
	}

	if key, ok := strings.CutPrefix(s, defaultScopedPrefix); ok {
		scope, err := ParseScopeKey(key)
		if err != nil {
			return "", fmt.Errorf("requirement %q: %w", s, err)
		}

		if !scope.HoldsApprovalDefault() {
			return "", fmt.Errorf("%w: requirement %q: the scope holds no approval default", ErrInvalid, s)
		}

		return ScopedDefaultRequirement(scope), nil
	}

	if id, ok := strings.CutPrefix(s, ownersPrefix); ok {
		parsed, err := ParseID(id)
		if err != nil {
			return "", fmt.Errorf("requirement %q: %w", s, err)
		}

		return OwnersRequirement(parsed), nil
	}

	id, err := ParseRuleID(s)
	if err != nil {
		return "", fmt.Errorf("requirement %q: %w", s, err)
	}

	return RuleRequirement(id), nil
}

// recordPrefix starts every decision record ID.
const recordPrefix = "gdr-"

// recordIDLength is the length of the random part of a record ID.
const recordIDLength = 20

// RecordID is the ID of a decision record: "gdr-" and 20 lowercase letters
// and digits.
type RecordID string

// NewRecordID returns a new random record ID.
func NewRecordID() RecordID {
	return RecordID(ident.NewID(recordPrefix))
}

// ParseRecordID returns s when it is a record ID, or ErrInvalid.
func ParseRecordID(s string) (RecordID, error) {
	rest, ok := strings.CutPrefix(s, recordPrefix)
	if !ok || len(rest) != recordIDLength {
		return "", fmt.Errorf("%w: record ID %q", ErrInvalid, s)
	}

	for _, c := range []byte(rest) {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return "", fmt.Errorf("%w: record ID %q", ErrInvalid, s)
		}
	}

	return RecordID(s), nil
}
