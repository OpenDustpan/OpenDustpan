package errcode

import (
	"regexp"
	"slices"
	"strings"
)

// grammar is the form of a code: two or more lowercase segments that dots
// join, with hyphens between the words of a segment.
var grammar = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*(\.[a-z][a-z0-9]*(-[a-z0-9]+)*)+$`)

// The length limits of a code.
const (
	minCodeLength = 3
	maxCodeLength = 64
)

// Valid reports whether code has the form of a code. It does not look in the
// catalog.
func Valid(code string) bool {
	return len(code) >= minCodeLength && len(code) <= maxCodeLength && grammar.MatchString(code)
}

// Kind is the kind of an error argument. A client formats each kind in its
// own way.
type Kind int

// The kinds of an error argument.
const (
	// KindIdentifier is a name, for example of a stack or of a field. A client
	// shows it as it is and never translates it.
	KindIdentifier Kind = iota + 1
	// KindQuantity is an integer, for example a count or a limit. A client
	// formats it as a number of its locale.
	KindQuantity
	// KindTime is an instant. The answer holds it as an RFC 3339 string in
	// UTC. A client formats it as a date and a time of its locale.
	KindTime
)

// Arg declares one argument of a code: its camelCase name and its kind.
type Arg struct {
	Name string
	Kind Kind
}

// Spec declares one code: its meaning and the arguments that each answer
// with the code carries. A code is never removed and never changes its
// meaning or its arguments.
type Spec struct {
	Code    string
	Meaning string
	Args    []Arg
}

// Catalog returns every code, in the order of the codes. It is the one place
// that declares a code. Each adapter takes its codes from here.
func Catalog() []Spec {
	lists := areas()

	parts := make([][]Spec, 0, len(lists))
	for _, area := range lists {
		parts = append(parts, area())
	}

	specs := slices.Concat(parts...)

	slices.SortFunc(specs, func(a, b Spec) int { return strings.Compare(a.Code, b.Code) })

	return specs
}

// areas returns the spec list of each area, one area on each line. A new
// area gets its own list in a file catalog_<area>.go and its own line here,
// so that two changes to two areas touch different lists.
func areas() []func() []Spec {
	return []func() []Spec{
		accessSpecs,
		auditChainSpecs,
		auditSpecs,
		authSpecs,
		backupSpecs,
		collectionSpecs,
		contextSpecs,
		driftSpecs,
		gateSpecs,
		gitHostSpecs,
		grantSpecs,
		instanceSpecs,
		issuerSpecs,
		jobLogSpecs,
		jobSpecs,
		journalSpecs,
		localUserSpecs,
		lockSpecs,
		maintenanceSpecs,
		mfaSpecs,
		namespaceSpecs,
		overrideSpecs,
		passwordSpecs,
		placementSpecs,
		preferencesSpecs,
		principalSpecs,
		profileSpecs,
		providerSpecs,
		registrySpecs,
		requestSpecs,
		restoreSpecs,
		runSpecs,
		selfServiceSpecs,
		runnerSpecs,
		schemaSpecs,
		secretSpecs,
		serverSpecs,
		settingsSpecs,
		sessionSpecs,
		signInSpecs,
		sinkSpecs,
		sourceSpecs,
		stackSpecs,
		stateVersionSpecs,
		storeCheckSpecs,
		storeSpecs,
		tokenSpecs,
		triggerSpecs,
		unitSettingsSpecs,
		trustRuleSpecs,
		unitSpecs,
		updateSpecs,
		upgradeSpecs,
		viewSpecs,
		workerSpecs,
		workspaceSpecs,
	}
}

// Lookup returns the spec of code, and false when the catalog does not have
// it.
func Lookup(code string) (Spec, bool) {
	specs := Catalog()

	i := slices.IndexFunc(specs, func(s Spec) bool { return s.Code == code })
	if i < 0 {
		return Spec{Code: "", Meaning: "", Args: nil}, false
	}

	return specs[i], true
}
