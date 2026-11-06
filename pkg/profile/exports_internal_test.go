package profile

import (
	"errors"
	"fmt"
	"testing"
)

// TestProxyOnlyForJobProxy sends each consumer and each export key straight
// to the defense layer, as if the allowlist of the consumers gave proxy to
// every consumer.
func TestProxyOnlyForJobProxy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		consumer Consumer
		key      string
		want     bool
	}{
		{ConsumerJobProxy, exportProxy, true},
		{ConsumerLauncher, exportProxy, false},
		{Consumer("runner"), exportProxy, false},
		{Consumer(""), exportProxy, false},
		{ConsumerLauncher, exportVariables, true},
	}

	for _, tc := range tests {
		t.Run(string(tc.consumer)+"/"+tc.key, func(t *testing.T) {
			t.Parallel()

			if got := proxyOnlyForJobProxy(tc.consumer, tc.key); got != tc.want {
				t.Errorf("proxyOnlyForJobProxy(%q, %q) = %v, want %v", tc.consumer, tc.key, got, tc.want)
			}
		})
	}
}

// secretTargetDoc is the document of TestCheckSecretTarget. Each key under
// values is one target. head goes after schemaVersion, for example an
// import, so that a missing path can come from that import.
func secretTargetDoc(t *testing.T, head string) *Document {
	t.Helper()

	text := "schemaVersion: 1\n" + head + `values:
  s: { $secret: abc }
  plain: abc
  literal: "${ 'static-key' }"
  dollar: "static${"
  escaped: "$${ values.s }"
  entry: { $value: "${ 'static' }" }
  bearer: "Bearer ${ values.s }"
  provider: "${ providers.db.password }"
  rotator: "${ rotators.db.password }"
  other: '${ profiles["shared/keys"].values.k }'
  imported: '${ imports["shared/keys"].values.k }'
  undeclared: '${ imports["other/keys"].values.k }'
  taggedImport: '${ imports["shared/keys@stable"].values.k }'
  chain: "${ values.chainEntry }"
  chainEntry: { $value: "${ values.s }" }
  env: "${ $env.TOKEN }"
  loopA: "${ values.loopB }"
  loopB: "${ values.loopA }"
  list: ["${ values.s }"]
  ternary: "${ true ? 'static-key' : values.s }"
  compare: "${ values.s != '' ? 'static-key' : 'x' }"
  replaced: "${ replace(values.s, values.s, 'k') }"
  indexed: "${ {'a': 'static-key'}[values.s] }"
  sliced: "${ 'static-key' + values.s[0:0] }"
  prefixed: "static-key${ values.s }"
  twoParts: "${ 'static-key' } ${ providers.x }"
  quotedProvider: "${ 'providers.x' }"
  quotedImport: "${ 'imports[' && 'static-key' }"
  coalesce: "${ values.nope ?? 'static-key' }"
  suffixed: "${ values.nope }static-key"
  missing: "${ values.x.y }"
  inside: "${ values.plain.x }"
`

	d, err := ParseDocument([]byte(text))
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}

	return d
}

// TestCheckSecretTarget sends each target straight to the check of a secret
// field, as if the parse of exports.proxy had let it through.
func TestCheckSecretTarget(t *testing.T) {
	t.Parallel()

	refused := []string{
		"plain", "literal", "dollar", "escaped", "entry", "bearer", "env", "loopA", "list", "ternary",
		"compare", "replaced", "indexed", "sliced", "prefixed", "twoParts", "quotedProvider", "quotedImport",
		"coalesce", "suffixed", "inside", "undeclared", "taggedImport",
	}
	accepted := []string{"s", "provider", "rotator", "other", "chain"}

	type targetCase struct {
		name    string
		imports bool
		path    string
		ok      bool
	}

	tests := make([]targetCase, 0, 4+2*(len(refused)+len(accepted)))
	tests = append(tests,
		targetCase{"missing path with no imports", false, "missing", false},
		targetCase{"missing path with imports", true, "missing", true},
		targetCase{"declared import with no imports", false, "imported", false},
		targetCase{"declared import", true, "imported", true},
	)

	for _, imports := range []bool{false, true} {
		for _, p := range refused {
			tests = append(tests, targetCase{fmt.Sprintf("%s imports %v", p, imports), imports, p, false})
		}

		for _, p := range accepted {
			tests = append(tests, targetCase{fmt.Sprintf("%s imports %v", p, imports), imports, p, true})
		}
	}

	docs := map[bool]*Document{false: secretTargetDoc(t, ""), true: secretTargetDoc(t, "imports: [shared/keys]\n")}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			d := docs[tc.imports]

			err := d.checkSecretTarget("exports.proxy.g.value", d.nodeAt([]string{tc.path}))
			if tc.ok && err != nil || !tc.ok && !errors.Is(err, ErrInvalid) {
				t.Fatalf("checkSecretTarget(%s) = %v, want ok %v", tc.path, err, tc.ok)
			}
		})
	}
}
