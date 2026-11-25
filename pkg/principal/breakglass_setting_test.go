package principal_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/setting"
)

// errSettingsUnreadable is the failure of a reader in these tests.
var errSettingsUnreadable = errors.New("the settings cannot be read")

// TestBreakGlassLockSecondsSetting proves that the lock timeout is a live
// installation setting with the default of 5 seconds, that it shows that
// effective value and its source, and that it refuses a value outside its
// bounds.
func TestBreakGlassLockSecondsSetting(t *testing.T) {
	t.Parallel()

	decls := principal.BreakGlassSettingDeclarations()
	if len(decls) != 1 {
		t.Fatalf("declarations = %d, want 1", len(decls))
	}

	registry, err := setting.NewRegistry(decls)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	d, ok := registry.Lookup(setting.LevelInstallation, principal.SettingBreakGlassLockSeconds)
	if !ok {
		t.Fatalf("no declaration for %s", principal.SettingBreakGlassLockSeconds)
	}

	if d.Kind != setting.KindInt || d.Default != "5" || d.Apply != setting.ApplyLive || !d.Database {
		t.Fatalf("declaration = %+v, want a live integer with the database layer and the default 5", d)
	}

	checkBreakGlassEffective(t, d, nil, "5", setting.SourceDefault)

	stored := "9"
	checkBreakGlassEffective(t, d, &stored, "9", setting.SourceDatabase)

	checkBreakGlassLockValues(t)
	checkBreakGlassLockRefusals(t)
}

// checkBreakGlassEffective proves the effective value and its source of d.
func checkBreakGlassEffective(
	t *testing.T, d setting.Declaration, stored *string, want string, source setting.Source,
) {
	t.Helper()

	item := setting.Effective(d, stored, nil)
	if item.Value != want || item.Source != source {
		t.Fatalf("effective value = %+v, want %q from %q", item, want, source)
	}
}

// checkBreakGlassLockValues proves the values that the parser accepts.
func checkBreakGlassLockValues(t *testing.T) {
	t.Helper()

	for _, tc := range []struct {
		value string
		want  time.Duration
	}{
		{value: "1", want: time.Second},
		{value: "5", want: 5 * time.Second},
		{value: "60", want: 60 * time.Second},
	} {
		got, err := principal.ParseBreakGlassLockSeconds(tc.value)
		if err != nil || got != tc.want {
			t.Fatalf("ParseBreakGlassLockSeconds(%q) = %v, %v; want %v", tc.value, got, err, tc.want)
		}
	}
}

// checkBreakGlassLockRefusals proves the values that the parser refuses.
func checkBreakGlassLockRefusals(t *testing.T) {
	t.Helper()

	for _, value := range []string{"", "0", "61", "-1", "1.5", "five"} {
		if _, err := principal.ParseBreakGlassLockSeconds(value); !errors.Is(err, setting.ErrInvalidValue) {
			t.Fatalf("ParseBreakGlassLockSeconds(%q) = %v, want ErrInvalidValue", value, err)
		}
	}
}

// TestBreakGlassLockTimeoutFailsTowardTheDefault proves that a reader that
// fails or answers a value outside the bounds gives the default, never no
// wait.
func TestBreakGlassLockTimeoutFailsTowardTheDefault(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		read principal.BreakGlassLockTimeout
		want time.Duration
	}{
		{read: nil, want: principal.DefaultBreakGlassLockSeconds * time.Second},
		{
			read: func(context.Context) (time.Duration, error) {
				return 0, errSettingsUnreadable
			},
			want: principal.DefaultBreakGlassLockSeconds * time.Second,
		},
		{
			read: func(context.Context) (time.Duration, error) {
				return 0, nil
			},
			want: principal.DefaultBreakGlassLockSeconds * time.Second,
		},
		{
			read: func(context.Context) (time.Duration, error) {
				return 90 * time.Second, nil
			},
			want: principal.DefaultBreakGlassLockSeconds * time.Second,
		},
		{
			read: func(context.Context) (time.Duration, error) {
				return 9 * time.Second, nil
			},
			want: 9 * time.Second,
		},
	} {
		if got := principal.LockTimeout(t.Context(), tc.read); got != tc.want {
			t.Fatalf("LockTimeout = %v, want %v", got, tc.want)
		}
	}
}
