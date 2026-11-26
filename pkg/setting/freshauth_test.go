package setting_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/setting"
)

// TestFreshAuthEnabledDefault checks that freshAuthEnabled is a boolean of the
// installation with the effective default false, and that its effective value
// shows the default as its source.
func TestFreshAuthEnabledDefault(t *testing.T) {
	t.Parallel()

	reg, err := setting.NewRegistry([]setting.Declaration{setting.FreshAuthEnabledDeclaration()})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	d, ok := reg.Lookup(setting.LevelInstallation, setting.SettingFreshAuthEnabled)
	if !ok {
		t.Fatalf("Lookup(%q) found no declaration", setting.SettingFreshAuthEnabled)
	}

	if d.Kind != setting.KindBool || d.Level != setting.LevelInstallation || !d.Database {
		t.Fatalf("declaration = kind %q level %q database %v, want a bool of the installation in the database",
			d.Kind, d.Level, d.Database)
	}

	item := setting.Effective(d, nil, nil)
	if item.Value != "false" || item.Source != setting.SourceDefault {
		t.Fatalf("effective value = %q from %q, want \"false\" from the default", item.Value, item.Source)
	}
}

// TestFreshAuthEnabledReducesSecurity checks that turning the setting off is a
// weaker change, and that turning it on is not.
func TestFreshAuthEnabledReducesSecurity(t *testing.T) {
	t.Parallel()

	d := setting.FreshAuthEnabledDeclaration()
	if d.ReducesSecurity == nil {
		t.Fatal("ReducesSecurity = nil, want a function")
	}

	tests := []struct {
		name     string
		old, new string
		want     bool
	}{
		{name: "on to off reduces security", old: "true", new: "false", want: true},
		{name: "off to on does not reduce security", old: "false", new: "true", want: false},
		{name: "on to on does not reduce security", old: "true", new: "true", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := d.ReducesSecurity(tt.old, tt.new); got != tt.want {
				t.Fatalf("ReducesSecurity(%q, %q) = %v, want %v", tt.old, tt.new, got, tt.want)
			}
		})
	}
}
