package principal_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/setting"
)

// removalAlertDeclaration returns the removal alert declaration of key.
func removalAlertDeclaration(t *testing.T, key string) setting.Declaration {
	t.Helper()

	for _, d := range principal.RemovalAlertSettingDeclarations() {
		if d.Key == key {
			return d
		}
	}

	t.Fatalf("no declaration of %s", key)

	return setting.Declaration{
		Key: "", Level: "", Kind: "", Default: "", Secret: false, Database: false, Apply: "", Check: nil,
		StaticCredentialHost: false, ReducesSecurity: nil, Doc: "",
	}
}

// TestParseRemovalAlertCount checks the bounds of the count setting.
func TestParseRemovalAlertCount(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		value   string
		want    int
		wantErr bool
	}{
		{"the default", "10", 10, false},
		{"the smallest count", "1", 1, false},
		{"the largest count", "10000", 10000, false},
		{"zero", "0", 0, true},
		{"above the largest count", "10001", 0, true},
		{"a negative count", "-1", 0, true},
		{"an empty value", "", 0, true},
		{"a value that is not a number", "ten", 0, true},
		{"a fractional value", "1.5", 0, true},
		{"a value with a space", " 10", 0, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := principal.ParseRemovalAlertCount(tc.value)
			if tc.wantErr {
				if !errors.Is(err, principal.ErrInvalid) {
					t.Fatalf("ParseRemovalAlertCount(%q) = %v, want ErrInvalid", tc.value, err)
				}

				return
			}

			if err != nil || got != tc.want {
				t.Fatalf("ParseRemovalAlertCount(%q) = %d, %v, want %d", tc.value, got, err, tc.want)
			}
		})
	}
}

// TestParseRemovalAlertPercent checks the bounds of the percent setting.
func TestParseRemovalAlertPercent(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		value   string
		want    int
		wantErr bool
	}{
		{"the default", "20", 20, false},
		{"the smallest percent", "1", 1, false},
		{"the largest percent", "100", 100, false},
		{"zero", "0", 0, true},
		{"above the largest percent", "101", 0, true},
		{"a negative percent", "-20", 0, true},
		{"an empty value", "", 0, true},
		{"a value that is not a number", "twenty", 0, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := principal.ParseRemovalAlertPercent(tc.value)
			if tc.wantErr {
				if !errors.Is(err, principal.ErrInvalid) {
					t.Fatalf("ParseRemovalAlertPercent(%q) = %v, want ErrInvalid", tc.value, err)
				}

				return
			}

			if err != nil || got != tc.want {
				t.Fatalf("ParseRemovalAlertPercent(%q) = %d, %v, want %d", tc.value, got, err, tc.want)
			}
		})
	}
}

// TestParseRemovalAlertWindow checks the bounds and the whole seconds of the
// window setting.
func TestParseRemovalAlertWindow(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		value   string
		want    time.Duration
		wantErr bool
	}{
		{"the default", "15m", 15 * time.Minute, false},
		{"the shortest window", "1m", time.Minute, false},
		{"the longest window", "24h", principal.MaxRemovalWindow, false},
		{"ninety minutes", "90m", 90 * time.Minute, false},
		{"below the shortest window", "59s", 0, true},
		{"above the longest window", "24h1s", 0, true},
		{"a part of a second", "15m0.5s", 0, true},
		{"zero", "0", 0, true},
		{"an empty value", "", 0, true},
		{"a value that is not a duration", "forever", 0, true},
		{"a count of seconds", "900", 0, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := principal.ParseRemovalAlertWindow(tc.value)
			if tc.wantErr {
				if !errors.Is(err, principal.ErrInvalid) {
					t.Fatalf("ParseRemovalAlertWindow(%q) = %v, want ErrInvalid", tc.value, err)
				}

				return
			}

			if err != nil || got != tc.want {
				t.Fatalf("ParseRemovalAlertWindow(%q) = %s, %v, want %s", tc.value, got, err, tc.want)
			}
		})
	}
}

// removalAlertCase holds the values of one removal alert setting of the
// tests.
type removalAlertCase struct {
	name   string
	key    string
	kind   setting.Kind
	def    string
	ok     string
	bad    string
	reduce [2]string
	keep   [2]string
	relax  [2]string
}

