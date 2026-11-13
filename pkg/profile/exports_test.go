package profile_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/profile"
)

// proxyDoc returns a document with values for the credentials and the
// exports.proxy map body, indented by four spaces.
func proxyDoc(body string) string {
	return `schemaVersion: 1
providers:
  db: { use: secrets/vault }
values:
  user: admin
  plain: not-a-secret
  computed: "${ values.user }"
  cert: "-----BEGIN CERTIFICATE-----"
  grafana:
    token: { $secret: abc }
  multi: { $secret: "a\r\nX-Evil: 1" }
  colon: "ad:min"
  nested: { a: 1 }
  static: "${ 'static-key' }"
  dollar: "static${"
  staticEntry: { $value: "${ 'static' }" }
  escaped: "$${ values.grafana.token }"
  bearer: "Bearer ${ values.grafana.token }"
  fromProvider: "${ providers.db.password }"
  fromUndeclared: "${ providers.nope.password }"
  fromImport: '${ imports["shared/keys"].values.k }'
  envRead: "${ $env.TOKEN }"
  cycleA: "${ values.cycleB }"
  cycleB: "${ values.cycleA }"
exports:
  proxy:
` + body
}

func TestParseProxyExport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want error
	}{
		{
			"header",
			"    g: { type: header, name: Authorization, value: \"${ values.grafana.token }\" }\n",
			nil,
		},
		{
			"basic",
			"    g: { type: basic, user: \"${ values.user }\", password: \"${ values.grafana.token }\" }\n",
			nil,
		},
		{"query", "    g: { type: query, name: api_key, value: \"${ values.grafana.token }\" }\n", nil},
		{
			"client certificate",
			"    g: { type: client-certificate, certificate: \"${ values.cert }\", key: \"${ values.grafana.token }\" }\n",
			nil,
		},
		{"ca", "    g: { type: ca, certificates: \"${ values.cert }\" }\n", nil},
		{
			"missing path and no imports",
			"    g: { type: header, name: X-Key, value: \"${ values.shared.key }\" }\n",
			profile.ErrInvalid,
		},
		{
			"computed from plain values", "    g: { type: header, name: X-Key, value: \"${ values.computed }\" }\n",
			profile.ErrInvalid,
		},
		{
			"computed from a secret", "    g: { type: header, name: X-Key, value: \"${ values.bearer }\" }\n",
			profile.ErrInvalid,
		},
		{"provider output", "    g: { type: header, name: X-Key, value: \"${ values.fromProvider }\" }\n", nil},
		{
			"output of a provider that is not declared",
			"    g: { type: header, name: X-Key, value: \"${ values.fromUndeclared }\" }\n",
			profile.ErrInvalid,
		},
		{
			"value of an import that is not declared",
			"    g: { type: header, name: X-Key, value: \"${ values.fromImport }\" }\n",
			profile.ErrInvalid,
		},
		{
			"literal expression",
			"    g: { type: header, name: X-Key, value: \"${ values.static }\" }\n",
			profile.ErrInvalid,
		},
		{
			"plain string with ${", "    g: { type: header, name: X-Key, value: \"${ values.dollar }\" }\n",
			profile.ErrInvalid,
		},
		{
			"entry with a literal expression",
			"    g: { type: header, name: X-Key, value: \"${ values.staticEntry }\" }\n",
			profile.ErrInvalid,
		},
		{
			"escaped expression",
			"    g: { type: header, name: X-Key, value: \"${ values.escaped }\" }\n",
			profile.ErrInvalid,
		},
		{"read of env", "    g: { type: header, name: X-Key, value: \"${ values.envRead }\" }\n", profile.ErrInvalid},
		{
			"cycle of plain reads",
			"    g: { type: header, name: X-Key, value: \"${ values.cycleA }\" }\n",
			profile.ErrInvalid,
		},
		{
			"basic user literal", "    g: { type: basic, user: admin, password: \"${ values.grafana.token }\" }\n",
			profile.ErrInvalid,
		},
		{
			"basic password literal", "    g: { type: basic, user: \"${ values.user }\", password: abc }\n",
			profile.ErrInvalid,
		},
		{"query value literal", "    g: { type: query, name: api_key, value: abc }\n", profile.ErrInvalid},
		{
			"client certificate literal",
			"    g: { type: client-certificate, certificate: PEM, key: \"${ values.grafana.token }\" }\n",
			profile.ErrInvalid,
		},
		{
			"client key literal",
			"    g: { type: client-certificate, certificate: \"${ values.cert }\", key: abc }\n",
			profile.ErrInvalid,
		},
		{"ca certificates literal", "    g: { type: ca, certificates: PEM }\n", profile.ErrInvalid},
		{"bracket path", "    g: { type: header, name: X-Key, value: \"${ values[\\\"grafana\\\"].token }\" }\n", nil},
		{
			"unknown type",
			"    g: { type: cookie, name: a, value: \"${ values.grafana.token }\" }\n",
			profile.ErrInvalid,
		},
		{"no type", "    g: { name: a, value: \"${ values.grafana.token }\" }\n", profile.ErrInvalid},
		{
			"type not text",
			"    g: { type: [header], name: a, value: \"${ values.grafana.token }\" }\n",
			profile.ErrInvalid,
		},
		{"missing field", "    g: { type: header, name: Authorization }\n", profile.ErrInvalid},
		{"missing secret field", "    g: { type: basic, user: \"${ values.user }\" }\n", profile.ErrInvalid},
		{
			"field of another type",
			"    g: { type: header, name: A, value: \"${ values.grafana.token }\", user: \"${ values.user }\" }\n",
			profile.ErrInvalid,
		},
		{"secret value that is not a reference", "    g: { type: header, name: A, value: abc }\n", profile.ErrInvalid},
		{
			"secret value with text around the reference",
			"    g: { type: header, name: A, value: \"Bearer ${ values.grafana.token }\" }\n",
			profile.ErrInvalid,
		},
		{
			"two references",
			"    g: { type: header, name: A, value: \"${ values.grafana.token + values.user }\" }\n",
			profile.ErrInvalid,
		},
		{"reference to env", "    g: { type: header, name: A, value: \"${ $env.TOKEN }\" }\n", profile.ErrInvalid},
		{
			"secret field reads a plain value", "    g: { type: header, name: A, value: \"${ values.plain }\" }\n",
			profile.ErrInvalid,
		},
		{"field reads a map", "    g: { type: ca, certificates: \"${ values.nested }\" }\n", profile.ErrInvalid},
		{
			"field reads inside a value",
			"    g: { type: ca, certificates: \"${ values.user.x }\" }\n",
			profile.ErrInvalid,
		},
		{"field is a number", "    g: { type: ca, certificates: 12 }\n", profile.ErrInvalid},
		{"field is a map", "    g: { type: ca, certificates: { a: b } }\n", profile.ErrInvalid},
		{
			"header name with a space",
			"    g: { type: header, name: \"X Key\", value: \"${ values.grafana.token }\" }\n",
			profile.ErrInvalid,
		},
		{
			"header name with CR LF",
			"    g: { type: header, name: \"X\\r\\nY\", value: \"${ values.grafana.token }\" }\n",
			profile.ErrInvalid,
		},
		{
			"header name Host", "    g: { type: header, name: host, value: \"${ values.grafana.token }\" }\n",
			profile.ErrInvalid,
		},
		{
			"empty header name", "    g: { type: header, name: \"\", value: \"${ values.grafana.token }\" }\n",
			profile.ErrInvalid,
		},
		{
			"query name with a slash", "    g: { type: query, name: \"a/b\", value: \"${ values.grafana.token }\" }\n",
			profile.ErrInvalid,
		},
		{
			"header value with CR LF", "    g: { type: header, name: A, value: \"${ values.multi }\" }\n",
			profile.ErrInvalid,
		},
		{
			"basic user with a colon",
			"    g: { type: basic, user: \"${ values.colon }\", password: \"${ values.grafana.token }\" }\n",
			profile.ErrInvalid,
		},
		{"bad credential name", "    Bad.Name: { type: ca, certificates: \"${ values.cert }\" }\n", profile.ErrInvalid},
		{"credential not a map", "    g: header\n", profile.ErrInvalid},
		{"secret entry in proxy", "    g: { type: header, name: A, value: { $secret: abc } }\n", profile.ErrInvalid},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := profile.ParseDocument([]byte(proxyDoc(tc.body)))
			if tc.want == nil && err != nil || tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestParseProxyExportImportPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want error
	}{
		{
			"path that only an import gives",
			"schemaVersion: 1\nimports: [shared/keys]\nexports:\n  proxy:\n" +
				"    g: { type: header, name: X-Key, value: \"${ values.shared.key }\" }\n",
			nil,
		},
		{
			"same path with no imports",
			"schemaVersion: 1\nexports:\n  proxy:\n    g: { type: header, name: X-Key, value: \"${ values.shared.key }\" }\n",
			profile.ErrInvalid,
		},
		{
			"empty imports",
			"schemaVersion: 1\nimports: []\nexports:\n  proxy:\n" +
				"    g: { type: header, name: X-Key, value: \"${ values.shared.key }\" }\n",
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

func TestParseProxyExportShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want error
	}{
		{"proxy not a map", "schemaVersion: 1\nexports:\n  proxy: [a]\n", profile.ErrInvalid},
		{"empty proxy", "schemaVersion: 1\nexports:\n  proxy: {}\n", nil},
		{"proxy over the limit", proxyMap(profile.MaxProxyExports + 1), profile.ErrLimit},
		{"proxy at the limit", proxyMap(profile.MaxProxyExports), nil},
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

// proxyMap returns a document whose exports.proxy holds n credentials.
func proxyMap(n int) string {
	var sb strings.Builder

	sb.WriteString("schemaVersion: 1\nvalues:\n  t: { $secret: abc }\nexports:\n  proxy:\n")

	for i := range n {
		fmt.Fprintf(&sb, "    c%d: { type: header, name: X-Key, value: \"${ values.t }\" }\n", i)
	}

	return sb.String()
}

// allExports is a document with all five export keys set.
const allExports = `schemaVersion: 1
values:
  region: eu-west-1
  token: { $secret: abc }
exports:
  variables: { AWS_REGION: "${ values.region }" }
  files: { KUBECONFIG: "x" }
  pulumi: { "aws:region": "${ values.region }" }
  opentofu: { region: "${ values.region }" }
  proxy:
    grafana-admin: { type: header, name: Authorization, value: "${ values.token }" }
`

func TestExportsFor(t *testing.T) {
	t.Parallel()

	all := []string{"variables", "files", "pulumi", "opentofu", "proxy"}

	tests := []struct {
		consumer profile.Consumer
		want     []string
	}{
		{profile.ConsumerLauncher, []string{"files", "opentofu", "pulumi", "variables"}},
		{profile.ConsumerJobProxy, []string{"proxy"}},
	}

	doc := mustDoc(t, allExports)

	for _, tc := range tests {
		t.Run(string(tc.consumer), func(t *testing.T) {
			t.Parallel()

			got, err := profile.ExportsFor(doc, tc.consumer)
			if err != nil {
				t.Fatalf("ExportsFor: %v", err)
			}

			for _, key := range all {
				_, has := got[key]
				if has != slices.Contains(tc.want, key) {
					t.Errorf("ExportsFor(%s) has %q = %v, want %v", tc.consumer, key, has, !has)
				}
			}

			if len(got) != len(tc.want) {
				t.Errorf("ExportsFor(%s) = %d keys, want %d: %v", tc.consumer, len(got), len(tc.want), got)
			}
		})
	}
}

func TestExportsForProxyValue(t *testing.T) {
	t.Parallel()

	got, err := profile.ExportsFor(mustDoc(t, allExports), profile.ConsumerJobProxy)
	if err != nil {
		t.Fatalf("ExportsFor: %v", err)
	}

	proxy, ok := got["proxy"].(map[string]profile.ProxyExport)
	if !ok {
		t.Fatalf("proxy = %T, want map[string]profile.ProxyExport", got["proxy"])
	}

	want := profile.ProxyExport{
		Type: profile.ProxyHeader, Name: "Authorization", Value: "${ values.token }", User: "", Password: "",
		Certificate: "", Key: "", Certificates: "",
	}
	if proxy["grafana-admin"] != want {
		t.Errorf("proxy[grafana-admin] = %+v, want %+v", proxy["grafana-admin"], want)
	}
}

func TestExportsForFailsClosed(t *testing.T) {
	t.Parallel()

	doc := mustDoc(t, allExports)

	tests := []struct {
		name     string
		doc      *profile.Document
		consumer profile.Consumer
	}{
		{"unknown consumer", doc, profile.Consumer("runner")},
		{"empty consumer", doc, profile.Consumer("")},
		{"consumer in another case", doc, profile.Consumer("Job-Proxy")},
		{"nil document", nil, profile.ConsumerJobProxy},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := profile.ExportsFor(tc.doc, tc.consumer)
			if !errors.Is(err, profile.ErrInvalid) || got != nil {
				t.Fatalf("ExportsFor = %v, %v, want nil, ErrInvalid", got, err)
			}
		})
	}
}

