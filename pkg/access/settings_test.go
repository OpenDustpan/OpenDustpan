package access_test

import (
	"context"
	"errors"
	"math"
	"strconv"
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/setting"
)

// errSettingsGone is the failure of a settings reader of a test.
var errSettingsGone = errors.New("the settings store is gone")

// capReader answers the value of the cap of the export of a collection that a
// test sets. A nil value answers the default, and err answers a read that
// fails.
type capReader struct {
	value *string
	err   error
}

func (r capReader) Effective(context.Context, setting.Scope, string) (setting.Item, error) {
	return setting.Effective(access.SettingDeclarations()[0], r.value, nil), r.err
}

// TestCollectionUnitCapResolution proves that the cap of the export of a
// collection fails toward the default: no reader, a reader that fails, and a
// value outside the bounds of MinCollectionUnitCap to MaxCollectionUnitCap
// give the default cap.
func TestCollectionUnitCapResolution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		settings access.Settings
		want     int
	}{
		{name: "no reader", settings: nil, want: access.DefaultCollectionUnitCap},
		{
			name: "a reader that fails", settings: capReader{value: nil, err: errSettingsGone},
			want: access.DefaultCollectionUnitCap,
		},
		{
			name: "a reader with no value", settings: capReader{value: nil, err: nil},
			want: access.DefaultCollectionUnitCap,
		},
		{
			name: "a value of 0", settings: capReader{value: new("0"), err: nil},
			want: access.DefaultCollectionUnitCap,
		},
		{
			name: "a value below 0", settings: capReader{value: new("-1"), err: nil},
			want: access.DefaultCollectionUnitCap,
		},
		{
			name: "a value that is not a number", settings: capReader{value: new("many"), err: nil},
			want: access.DefaultCollectionUnitCap,
		},
		{
			name: "a value above the max", settings: capReader{value: new("100001"), err: nil},
			want: access.DefaultCollectionUnitCap,
		},
		{
			name: "the largest whole number", settings: capReader{value: new("9223372036854775807"), err: nil},
			want: access.DefaultCollectionUnitCap,
		},
		{name: "a value of 1", settings: capReader{value: new("1"), err: nil}, want: 1},
		{name: "a value of 7", settings: capReader{value: new("7"), err: nil}, want: 7},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := access.CollectionUnitCapForTest(t.Context(), tc.settings); got != tc.want {
				t.Fatalf("cap = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestParseCollectionUnitCap checks the bounds of the parse of the cap of the
// export of a collection. A value below 1 or above 100000 is refused, and the
// refusal names the range and never the value.
func TestParseCollectionUnitCap(t *testing.T) {
	t.Parallel()

	wantRefusal := "the value is not a whole number from " + strconv.Itoa(access.MinCollectionUnitCap) +
		" to " + strconv.Itoa(access.MaxCollectionUnitCap)

	accepted := []struct {
		name  string
		value string
		want  int
	}{
		{name: "1 is accepted", value: "1", want: access.MinCollectionUnitCap},
		{name: "100000 is accepted", value: "100000", want: access.MaxCollectionUnitCap},
		{name: "a leading plus sign is accepted", value: "+5", want: 5},
	}

	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := access.ParseCollectionUnitCap(tc.value)
			if err != nil {
				t.Fatalf("parse %q: %v", tc.value, err)
			}

			if got != tc.want {
				t.Fatalf("parse %q = %d, want %d", tc.value, got, tc.want)
			}
		})
	}

	refused := []struct {
		name  string
		value string
	}{
		{name: "0 is refused", value: "0"},
		{name: "-1 is refused", value: "-1"},
		{name: "100001 is refused", value: "100001"},
		{name: "the largest whole number is refused", value: "9223372036854775807"},
		{name: "a whole number above the largest is refused", value: "9223372036854775808"},
		{name: "a word is refused", value: "many"},
		{name: "an empty value is refused", value: ""},
		{name: "a leading space is refused", value: " 10"},
		{name: "a trailing space is refused", value: "10 "},
		{name: "a power of ten is refused", value: "1e3"},
		{name: "a hexadecimal value is refused", value: "0x10"},
	}

	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := access.ParseCollectionUnitCap(tc.value)
			if err == nil {
				t.Fatalf("parse %q = no error, want an error", tc.value)
			}

			// The refusal is the same text for each refused value, so it
			// holds no value.
			if err.Error() != wantRefusal {
				t.Fatalf("the refusal of %q = %q, want the constant %q", tc.value, err.Error(), wantRefusal)
			}
		})
	}
}

// TestBoundCollectionUnitCap checks the defense-in-depth bound of the cap of
// the export of a collection: a value above the max gives the max, and a value
// below the min gives the default.
func TestBoundCollectionUnitCap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		n    int
		want int
	}{
		{name: "0 gives the default", n: 0, want: access.DefaultCollectionUnitCap},
		{name: "-1 gives the default", n: -1, want: access.DefaultCollectionUnitCap},
		{name: "1 stays", n: access.MinCollectionUnitCap, want: access.MinCollectionUnitCap},
		{name: "the max stays", n: access.MaxCollectionUnitCap, want: access.MaxCollectionUnitCap},
		{name: "one over the max gives the max", n: access.MaxCollectionUnitCap + 1, want: access.MaxCollectionUnitCap},
		{name: "the largest whole number gives the max", n: math.MaxInt, want: access.MaxCollectionUnitCap},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := access.BoundCollectionUnitCapForTest(tc.n); got != tc.want {
				t.Fatalf("bound %d = %d, want %d", tc.n, got, tc.want)
			}
		})
	}
}

