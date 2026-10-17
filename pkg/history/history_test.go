package history_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/secret"
)

func TestValidateReason(t *testing.T) {
	t.Parallel()

	cases := []struct {
		want   error
		name   string
		reason string
	}{
		{name: "plain text", reason: "a password in a plain attribute", want: nil},
		{name: "the longest reason", reason: strings.Repeat("é", history.MaxReasonLength), want: nil},
		{name: "empty", reason: "", want: history.ErrInvalidReason},
		{name: "only spaces", reason: "   ", want: history.ErrInvalidReason},
		{
			name:   "one character too long",
			reason: strings.Repeat("a", history.MaxReasonLength+1),
			want:   history.ErrInvalidReason,
		},
		{name: "a new line", reason: "first\nsecond", want: history.ErrInvalidReason},
		{name: "not UTF-8", reason: "\xff", want: history.ErrInvalidReason},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if err := history.ValidateReason(tc.reason); !errors.Is(err, tc.want) {
				t.Errorf("ValidateReason err = %v, want %v", err, tc.want)
			}
		})
	}
}

// validRedaction returns a record that passes Validate.
func validRedaction() history.Redaction {
	return history.Redaction{
		At: time.Date(2026, time.October, 17, 9, 0, 0, 0, time.UTC),
		Item: history.ItemRef{
			Unit: history.Unit{Namespace: "acme", Tool: "pulumi", ID: "stk-one"},
			Kind: secret.ContentKindState,
			Item: "upd-1",
		},
		ID:         history.NewRedactionID(),
		By:         history.Actor{Subject: "ana", TokenID: "", JobID: ""},
		Reason:     "a token in a plain output",
		Cause:      history.CauseRequest,
		ContentKey: secret.NewContentKeyID(),
		Blobs:      nil,
		Targets:    nil,
	}
}

func TestRedactionValidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		edit func(r *history.Redaction)
		want error
		name string
	}{
		{name: "a valid request", edit: func(*history.Redaction) {}, want: nil},
		{
			name: "a restore needs no reason",
			edit: func(r *history.Redaction) { r.Cause, r.Reason = history.CauseRestore, "" },
			want: nil,
		},
		{
			name: "a request needs a reason",
			edit: func(r *history.Redaction) { r.Reason = "" },
			want: history.ErrInvalidReason,
		},
		{
			name: "a retention needs its reason",
			edit: func(r *history.Redaction) { r.Cause, r.Reason = history.CauseRetention, "" },
			want: history.ErrInvalidReason,
		},
		{
			name: "an ID without the prefix",
			edit: func(r *history.Redaction) { r.ID = "abc" },
			want: history.ErrInvalidRedaction,
		},
		{
			name: "an unknown cause",
			edit: func(r *history.Redaction) { r.Cause = "manual" },
			want: history.ErrInvalidRedaction,
		},
		{name: "no time", edit: func(r *history.Redaction) { r.At = time.Time{} }, want: history.ErrInvalidRedaction},
		{
			name: "a bad content key",
			edit: func(r *history.Redaction) { r.ContentKey = "key" },
			want: history.ErrInvalidRedaction,
		},
		{
			name: "an unknown kind",
			edit: func(r *history.Redaction) { r.Item.Kind = "events" },
			want: history.ErrInvalidItem,
		},
		{
			name: "a bad namespace",
			edit: func(r *history.Redaction) { r.Item.Unit.Namespace = "A B" },
			want: history.ErrInvalidItem,
		},
		{name: "no tool", edit: func(r *history.Redaction) { r.Item.Unit.Tool = "" }, want: history.ErrInvalidItem},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := validRedaction()
			tc.edit(&r)

			if err := r.Validate(); !errors.Is(err, tc.want) {
				t.Errorf("Validate err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestLedgerHoldsNoReason(t *testing.T) {
	t.Parallel()

	r := validRedaction()
	l := r.Ledger()

	if l.ID != r.ID || l.UnitID != "stk-one" || l.Item != "upd-1" || l.ContentKey != r.ContentKey ||
		l.AtMs != r.At.UnixMilli() {
		t.Errorf("ledger = %+v, want the fields of %+v", l, r)
	}

	if got := history.LedgerObject(r.ID).String(); got != "redactions/"+string(r.ID)+".json" {
		t.Errorf("ledger object = %q", got)
	}
}

func TestRetention(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, time.October, 17, 0, 0, 0, 0, time.UTC)
	r := history.DefaultRetention()

	if err := r.Validate(); err != nil {
		t.Fatalf("default retention: %v", err)
	}

	if got, want := r.PlanExpiry(created), created.AddDate(0, 0, 400); !got.Equal(want) {
		t.Errorf("plan expiry = %v, want %v", got, want)
	}

	if got, want := r.JobLogExpiry(created), created.AddDate(0, 0, 400); !got.Equal(want) {
		t.Errorf("job log expiry = %v, want %v", got, want)
	}

	short := history.Retention{Plans: time.Hour, JobLogs: history.MinRetention}
	if err := short.Validate(); !errors.Is(err, history.ErrInvalidRetention) {
		t.Errorf("an hour of plan retention err = %v, want ErrInvalidRetention", err)
	}
}
