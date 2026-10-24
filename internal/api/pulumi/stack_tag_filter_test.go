package pulumi_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
)

// TestListStacksRefusesRepeatedOrBinaryTagFilters proves that a tagName or a
// tagValue sent more than one time, and a value that is not UTF-8 text or that
// holds a control character, answer 400 in the Pulumi error shape and name the
// command, with and without the organization.
func TestListStacksRefusesRepeatedOrBinaryTagFilters(t *testing.T) {
	t.Parallel()

	handler := manyStacks(t, 3, selectiveAuthorizer{deny: map[identity.Action]bool{}}, "acme")

	for _, query := range []string{
		"tagName=team&tagValue=red&tagValue=blue",
		"tagName=a&tagName=b",
		"tagName=%00&tagValue=%ff",
		"tagName=team&tagValue=%ff",
		"tagName=te%0Aam",
		"tagName=team&tagValue=r%7Fd",
	} {
		for _, prefix := range []string{"/api/user/stacks?", "/api/user/stacks?organization=acme&"} {
			target := prefix + query
			rec := get(t, handler, target)

			var body struct {
				Message string `json:"message"`
				Code    int    `json:"code"`
			}

			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusBadRequest ||
				body.Code != http.StatusBadRequest || !strings.Contains(body.Message, "pulumi stack ls --tag name=value") {
				t.Errorf("GET %s answers %d %s, want 400 that names the command", target, rec.Code, rec.Body.String())
			}
		}
	}
}
