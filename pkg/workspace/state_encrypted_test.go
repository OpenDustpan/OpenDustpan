package workspace_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/workspace"
)

// TestStateClientEncrypted proves that the service tells a state the client
// encrypted from a plain one, for the version it names and for no other, and
// that an unknown version fails closed.
func TestStateClientEncrypted(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	if _, err := f.svc.Lock(t.Context(), f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	pushed := func(serial int64, content string) workspace.StateVersionID {
		t.Helper()

		if err := f.push(t, newState("lineage-1", serial, content), content); err != nil {
			t.Fatalf("push serial %d: %v", serial, err)
		}

		sv, err := f.svc.Current(t.Context(), f.ws.ID)
		if err != nil {
			t.Fatalf("current: %v", err)
		}

		return sv.ID
	}

	plain := pushed(1, `{"serial":1,"lineage":"lineage-1","outputs":{}}`)
	encrypted := pushed(2, `{"serial":2,"lineage":"lineage-1","encrypted_data":"aa"}`)
	again := pushed(3, `{"serial":3,"lineage":"lineage-1","outputs":{}}`)

	tests := []struct {
		name string
		id   workspace.StateVersionID
		want bool
	}{
		{name: "a plain state", id: plain, want: false},
		{name: "a state the client encrypted", id: encrypted, want: true},
		{name: "a plain state after an encrypted one", id: again, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := f.svc.StateClientEncrypted(t.Context(), tt.id)
			if err != nil {
				t.Fatalf("StateClientEncrypted: %v", err)
			}

			if got != tt.want {
				t.Errorf("StateClientEncrypted = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("an unknown version fails closed", func(t *testing.T) {
		t.Parallel()

		got, err := f.svc.StateClientEncrypted(t.Context(), "sv-unknown")
		if !errors.Is(err, workspace.ErrStateVersionNotFound) || got {
			t.Errorf("StateClientEncrypted = %v, %v; want false and ErrStateVersionNotFound", got, err)
		}
	})
}
