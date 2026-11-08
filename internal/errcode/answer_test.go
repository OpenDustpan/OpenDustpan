package errcode_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/errcode"
)

// lockSpec returns a code with one argument of each kind.
func lockSpec() errcode.Spec {
	return errcode.Spec{
		Code:    "workspace.locked",
		Meaning: "the workspace has a lock of another holder",
		Args: []errcode.Arg{
			{Name: "workspace", Kind: errcode.KindIdentifier},
			{Name: "waiting", Kind: errcode.KindQuantity},
			{Name: "lockedAt", Kind: errcode.KindTime},
		},
	}
}

// lockedAt returns a time with a zone and nanoseconds, to prove the
// conversion to UTC.
func lockedAt() time.Time {
	return time.Date(2026, 10, 23, 11, 30, 0, 125000000, time.FixedZone("CEST", 2*60*60))
}

func TestSpecAnswer(t *testing.T) {
	t.Parallel()

	answer, err := errcode.SpecAnswer(lockSpec(),
		errcode.Identifier("workspace", "prod"),
		errcode.Quantity("waiting", 3),
		errcode.Time("lockedAt", lockedAt()),
	)
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}

	if got := answer.Code(); got != "workspace.locked" {
		t.Fatalf("code = %q, want workspace.locked", got)
	}

	body, err := json.Marshal(answer.Args())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	want := `{"lockedAt":"2026-10-23T09:30:00.125Z","waiting":3,"workspace":"prod"}`
	if string(body) != want {
		t.Fatalf("args = %s, want %s", body, want)
	}
}

// TestAnswerHasNoWireFields proves that an answer has no exported field and
// so no JSON form of its own: the adapter copies the code and the arguments
// to the fields of its shape, which have other names in each shape.
func TestAnswerHasNoWireFields(t *testing.T) {
	t.Parallel()

	typ := reflect.TypeFor[errcode.Answer]()

	for field := range typ.Fields() {
		if field.IsExported() {
			t.Errorf("Answer has the exported field %s", field.Name)
		}
	}

	if typ.Implements(reflect.TypeFor[json.Marshaler]()) {
		t.Error("Answer has a JSON form of its own")
	}
}

