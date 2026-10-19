package profile_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/profile"
)

func TestParseDocument(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want error
	}{
		{"minimal", "schemaVersion: 1\n", nil},
		{
			"values and entries",
			"schemaVersion: 1\nvalues:\n  region: eu-west-1\n  token:\n    $secret: abc\n    $neverStore: true\n",
			nil,
		},
		{"empty", "", profile.ErrInvalid},
		{"not a map", "- a\n", profile.ErrInvalid},
		{"no schema version", "values: {}\n", profile.ErrInvalid},
		{"higher schema version", "schemaVersion: 2\n", profile.ErrSchemaVersion},
		{"unknown top-level key", "schemaVersion: 1\nfoo: 1\n", profile.ErrInvalid},
		{"duplicate key", "schemaVersion: 1\nvalues:\n  a: 1\n  a: 2\n", profile.ErrInvalid},
		{"alias", "schemaVersion: 1\nvalues:\n  a: &x 1\n  b: *x\n", profile.ErrInvalid},
		{"two documents", "schemaVersion: 1\n---\nschemaVersion: 1\n", profile.ErrInvalid},
		{
			"entry with two forms",
			"schemaVersion: 1\nvalues:\n  a:\n    $value: 1\n    $secret: x\n",
			profile.ErrInvalid,
		},
		{"entry with no form", "schemaVersion: 1\nvalues:\n  a:\n    $final: true\n", profile.ErrInvalid},
		{"entry mixed with user key", "schemaVersion: 1\nvalues:\n  a:\n    $value: 1\n    b: 2\n", profile.ErrInvalid},
		{"unknown entry key", "schemaVersion: 1\nvalues:\n  a:\n    $value: 1\n    $bogus: 2\n", profile.ErrInvalid},
		{
			"final is not a boolean",
			"schemaVersion: 1\nvalues:\n  a:\n    $value: 1\n    $final: yes please\n",
			profile.ErrInvalid,
		},
		{
			"never store on a plain value",
			"schemaVersion: 1\nvalues:\n  a:\n    $value: 1\n    $neverStore: true\n",
			profile.ErrInvalid,
		},
		{
			"bad type kind",
			"schemaVersion: 1\nvalues:\n  a:\n    $value: 1\n    $type: {kind: blob}\n",
			profile.ErrInvalid,
		},
		{"provider without use", "schemaVersion: 1\nproviders:\n  a: {}\n", profile.ErrInvalid},
		{
			"secret under providers",
			"schemaVersion: 1\nproviders:\n  a:\n    use: login/x\n    with: { token: { $secret: abc } }\n",
			profile.ErrInvalid,
		},
		{
			"sealed under providers",
			"schemaVersion: 1\nproviders:\n  a:\n    use: login/x\n    with: { token: { $sealed: abc } }\n",
			profile.ErrInvalid,
		},
		{
			"secret under rotators",
			"schemaVersion: 1\nrotators:\n  a:\n    use: rotate/x\n    with: { token: { $secret: abc } }\n",
			profile.ErrInvalid,
		},
		{
			"secret under exports variables",
			"schemaVersion: 1\nexports:\n  variables:\n    TOKEN: { $secret: abc }\n",
			profile.ErrInvalid,
		},
		{"export key", "schemaVersion: 1\nexports:\n  bogus: {}\n", profile.ErrInvalid},
		{"import with bad name", "schemaVersion: 1\nimports: [Bad Name]\n", profile.ErrInvalid},
		{"import with tag", "schemaVersion: 1\nimports: [aws/prod@stable]\n", nil},
		{
			"document over the limit",
			"schemaVersion: 1\ndescription: " + strings.Repeat("a", profile.MaxDocumentBytes),
			profile.ErrLimit,
		},
		{
			"description over the limit",
			"schemaVersion: 1\ndescription: " + strings.Repeat("a", 1001) + "\n",
			profile.ErrInvalid,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := profile.ParseDocument([]byte(tc.in))
			if tc.want == nil && err != nil || tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestEntries(t *testing.T) {
	t.Parallel()

	doc := mustDoc(t, `schemaVersion: 1
values:
  region: eu-west-1
  db:
    host: db.internal
    password: { $secret: hunter2, $description: Admin password }
  tags: [a, b]
  a.b: dotted
  key:
    $value: v
    $final: true
    $type: { kind: string }
  never:
    $secret: x
    $neverStore: true
`)

	got := map[string]profile.Entry{}
	for _, e := range doc.Entries() {
		got[e.Path] = e
	}

	tests := []struct {
		path       string
		secret     bool
		final      bool
		neverStore bool
	}{
		{"region", false, false, false},
		{"db.host", false, false, false},
		{"db.password", true, false, false},
		{"tags", false, false, false},
		{`a\.b`, false, false, false},
		{"key", false, true, false},
		{"never", true, false, true},
	}

	if len(got) != len(tests) {
		t.Fatalf("got %d entries, want %d: %+v", len(got), len(tests), got)
	}

	for _, tc := range tests {
		e, ok := got[tc.path]
		if !ok {
			t.Errorf("no entry at %q", tc.path)

			continue
		}

		if e.Secret != tc.secret || e.Final != tc.final || e.NeverStore != tc.neverStore {
			t.Errorf("%s = %+v, want secret %v final %v neverStore %v", tc.path, e, tc.secret, tc.final, tc.neverStore)
		}

		if e.Secret && e.Value != nil {
			t.Errorf("%s: a secret entry holds a value", tc.path)
		}
	}

	if got["key"].Kind != "string" {
		t.Errorf("kind = %q, want string", got["key"].Kind)
	}
}

func TestSealReplacesSecretsAndKeepsTheRest(t *testing.T) {
	t.Parallel()

	doc := mustDoc(t, "# keep this comment\nschemaVersion: 1\nvalues:\n  a: 1\n  s:\n    $secret: hunter2\n")

	text, err := doc.Seal(func(plain [][]byte) ([][]byte, error) {
		out := make([][]byte, len(plain))
		for i, p := range plain {
			out[i] = append([]byte("sealed:"), p...)
		}

		return out, nil
	})
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	s := string(text)
	if strings.Contains(s, "hunter2") || strings.Contains(s, "$secret") {
		t.Errorf("sealed text still holds the secret:\n%s", s)
	}

	if !strings.Contains(s, "$sealed") || !strings.Contains(s, "# keep this comment") {
		t.Errorf("sealed text lost the sealed key or the comment:\n%s", s)
	}

	back := mustDoc(t, s)

	secrets, err := back.Secrets()
	if err != nil || len(secrets) != 1 || string(secrets[0].Sealed) != "sealed:hunter2" {
		t.Errorf("Secrets() = %+v, %v", secrets, err)
	}
}

func TestSealWithoutSecretsKeepsTheText(t *testing.T) {
	t.Parallel()

	in := "schemaVersion: 1\nvalues:   {a:    1}\n"

	text, err := mustDoc(t, in).Seal(func([][]byte) ([][]byte, error) {
		t.Error("sealer called for a document with no secret")

		return nil, nil
	})
	if err != nil || string(text) != in {
		t.Errorf("Seal() = %q, %v, want the text as written", text, err)
	}
}

func TestRoutingChanges(t *testing.T) {
	t.Parallel()

	base := `schemaVersion: 1
providers:
  vault:
    use: login/vault
    with: { address: "${ values.vaultHost }", role: payments }
  other:
    use: secrets/vault
    with: { path: "${ values.paths.db }" }
values:
  vaultHost: https://vault.internal
  region: eu-west-1
  paths: { db: secret/db, cache: secret/cache }
  note: hello
exports:
  variables: { AWS_REGION: "${ values.region }" }
`

	tests := []struct {
		name  string
		after string
		want  []string
	}{
		{"same", base, nil},
		{"plain value no block reads", strings.Replace(base, "note: hello", "note: bye", 1), nil},
		{
			"value that a block reads",
			strings.Replace(base, "vault.internal", "evil.example", 1),
			[]string{"values.vaultHost"},
		},
		{"value under a read map", strings.Replace(base, "secret/db", "secret/other", 1), []string{"values.paths.db"}},
		{"value beside a read leaf", strings.Replace(base, "secret/cache", "secret/c2", 1), nil},
		{"block text", strings.Replace(base, "role: payments", "role: admin", 1), []string{"providers.vault"}},
		{"exports", strings.Replace(base, "AWS_REGION", "REGION", 1), []string{"exports"}},
		{
			"new import",
			strings.Replace(base, "providers:", "imports: [shared/tags]\nproviders:", 1),
			[]string{"imports"},
		},
		{
			"removed block",
			strings.Replace(
				base,
				"  other:\n    use: secrets/vault\n    with: { path: \"${ values.paths.db }\" }\n",
				"",
				1,
			),
			[]string{"providers.other"},
		},
	}

	before := mustDoc(t, base)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := profile.RoutingChanges(before, mustDoc(t, tc.after))
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("RoutingChanges() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRoutingChangesFollowsExpressionsAndNewDocuments(t *testing.T) {
	t.Parallel()

	chain := `schemaVersion: 1
providers:
  p: { use: login/x, with: { url: "${ values.a }" } }
values:
  a: "${ values.b }"
  b: one
`

	got := profile.RoutingChanges(mustDoc(t, chain), mustDoc(t, strings.Replace(chain, "b: one", "b: two", 1)))
	if strings.Join(got, ",") != "values.b" {
		t.Errorf("transitive read: got %v, want [values.b]", got)
	}

	if got = profile.RoutingChanges(nil, mustDoc(t, chain)); len(got) == 0 {
		t.Error("a new document with a provider block has no routing change")
	}

	if got = profile.RoutingChanges(nil, mustDoc(t, "schemaVersion: 1\nvalues: {a: 1}\n")); len(got) != 0 {
		t.Errorf("a new document with only values has routing changes: %v", got)
	}

	computed := `schemaVersion: 1
providers:
  p: { use: login/x, with: { url: "${ values[context.unit] }" } }
values:
  m: { x: 1 }
`
	got = profile.RoutingChanges(mustDoc(t, computed), mustDoc(t, strings.Replace(computed, "x: 1", "x: 2", 1)))
	if len(got) == 0 {
		t.Error("a computed index must count as a read of the whole tree")
	}
}

func TestRoutingChangesSeesHiddenReads(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		url  string
	}{
		{"brace in a string", `${ "}" + values.host }`},
		{"computed name through env", `${ $env["val" + "ues"].host }`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			text := "schemaVersion: 1\nproviders:\n  p: { use: login/x, with: { url: '" + tc.url +
				"' } }\nvalues:\n  host: a\n"
			got := profile.RoutingChanges(mustDoc(t, text), mustDoc(t, strings.Replace(text, "host: a", "host: b", 1)))

			if len(got) == 0 {
				t.Error("a change of values.host is not a routing change")
			}
		})
	}
}

func mustDoc(t *testing.T, text string) *profile.Document {
	t.Helper()

	d, err := profile.ParseDocument([]byte(text))
	if err != nil {
		t.Fatalf("ParseDocument: %v\n%s", err, text)
	}

	return d
}
