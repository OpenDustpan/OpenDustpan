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
// makes the collection everything and one catch-all rule for Pulumi stacks and
// one for OpenTofu workspaces in the namespace dev, and that the banner names the rule and the command that
// removes it.
func TestDevFirstStartMakesThePlacementRule(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "trial")
	r := mustStartDev(t, newDevTestProcess(false), t.TempDir(), trialArgs(t, dir)...)
	token := readToken(t, dir)

	list := devRules(t, r.url, token)
	if len(list.Rules) != 2 {
		t.Fatalf("the first start made %d rules, want 2", len(list.Rules))
	}

	for i, tool := range []string{"pulumi", "opentofu"} {
		got := list.Rules[i]
		if got.Tool != tool || got.Project != "*" || got.Stack != "*" || got.Collection != "everything" {
			t.Fatalf("rule %d = %+v, want %s */* in everything", i, got, tool)
		}
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

// deleteDevRule deletes the placement rule id of the namespace dev.
func deleteDevRule(t *testing.T, serverURL, token, id string) {
	t.Helper()

	req, err := http.NewRequestWithContext(
		t.Context(), http.MethodDelete, serverURL+devRulesPath+"/"+id, http.NoBody,
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

	for _, rule := range devRules(t, first.url, token).Rules {
		deleteDevRule(t, first.url, token, rule.ID)
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

// TestDevDemoMakesThePlacementRuleInEachNamespace proves that dev --demo makes
// the collection and the catch-all rule in the namespace dev and in each demo
// namespace that it makes, that the banner names the three namespaces, and
// that a first stack create in each of them succeeds.
func TestDevDemoMakesThePlacementRuleInEachNamespace(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "trial")
	r := mustStartDev(t, newDevTestProcess(false), t.TempDir(), append(trialArgs(t, dir), "--demo")...)
	token := readToken(t, dir)

	for _, ns := range []string{"dev", "demo-platform", "demo-data"} {
		status, body := devGet(t, http.DefaultClient, r.url+"/api/dustpan/v1/namespaces/"+ns+"/placement-rules", token)
		if status != http.StatusOK || !strings.Contains(body, `"collection":"everything"`) {
			t.Errorf("rules of %s = %d %s, want the rule for everything", ns, status, body)
		}

		req, err := http.NewRequestWithContext(
			t.Context(), http.MethodPost, r.url+"/api/stacks/"+ns+"/app", strings.NewReader(`{"stackName":"first"}`),
		)
		if err != nil {
			t.Fatalf("request: %v", err)
		}

		req.Header.Set("Authorization", "token "+token)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("create the first stack in %s: %v", ns, err)
		}

		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Fatalf("close body: %v", closeErr)
		}

		if resp.StatusCode != http.StatusOK {
			t.Errorf("the first stack in %s answers %d, want 200", ns, resp.StatusCode)
		}
	}

	banner := r.stderr.String()
	if !strings.Contains(banner, "dev, demo-platform, demo-data") {
		t.Errorf("the banner does not name the three namespaces:\n%s", banner)
	}
}