// TestCollectionUnitCapDeclaration proves the shape of the declaration of the
// cap: an installation setting that the database layer holds, applied live,
// with the default cap, and a check that refuses each value outside the
// bounds.
func TestCollectionUnitCapDeclaration(t *testing.T) {
	t.Parallel()

	decls := access.SettingDeclarations()
	if len(decls) != 1 {
		t.Fatalf("declarations = %d, want 1", len(decls))
	}

	d := decls[0]
	if d.Key != access.SettingCollectionUnitCap || d.Level != setting.LevelInstallation || d.Kind != setting.KindInt ||
		d.Default != strconv.Itoa(access.DefaultCollectionUnitCap) || !d.Database || d.Secret ||
		d.Apply != setting.ApplyLive || d.Doc == "" {
		t.Fatalf("declaration = %+v, want the installation cap of the export of a collection", d)
	}

	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "1 is allowed", value: "1", want: true},
		{name: "the default is allowed", value: strconv.Itoa(access.DefaultCollectionUnitCap), want: true},
		{name: "0 is refused", value: "0", want: false},
		{name: "-1 is refused", value: "-1", want: false},
		{name: "a text value is refused", value: "many", want: false},
		{name: "an empty value is refused", value: "", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := d.CheckValue(tc.value)
			if (err == nil) != tc.want {
				t.Fatalf("check %q = %v, want an error: %t", tc.value, err, !tc.want)
			}
		})
	}
}

// TestCollectionUnitCapReducesSecurity checks that a raise of the cap of the
// export of a collection counts as a change that reduces security, and that a
// value that does not parse on either side does too.
func TestCollectionUnitCapReducesSecurity(t *testing.T) {
	t.Parallel()

	var reduces func(old, next string) bool

	for _, d := range access.SettingDeclarations() {
		if d.Key == access.SettingCollectionUnitCap {
			reduces = d.ReducesSecurity
		}
	}

	if reduces == nil {
		t.Fatalf("the declaration of %s has no ReducesSecurity function", access.SettingCollectionUnitCap)
	}

	tests := []struct {
		name string
		old  string
		next string
		want bool
	}{
		{name: "a raise reduces security", old: "10", next: "11", want: true},
		{name: "the same value does not", old: "10", next: "10", want: false},
		{name: "a lower value does not", old: "10", next: "9", want: false},
		{name: "an old value that does not parse reduces security", old: "many", next: "10", want: true},
		{name: "a next value that does not parse reduces security", old: "10", next: "many", want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := reduces(tc.old, tc.next); got != tc.want {
				t.Fatalf("ReducesSecurity(%q, %q) = %v, want %v", tc.old, tc.next, got, tc.want)
			}
		})
	}
}

// collectionOfUnits returns the units of one collection of acme, count of
// them.
func collectionOfUnits(name string, count int) collections {
	units := make(collections, count)

	for i := range count {
		units["pulumi:acme/web/unit-"+strconv.Itoa(i)] = name
	}

	return units
}

// checkCollectionUnitCapRefusal proves that err is the refusal of the export
// of a collection, with the count, the cap and the setting key.
func checkCollectionUnitCapRefusal(t *testing.T, err error, count, wantCap int) {
	t.Helper()

	if !errors.Is(err, access.ErrCollectionUnitCap) {
		t.Fatalf("review = %v, want ErrCollectionUnitCap", err)
	}

	over := &access.CollectionUnitCapError{Key: "", Count: 0, Cap: 0}
	if !errors.As(err, &over) {
		t.Fatalf("review = %v, want a CollectionUnitCapError", err)
	}

	if over.Count != count || over.Cap != wantCap || over.Key != access.SettingCollectionUnitCap {
		t.Fatalf("refusal = %+v, want count %d, cap %d and key %s",
			over, count, wantCap, access.SettingCollectionUnitCap)
	}

	want := "the collection holds " + strconv.Itoa(count) + " units; the export allows at most " +
		strconv.Itoa(wantCap) + "; change the setting " + access.SettingCollectionUnitCap
	if err.Error() != want {
		t.Fatalf("refusal text = %q, want %q", err.Error(), want)
	}
}

// TestReviewOfACollectionAtTheCap proves that the export of a collection
// holds a collection with exactly the cap, refuses one over the cap naming
// the count, the cap and the setting key, and uses the default cap when the
// reader fails.
func TestReviewOfACollectionAtTheCap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		settings   access.Settings
		units      int
		wantCount  int
		wantCap    int
		wantRefuse bool
	}{
		{
			name: "a collection with exactly the cap is exported", settings: capReader{value: new("2"), err: nil},
			units: 2, wantCount: 2, wantCap: 2, wantRefuse: false,
		},
		{
			name: "a collection over the cap is refused", settings: capReader{value: new("2"), err: nil},
			units: 3, wantCount: 3, wantCap: 2, wantRefuse: true,
		},
		{
			name:     "a collection over the default cap is refused when the reader fails",
			settings: capReader{value: nil, err: errSettingsGone}, units: access.DefaultCollectionUnitCap + 1,
			wantCount: access.DefaultCollectionUnitCap + 1, wantCap: access.DefaultCollectionUnitCap, wantRefuse: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixtureWithSettings(t, collectionOfUnits("payments", tc.units), tc.settings)

			page, err := f.svc.Review(t.Context(), identity.CollectionObject("acme", "payments"), access.ReviewRequest{
				From: nil, To: nil, Subject: "", Cursor: "", Limit: 0, Expand: false,
			})

			if tc.wantRefuse {
				checkCollectionUnitCapRefusal(t, err, tc.wantCount, tc.wantCap)

				return
			}

			if err != nil {
				t.Fatalf("review: %v", err)
			}

			// The total counts the bootstrap row.
			if page.Total == nil || *page.Total != 1 {
				t.Fatalf("total = %v, want 1", page.Total)
			}
		})
	}
}
