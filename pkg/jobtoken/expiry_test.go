package jobtoken_test

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/jobtoken"
)

// TestExpiryRule proves the rule of the expiry of a job token: the earlier of
// the end of the claim and the end of the step timeout plus the grace period,
// counted from the start of the step. A past time comes back as it is, so the
// caller refuses an issue whose expiry is not after its clock.
func TestExpiryRule(t *testing.T) {
	t.Parallel()

	claimEnd := time.UnixMilli(1_700_000_000_000).UTC()
	stepStart := claimEnd.Add(-time.Hour)
	timeout := 30 * time.Minute
	grace := 10 * time.Minute
	stepEnd := stepStart.Add(timeout + grace)

	cases := []struct {
		name      string
		claimEnd  time.Time
		stepStart time.Time
		timeout   time.Duration
		grace     time.Duration
		want      time.Time
	}{
		{
			name:      "the claim ends first",
			claimEnd:  stepStart.Add(20 * time.Minute),
			stepStart: stepStart,
			timeout:   timeout,
			grace:     grace,
			want:      stepStart.Add(20 * time.Minute),
		},
		{
			name:      "the step end plus grace comes first",
			claimEnd:  claimEnd,
			stepStart: stepStart,
			timeout:   timeout,
			grace:     grace,
			want:      stepEnd,
		},
		{
			name:      "both times are equal",
			claimEnd:  stepEnd,
			stepStart: stepStart,
			timeout:   timeout,
			grace:     grace,
			want:      stepEnd,
		},
		{
			name:      "the earlier time is in the past",
			claimEnd:  claimEnd.Add(-2 * time.Hour),
			stepStart: stepStart,
			timeout:   timeout,
			grace:     grace,
			want:      claimEnd.Add(-2 * time.Hour),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := jobtoken.Expiry(tc.claimEnd, tc.stepStart, tc.timeout, tc.grace)
			if !got.Equal(tc.want) {
				t.Fatalf("Expiry = %s, want %s", got, tc.want)
			}
		})
	}
}
