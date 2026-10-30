package trigger_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/trigger"
)

func utc(y int, m time.Month, d, h, minute int) time.Time {
	return time.Date(y, m, d, h, minute, 0, 0, time.UTC)
}

func cronFreq(expr string) trigger.Frequency { return trigger.Frequency{Preset: "", Cron: expr} }

func TestNextSlot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		freq  trigger.Frequency
		after time.Time
		want  time.Time
	}{
		{"hourly from inside an hour", cronFreq("0 * * * *"), utc(2026, 10, 30, 10, 20), utc(2026, 10, 30, 11, 0)},
		{"hourly from a slot", cronFreq("0 * * * *"), utc(2026, 10, 30, 11, 0), utc(2026, 10, 30, 12, 0)},
		{
			"seconds are ignored",
			cronFreq("0 * * * *"),
			utc(2026, 10, 30, 10, 59).Add(59 * time.Second),
			utc(2026, 10, 30, 11, 0),
		},
		{
			"another zone gives UTC",
			cronFreq("30 4 * * *"),
			utc(2026, 10, 30, 6, 0).In(time.FixedZone("x", 7200)),
			utc(2026, 10, 31, 4, 30),
		},
		{"leap day", cronFreq("0 0 29 2 *"), utc(2026, 10, 30, 0, 0), utc(2028, 2, 29, 0, 0)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := trigger.NextSlot(schedule("s1", tc.freq), tc.after)
			if err != nil || !got.Equal(tc.want) || got.Location() != time.UTC {
				t.Errorf("NextSlot = %v, %v, want %v", got, err, tc.want)
			}
		})
	}
}