// TestAnswerArgsIsACopy proves that a change to the map of Args does not
// change the answer.
func TestAnswerArgsIsACopy(t *testing.T) {
	t.Parallel()

	answer, err := errcode.New(errcode.PreferencesFieldInvalid, errcode.Identifier("field", "theme"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	args := answer.Args()
	if args == nil {
		t.Fatal("Args = nil, want the field argument")
	}

	args["field"] = "other"

	if got := answer.Args()["field"]; got != "theme" {
		t.Fatalf("field = %v, want theme", got)
	}
}

// TestZeroAnswerHasNoCode proves that an answer that New did not make has no
// code, so an adapter cannot send a code that is not in the catalog.
func TestZeroAnswerHasNoCode(t *testing.T) {
	t.Parallel()

	var answer errcode.Answer

	if answer.Code() != "" || answer.Args() != nil {
		t.Fatalf("zero answer = %q %v, want no code and no arguments", answer.Code(), answer.Args())
	}
}

func TestSpecAnswerWithoutArgs(t *testing.T) {
	t.Parallel()

	answer, err := errcode.New(errcode.AccessDenied)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if answer.Code() != errcode.AccessDenied || answer.Args() != nil {
		t.Fatalf("answer = %+v, want the code and no arguments", answer)
	}
}

func TestSpecAnswerRefuses(t *testing.T) {
	t.Parallel()

	workspace := errcode.Identifier("workspace", "prod")
	waiting := errcode.Quantity("waiting", 3)
	at := errcode.Time("lockedAt", lockedAt())

	tests := []struct {
		name   string
		values []errcode.Value
		want   error
	}{
		{"missing argument", []errcode.Value{workspace, waiting}, errcode.ErrArgMissing},
		{
			"undeclared argument",
			[]errcode.Value{workspace, waiting, at, errcode.Identifier("stack", "x")},
			errcode.ErrArgUndeclared,
		},
		{"argument two times", []errcode.Value{workspace, waiting, at, workspace}, errcode.ErrArgRepeated},
		{"wrong kind", []errcode.Value{errcode.Quantity("workspace", 1), waiting, at}, errcode.ErrArgKind},
		{"empty identifier", []errcode.Value{errcode.Identifier("workspace", ""), waiting, at}, errcode.ErrArgValue},
		{
			"long identifier",
			[]errcode.Value{errcode.Identifier("workspace", strings.Repeat("a", errcode.MaxIdentifier+1)), waiting, at},
			errcode.ErrArgValue,
		},
		{"line break", []errcode.Value{errcode.Identifier("workspace", "a\nb"), waiting, at}, errcode.ErrArgValue},
		{
			"control character",
			[]errcode.Value{errcode.Identifier("workspace", "a\x00b"), waiting, at},
			errcode.ErrArgValue,
		},
		{"not UTF-8", []errcode.Value{errcode.Identifier("workspace", "a\xffb"), waiting, at}, errcode.ErrArgValue},
		{
			"quantity above the safe range",
			[]errcode.Value{workspace, errcode.Quantity("waiting", errcode.MaxQuantity+1), at},
			errcode.ErrArgValue,
		},
		{
			"quantity below the safe range",
			[]errcode.Value{workspace, errcode.Quantity("waiting", -errcode.MaxQuantity-1), at},
			errcode.ErrArgValue,
		},
		{"zero time", []errcode.Value{workspace, waiting, errcode.Time("lockedAt", time.Time{})}, errcode.ErrArgValue},
		{"no value", []errcode.Value{workspace, waiting, {}}, errcode.ErrArgUndeclared},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := errcode.SpecAnswer(lockSpec(), tc.values...); !errors.Is(err, tc.want) {
				t.Fatalf("Answer error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestSpecAnswerAtTheLimits(t *testing.T) {
	t.Parallel()

	answer, err := errcode.SpecAnswer(lockSpec(),
		errcode.Identifier("workspace", strings.Repeat("é", errcode.MaxIdentifier)),
		errcode.Quantity("waiting", -errcode.MaxQuantity),
		errcode.Time("lockedAt", lockedAt()),
	)
	if err != nil {
		t.Fatalf("Answer at the limits: %v", err)
	}

	if got := answer.Args()["waiting"]; got != int64(-errcode.MaxQuantity) {
		t.Fatalf("waiting = %v, want %d", got, -errcode.MaxQuantity)
	}
}

func TestNewRefusesAnUnknownCode(t *testing.T) {
	t.Parallel()

	if _, err := errcode.New("stack.unknown-code"); !errors.Is(err, errcode.ErrUnknownCode) {
		t.Fatalf("New error = %v, want %v", err, errcode.ErrUnknownCode)
	}
}

// TestSpecAnswerRefusesTooManyArgs proves the limit for a spec that is not in
// the catalog: the catalog test keeps the catalog inside it.
func TestSpecAnswerRefusesTooManyArgs(t *testing.T) {
	t.Parallel()

	spec := errcode.Spec{Code: "test.many", Meaning: "many arguments", Args: nil}
	values := make([]errcode.Value, 0, errcode.MaxArgs+1)

	for i := range errcode.MaxArgs + 1 {
		name := "a" + strings.Repeat("b", i)
		spec.Args = append(spec.Args, errcode.Arg{Name: name, Kind: errcode.KindQuantity})
		values = append(values, errcode.Quantity(name, int64(i)))
	}

	if _, err := errcode.SpecAnswer(spec, values...); !errors.Is(err, errcode.ErrTooManyArgs) {
		t.Fatalf("Answer error = %v, want %v", err, errcode.ErrTooManyArgs)
	}
}

// TestIdentifierUpToHasItsOwnLimit proves that IdentifierUpTo takes a value up
// to its own limit and refuses a longer one, that a limit below MaxIdentifier is
// MaxIdentifier, and that Identifier keeps MaxIdentifier.
func TestIdentifierUpToHasItsOwnLimit(t *testing.T) {
	t.Parallel()

	spec := errcode.Spec{
		Code: "workspace.named", Meaning: "a workspace has a name",
		Args: []errcode.Arg{{Name: "workspace", Kind: errcode.KindIdentifier}},
	}

	const limit = 2 * errcode.MaxIdentifier

	tests := []struct {
		name  string
		value errcode.Value
		want  error
	}{
		{"value at the own limit", errcode.IdentifierUpTo("workspace", strings.Repeat("a", limit), limit), nil},
		{
			"value over the own limit",
			errcode.IdentifierUpTo("workspace", strings.Repeat("a", limit+1), limit), errcode.ErrArgValue,
		},
		{
			"limit below the shared one",
			errcode.IdentifierUpTo("workspace", strings.Repeat("a", errcode.MaxIdentifier), 1), nil,
		},
		{
			"shared limit still holds for a limit below it",
			errcode.IdentifierUpTo("workspace", strings.Repeat("a", errcode.MaxIdentifier+1), 1), errcode.ErrArgValue,
		},
		{
			"plain identifier over the shared limit",
			errcode.Identifier("workspace", strings.Repeat("a", errcode.MaxIdentifier+1)), errcode.ErrArgValue,
		},
		{"line break under the own limit", errcode.IdentifierUpTo("workspace", "a\nb", limit), errcode.ErrArgValue},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := errcode.SpecAnswer(spec, tt.value)
			if !errors.Is(err, tt.want) || (tt.want == nil && err != nil) {
				t.Fatalf("Answer = %v, want %v", err, tt.want)
			}
		})
	}
}

// TestDeniedArgs proves the two arguments of a denial: the action that the
// caller lacks and the kind of the object. A value that an argument cannot
// hold gives an error, so the answer of a denial never holds an invalid
// argument.
func TestDeniedArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		action     string
		objectKind string
		want       map[string]any
		wantErr    error
	}{
		{
			"the action and the kind",
			"collection:create-unit", "collection",
			map[string]any{"action": "collection:create-unit", "objectKind": "collection"},
			nil,
		},
		{"no action", "", "collection", nil, errcode.ErrArgValue},
		{"no kind", "unit:read", "", nil, errcode.ErrArgValue},
		{"line break in the action", "unit:read\nnext", "unit", nil, errcode.ErrArgValue},
		{"control character in the kind", "unit:read", "unit\x00", nil, errcode.ErrArgValue},
		{
			"action over the limit",
			strings.Repeat("a", errcode.MaxIdentifier+1), "unit", nil, errcode.ErrArgValue,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := errcode.DeniedArgs(tt.action, tt.objectKind)
			if !errors.Is(err, tt.wantErr) || (tt.wantErr == nil && err != nil) {
				t.Fatalf("DeniedArgs = %v, %v; want %v, %v", got, err, tt.want, tt.wantErr)
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("arguments = %v, want %v", got, tt.want)
			}
		})
	}
}
