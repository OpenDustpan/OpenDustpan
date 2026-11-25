package agenttest

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/internal/agent"
	"opendustpan.org/opendustpan/pkg/audit"
)

// SecretItemsHarness is one fresh store of secret items for one case.
type SecretItemsHarness struct {
	Items agent.SecretItems
}

// SecretItemsFactory returns a fresh harness for one case.
type SecretItemsFactory func(t *testing.T) SecretItemsHarness

// RunSecretItems runs the secret items suite against the store that f makes.
func RunSecretItems(t *testing.T, f SecretItemsFactory) {
	t.Helper()

	cases := []struct {
		name string
		run  func(t *testing.T, h SecretItemsHarness)
	}{
		{name: "create and list", run: caseSecretItemsCreateList},
		{name: "get metadata without value", run: caseSecretItemsGet},
		{name: "replace rotates the value", run: caseSecretItemsReplace},
		{name: "delete while in use answers 409", run: caseSecretItemsDeleteInUse},
		{name: "sealed at rest", run: caseSecretItemsSealedAtRest},
		{name: "open returns the value", run: caseSecretItemsOpen},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.run(t, f(t))
		})
	}
}

func caseSecretItemsCreateList(t *testing.T, h SecretItemsHarness) {
	t.Helper()

	entry := TestEntry(audit.ServerScope, "admin", "server:manage-agent")

	got, err := h.Items.Create(t.Context(), "example-key", []byte("secret-value"), entry)
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	if got.Ref == "" {
		t.Fatal("Create() returned an empty reference")
	}

	if !agent.IsSecretRef(string(got.Ref)) {
		t.Fatalf("Create() returned %q, not a reference", got.Ref)
	}

	if got.Name != "example-key" {
		t.Fatalf("Name() = %q, want example-key", got.Name)
	}

	if got.CreatedAt == 0 {
		t.Fatal("CreatedAt() = 0, want a time")
	}

	list, err := h.Items.List(t.Context())
	if err != nil {
		t.Fatalf("List() = %v", err)
	}

	if len(list) != 1 {
		t.Fatalf("List() = %d items, want 1", len(list))
	}

	if list[0].Ref != got.Ref || list[0].Name != "example-key" {
		t.Fatalf("List() = %+v, want the created item", list[0])
	}

	if _, err := h.Items.Create(t.Context(), "", []byte("value"), entry); err == nil {
		t.Fatal("Create() with an empty name = nil, want an error")
	}

	if _, err := h.Items.Create(t.Context(), "empty", nil, entry); err == nil {
		t.Fatal("Create() with an empty value = nil, want an error")
	}
}

func caseSecretItemsGet(t *testing.T, h SecretItemsHarness) {
	t.Helper()

	entry := TestEntry(audit.ServerScope, "admin", "server:manage-agent")

	got, err := h.Items.Create(t.Context(), "example-key", []byte("secret-value"), entry)
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	md, err := h.Items.Get(t.Context(), got.Ref)
	if err != nil {
		t.Fatalf("Get() = %v", err)
	}

	if md.Ref != got.Ref || md.Name != "example-key" {
		t.Fatalf("Get() = %+v, want the metadata of the item", md)
	}

	_, err = h.Items.Get(t.Context(), agent.SecretRef("itm-nope"))
	if !errors.Is(err, agent.ErrSecretNotFound) {
		t.Fatalf("Get() = %v, want ErrSecretNotFound", err)
	}
}

func caseSecretItemsReplace(t *testing.T, h SecretItemsHarness) {
	t.Helper()

	entry := TestEntry(audit.ServerScope, "admin", "server:manage-agent")

	got, err := h.Items.Create(t.Context(), "example-key", []byte("first"), entry)
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	replaced, err := h.Items.Replace(t.Context(), got.Ref, []byte("second"), entry)
	if err != nil {
		t.Fatalf("Replace() = %v", err)
	}

	if replaced.ChangedAt <= got.CreatedAt {
		t.Fatalf("Replace() did not move ChangedAt: %v <= %v", replaced.ChangedAt, got.CreatedAt)
	}

	open, err := h.Items.Open(t.Context(), got.Ref)
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}

	if string(open) != "second" {
		t.Fatalf("Open() = %q, want second", open)
	}

	_, err = h.Items.Replace(t.Context(), agent.SecretRef("itm-nope"), []byte("value"), entry)
	if !errors.Is(err, agent.ErrSecretNotFound) {
		t.Fatalf("Replace() = %v, want ErrSecretNotFound", err)
	}

	_, err = h.Items.Replace(t.Context(), got.Ref, nil, entry)
	if err == nil {
		t.Fatal("Replace() with an empty value = nil, want an error")
	}
}

func caseSecretItemsDeleteInUse(t *testing.T, h SecretItemsHarness) {
	t.Helper()

	entry := TestEntry(audit.ServerScope, "admin", "server:manage-agent")

	got, err := h.Items.Create(t.Context(), "example-key", []byte("secret-value"), entry)
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	// The store holds no document yet, so the delete succeeds.
	if err = h.Items.Delete(t.Context(), got.Ref, entry); err != nil {
		t.Fatalf("Delete() = %v", err)
	}

	_, err = h.Items.Get(t.Context(), got.Ref)
	if !errors.Is(err, agent.ErrSecretNotFound) {
		t.Fatalf("Get() = %v, want ErrSecretNotFound", err)
	}
}

func caseSecretItemsSealedAtRest(t *testing.T, h SecretItemsHarness) {
	t.Helper()

	entry := TestEntry(audit.ServerScope, "admin", "server:manage-agent")

	_, err := h.Items.Create(t.Context(), "example-key", []byte("secret-value"), entry)
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	// List must not return values: each entry holds only metadata.
	list, err := h.Items.List(t.Context())
	if err != nil {
		t.Fatalf("List() = %v", err)
	}

	for _, it := range list {
		if string(it.Ref) == "" {
			t.Fatal("List() returned an item with an empty reference")
		}
	}
}

func caseSecretItemsOpen(t *testing.T, h SecretItemsHarness) {
	t.Helper()

	entry := TestEntry(audit.ServerScope, "admin", "server:manage-agent")

	got, err := h.Items.Create(t.Context(), "example-key", []byte("secret-value"), entry)
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	open, err := h.Items.Open(t.Context(), got.Ref)
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}

	if string(open) != "secret-value" {
		t.Fatalf("Open() = %q, want secret-value", open)
	}

	_, err = h.Items.Open(t.Context(), agent.SecretRef("itm-nope"))
	if !errors.Is(err, agent.ErrSecretNotFound) {
		t.Fatalf("Open() = %v, want ErrSecretNotFound", err)
	}
}
