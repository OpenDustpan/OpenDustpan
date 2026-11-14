package selfservice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// MaxOptions bounds the options of one input that the options route
// returns. A longer list ends with OptionSet.Truncated set (ForRoute). The
// check of an answer uses every option.
const MaxOptions = 1000

// Source gives the options of a choice from the outputs of a set of units:
// the units made from one blueprint of the namespace, or the units of one
// collection. Exactly one of Blueprint and Collection is set. Value names
// the output that gives the value of an option, and Label the output that
// gives its text, or nil to show the value.
type Source struct {
	Label      *OutputRef `json:"label,omitempty"`
	Value      OutputRef  `json:"value"`
	Blueprint  string     `json:"blueprint,omitempty"`
	Collection string     `json:"collection,omitempty"`
	Where      []Filter   `json:"where,omitempty"`
}

// OutputRef names a top-level output of a unit, and a value in it with a
// JSON pointer, or the whole output when Path is empty.
type OutputRef struct {
	Output string `json:"output"`
	Path   string `json:"path,omitempty"`
}

// Filter keeps a unit when its output equals a value: a fixed JSON scalar,
// or the answer of another input. A filter on an input makes the choice
// depend on that input.
type Filter struct {
	Equals Operand `json:"equals"`
	Output string  `json:"output"`
	Path   string  `json:"path,omitempty"`
}

// Ref returns the output of f.
func (f Filter) Ref() OutputRef {
	return OutputRef{Output: f.Output, Path: f.Path}
}

// Operand is the value of a filter: the answer of the input Input, or the
// fixed JSON scalar Value. Exactly one of the two is set.
type Operand struct {
	Input string      `json:"input,omitempty"`
	Value ScalarValue `json:"value,omitempty"`
}

// ScalarValue is the JSON of a fixed filter value. Its JSON is the JSON of
// json.RawMessage. The server accepts a string, a number or a boolean only:
// Filter.validate refuses the others. The type exists so that the description
// of the API can name the scalar form.
type ScalarValue json.RawMessage

// MarshalJSON writes the JSON of s, as json.RawMessage does.
func (s ScalarValue) MarshalJSON() ([]byte, error) {
	data, err := json.RawMessage(s).MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("marshal a scalar value: %w", err)
	}

	return data, nil
}

// UnmarshalJSON reads the JSON into s, as json.RawMessage does.
func (s *ScalarValue) UnmarshalJSON(data []byte) error {
	if err := (*json.RawMessage)(s).UnmarshalJSON(data); err != nil {
		return fmt.Errorf("unmarshal a scalar value: %w", err)
	}

	return nil
}

// Outputs returns the output names that s reads.
func (s *Source) Outputs() []string {
	names := []string{s.Value.Output}
	if s.Label != nil {
		names = append(names, s.Label.Output)
	}

	for _, f := range s.Where {
		names = append(names, f.Output)
	}

	slices.Sort(names)

	return slices.Compact(names)
}

// DependsOn returns the inputs that the filters of s name, sorted.
func (s *Source) DependsOn() []string {
	var deps []string

	for _, f := range s.Where {
		if f.Equals.Input != "" {
			deps = append(deps, f.Equals.Input)
		}
	}

	slices.Sort(deps)

	return slices.Compact(deps)
}

func (s *Source) validate(self string) error {
	switch {
	case (s.Blueprint == "") == (s.Collection == ""):
		return fmt.Errorf("%w: a source names one blueprint or one collection", ErrInvalid)
	case s.Value.Output == "":
		return fmt.Errorf("%w: a source names the output of its values", ErrInvalid)
	case s.Label != nil && s.Label.Output == "":
		return fmt.Errorf("%w: a label names an output", ErrInvalid)
	case len(s.Where) > MaxFilters:
		return fmt.Errorf("%w: %d filters, the limit is %d", ErrLimit, len(s.Where), MaxFilters)
	default:
	}

	for _, f := range s.Where {
		if err := f.validate(self); err != nil {
			return err
		}
	}

	return nil
}

