package dustpan

import (
	"testing"
)

// TestNewDurableRecorderNeedsItsStores proves that a server without a job
// store, a blob store or an update store builds no recorder, so the step and
// log paths of a job session answer not-found and no launcher writes a byte.
func TestNewDurableRecorderNeedsItsStores(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		set  func(*settings)
	}{
		{name: "no stores", set: func(*settings) {}},
		{name: "no job store", set: func(o *settings) { o.blobStore = nil }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := defaultSettings()
			tc.set(cfg)

			got, ok, err := newDurableRecorder(cfg, nil)
			if err != nil {
				t.Fatalf("newDurableRecorder: %v", err)
			}

			if ok || got != nil {
				t.Fatalf("newDurableRecorder = %v, %t, want no recorder", got, ok)
			}
		})
	}
}
