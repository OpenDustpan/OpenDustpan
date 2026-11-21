package outputsharestore_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/store/outputsharestore"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/outputshare"
)

func TestRemovedChangeListsEachRule(t *testing.T) {
	t.Parallel()

	if _, ok := outputsharestore.RemovedChange(nil); ok {
		t.Fatal("RemovedChange(nil) reports a change, want none: no rule was removed")
	}

	c, ok := outputsharestore.RemovedChange([]string{"acme/rule-001", "globex/rule-002"})
	if !ok || c.Key != outputsharestore.RemovedKey || c.Old == nil || *c.Old != "acme/rule-001\nglobex/rule-002" {
		t.Fatalf("RemovedChange = %+v, %v, want the key and the two rules", c, ok)
	}
}

func TestRemovedChangeIsBounded(t *testing.T) {
	t.Parallel()

	const total = 5000

	removed := make([]string, total)
	for i := range removed {
		removed[i] = fmt.Sprintf("acme/rule-%05d", i)
	}

	c, ok := outputsharestore.RemovedChange(removed)
	if !ok || c.Old == nil {
		t.Fatal("RemovedChange reports no change for 5000 rules")
	}

	if len(*c.Old) > audit.MaxChangeValueLen {
		t.Fatalf("the change holds %d bytes, want at most %d", len(*c.Old), audit.MaxChangeValueLen)
	}

	if !strings.HasSuffix(*c.Old, "\n(4000 more)") {
		t.Fatalf(
			"the change does not end with the count of the rules that it leaves out: %q",
			(*c.Old)[len(*c.Old)-20:],
		)
	}
}

// TestCheckPutRefusesHostileConsumers sends each hostile consumer to the store
// check alone, as if the service and the handler had passed it. The same rule
// with a good ID passes, so the refusal comes from the consumer ID.
func TestCheckPutRefusesHostileConsumers(t *testing.T) {
	t.Parallel()

	unit, err := identity.UnitObject("opentofu", "acme", "a")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	fence := namespace.Fence{Namespace: "acme", FailoverVersion: 1}
	var entry audit.Entry
	entry.At = time.UnixMilli(1_700_000_000_000)

	rule := func(id string) outputshare.Rule {
		return outputshare.Rule{
			CreatedAt: time.Time{}, ID: "rule-001", CreatedBy: "tester", Producer: unit.WithUnitID("ws-a"),
			Consumer: outputshare.Consumer{Kind: outputshare.ConsumerUnit, Namespace: "globex", ID: id},
		}
	}

	if _, err = outputsharestore.CheckPut(rule("ws-ok"), fence, entry); err != nil {
		t.Fatalf("CheckPut with a good consumer ID = %v, want nil", err)
	}

	for _, id := range []string{
		"", "A", "a/b", "a b", "a\nb", "../x", "a.b", strings.Repeat("a", outputshare.MaxIDBytes+1),
	} {
		if _, err = outputsharestore.CheckPut(rule(id), fence, entry); !errors.Is(err, outputshare.ErrInvalid) {
			t.Fatalf("CheckPut with the consumer ID %q = %v, want ErrInvalid", id, err)
		}
	}
}