func TestNextSlotRefusals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		tr   trigger.Trigger
	}{
		{"follows trigger", follows("f1", "a", "b")},
		{"no frequency", schedule("s1", cronFreq(""))},
		{"too frequent", schedule("s1", cronFreq("*/10 * * * *"))},
		{"both preset and cron", schedule("s1", trigger.Frequency{Preset: "daily", Cron: "0 * * * *"})},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := trigger.NextSlot(tc.tr, utc(2026, 10, 30, 10, 0))
			if !errors.Is(err, trigger.ErrInvalid) {
				t.Errorf("NextSlot = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestIsSlot(t *testing.T) {
	t.Parallel()

	hourly := schedule("s1", cronFreq("0 * * * *"))
	base := utc(2026, 10, 30, 10, 0)

	tests := []struct {
		name string
		tr   trigger.Trigger
		slot time.Time
		want bool
	}{
		{"a slot", hourly, base, true},
		{"a slot in another zone", hourly, base.In(time.FixedZone("x", 3600)), true},
		{"a minute late", hourly, base.Add(time.Minute), false},
		{"a millisecond late", hourly, base.Add(time.Millisecond), false},
		{"the zero time", hourly, time.Time{}, false},
		{"a trigger that is not a schedule", follows("f1", "a", "b"), base, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := trigger.IsSlot(tc.tr, tc.slot); got != tc.want {
				t.Errorf("IsSlot = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLatestSlot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		freq     trigger.Frequency
		from     time.Time
		now      time.Time
		want     time.Time
		wantSlot bool
	}{
		{
			"slot at now", cronFreq("0 * * * *"), utc(2026, 10, 30, 8, 0), utc(2026, 10, 30, 10, 0),
			utc(2026, 10, 30, 10, 0), true,
		},
		{
			"after an outage of a year", cronFreq("0 * * * *"), utc(2025, 10, 30, 8, 0), utc(2026, 10, 30, 10, 20),
			utc(2026, 10, 30, 10, 0), true,
		},
		{
			"from is the only slot", cronFreq("0 * * * *"), utc(2026, 10, 30, 10, 0), utc(2026, 10, 30, 10, 59),
			utc(2026, 10, 30, 10, 0), true,
		},
		{
			"no slot yet", cronFreq("0 * * * *"), utc(2026, 10, 30, 11, 0), utc(2026, 10, 30, 10, 59),
			time.Time{},
			false,
		},
		{
			"no stored slot, hourly", cronFreq("0 * * * *"),
			time.Time{},
			utc(2026, 10, 30, 10, 20),
			utc(2026, 10, 30, 10, 0), true,
		},
		{
			"no stored slot, yearly", cronFreq("0 0 1 1 *"),
			time.Time{},
			utc(2026, 10, 30, 10, 20),
			utc(2026, 1, 1, 0, 0), true,
		},
		{
			"no stored slot, leap day", cronFreq("0 0 29 2 *"),
			time.Time{},
			utc(2026, 10, 30, 10, 20),
			utc(2024, 2, 29, 0, 0), true,
		},
		{
			"from after the latest slot of a sparse schedule", cronFreq("0 0 1 1 *"), utc(2026, 6, 1, 0, 0),
			utc(2026, 10, 30, 10, 20),
			time.Time{},
			false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok, err := trigger.LatestSlot(schedule("s1", tc.freq), tc.from, tc.now)
			if err != nil || ok != tc.wantSlot || !got.Equal(tc.want) {
				t.Errorf("LatestSlot = %v, %v, %v, want %v, %v", got, ok, err, tc.want, tc.wantSlot)
			}
		})
	}

	_, _, err := trigger.LatestSlot(follows("f1", "a", "b"), time.Time{}, utc(2026, 10, 30, 10, 0))
	if !errors.Is(err, trigger.ErrInvalid) {
		t.Errorf("LatestSlot of a follows trigger = %v, want ErrInvalid", err)
	}
}

func claim(slot time.Time) trigger.Claim {
	return trigger.Claim{Namespace: "acme", Trigger: "s1", Slot: slot}
}

func TestClaimValidate(t *testing.T) {
	t.Parallel()

	slot := utc(2026, 10, 30, 10, 0)

	tests := []struct {
		name string
		c    trigger.Claim
		ok   bool
	}{
		{"valid", claim(slot), true},
		{"zero slot", claim(time.Time{}), false},
		{"not a whole minute", claim(slot.Add(time.Second)), false},
		{"bad ID", trigger.Claim{Namespace: "acme", Trigger: "No Way", Slot: slot}, false},
		{"bad namespace", trigger.Claim{Namespace: "No Way", Trigger: "s1", Slot: slot}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.c.Validate()
			if (err == nil) != tc.ok || (err != nil && !errors.Is(err, trigger.ErrInvalid)) {
				t.Errorf("Validate = %v, want ok %v", err, tc.ok)
			}
		})
	}
}

func TestCheckClaim(t *testing.T) {
	t.Parallel()

	hourly := schedule("s1", cronFreq("0 * * * *"))
	off := hourly
	off.Enabled = false

	other := hourly
	other.ID = "s2"

	slot := utc(2026, 10, 30, 10, 0)
	next := slot.Add(time.Hour)
	later := slot.Add(5 * time.Hour)
	far := utc(2100, 1, 1, 0, 0)

	tests := []struct {
		name    string
		c       trigger.Claim
		tr      trigger.Trigger
		stored  time.Time
		now     time.Time
		want    time.Time
		wantErr error
	}{
		{"due", claim(slot), hourly, slot, slot, next, nil},
		{"due inside the skew", claim(slot), hourly, slot, slot.Add(-trigger.MaxClaimSkew), next, nil},
		{"a later slot skips", claim(later), hourly, slot, later, later.Add(time.Hour), nil},
		{"no stored slot", claim(slot), hourly, time.Time{}, slot, time.Time{}, trigger.ErrInvalid},
		{"taken", claim(slot), hourly, next, next, time.Time{}, trigger.ErrNotDue},
		{"off", claim(slot), off, slot, slot, time.Time{}, trigger.ErrDisabled},
		{
			"not a slot",
			claim(slot.Add(time.Minute)),
			hourly,
			slot,
			slot.Add(time.Hour),
			time.Time{},
			trigger.ErrInvalid,
		},
		{"other trigger", claim(slot), other, slot, slot, time.Time{}, trigger.ErrInvalid},
		{"follows", claim(slot), follows("s1", "a", "b"), slot, slot, time.Time{}, trigger.ErrInvalid},
		{"slot after the clock of the store", claim(next), hourly, slot, slot, time.Time{}, trigger.ErrInvalid},
		{"a slot in 2100 with an old store clock", claim(far), hourly, slot, slot, time.Time{}, trigger.ErrInvalid},
		{
			"just past the skew", claim(slot), hourly, slot, slot.Add(-trigger.MaxClaimSkew - time.Minute),
			time.Time{},
			trigger.ErrInvalid,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := trigger.CheckClaim(tc.c, tc.tr, tc.stored, tc.now)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) || !got.IsZero() {
					t.Errorf("CheckClaim = %v, %v, want %v", got, err, tc.wantErr)
				}

				return
			}

			if err != nil || !got.Equal(tc.want) {
				t.Errorf("CheckClaim = %v, %v, want %v", got, err, tc.want)
			}
		})
	}
}

func TestPutSlot(t *testing.T) {
	t.Parallel()

	hourly := schedule("s1", cronFreq("0 * * * *"))
	half := schedule("s1", cronFreq("30 * * * *"))
	off := hourly
	off.Enabled = false

	review := follows("f1", "a", "b")

	now := utc(2026, 10, 30, 10, 20)
	first := utc(2026, 10, 30, 11, 0)
	later := first.Add(time.Hour)

	tests := []struct {
		name     string
		tr       trigger.Trigger
		prev     *trigger.Trigger
		prevNext time.Time
		want     time.Time
	}{
		{"new", hourly, nil, time.Time{}, first},
		{"same frequency keeps the slot", hourly, &hourly, later, later},
		{"other frequency starts again", half, &hourly, later.Add(4 * time.Hour), utc(2026, 10, 30, 10, 30)},
		{"was off", hourly, &off, time.Time{}, first},
		{"no recorded slot", hourly, &hourly, time.Time{}, first},
		{"now off", off, &hourly, first, time.Time{}},
		{"follows", review, nil, time.Time{}, time.Time{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := trigger.PutSlot(tc.tr, now, tc.prev, tc.prevNext)
			if err != nil || !got.Equal(tc.want) {
				t.Errorf("PutSlot = %v, %v, want %v", got, err, tc.want)
			}
		})
	}
}
