package cli_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/cli"
	"opendustpan.org/opendustpan/pkg/egress/plainconn"
	"opendustpan.org/opendustpan/pkg/setting"
)

func TestSettingDeclarationsAreValidAndCoverTheFileKeys(t *testing.T) {
	t.Parallel()

	decls := cli.SettingDeclarations()

	registry, err := setting.NewRegistry(decls)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	// Only the settings that the API can change have a database layer.
	databaseKeys := map[string]bool{
		"branding.preset":                      true,
		plainconn.SettingAllowPlainConnections: true,
	}

	for _, s := range cli.ServeSettings() {
		if s.File == "" {
			continue
		}

		d, ok := registry.Lookup(setting.LevelInstallation, s.File)
		if !ok {
			t.Fatalf("no declaration for the file key %s", s.File)
		}

		if d.Secret != s.Sensitive {
			t.Fatalf("%s: secret = %v, want %v", s.File, d.Secret, s.Sensitive)
		}

		if d.Secret && d.Default != "" {
			t.Fatalf("%s: a secret declaration shows a default", s.File)
		}

		if d.Database != databaseKeys[s.File] {
			t.Fatalf("%s: database layer = %v", s.File, d.Database)
		}
	}
}

// TestAllowPlainConnectionsIsDeclaredAndLockedByTheStaticLayer proves the
// serve setting: the declaration is a bool with the database layer, false by
// default, and a value from the variable locks the key.
func TestAllowPlainConnectionsIsDeclaredAndLockedByTheStaticLayer(t *testing.T) {
	t.Parallel()

	registry, err := setting.NewRegistry(cli.SettingDeclarations())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	d, ok := registry.Lookup(setting.LevelInstallation, plainconn.SettingAllowPlainConnections)
	if !ok || d.Kind != setting.KindBool || d.Default != "false" || !d.Database || d.Apply != setting.ApplyLive {
		t.Fatalf("declaration = %+v, %v; want a bool with the database layer, false by default", d, ok)
	}

	resolved, err := cli.Resolve(serveFlagSet(t), cli.ServeSettings(), mapLookup(map[string]string{
		"DUSTPAN_ALLOW_PLAIN_CONNECTIONS": "false",
	}), cli.ConfigFile{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	layer := cli.NewStaticLayer(resolved)
	checkStatic(t, layer, plainconn.SettingAllowPlainConnections,
		staticWant{setting.SourceEnv, "false", "DUSTPAN_ALLOW_PLAIN_CONNECTIONS"})
}

func TestBrandingPresetDeclaration(t *testing.T) {
	t.Parallel()

	registry, err := setting.NewRegistry(cli.SettingDeclarations())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	d, ok := registry.Lookup(setting.LevelInstallation, "branding.preset")
	if !ok {
		t.Fatal("no declaration for branding.preset")
	}

	for _, good := range []string{"", "high-contrast", "a", "p1"} {
		if err := d.CheckValue(good); err != nil {
			t.Fatalf("CheckValue(%q) = %v", good, err)
		}
	}

	for _, bad := range []string{"High", "-x", "a b", "a_b", strings.Repeat("a", 33), "a\nb"} {
		if err := d.CheckValue(bad); !errors.Is(err, setting.ErrInvalidValue) {
			t.Fatalf("CheckValue(%q) = %v, want ErrInvalidValue", bad, err)
		}
	}
}

func TestStaticLayerGivesSourceAndHidesSecrets(t *testing.T) {
	t.Parallel()

	const canary = "sqlite:/tmp/canary-static-9c2f.db"

	fs := serveFlagSet(t, "--log-level=debug")
	file := cli.ConfigFile{"branding.preset": "from-file", "log-level": "warn"}

	resolved, err := cli.Resolve(fs, cli.ServeSettings(), mapLookup(map[string]string{
		"DUSTPAN_DATABASE": canary, "DUSTPAN_LOG_FORMAT": "text",
	}), file)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	layer := cli.NewStaticLayer(resolved)

	checkStatic(t, layer, "log-level", staticWant{setting.SourceFlag, "debug", "--log-level"})
	checkStatic(t, layer, "log-format", staticWant{setting.SourceEnv, "text", "DUSTPAN_LOG_FORMAT"})
	checkStatic(t, layer, "branding.preset", staticWant{setting.SourceFile, "from-file", "branding.preset"})

	if _, found := layer.Lookup("listen"); found {
		t.Fatal("a default is not a static value")
	}

	// A secret has a source and no text: not in its value and not in its description.
	checkStatic(t, layer, "database", staticWant{setting.SourceEnv, "", "DUSTPAN_DATABASE"})

	db, _ := layer.Lookup("database")
	if strings.Contains(db.By+db.Value, "canary") {
		t.Fatal("the static layer holds a secret")
	}
}

// checkStatic checks the static value of key: its source, its value and a
// part of the text that says which flag, variable or file key set it.
// staticWant is what checkStatic expects of a static value.
type staticWant struct {
	source setting.Source
	value  string
	by     string
}

func checkStatic(t *testing.T, layer *cli.StaticLayer, key string, want staticWant) {
	t.Helper()

	got, found := layer.Lookup(key)
	if !found || got.Source != want.source || got.Value != want.value || !strings.Contains(got.By, want.by) {
		t.Fatalf("%s = %+v, %v; want source %s, value %q and %q in the description",
			key, got, found, want.source, want.value, want.by)
	}
}

func TestStaticLayerReplace(t *testing.T) {
	t.Parallel()

	first, err := cli.Resolve(serveFlagSet(t), cli.ServeSettings(), mapLookup(nil), cli.ConfigFile{"log-level": "warn"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	layer := cli.NewStaticLayer(first)

	if v, ok := layer.Lookup("log-level"); !ok || v.Value != "warn" {
		t.Fatalf("log-level = %+v, %v", v, ok)
	}

	second, err := cli.Resolve(serveFlagSet(t), cli.ServeSettings(), mapLookup(nil), cli.ConfigFile{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	layer.Replace(second)

	if _, ok := layer.Lookup("log-level"); ok {
		t.Fatal("a key that the file dropped is still static")
	}
}

func TestEmptyStaticLayerSetsNothing(t *testing.T) {
	t.Parallel()

	var layer cli.StaticLayer

	if _, ok := layer.Lookup("log-level"); ok {
		t.Fatal("a layer that was never filled sets a key")
	}
}

// TestRetentionSettingsShowTheirSource proves that a configured retention is
// the effective value of the settings resource, with the layer that set it: a
// flag, a variable and a file key each give the value and the source.
func TestRetentionSettingsShowTheirSource(t *testing.T) {
	t.Parallel()

	fs := serveFlagSet(t, "--plan-retention=72h")
	file := cli.ConfigFile{"job-log-retention": "96h"}

	resolved, err := cli.Resolve(fs, cli.ServeSettings(), mapLookup(map[string]string{}), file)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	layer := cli.NewStaticLayer(resolved)

	checkStatic(t, layer, "plan-retention", staticWant{setting.SourceFlag, "72h", "--plan-retention"})
	checkStatic(t, layer, "job-log-retention", staticWant{setting.SourceFile, "96h", "job-log-retention"})

	envResolved, err := cli.Resolve(serveFlagSet(t), cli.ServeSettings(), mapLookup(map[string]string{
		"DUSTPAN_JOB_LOG_RETENTION": "48h",
	}), cli.ConfigFile{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	checkStatic(t, cli.NewStaticLayer(envResolved), "job-log-retention",
		staticWant{setting.SourceEnv, "48h", "DUSTPAN_JOB_LOG_RETENTION"})
}
