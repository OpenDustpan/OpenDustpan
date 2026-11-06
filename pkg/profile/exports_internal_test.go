package profile

import "testing"

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
