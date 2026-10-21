package connectiontest

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/namespace"
)

const (
	testTimeMs   = 1_700_000_000_000
	subjectAlice = "alice"
	testAppID    = "12345"
	testBaseURL  = "https://api.github.com"
	secretKeyA   = "sealed-key-a"
	secretKeyB   = "sealed-key-b"
)

const (
	nsAcme namespace.Name = "acme"
	nsBeta namespace.Name = "beta"
)

// newRecord returns a valid connection record in ns for the installation.
func newRecord(ns namespace.Name, installationID string) forge.ConnectionRecord {
	at := time.UnixMilli(testTimeMs).UTC()

	return forge.ConnectionRecord{
		CreatedAt: at, UpdatedAt: at, Namespace: ns, ID: forge.NewConnectionID(), Kind: forge.KindGitHub,
		Auth: forge.AuthApp, Scope: forge.AppScopeInstallation, InstallationID: installationID, Version: 1,
		Push: false, Default: false,
	}
}

// newRegistration returns a valid registration with the sealed key key.
func newRegistration(key string) forge.Registration {
	at := time.UnixMilli(testTimeMs).UTC()
	r := forge.Registration{
		CreatedAt: at, UpdatedAt: at, KeyCreatedAt: at, EventSecretCreatedAt: at, Kind: forge.KindGitHub,
		AppID: testAppID, BaseURL: testBaseURL, HookID: forge.NewHookID(), KeySetID: forge.NewSetID(),
		EventSecretSetID: forge.NewSetID(), AddressDigest: "", SealedKey: []byte(key),
		SealedEventSecret: []byte("sealed-event-" + key), Version: 1,
	}
	r.AddressDigest = r.Digest()

	return r
}

// newAssignment returns a valid assignment of the installation to ns.
func newAssignment(installationID string, ns namespace.Name) forge.Assignment {
	at := time.UnixMilli(testTimeMs).UTC()

	return forge.Assignment{CreatedAt: at, UpdatedAt: at, InstallationID: installationID, Namespace: ns, Version: 1}
}

// entry returns a valid audit entry for a write in scope.
func entry(scope namespace.Name, action string) audit.Entry {
	return audit.Entry{
		At: time.UnixMilli(1), Namespace: scope, Subject: subjectAlice, IDPSubject: "", TokenID: "", Action: action,
		Object: scope.String(), Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1",
		UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess,
		RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: "",
		JobID: "", Reason: "",
	}
}

// staleFence returns a fence of the namespace of f that is not the current one.
func staleFence(f namespace.Fence) namespace.Fence {
	return namespace.Fence{Namespace: f.Namespace, FailoverVersion: f.FailoverVersion + 1}
}

// mustCreate stores a connection to the installation in ns and returns it.
func mustCreate(t *testing.T, h Harness, ns namespace.Name, installationID string) forge.ConnectionRecord {
	t.Helper()

	fence := h.Namespace(t, ns.String())

	got, err := h.Store.CreateConnection(
		t.Context(), newRecord(ns, installationID), fence, entry(ns, "git-host-connection:create"),
	)
	if err != nil {
		t.Fatalf("create connection to installation %s in %s: %v", installationID, ns, err)
	}

	return got
}

// count returns the number of audit entries of scope.
func count(t *testing.T, h Harness, scope namespace.Name) int {
	t.Helper()

	return len(h.Audit(t, scope))
}
