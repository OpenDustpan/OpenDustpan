// Package historytest is the conformance suite for the ports of the history
// package: the redaction log, the plan store and the job log store. Every
// built-in store runs it, and a custom store should too:
//
//	func TestHistoryConformance(t *testing.T) {
//		t.Parallel()
//		historytest.RunRedactions(t, func(t *testing.T) historytest.RedactionStore { return newMyStore(t) })
//		historytest.RunPlans(t, func(t *testing.T) historytest.PlanSuiteStore { return newMyStore(t) })
//		historytest.RunJobLogs(t, func(t *testing.T) historytest.JobLogSuiteStore { return newMyStore(t) })
//	}
package historytest

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// suiteTime() returns the fixed time of the records of the suite.
func suiteTime() time.Time {
	return time.Date(2026, time.October, 17, 9, 0, 0, 0, time.UTC)
}

// backupTarget is the backup target that the cases name.
const backupTarget = "dir:/backups"

// suiteFenceVersion is the failover version of the namespaces of the
// suite. A fence with a lower version is stale.
const suiteFenceVersion = 2

// makeNamespace creates the namespace "acme" and returns its fence.
func makeNamespace(t *testing.T, s namespace.Store) namespace.Fence {
	t.Helper()

	const name namespace.Name = "acme"

	ns := namespace.Namespace{
		CreatedAt:       suiteTime(),
		Name:            name,
		HomeRegion:      "local",
		FailoverVersion: suiteFenceVersion,
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace %s: %v", name, err)
	}

	return namespace.Fence{Namespace: name, FailoverVersion: suiteFenceVersion}
}

// staleFence returns a fence of the namespace of f with an older version.
func staleFence(f namespace.Fence) namespace.Fence {
	return namespace.Fence{Namespace: f.Namespace, FailoverVersion: f.FailoverVersion - 1}
}

// unitOf returns a unit of the tool "opentofu" in the namespace "acme".
func unitOf(id string) history.Unit {
	return history.Unit{Namespace: "acme", Tool: "opentofu", ID: id}
}

// actor returns a person actor.
func actor() history.Actor {
	return history.Actor{Subject: "ana", TokenID: "tok-1", JobID: ""}
}

// newRedaction returns a valid record for item, made at at.
func newRedaction(item history.ItemRef, at time.Time) history.Redaction {
	return history.Redaction{
		At:         at,
		Item:       item,
		ID:         history.NewRedactionID(),
		By:         actor(),
		Reason:     "a password in a plain attribute",
		Cause:      history.CauseRequest,
		ContentKey: secret.NewContentKeyID(),
		Blobs:      nil,
		Targets:    nil,
	}
}

// changeEntry returns the audit entry of a redaction in ns.
func changeEntry(item history.ItemRef) audit.Entry {
	ns := item.Unit.Namespace

	return audit.Entry{
		At:           time.Time{},
		Namespace:    ns,
		Subject:      "ana",
		IDPSubject:   "",
		TokenID:      "tok-1",
		Action:       "unit:redact",
		Object:       item.Unit.Tool + ":" + ns.String() + "/" + item.Unit.ID,
		Version:      item.Item,
		ValuePath:    "",
		ValueCount:   nil,
		Changes:      nil,
		SourceIP:     "192.0.2.1",
		UserAgent:    "historytest",
		Channel:      audit.ChannelAPI,
		Kind:         audit.KindChange,
		Outcome:      audit.OutcomeSuccess,
		RequestID:    "req-1",
		ObjectID:     item.Unit.ID,
		PrevHash:     "",
		Hash:         "",
		Sequence:     0,
		FenceVersion: 0,
	}
}

// contentKeyFor stores the content key record of item and returns its ID.
func contentKeyFor(
	t *testing.T, keys secret.ContentKeyIndex, item history.ItemRef, fence namespace.Fence,
) secret.ContentKeyID {
	t.Helper()

	ref, err := item.ContentRef()
	if err != nil {
		t.Fatalf("content ref of %+v: %v", item, err)
	}

	k, err := keys.CreateContentKey(t.Context(), secret.ContentKey{
		CreatedAt: suiteTime(), ShreddedAt: time.Time{}, Ref: ref, ID: secret.NewContentKeyID(), WrapperID: "builtin-1",
	}, fence)
	if err != nil {
		t.Fatalf("create content key: %v", err)
	}

	return k.ID
}