func (f Filter) validate(self string) error {
	switch {
	case f.Output == "":
		return fmt.Errorf("%w: a filter names an output", ErrInvalid)
	case (f.Equals.Input == "") == (len(f.Equals.Value) == 0):
		return fmt.Errorf("%w: a filter equals one input or one value", ErrInvalid)
	case f.Equals.Input == self:
		return fmt.Errorf("%w: input %s depends on itself", ErrChoiceCycle, self)
	case len(f.Equals.Value) > 0 && !isScalar(json.RawMessage(f.Equals.Value)):
		return fmt.Errorf("%w: a filter value is a string, a number or a boolean", ErrInvalid)
	default:
		return nil
	}
}

// OutputValue is one output of a unit, as JSON. Secret is true when the
// tool marks the output as secret. The masked read never gives the value
// of a secret output, and the engine never makes an option of one.
type OutputValue struct {
	Value  json.RawMessage
	Secret bool
}

// UnitOutputs is the row of one unit that an OutputSource gives: the unit
// reference with its unit ID, the state version that the outputs come
// from, and each output that the caller asked for and the unit has.
type UnitOutputs struct {
	Unit         identity.Object
	Outputs      map[string]OutputValue
	StateVersion string
}

// SourceQuery is what OutputSource.Outputs reads: the units of Blueprint or
// of Collection in Namespace, and the outputs Names.
type SourceQuery struct {
	Namespace  namespace.Name
	Blueprint  string
	Collection string
	Names      []string
}

// OutputSource is the port that reads the outputs of the units of a source.
// The internal wiring implements it over the masked state read of each
// tool package.
type OutputSource interface {
	// Outputs returns one row for each unit of q that has a state version
	// and is not deleted, from the outputs of its last state version. It
	// returns an error that wraps ErrSourceUnavailable when it cannot read,
	// and ErrLimit above the units that a source reads. It never returns a
	// partial list with no error.
	Outputs(ctx context.Context, q SourceQuery) ([]UnitOutputs, error)
}

// Option is one option of a choice: its value, and the labels of the units
// that give it, sorted.
type Option struct {
	Value  string   `json:"value"`
	Labels []string `json:"labels"`
}

// OptionSet is the options of one input. WaitingFor lists the inputs that
// the choice depends on and that have no answer yet: the options are then
// empty. Truncated is true when ForRoute cut the list at MaxOptions.
type OptionSet struct {
	Input      string   `json:"input"`
	Options    []Option `json:"options"`
	WaitingFor []string `json:"waitingFor,omitempty"`
	Truncated  bool     `json:"truncated,omitempty"`
}

// Options returns every option of the choice of in, with the answers so
// far and the rows that the OutputSource gave for its source. A unit whose
// ID is exclude, the unit of the request itself, gives no option. The
// options are sorted by their first label, then by value. Two units that
// give one value give one option with the labels of both. An input with no
// choice gives an empty set. The list has no limit: CheckAnswer needs each
// option, and the options route cuts it with ForRoute.
func Options(in *Input, answers map[string]json.RawMessage, rows []UnitOutputs, exclude string) OptionSet {
	set := OptionSet{Input: in.Name, Options: []Option{}, WaitingFor: nil, Truncated: false}

	switch {
	case in.Choice == nil:
		return set
	case in.Choice.Source == nil:
		for _, v := range in.Choice.Values {
			set.Options = append(set.Options, Option{Value: v, Labels: []string{v}})
		}

		return set
	default:
	}

	src := in.Choice.Source
	for _, dep := range src.DependsOn() {
		if _, ok := answers[dep]; !ok {
			set.WaitingFor = append(set.WaitingFor, dep)
		}
	}

	if len(set.WaitingFor) > 0 {
		return set
	}

	set.Options = mergeOptions(src, rows, answers, exclude)

	return set
}

// ForRoute returns s as the options route gives it: at most MaxOptions
// options, with Truncated set when the list was longer. CheckAnswer refuses
// the result of ForRoute when it cut the list.
func (s OptionSet) ForRoute() OptionSet {
	if len(s.Options) > MaxOptions {
		s.Options = s.Options[:MaxOptions]
		s.Truncated = true
	}

	return s
}