func TestExportsForNoExports(t *testing.T) {
	t.Parallel()

	got, err := profile.ExportsFor(mustDoc(t, "schemaVersion: 1\n"), profile.ConsumerLauncher)
	if err != nil || len(got) != 0 {
		t.Fatalf("ExportsFor = %v, %v, want empty", got, err)
	}
}

func TestRoutingChangesReadsOfExportsProxy(t *testing.T) {
	t.Parallel()

	base := `schemaVersion: 1
values:
  grafana:
    token: "${ values.source }"
  source: { $secret: abc }
  other: { $secret: def }
  note: hello
exports:
  proxy:
    g: { type: header, name: Authorization, value: "${ values.grafana.token }" }
`

	tests := []struct {
		name  string
		after string
		want  []string
	}{
		{"same", base, nil},
		{"value that no export reads", strings.Replace(base, "note: hello", "note: bye", 1), nil},
		{
			"value that proxy reads",
			strings.Replace(base, `token: "${ values.source }"`, `token: "${ values.other }"`, 1),
			[]string{"values.grafana.token"},
		},
		{
			"value that proxy reads through an expression",
			strings.Replace(base, "source: { $secret: abc }", "source: { $secret: xyz }", 1),
			[]string{"values.source"},
		},
		{
			"proxy text",
			strings.Replace(base, "name: Authorization", "name: X-Key", 1),
			[]string{"exports"},
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

// TestRoutingChangesReadsOfExportsFiles sends changes of values that
// exports.files reads straight to RoutingChanges. A value that a file export
// reads, directly or through an expression, decides which secret goes into
// the file, so each change must be a routing change.
func TestRoutingChangesReadsOfExportsFiles(t *testing.T) {
	t.Parallel()

	base := `schemaVersion: 1
values:
  dbPassword: "${ values.source }"
  source: { $secret: abc }
  other: { $secret: def }
  note: hello
exports:
  files:
    DB_PASSWORD_FILE: "${ values.dbPassword }"
`

	tests := []struct {
		name  string
		after string
		want  []string
	}{
		{"same", base, nil},
		{"value that no export reads", strings.Replace(base, "note: hello", "note: bye", 1), nil},
		{
			"value that a file export reads, moved to another secret",
			strings.Replace(base, `dbPassword: "${ values.source }"`, `dbPassword: "${ values.other }"`, 1),
			[]string{"values.dbPassword"},
		},
		{
			"value that a file export reads through an expression",
			strings.Replace(base, "source: { $secret: abc }", "source: { $secret: xyz }", 1),
			[]string{"values.source"},
		},
		{
			"file export text",
			strings.Replace(base, "DB_PASSWORD_FILE", "OTHER_FILE", 1),
			[]string{"exports"},
		},
	}

	before := mustDoc(t, base)

	// Reads that the walk cannot follow by name count as a read of the whole
	// values tree, and a chain is followed to its end.
	chain := `schemaVersion: 1
values:
  a: "${ values.b }"
  b: "${ values.c }"
  c: { $secret: abc }
  note: hello
exports:
  files:
    F: "${ values.a }"
`
	env := strings.Replace(base, `DB_PASSWORD_FILE: "${ values.dbPassword }"`,
		`DB_PASSWORD_FILE: "${ $env.X + values.dbPassword }"`, 1)
	index := strings.Replace(base, `DB_PASSWORD_FILE: "${ values.dbPassword }"`,
		`DB_PASSWORD_FILE: "${ values[values.key] }"`, 1)
	index = strings.Replace(index, "note: hello", "note: hello\n  key: dbPassword", 1)

	for _, tc := range []struct {
		name, before, after string
		want                []string
	}{
		{
			"a chain of three values, with a change at its end",
			chain, strings.Replace(chain, "c: { $secret: abc }", "c: { $secret: xyz }", 1),
			[]string{"values.c"},
		},
		{
			"an expression with $env and a value, with a change of another value",
			env, strings.Replace(env, "note: hello", "note: bye", 1),
			[]string{"values"},
		},
		{
			"a computed index, with a change of another value",
			index, strings.Replace(index, "note: hello", "note: bye", 1),
			[]string{"values"},
		},
	} {
		got := profile.RoutingChanges(mustDoc(t, tc.before), mustDoc(t, tc.after))
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s: RoutingChanges() = %v, want %v", tc.name, got, tc.want)
		}
	}

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

// TestRoutingChangesReadsOfEachLauncherExport sends, for each export that
// the launcher gets, a change of the value that the export reads straight to
// RoutingChanges. The value decides which secret goes into the environment,
// a file or the configuration of the tool, so each change must be a routing
// change.
func TestRoutingChangesReadsOfEachLauncherExport(t *testing.T) {
	t.Parallel()

	exports := map[string]string{
		"variables": "  variables:\n    DB_PASSWORD: \"${ values.secretRef }\"\n",
		"files":     "  files:\n    DB_PASSWORD_FILE: \"${ values.secretRef }\"\n",
		"pulumi":    "  pulumi:\n    \"app:dbPassword\": \"${ values.secretRef }\"\n",
		"opentofu":  "  opentofu:\n    db_password: \"${ values.secretRef }\"\n",
	}

	for name, export := range exports {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			base := "schemaVersion: 1\nvalues:\n  secretRef: \"${ values.source }\"\n" +
				"  source: { $secret: abc }\n  other: { $secret: def }\n  note: hello\nexports:\n" + export
			before := mustDoc(t, base)

			tests := []struct {
				name, after string
				want        []string
			}{
				{"value that no export reads", strings.Replace(base, "note: hello", "note: bye", 1), nil},
				{
					"value moved to another secret",
					strings.Replace(base, `secretRef: "${ values.source }"`, `secretRef: "${ values.other }"`, 1),
					[]string{"values.secretRef"},
				},
				{
					"value read through an expression",
					strings.Replace(base, "source: { $secret: abc }", "source: { $secret: xyz }", 1),
					[]string{"values.source"},
				},
			}

			for _, tc := range tests {
				got := profile.RoutingChanges(before, mustDoc(t, tc.after))
				if strings.Join(got, ",") != strings.Join(tc.want, ",") {
					t.Errorf("%s: RoutingChanges() = %v, want %v", tc.name, got, tc.want)
				}
			}
		})
	}
}

func FuzzParseProxyExport(f *testing.F) {
	f.Add(proxyMap(profile.MaxProxyExports), "header", "name", "X-Key", "value", "${ values.t }")
	f.Add(proxyMap(profile.MaxProxyExports+1), "header", "name", "Authorization", "value", "${ values[\"t\"] }")
	f.Add(proxyMap(2), "basic", "user", "a\r\nb", "password", "abc")
	f.Add(proxyMap(1), "client-certificate", "certificate", "${ values.t }", "key", "${ values.t }")
	f.Add(proxyMap(1), "ca", "certificates", "${ values.t }", "extra", "x")
	f.Add(proxyMap(1), "query", "name", "k", "value", "${ 'static' }")

	f.Fuzz(func(t *testing.T, base, typ, key1, val1, key2, val2 string) {
		doc := base + fmt.Sprintf("    fuzz: { type: %q, %q: %q, %q: %q }\n", typ, key1, val1, key2, val2)

		d, err := profile.ParseDocument([]byte(doc))
		if err != nil {
			return
		}

		got, err := profile.ExportsFor(d, profile.ConsumerJobProxy)
		if err != nil {
			t.Fatalf("ExportsFor after a parse that passed: %v", err)
		}

		checkFuzzProxy(t, got)

		launcher, err := profile.ExportsFor(d, profile.ConsumerLauncher)
		if err != nil {
			t.Fatalf("ExportsFor(launcher): %v", err)
		}

		if _, has := launcher["proxy"]; has {
			t.Fatal("the launcher got proxy")
		}
	})
}

// checkFuzzProxy checks the proxy exports of a document that passed the
// parse.
func checkFuzzProxy(t *testing.T, got profile.Exports) {
	t.Helper()

	proxy, ok := got["proxy"].(map[string]profile.ProxyExport)
	if !ok {
		t.Fatalf("proxy = %T, want map[string]profile.ProxyExport", got["proxy"])
	}

	if len(proxy) > profile.MaxProxyExports {
		t.Fatalf("proxy holds %d credentials, the limit is %d", len(proxy), profile.MaxProxyExports)
	}

	for k := range proxy {
		e := proxy[k]
		if strings.ContainsAny(e.Name, "\r\n") {
			t.Fatalf("credential %q passed with name %q", k, e.Name)
		}

		for _, ref := range []string{e.Value, e.User, e.Password, e.Certificate, e.Key, e.Certificates} {
			if ref != "" && !strings.HasPrefix(ref, "${") {
				t.Fatalf("credential %q of type %q passed with the literal %q", k, e.Type, ref)
			}
		}
	}
}
