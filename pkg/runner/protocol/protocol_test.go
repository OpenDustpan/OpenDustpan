package protocol_test

import (
	"encoding/json"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

func TestDurationIsMilliseconds(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   time.Duration
		want string
	}{
		{90 * time.Second, "90000"},
		{1500 * time.Microsecond, "1"},
		{0, "0"},
	}

	for _, tc := range cases {
		b, err := json.Marshal(protocol.FromStd(tc.in))
		if err != nil || string(b) != tc.want {
			t.Errorf("Marshal(%v) = %s, %v, want %s", tc.in, b, err, tc.want)
		}
	}

	if got := protocol.FromStd(90 * time.Second).Std(); got != 90*time.Second {
		t.Errorf("Std = %v, want 90s", got)
	}
}

func TestPathsAreUnderThePrefix(t *testing.T) {
	t.Parallel()

	for _, p := range []string{
		protocol.PathJoin, protocol.PathRotate, protocol.PathHeartbeat, protocol.PathPoll, protocol.PathClaimHandoff,
		protocol.PathClaimRenew, protocol.PathClaimRelease, protocol.PathSession, protocol.PathSessionRenew,
		protocol.PathSessionSource, protocol.PathSessionSourceObject, protocol.PathSessionPlanPart,
		protocol.PathSessionCredentials, protocol.PathSessionStepBegin, protocol.PathSessionStepEnd,
		protocol.PathSessionLog, protocol.PathSessionPlanUpload, protocol.PathSessionPlanCommit,
		protocol.PathSessionSweepReport, protocol.PathSessionFinish,
	} {
		if len(p) <= len(protocol.Prefix) || p[:len(protocol.Prefix)+1] != protocol.Prefix+"/" {
			t.Errorf("path %s is not under %s", p, protocol.Prefix)
		}
	}
}

func TestErrorShape(t *testing.T) {
	t.Parallel()

	b, err := json.Marshal(protocol.Error{Code: protocol.ErrorClaimLost, Message: "claim is lost", Supported: ""})
	if err != nil || string(b) != `{"code":"claim-lost","message":"claim is lost"}` {
		t.Fatalf("Marshal = %s, %v", b, err)
	}
}

func TestNeedsNonce(t *testing.T) {
	t.Parallel()

	for _, p := range []string{protocol.PathRotate, protocol.PathPoll, protocol.PathClaimHandoff, protocol.PathSession} {
		if !protocol.NeedsNonce(p) {
			t.Errorf("NeedsNonce(%s) = false, want true", p)
		}
	}

	for _, p := range []string{
		protocol.PathHeartbeat, protocol.PathClaimRenew, protocol.PathSessionRenew, protocol.PathSessionLog,
		protocol.PathSessionStepBegin, protocol.PathSessionPlanCommit, protocol.PathSessionFinish,
	} {
		if protocol.NeedsNonce(p) {
			t.Errorf("NeedsNonce(%s) = true, want false: the request is safe to repeat", p)
		}
	}
}

func TestSweepReportIsASessionPath(t *testing.T) {
	t.Parallel()

	if protocol.PathSessionSweepReport != "/runner/v1/session/sweep-report" {
		t.Fatalf("PathSessionSweepReport = %s", protocol.PathSessionSweepReport)
	}
}

// TestAssignmentProfile checks the wire shape of the profile of an
// assignment: a named profile survives a round trip, and an empty profile is
// left out of the JSON.
func TestAssignmentProfile(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		profile string
		member  bool
	}{
		{name: "a named profile", profile: "prod", member: true},
		{name: "no profile", profile: "", member: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			member, got := roundTripProfile(t, tc.profile)
			if member != tc.member {
				t.Fatalf("JSON has the member profile = %t, want %t", member, tc.member)
			}

			if got != tc.profile {
				t.Fatalf("round trip profile = %q, want %q", got, tc.profile)
			}
		})
	}
}

// roundTripProfile encodes an assignment with the profile, and returns
// whether the JSON has the member profile and the profile that the decode
// gives back.
func roundTripProfile(t *testing.T, profile string) (bool, string) {
	t.Helper()

	in := protocol.Assignment{
		Namespace: "acme", Unit: "", Tool: "", Job: "", Phase: "", Sweep: "", ServerURL: "",
		Source: nil, Steps: nil, Plan: nil, GitCredentials: "", GitHelper: "", Profile: profile,
	}

	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var members map[string]json.RawMessage
	if err := json.Unmarshal(b, &members); err != nil {
		t.Fatalf("Unmarshal into a map: %v", err)
	}

	var out protocol.Assignment
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	_, member := members["profile"]

	return member, out.Profile
}
