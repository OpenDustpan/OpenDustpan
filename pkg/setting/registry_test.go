package setting_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/setting"
	"opendustpan.org/opendustpan/pkg/setting/settingtest"
)

func validDecl() setting.Declaration {
	return setting.Declaration{
		Key: "a.b", Level: setting.LevelInstallation, Kind: setting.KindInt, Default: "1", Secret: false,
		Database: true, Apply: setting.ApplyLive, Check: nil, ReducesSecurity: nil, Doc: "A setting.",
	}
}

func TestDeclarationValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(d *setting.Declaration)
		ok     bool
	}{
		{"valid", func(*setting.Declaration) {}, true},
		{"empty key", func(d *setting.Declaration) { d.Key = "" }, false},
		{"upper-case start", func(d *setting.Declaration) { d.Key = "A" }, false},
		{"empty word", func(d *setting.Declaration) { d.Key = "a..b" }, false},
		{"a key with a space", func(d *setting.Declaration) { d.Key = "a b" }, false},
		{
			"a key that is too long",
			func(d *setting.Declaration) { d.Key = strings.Repeat("a", setting.MaxKeyBytes+1) },
			false,
		},
		{"no level", func(d *setting.Declaration) { d.Level = "" }, false},
		{"no apply", func(d *setting.Declaration) { d.Apply = "" }, false},
		{"no doc", func(d *setting.Declaration) { d.Doc = "" }, false},
		{"a secret in the database layer", func(d *setting.Declaration) { d.Secret = true }, false},
		{"a namespace setting with no database layer", func(d *setting.Declaration) {
			d.Level, d.Database = setting.LevelNamespace, false
		}, false},
		{"a default that fails the kind", func(d *setting.Declaration) { d.Default = "x" }, false},
		{"a static-only setting needs no valid default", func(d *setting.Declaration) {
			d.Database, d.Default = false, "x"
		}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			d := validDecl()
			tc.mutate(&d)

			err := d.Validate()
			if tc.ok != (err == nil) || (err != nil && !errors.Is(err, setting.ErrInvalidDeclaration)) {
				t.Fatalf("Validate = %v, want ok = %v", err, tc.ok)
			}
		})
	}
}

func TestNewRegistryRefusesAKeyTwice(t *testing.T) {
	t.Parallel()

	_, err := setting.NewRegistry([]setting.Declaration{validDecl(), validDecl()})
	if !errors.Is(err, setting.ErrInvalidDeclaration) {
		t.Fatalf("NewRegistry = %v, want ErrInvalidDeclaration", err)
	}

	other := validDecl()
	other.Level = setting.LevelNamespace

	if _, err := setting.NewRegistry([]setting.Declaration{validDecl(), other}); err != nil {
		t.Fatalf("one key at two levels: %v", err)
	}
}

func TestEffectivePrecedence(t *testing.T) {
	t.Parallel()

	d := validDecl()
	file := &setting.StaticValue{Source: setting.SourceFile, Value: "3", By: "the file"}
	two := "2"

	tests := []struct {
		name     string
		stored   *string
		static   *setting.StaticValue
		value    string
		source   setting.Source
		locked   bool
		shadowed bool
	}{
		{"default", nil, nil, "1", setting.SourceDefault, false, false},
		{"database over default", &two, nil, "2", setting.SourceDatabase, false, false},
		{"static over default", nil, file, "3", setting.SourceFile, true, false},
		{"static over database", &two, file, "3", setting.SourceFile, true, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			it := setting.Effective(d, tc.stored, tc.static)
			if it.Value != tc.value || it.Source != tc.source || it.Locked != tc.locked || it.Shadowed != tc.shadowed {
				t.Fatalf("item = %+v", it)
			}
		})
	}
}

func TestEffectiveOfAStaticOnlySettingIsLocked(t *testing.T) {
	t.Parallel()

	d := validDecl()
	d.Database = false

	// A stored value of a key that the database layer cannot hold, for
	// example after a downgrade, never wins.
	stored := "9"
	it := setting.Effective(d, &stored, nil)
	if it.Value != "1" || it.Source != setting.SourceDefault || !it.Locked || it.LockedBy == "" || it.HasDatabaseValue {
		t.Fatalf("item = %+v", it)
	}
}

func TestDigestHoldsNoSecretAndChangesWithTheLayer(t *testing.T) {
	t.Parallel()

	reg := settingtest.Registry()
	secretA := settingtest.StaticMap{settingtest.KeySecret: {Source: setting.SourceEnv, Value: "aaa", By: ""}}
	secretB := settingtest.StaticMap{settingtest.KeySecret: {Source: setting.SourceEnv, Value: "bbb", By: ""}}

	if reg.Digest(secretA) != reg.Digest(secretB) {
		t.Fatal("the digest depends on the value of a secret")
	}

	if reg.Digest(secretA) == reg.Digest(settingtest.StaticMap{}) {
		t.Fatal("the digest ignores that a secret is set")
	}

	a := settingtest.StaticMap{settingtest.KeyLimit: settingtest.File("1")}
	b := settingtest.StaticMap{settingtest.KeyLimit: settingtest.File("2")}

	if reg.Digest(a) == reg.Digest(b) {
		t.Fatal("the digest ignores a changed value")
	}

	moved := settingtest.StaticMap{
		settingtest.KeyLimit: {Source: setting.SourceEnv, Value: "1", By: ""},
	}
	if reg.Digest(a) == reg.Digest(moved) {
		t.Fatal("the digest ignores a changed source")
	}
}
