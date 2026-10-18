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
		protocol.PathSessionScanReport, protocol.PathSessionFinish,
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
