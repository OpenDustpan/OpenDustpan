package drift

import (
	"fmt"
	"slices"
	"strings"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// MaxCronBytes bounds a custom cron expression.
const MaxCronBytes = 256

// cronFields is the number of fields of a custom cron expression: minute,
// hour, day of month, month and day of week.
const cronFields = 5

// maxScopeNameLength bounds the name of a collection and the ID of a unit in
// a scope. It is the bound of an ID in the unit graph.
const maxScopeNameLength = 100

// Preset is a frequency from the fixed list.
type Preset string

// The frequency presets.
const (
	PresetHourly      Preset = "hourly"
	PresetEvery6Hours Preset = "every-6-hours"
	PresetDaily       Preset = "daily"
	PresetWeekly      Preset = "weekly"
)

// Presets returns the frequency presets, from the most to the least frequent.
// Each call returns a new slice.
func Presets() []Preset {
	return []Preset{PresetHourly, PresetEvery6Hours, PresetDaily, PresetWeekly}
}

// Frequency says how often a drift check starts. Exactly one of Preset and
// Cron is set. Cron is a custom expression of five fields that runs in UTC.
type Frequency struct {
	Preset Preset
	Cron   string
}

// Validate reports whether f is a valid frequency. It returns an error that
// wraps ErrInvalid. For a custom expression it checks the form only: five
// fields of digits and the characters "*", "/", "," and "-". The schedule
// slice adds the parser and the minimum interval of one hour.
func (f Frequency) Validate() error {
	switch {
	case f.Preset != "" && f.Cron != "":
		return fmt.Errorf("%w: a frequency has a preset or a cron expression, not both", ErrInvalid)
	case f.Preset != "":
		if !slices.Contains(Presets(), f.Preset) {
			return fmt.Errorf("%w: preset %q", ErrInvalid, f.Preset)
		}

		return nil
	case f.Cron != "":
		return validateCron(f.Cron)
	default:
		return fmt.Errorf("%w: a frequency needs a preset or a cron expression", ErrInvalid)
	}
}

// validateCron reports whether expr has the form of a custom expression.
func validateCron(expr string) error {
	if len(expr) > MaxCronBytes {
		return fmt.Errorf("%w: cron expression of %d bytes", ErrInvalid, len(expr))
	}

	if len(strings.Fields(expr)) != cronFields {
		return fmt.Errorf("%w: a cron expression has %d fields", ErrInvalid, cronFields)
	}

	for _, r := range expr {
		if (r < '0' || r > '9') && !strings.ContainsRune("*/,- ", r) {
			return fmt.Errorf("%w: cron expression has the character %q", ErrInvalid, r)
		}
	}

	return nil
}

// Policy is the drift schedule setting of one scope. The zero value is off:
// without a setting, no drift check starts on its own.
// A disabled policy may keep a frequency, so a team can switch checks off and
// on again.
type Policy struct {
	Frequency Frequency
	Enabled   bool
}

// Validate reports whether p is valid. An enabled policy needs a valid
// frequency. A disabled policy may have none. It returns an error that wraps
// ErrInvalid.
func (p Policy) Validate() error {
	empty := p.Frequency == Frequency{Preset: "", Cron: ""}
	if !p.Enabled && empty {
		return nil
	}

	return p.Frequency.Validate()
}

// ScopeKind is the kind of object that holds a policy.
type ScopeKind string

// The scope kinds, from the most to the least specific.
const (
	ScopeUnit       ScopeKind = "unit"
	ScopeCollection ScopeKind = "collection"
	ScopeNamespace  ScopeKind = "namespace"
)

// Scope names the object that holds a policy. Name is the stable unit ID for
// a unit and the collection name for a collection. It is empty for a
// namespace.
type Scope struct {
	Namespace namespace.Name
	Kind      ScopeKind
	Name      string
}

// Validate reports whether s names an object. It returns an error that wraps
// ErrInvalid.
func (s Scope) Validate() error {
	if _, err := namespace.ParseName(s.Namespace.String()); err != nil {
		return fmt.Errorf("%w: scope namespace: %w", ErrInvalid, err)
	}

	switch s.Kind {
	case ScopeNamespace:
		if s.Name != "" {
			return fmt.Errorf("%w: a namespace scope has no name", ErrInvalid)
		}
	case ScopeUnit, ScopeCollection:
		if s.Name == "" || len(s.Name) > maxScopeNameLength {
			return fmt.Errorf("%w: scope name length %d", ErrInvalid, len(s.Name))
		}

		if err := validateAddress(s.Name); err != nil {
			return fmt.Errorf("scope name: %w", err)
		}
	default:
		return fmt.Errorf("%w: scope kind %q", ErrInvalid, s.Kind)
	}

	return nil
}

// Resolve returns the policy that applies to a unit: the first one that is
// set, in the order unit, collection, namespace. A nil argument means that the
// scope has no setting. With none set, it returns the zero Policy, which is
// off. A set policy with Enabled false switches checks off for its scope and
// below, even when a wider scope switches them on.
func Resolve(unit, collection, ns *Policy) Policy {
	for _, p := range []*Policy{unit, collection, ns} {
		if p != nil {
			return *p
		}
	}

	return Policy{Frequency: Frequency{Preset: "", Cron: ""}, Enabled: false}
}