// removalAlertCases returns the three settings of the removal alert.
func removalAlertCases() []removalAlertCase {
	return []removalAlertCase{
		{
			name: "the count", key: principal.SettingRemovalAlertCount, kind: setting.KindInt, def: "10",
			ok: "10000", bad: "10001", reduce: [2]string{"10", "11"}, keep: [2]string{"10", "10"},
			relax: [2]string{"10", "9"},
		},
		{
			name: "the percent", key: principal.SettingRemovalAlertPercent, kind: setting.KindInt, def: "20",
			ok: "100", bad: "0", reduce: [2]string{"20", "21"}, keep: [2]string{"20", "20"},
			relax: [2]string{"20", "19"},
		},
		{
			name: "the window", key: principal.SettingRemovalAlertWindow, kind: setting.KindString, def: "15m0s",
			ok: "1m", bad: "30s", reduce: [2]string{"15m", "5m"}, keep: [2]string{"15m", "15m"},
			relax: [2]string{"15m", "20m"},
		},
	}
}

// TestRemovalAlertSettingDeclarations checks the shape of each removal alert
// setting.
func TestRemovalAlertSettingDeclarations(t *testing.T) {
	t.Parallel()

	for _, tc := range removalAlertCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkRemovalAlertShape(t, tc, removalAlertDeclaration(t, tc.key))
		})
	}
}

// checkRemovalAlertShape fails unless d is an installation setting of the
// database layer with a live apply and a description.
func checkRemovalAlertShape(t *testing.T, tc removalAlertCase, d setting.Declaration) {
	t.Helper()

	if d.Level != setting.LevelInstallation {
		t.Fatalf("%s level = %q, want installation", tc.key, d.Level)
	}

	if d.Kind != tc.kind {
		t.Fatalf("%s kind = %q, want %q", tc.key, d.Kind, tc.kind)
	}

	if !d.Database {
		t.Fatalf("%s has no database layer", tc.key)
	}

	if d.Secret {
		t.Fatalf("%s is a secret", tc.key)
	}

	if d.Apply != setting.ApplyLive {
		t.Fatalf("%s apply = %q, want live", tc.key, d.Apply)
	}

	if d.Default != tc.def {
		t.Fatalf("%s default = %q, want %q", tc.key, d.Default, tc.def)
	}

	if d.Check == nil || d.ReducesSecurity == nil {
		t.Fatalf("%s has no check or no security direction", tc.key)
	}

	if d.Doc == "" {
		t.Fatalf("%s has no description", tc.key)
	}
}

// TestRemovalAlertSettingChecks checks that the check of each setting takes a
// value inside the bounds and refuses one outside them.
func TestRemovalAlertSettingChecks(t *testing.T) {
	t.Parallel()

	for _, tc := range removalAlertCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			d := removalAlertDeclaration(t, tc.key)

			if err := d.Check(tc.ok); err != nil {
				t.Fatalf("%s check(%q) = %v, want nil", tc.key, tc.ok, err)
			}

			if err := d.Check(tc.bad); !errors.Is(err, principal.ErrInvalid) {
				t.Fatalf("%s check(%q) = %v, want ErrInvalid", tc.key, tc.bad, err)
			}
		})
	}
}

// TestRemovalAlertSettingSecurityDirection checks that a value that gives a
// source more room reduces security, and that a value that gives it less
// room, the same value and a value that does not parse do not.
func TestRemovalAlertSettingSecurityDirection(t *testing.T) {
	t.Parallel()

	for _, tc := range removalAlertCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			d := removalAlertDeclaration(t, tc.key)

			if !d.ReducesSecurity(tc.reduce[0], tc.reduce[1]) {
				t.Fatalf("%s reduces security from %q to %q = false, want true", tc.key, tc.reduce[0], tc.reduce[1])
			}

			if d.ReducesSecurity(tc.keep[0], tc.keep[1]) {
				t.Fatalf("%s reduces security from %q to %q = true, want false", tc.key, tc.keep[0], tc.keep[1])
			}

			if d.ReducesSecurity(tc.relax[0], tc.relax[1]) {
				t.Fatalf("%s reduces security from %q to %q = true, want false", tc.key, tc.relax[0], tc.relax[1])
			}

			// A value that does not parse reduces security: the check fails
			// closed.
			if !d.ReducesSecurity("not a value", tc.keep[1]) {
				t.Fatalf("%s reduces security from a value that does not parse = false, want true", tc.key)
			}
		})
	}
}

// TestDefaultRemovalAlertLimits checks the defaults of the limits.
func TestDefaultRemovalAlertLimits(t *testing.T) {
	t.Parallel()

	got := principal.DefaultRemovalAlertLimits()
	if got.Count != principal.DefaultRemovalAlertCount || got.Percent != principal.DefaultRemovalAlertPercent ||
		got.Window != principal.DefaultRemovalAlertWindow {
		t.Fatalf("DefaultRemovalAlertLimits() = %+v, want the default count, percent and window", got)
	}
}
