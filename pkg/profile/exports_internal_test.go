package profile

import (
	"errors"
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

// TestCheckSecretTarget sends each target straight to the check of a secret
// field, as if the parse of exports.proxy had let it through.
func TestCheckSecretTarget(t *testing.T) {
	t.Parallel()

	d, err := ParseDocument([]byte(`schemaVersion: 1
values:
  plain: abc
  literal: "${ 'static-key' }"
  dollar: "static${"
  escaped: "$${ values.secret }"
  entry: { $value: "${ 'static' }" }
  secret: { $secret: abc }
  bearer: "Bearer ${ values.secret }"
  provider: "${ providers.db.password }"
  other: '${ profiles["shared/keys"].values.k }'
  env: "${ $env.TOKEN }"
  loopA: "${ values.loopB }"
  loopB: "${ values.loopA }"
  list: ["${ values.secret }"]
`))
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}

	tests := []struct {
		path string
		ok   bool
	}{
		{"plain", false},
		{"literal", false},
		{"dollar", false},
		{"escaped", false},
		{"entry", false},
		{"env", false},
		{"loopA", false},
		{"secret", true},
		{"bearer", true},
		{"provider", true},
		{"other", true},
		{"list", true},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()

			err := d.checkSecretTarget("exports.proxy.g.value", d.nodeAt([]string{tc.path}))
			if tc.ok && err != nil || !tc.ok && !errors.Is(err, ErrInvalid) {
				t.Fatalf("checkSecretTarget(%s) = %v, want ok %v", tc.path, err, tc.ok)
			}
		})
	}
}
