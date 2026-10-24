package pulumi_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
)

// TestStateAtNamesTheTimeOfTheRead proves that a state-at read with a time
// that is not Unix milliseconds in ASCII digits answers 400 with a text that
// names the time and an example, and not an update. A sign is refused too.
func TestStateAtNamesTheTimeOfTheRead(t *testing.T) {
	t.Parallel()

	const example = " in the path is not Unix milliseconds: send a number such as 1792754840074"

	cases := []struct{ name, at, want string }{
		{name: "a word", at: "abc", want: `time "abc"` + example},
		{name: "a plus sign", at: "+5", want: `time "+5"` + example},
		{name: "a minus sign", at: "-5", want: `time "-5"` + example},
		{name: "a number above int64", at: "99999999999999999999", want: `time "99999999999999999999"` + example},
		{name: "a long word", at: strings.Repeat("x", 100), want: `time "` + strings.Repeat("x", 40) + `"` + example},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newUpdateFixture(t, allowAll())
			seedStateVersions(t, f)

			status, body := f.doUpdate(t, accessRequest(http.MethodGet,
				"/api/stacks/acme/web/prod/state-at/"+tc.at+"/resources", ""))

			var got struct {
				Message   string `json:"message"`
				ErrorCode string `json:"errorCode"`
			}
			if err := json.Unmarshal([]byte(body), &got); err != nil {
				t.Fatalf("decode %q: %v", body, err)
			}

			if status != http.StatusBadRequest || got.Message != tc.want {
				t.Errorf("answer = %d %q, want 400 %q", status, got.Message, tc.want)
			}

			if got.ErrorCode != errcode.UpdateInvalid {
				t.Errorf("errorCode = %q, want %q", got.ErrorCode, errcode.UpdateInvalid)
			}
		})
	}
}
