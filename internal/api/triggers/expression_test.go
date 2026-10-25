package triggers_test

import (
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/internal/cron"
)

// expressionCase is one schedule body with the cron expression that its answer
// must show for the unit net.
type expressionCase struct {
	name string
	body string
	want func(t *testing.T) string
}

// presetExpression returns the expression of p for the unit net, as the server
// computes it.
func presetExpression(p cron.Preset) func(t *testing.T) string {
	return func(t *testing.T) string {
		t.Helper()

		expr, err := cron.Expression(p, netID)
		if err != nil {
			t.Fatalf("cron.Expression(%s): %v", p, err)
		}

		return expr
	}
}

func literalExpression(expr string) func(t *testing.T) string {
	return func(*testing.T) string { return expr }
}

func TestScheduleAnswerShowsTheEffectiveExpression(t *testing.T) {
	t.Parallel()

	tests := []expressionCase{
		{"a preset hourly", `{"kind":"schedule","frequency":{"preset":"hourly"}}`, presetExpression(cron.PresetHourly)},
		{
			"a preset every 6 hours",
			`{"kind":"schedule","frequency":{"preset":"every-6-hours"}}`,
			presetExpression(cron.PresetEvery6Hours),
		},
		{"a preset daily", `{"kind":"schedule","frequency":{"preset":"daily"}}`, presetExpression(cron.PresetDaily)},
		{"a preset weekly", `{"kind":"schedule","frequency":{"preset":"weekly"}}`, presetExpression(cron.PresetWeekly)},
		{
			"a cron expression",
			`{"kind":"schedule","frequency":{"cron":"30 4 * * 1-5"}}`,
			literalExpression("30 4 * * 1-5"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := fullRig(t)

			code, body, _ := do(t, r, http.MethodPost, basePath, tt.body)
			if code != http.StatusCreated {
				t.Fatalf("create status = %d %s, want 201", code, body)
			}

			created := decode[answer](t, body)
			want := tt.want(t)

			if created.Frequency == nil || created.Frequency.Expression != want {
				t.Errorf("create answer frequency = %+v, want expression %q", created.Frequency, want)
			}

			code, body, _ = do(t, r, http.MethodGet, basePath+"/trg-1", "")
			if code != http.StatusOK {
				t.Fatalf("get status = %d %s, want 200", code, body)
			}

			got := decode[answer](t, body)

			if got.Frequency == nil || got.Frequency.Expression != want {
				t.Errorf("get answer frequency = %+v, want expression %q", got.Frequency, want)
			}
		})
	}
}

func TestScheduleRequestStillRefusesAnExpression(t *testing.T) {
	t.Parallel()

	r := fullRig(t)

	code, body, _ := do(t, r, http.MethodPost, basePath,
		`{"kind":"schedule","frequency":{"preset":"daily","expression":"0 4 * * *"}}`)
	if code != http.StatusBadRequest {
		t.Errorf("status = %d %s, want 400: a request cannot set the expression", code, body)
	}
}

func TestFollowsAnswerHasNoFrequency(t *testing.T) {
	t.Parallel()

	r := fullRig(t)

	code, body, _ := do(t, r, http.MethodPost, basePath, `{"kind":"follows","source":{"id":"`+appID+`"}}`)
	if code != http.StatusCreated {
		t.Fatalf("create status = %d %s, want 201", code, body)
	}

	if got := decode[answer](t, body); got.Frequency != nil {
		t.Errorf("follows answer frequency = %+v, want none", got.Frequency)
	}
}
