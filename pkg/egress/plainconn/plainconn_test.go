package plainconn_test

import (
	"context"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/egress/plainconn"
	"opendustpan.org/opendustpan/pkg/setting"
)

// errRead is the error of a settings read that fails.
var errRead = errors.New("the settings cannot be read")

// plainReader answers one effective item or one error, as the settings
// service of the installation does.
type plainReader struct {
	item setting.Item
	err  error
}

// Effective implements plainconn.PlainRead.
func (r plainReader) Effective(_ context.Context, _ setting.Scope, _ string) (setting.Item, error) {
	return r.item, r.err
}

// plainReaderOf returns a reader that answers the effective item.
func plainReaderOf(item setting.Item) plainReader {
	return plainReader{item: item, err: nil}
}

// plainReaderError returns a reader that fails with err.
func plainReaderError(err error) plainReader {
	return plainReader{item: setting.Effective(plainDeclaration(), nil, nil), err: err}
}

// plainDeclaration returns the declaration of the setting of the tests. The
// declarations always hold it, so the panic fails fast.
func plainDeclaration() setting.Declaration {
	for _, d := range plainconn.SettingDeclarations() {
		if d.Key == plainconn.SettingAllowPlainConnections {
			return d
		}
	}

	panic("plainconn: no declaration for " + plainconn.SettingAllowPlainConnections)
}

// plainItem returns the effective item of the setting with the value.
func plainItem(value string) setting.Item {
	return setting.Effective(plainDeclaration(), &value, nil)
}

// TestAllowPlainConnectionsDefaultsOff proves the declaration of the
// setting: a bool with the database layer, live, false by default, and
// PlainAllowed answers false for the default item.
func TestAllowPlainConnectionsDefaultsOff(t *testing.T) {
	t.Parallel()

	registry, err := setting.NewRegistry(plainconn.SettingDeclarations())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	res := setting.Read(registry, setting.InstallationScope(), setting.NoStatic{}, setting.Stored{
		Values: nil, Revision: 0, Digest: "",
	})

	if len(res.Items) != 1 {
		t.Fatalf("settings = %d, want the one setting", len(res.Items))
	}

	item := res.Items[0]
	checkDefaultItem(t, item)

	if plainconn.PlainAllowed(t.Context(), plainReaderOf(item)) {
		t.Fatal("PlainAllowed of the default item = true, want false")
	}
}

// checkDefaultItem fails the test unless item is the declaration of the
// setting with its default value.
func checkDefaultItem(t *testing.T, item setting.Item) {
	t.Helper()

	if item.Key != plainconn.SettingAllowPlainConnections || item.Kind != setting.KindBool || !item.Database {
		t.Fatalf("item = %+v, want a bool with the database layer", item)
	}

	if item.Apply != setting.ApplyLive || item.Value != "false" || item.Default != "false" {
		t.Fatalf("item = %+v, want live and false by default", item)
	}

	if item.Source != setting.SourceDefault || item.Locked || !item.ReducesSecurity {
		t.Fatalf("item = %+v, want the default source and the security rule", item)
	}
}

// TestPlainAllowedFailsClosed is layer 2 for the reader: only the exact
// text true gives true, and the declaration refuses each other text as a
// stored value.
func TestPlainAllowedFailsClosed(t *testing.T) {
	t.Parallel()

	decl := plainDeclaration()

	tests := []struct {
		value string
		name  string
		want  bool
		valid bool
	}{
		{name: "true", value: "true", want: true, valid: true},
		{name: "false", value: "false", want: false, valid: true},
		{name: "the empty text", value: "", want: false, valid: false},
		{name: "upper case TRUE", value: "TRUE", want: false, valid: false},
		{name: "the digit 1", value: "1", want: false, valid: false},
		{name: "yes", value: "yes", want: false, valid: false},
		{name: "a leading space", value: " true", want: false, valid: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := plainconn.PlainAllowed(t.Context(), plainReaderOf(plainItem(tc.value))); got != tc.want {
				t.Errorf("PlainAllowed(%q) = %v, want %v", tc.value, got, tc.want)
			}

			err := decl.CheckValue(tc.value)
			if tc.valid != (err == nil) || (err != nil && !errors.Is(err, setting.ErrInvalidValue)) {
				t.Errorf("CheckValue(%q) = %v, want valid %v", tc.value, err, tc.valid)
			}
		})
	}
}

// TestPlainAllowedRefusesAReadThatFails is layer 2 for the reader: a read
// error, an unknown key and no reader at all give false.
func TestPlainAllowedRefusesAReadThatFails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		reader plainconn.PlainRead
		name   string
	}{
		{name: "a read error", reader: plainReaderError(errRead)},
		{name: "an unknown key", reader: plainReaderError(setting.ErrUnknownKey)},
		{name: "no reader", reader: nil},
		{name: "a nil settings service", reader: (*setting.Service)(nil)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if plainconn.PlainAllowed(t.Context(), tc.reader) {
				t.Fatal("PlainAllowed = true, want false")
			}
		})
	}
}

// TestAllowPlainConnectionsReducesSecurity is layer 2 for the declaration:
// a change to true, and a value that does not parse, reduce security.
func TestAllowPlainConnectionsReducesSecurity(t *testing.T) {
	t.Parallel()

	decl := plainDeclaration()

	tests := []struct {
		name string
		old  string
		next string
		want bool
	}{
		{name: "off to on", old: "false", next: "true", want: true},
		{name: "on to off", old: "true", next: "false", want: false},
		{name: "on to on", old: "true", next: "true", want: false},
		{name: "off to off", old: "false", next: "false", want: false},
		{name: "an old value that does not parse", old: "", next: "true", want: true},
		{name: "a next value that does not parse", old: "false", next: "yes", want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := decl.ReducesSecurity(tc.old, tc.next); got != tc.want {
				t.Errorf("ReducesSecurity(%q, %q) = %v, want %v", tc.old, tc.next, got, tc.want)
			}
		})
	}
}
