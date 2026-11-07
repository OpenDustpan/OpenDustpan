package main

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// devRulesPath is the placement rule list of the namespace dev.
const devRulesPath = "/api/dustpan/v1/namespaces/dev/placement-rules"

// devRule is one placement rule in the list that the tests read.
type devRule struct {
	ID         string `json:"id"`
	Tool       string `json:"tool"`
	Project    string `json:"project"`
	Stack      string `json:"stack"`
	Collection string `json:"collection"`
}

// devRuleList is the part of the rule list that the tests read.
type devRuleList struct {
	Rules []devRule `json:"rules"`
}

// devRules reads the rule list of the namespace dev as the administrator.
func devRules(t *testing.T, url, token string) devRuleList {
	t.Helper()

	status, body := devGet(t, http.DefaultClient, url+devRulesPath, token)
	if status != http.StatusOK {
		t.Fatalf("GET %s = %d %s, want 200", devRulesPath, status, body)
	}

	var list devRuleList
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("decode the rule list %s: %v", body, err)
	}

	return list
}

// TestDevFirstStartMakesThePlacementRule proves that the first start of dev
// makes the collection everything and one catch-all rule for Pulumi stacks in
// the namespace dev, and that the banner names the rule and the command that
// removes it.
func TestDevFirstStartMakesThePlacementRule(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "trial")
	r := mustStartDev(t, newDevTestProcess(false), t.TempDir(), trialArgs(t, dir)...)
	token := readToken(t, dir)

	list := devRules(t, r.url, token)
	if len(list.Rules) != 1 {
		t.Fatalf("the first start made %d rules, want 1", len(list.Rules))
	}

	got := list.Rules[0]
	if got.Tool != "pulumi" || got.Project != "*" || got.Stack != "*" || got.Collection != "everything" {
		t.Fatalf("rule = %+v, want pulumi */* in everything", got)
	}

	banner := r.stderr.String()
	for _, want := range []string{"Rule:     ", "everything", "curl -X DELETE", "/placement-rules/<id>"} {
		if !strings.Contains(banner, want) {
			t.Errorf("the banner lacks %q:\n%s", want, banner)
		}
	}

	if strings.Contains(banner, token) {
		t.Fatal("the banner holds the administrator token")
	}
}

// TestDevLaterStartKeepsARemovedRule proves that a later start of dev does not
// make the rule again after the developer removed it, and prints no Placement
// line.
func TestDevLaterStartKeepsARemovedRule(t *testing.T) {
	t.Parallel()

	proc := newDevTestProcess(false)
	home := t.TempDir()
	dir := filepath.Join(t.TempDir(), "trial")
	args := trialArgs(t, dir)

	first := mustStartDev(t, proc, home, args...)
	token := readToken(t, dir)
	id := devRules(t, first.url, token).Rules[0].ID

	req, err := http.NewRequestWithContext(
		t.Context(), http.MethodDelete, first.url+devRulesPath+"/"+id, http.NoBody,
	)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE the rule: %v", err)
	}

	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Fatalf("close body: %v", closeErr)
	}

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE the rule = %d, want 204", resp.StatusCode)
	}

	if stopErr := first.stop(t); stopErr != nil {
		t.Fatalf("stop: %v", stopErr)
	}

	second := mustStartDev(t, proc, home, args...)

	if rules := devRules(t, second.url, token).Rules; len(rules) != 0 {
		t.Fatalf("a later start made %d rules, want none", len(rules))
	}

	if strings.Contains(second.stderr.String(), "Rule:") {
		t.Fatalf("a later start prints a Rule line:\n%s", second.stderr.String())
	}
}