// mergeOptions returns one option for each value that rows give, with the
// labels of each unit that gives it, sorted.
func mergeOptions(src *Source, rows []UnitOutputs, answers map[string]json.RawMessage, exclude string) []Option {
	byValue := map[string]*Option{}

	for i := range rows {
		o, ok := optionOf(src, &rows[i], answers, exclude)
		if !ok {
			continue
		}

		if have, ok := byValue[o.Value]; ok {
			have.Labels = append(have.Labels, o.Labels...)

			continue
		}

		byValue[o.Value] = &o
	}

	out := make([]Option, 0, len(byValue))

	for _, o := range byValue {
		slices.Sort(o.Labels)
		o.Labels = slices.Compact(o.Labels)
		out = append(out, *o)
	}

	slices.SortFunc(out, func(a, b Option) int {
		if c := strings.Compare(a.Labels[0], b.Labels[0]); c != 0 {
			return c
		}

		return strings.Compare(a.Value, b.Value)
	})

	return out
}

// optionOf returns the option that row gives, with one label, and false
// when the row gives no option.
func optionOf(src *Source, row *UnitOutputs, answers map[string]json.RawMessage, exclude string) (Option, bool) {
	if exclude != "" && row.Unit.UnitID() == exclude {
		return Option{Value: "", Labels: nil}, false
	}

	for _, f := range src.Where {
		want := json.RawMessage(f.Equals.Value)
		if f.Equals.Input != "" {
			want = answers[f.Equals.Input]
		}

		got, ok := scalarAt(row, f.Ref())
		if !ok || !equalJSON(got, want) {
			return Option{Value: "", Labels: nil}, false
		}
	}

	raw, ok := scalarAt(row, src.Value)
	if !ok {
		return Option{Value: "", Labels: nil}, false
	}

	value := scalarString(raw)
	label := value

	if src.Label != nil {
		if l, ok := scalarAt(row, *src.Label); ok {
			label = scalarString(l)
		}
	}

	return Option{Value: value, Labels: []string{label}}, true
}

// scalarAt returns the JSON scalar of row at ref, and false when the output
// is missing, secret, or not a scalar at its path.
func scalarAt(row *UnitOutputs, ref OutputRef) (json.RawMessage, bool) {
	out, ok := row.Outputs[ref.Output]
	if !ok || out.Secret || len(out.Value) == 0 {
		return nil, false
	}

	var v any
	if err := json.Unmarshal(out.Value, &v); err != nil {
		return nil, false
	}

	v, ok = pointer(v, ref.Path)
	if !ok {
		return nil, false
	}

	raw, err := json.Marshal(v)
	if err != nil || !isScalar(raw) {
		return nil, false
	}

	return raw, true
}

// pointer resolves the JSON pointer path in v.
func pointer(v any, path string) (any, bool) {
	if path == "" {
		return v, true
	}

	if !strings.HasPrefix(path, "/") {
		return nil, false
	}

	for part := range strings.SplitSeq(path[1:], "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")

		switch x := v.(type) {
		case map[string]any:
			next, ok := x[part]
			if !ok {
				return nil, false
			}

			v = next
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(x) {
				return nil, false
			}

			v = x[i]
		default:
			return nil, false
		}
	}

	return v, true
}

func isScalar(raw json.RawMessage) bool {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return false
	}

	switch v.(type) {
	case string, float64, bool:
		return true
	default:
		return false
	}
}

// scalarString returns the string form of a JSON scalar: a string as it
// is, and a number or a boolean as its JSON text.
func scalarString(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}

	return string(bytes.TrimSpace(raw))
}

// equalJSON reports whether a and b have equal canonical JSON
// (CanonicalJSON). So "1" does not equal 1, and 1 equals 1.0.
func equalJSON(a, b json.RawMessage) bool {
	ca, errA := CanonicalJSON(a)
	cb, errB := CanonicalJSON(b)

	return errA == nil && errB == nil && bytes.Equal(ca, cb)
}

