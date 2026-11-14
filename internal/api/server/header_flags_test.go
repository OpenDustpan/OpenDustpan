package server_test

import (
	"maps"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/apidesc/headertest"
)

// checkHeaderFlags runs the check of the header flags for each operation. An
// operation with a flag and no scenario fails, so a new flag cannot enter
// without a check. A write operation with no scenario fails too, because the
// check of the "no flag" direction needs a scenario. Only a read with no flag
// and no scenario is skipped.
func checkHeaderFlags(t *testing.T, ops []apidesc.Operation, scenarios map[string]headertest.Scenario) {
	t.Helper()

	for i := range ops {
		op := &ops[i]

		scenario, ok := scenarios[op.ID]
		if !ok {
			if headertest.Flagged(*op) || headertest.Writes(*op) {
				t.Errorf("%s sets a header flag or writes, and has no scenario", op.ID)
			}

			continue
		}

		t.Run(op.ID, func(t *testing.T) {
			t.Parallel()

			// The installation adapter ignores a key on a route that does not
			// take it, so the test does not check for a refusal.
			headertest.Check(t, *op, scenario, headertest.Options{RefusesKey: false})
		})
	}
}

// TestHeaderFlagsMatchTheHandler proves that the header flags of the
// operations of the approvals and of the preferences match what the handler
// does with the header.
func TestHeaderFlagsMatchTheHandler(t *testing.T) {
	t.Parallel()

	scenarios := approvalScenarios()
	maps.Copy(scenarios, preferenceScenarios())
	maps.Copy(scenarios, settingsScenarios())
	maps.Copy(scenarios, gateObjectScenarios())
	maps.Copy(scenarios, maintenanceScenarios())

	_, h := newGateHandler(t, adminGates())

	checkHeaderFlags(t, h.env.handler.Operations(), scenarios)
}

// approvalScenarios returns the scenarios of the approval operations.
func approvalScenarios() map[string]headertest.Scenario {
	const rule = `{"count":1,"approvers":{"groups":["ops"]}}`

	var (
		def     = gateBase + "acme" + gateDefault
		rules   = gateBase + "acme" + gateRulesURL
		ruleURL = rules + "/ops"
	)

	// scenario builds a handler with a default and a rule, and sends the
	// request of the operation. The ETag is the one that the write of etagOf
	// answered.
	scenario := func(method, target, body, etagOf string) headertest.Scenario {
		return func(t *testing.T) headertest.Probe {
			t.Helper()

			_, h := newGateHandler(t, adminGates())

			tags := map[string]string{}

			for url, doc := range map[string]string{def: platformDefault, ruleURL: rule} {
				put := serveGate(t, h, gateCall{http.MethodPut, url, doc, ""})
				wantStatus(t, put, http.StatusOK)

				tags[url] = put.Header().Get("ETag")
			}

			return headertest.Probe{
				ETag: tags[etagOf],
				Send: func(headers map[string]string) headertest.Answer {
					rec := serveGate(t, h, gateCall{method, target, body, headers["If-Match"]})

					return headertest.Answer{Status: rec.Code, Header: rec.Header(), Body: rec.Body.String()}
				},
				Replayed: nil,
			}
		}
	}

	return map[string]headertest.Scenario{
		"getApprovalDefault":    scenario(http.MethodGet, def, "", def),
		"setApprovalDefault":    scenario(http.MethodPut, def, platformDefault, def),
		"deleteApprovalDefault": scenario(http.MethodDelete, def, "", def),
		"listApprovalRules":     scenario(http.MethodGet, rules, "", ruleURL),
		"getApprovalRule":       scenario(http.MethodGet, ruleURL, "", ruleURL),
		"setApprovalRule":       scenario(http.MethodPut, ruleURL, rule, ruleURL),
		"deleteApprovalRule":    scenario(http.MethodDelete, ruleURL, "", ruleURL),
	}
}

// preferenceScenarios returns the scenarios of the preference operations.
func preferenceScenarios() map[string]headertest.Scenario {
	scenario := func(method, body string) headertest.Scenario {
		return func(t *testing.T) headertest.Probe {
			t.Helper()

			e := newPrefsEnv(t)

			current := e.get(t, tokenTester)
			if current.status != http.StatusOK {
				t.Fatalf("read the preferences = %d %s, want 200", current.status, current.body)
			}

			return headertest.Probe{
				ETag: current.header.Get("ETag"),
				Send: func(headers map[string]string) headertest.Answer {
					contentType := ""
					if body != "" {
						contentType = prefsPatchType
					}

					got := e.callIf(t, prefsCall{
						token: tokenTester, method: method, contentType: contentType, body: body,
						ifMatch: headers["If-Match"],
					})

					return headertest.Answer{Status: got.status, Header: got.header, Body: got.body}
				},
				Replayed: nil,
			}
		}
	}

	return map[string]headertest.Scenario{
		"getPreferences":    scenario(http.MethodGet, ""),
		"updatePreferences": scenario(http.MethodPatch, `{"theme":"terminal"}`),
	}
}

// gateObjectScenarios returns the scenarios of the gate and access limit
// operations of the three scope levels.
func gateObjectScenarios() map[string]headertest.Scenario {
	const (
		gateDoc = `{"decisionPoint":"state.write","condition":"context.operation.kind == \"write\"",` +
			`"outcome":"deny","message":"Writes are frozen","mode":"enforce"}`
		limitDoc = `{"policy":"forbid (principal, action == OpenDustpan::Action::\"unit:write\", resource);"}`
	)

	scopes := []struct{ infix, base string }{
		{"", gateBase + "acme"},
		{"Collection", gateBase + "acme/collections/payments"},
		{"Unit", gateBase + "acme/units/by-name/opentofu/net"},
	}

	out := map[string]headertest.Scenario{}

	for _, sc := range scopes {
		for _, res := range []struct{ plural, singular, segment, doc string }{
			{"Gates", "Gate", "gates", gateDoc},
			{"AccessLimits", "AccessLimit", "access-limits", limitDoc},
		} {
			list := sc.base + "/" + res.segment
			item := list + "/seed"

			scenario := func(method, target, body string) headertest.Scenario {
				return func(t *testing.T) headertest.Probe {
					t.Helper()

					_, h := newGateHandler(t, adminGates())

					put := serveGate(t, h, gateCall{http.MethodPut, item, res.doc, ""})
					wantStatus(t, put, http.StatusOK)

					return headertest.Probe{
						ETag: put.Header().Get("ETag"),
						Send: func(headers map[string]string) headertest.Answer {
							rec := serveGate(t, h, gateCall{method, target, body, headers["If-Match"]})

							return headertest.Answer{Status: rec.Code, Header: rec.Header(), Body: rec.Body.String()}
						},
						Replayed: nil,
					}
				}
			}

			out["list"+sc.infix+res.plural] = scenario(http.MethodGet, list, "")
			out["get"+sc.infix+res.singular] = scenario(http.MethodGet, item, "")
			out["set"+sc.infix+res.singular] = scenario(http.MethodPut, item, res.doc)
			out["delete"+sc.infix+res.singular] = scenario(http.MethodDelete, item, "")
		}
	}

	return out
}
