package errcode_test

import (
	"encoding/json"
	"errors"
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

	answer, err := lockSpec().Answer(
		errcode.Identifier("workspace", "prod"),
		errcode.Quantity("waiting", 3),
		errcode.Time("lockedAt", lockedAt()),
	)
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}

	body, err := json.Marshal(answer)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	want := `{"code":"workspace.locked","args":{"lockedAt":"2026-10-23T09:30:00.125Z","waiting":3,"workspace":"prod"}}`
	if string(body) != want {
		t.Fatalf("answer = %s, want %s", body, want)
	}
}

func TestSpecAnswerWithoutArgs(t *testing.T) {
	t.Parallel()

	answer, err := errcode.New(errcode.AccessDenied)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if answer.Code != errcode.AccessDenied || answer.Args != nil {
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

			if _, err := lockSpec().Answer(tc.values...); !errors.Is(err, tc.want) {
				t.Fatalf("Answer error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestSpecAnswerAtTheLimits(t *testing.T) {
	t.Parallel()

	answer, err := lockSpec().Answer(
		errcode.Identifier("workspace", strings.Repeat("é", errcode.MaxIdentifier)),
		errcode.Quantity("waiting", -errcode.MaxQuantity),
		errcode.Time("lockedAt", lockedAt()),
	)
	if err != nil {
		t.Fatalf("Answer at the limits: %v", err)
	}

	if got := answer.Args["waiting"]; got != int64(-errcode.MaxQuantity) {
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

	if _, err := spec.Answer(values...); !errors.Is(err, errcode.ErrTooManyArgs) {
		t.Fatalf("Answer error = %v, want %v", err, errcode.ErrTooManyArgs)
	}
}