// CheckAnswer returns the failures of the answer of in against set, every
// option that the server computed now with Options: an answer that is not
// among the options, also each item of a string-list. It returns nil for an
// input with no choice. It returns ErrInvalid for a set that ForRoute cut,
// because an answer past the cut is still an option.
func CheckAnswer(in *Input, answer json.RawMessage, set OptionSet) ([]Failure, error) {
	if in.Choice == nil {
		return nil, nil
	}

	if set.Truncated {
		return nil, fmt.Errorf("%w: the options of %s were cut for the route, check with every option", ErrInvalid,
			in.Name)
	}

	if len(set.WaitingFor) > 0 {
		return []Failure{{
			Input: in.Name, Code: FailureChoice,
			Message: "Answer " + strings.Join(set.WaitingFor, ", ") + " first.",
		}}, nil
	}

	values, ok := choiceValues(in.Type, answer)
	if !ok {
		message := "Give a string."
		if in.Type == TypeStringList {
			message = "Give a list of strings."
		}

		return []Failure{{Input: in.Name, Code: FailureType, Message: message}}, nil
	}

	var out []Failure

	for _, v := range values {
		if !slices.ContainsFunc(set.Options, func(o Option) bool { return o.Value == v }) {
			out = append(out, Failure{
				Input: in.Name, Code: FailureChoice,
				Message: fmt.Sprintf("%q is not an option now. Read the options again.", v),
			})
		}
	}

	return out, nil
}

// choiceValues returns the strings of an answer of a string or a
// string-list input, and false for an answer of another shape.
func choiceValues(t InputType, answer json.RawMessage) ([]string, bool) {
	if t == TypeStringList {
		var values []string

		err := json.Unmarshal(answer, &values)

		return values, err == nil
	}

	var v string

	err := json.Unmarshal(answer, &v)

	return []string{v}, err == nil
}

// Order returns the names of inputs in an order where each input comes
// after the inputs that its choice depends on, and else in the order of
// inputs. It returns ErrInvalid for a filter on an unknown input,
// ErrChoiceCycle for choices that depend on each other in a loop, and
// ErrLimit for a chain of more than MaxChoiceDepth dependent choices.
func Order(inputs []Input) ([]string, error) {
	deps := make(map[string][]string, len(inputs))

	for i := range inputs {
		in := &inputs[i]
		deps[in.Name] = nil

		if in.Choice != nil && in.Choice.Source != nil {
			deps[in.Name] = in.Choice.Source.DependsOn()
		}
	}

	for name, ds := range deps {
		for _, d := range ds {
			if _, ok := deps[d]; !ok {
				return nil, fmt.Errorf("%w: input %s depends on the unknown input %s", ErrInvalid, name, d)
			}
		}
	}

	o := orderer{deps: deps, depth: map[string]int{}, visiting: map[string]bool{}, out: nil}

	for i := range inputs {
		if _, err := o.visit(inputs[i].Name); err != nil {
			return nil, err
		}
	}

	return o.out, nil
}

type orderer struct {
	deps     map[string][]string
	depth    map[string]int
	visiting map[string]bool
	out      []string
}

// visit adds name after its dependencies, and returns the length of the
// longest chain of dependent choices that ends at name.
func (o *orderer) visit(name string) (int, error) {
	if d, done := o.depth[name]; done {
		return d, nil
	}

	if o.visiting[name] {
		return 0, fmt.Errorf("%w: at input %s", ErrChoiceCycle, name)
	}

	o.visiting[name] = true
	depth := 0

	for _, d := range o.deps[name] {
		n, err := o.visit(d)
		if err != nil {
			return 0, err
		}

		depth = max(depth, n+1)
	}

	if depth > MaxChoiceDepth {
		return 0, fmt.Errorf("%w: a chain of %d dependent choices at input %s, the limit is %d",
			ErrLimit, depth, name, MaxChoiceDepth)
	}

	o.visiting[name] = false
	o.depth[name] = depth
	o.out = append(o.out, name)

	return depth, nil
}
