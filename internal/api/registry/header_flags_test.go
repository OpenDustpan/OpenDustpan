package registry_test

import (
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc/headertest"
	"opendustpan.org/opendustpan/pkg/registry/pgpfixture"
)

// TestHeaderFlagsMatchTheHandler proves that the header flags of each
// operation of the description match what the handler does with the header:
// If-Match, ETag and Idempotency-Key. An operation with no scenario here
// fails, so a new route cannot enter without a check. Each probe runs on a
// fresh fixture, so a request that deletes or publishes does not disturb the
// next probe.
func TestHeaderFlagsMatchTheHandler(t *testing.T) {
	t.Parallel()

	const versionTarget = base + "/providers/" + providerName + "/versions/" + providerVersion

	probe := func(t *testing.T, f fixture, method, target string, body []byte) headertest.Probe {
		t.Helper()

		return headertest.Probe{
			ETag: "", Replayed: nil,
			Send: func(headers map[string]string) headertest.Answer {
				got := f.do(t, request{token: "admin", method: method, target: target, body: body, headers: headers})

				return headertest.Answer{Status: got.code, Header: got.header, Body: got.body}
			},
		}
	}

	// trusted makes a fixture whose namespace trusts a new key.
	trusted := func(t *testing.T) (fixture, pgpfixture.Signer) {
		t.Helper()

		f := newFixture(t)
		sg := pgpfixture.NewSigner(t)
		f.trust(t, "acme", sg)

		return f, sg
	}

	// released makes a fixture with a trusted key and one published release.
	released := func(t *testing.T) (fixture, pgpfixture.Signer) {
		t.Helper()

		f, sg := trusted(t)
		body := f.publishBody(t, buildSet(t, sg))
		wantCode(t, "publish", f.call(t, "admin", http.MethodPut, versionTarget, body), http.StatusOK)

		return f, sg
	}

	scenarios := map[string]headertest.Scenario{
		"stageRegistryUpload": func(t *testing.T) headertest.Probe {
			t.Helper()

			return probe(t, newFixture(t), http.MethodPost, base+"/uploads", []byte("bytes of a file"))
		},
		"publishProviderRelease": func(t *testing.T) headertest.Probe {
			t.Helper()

			f, sg := trusted(t)
			body := f.publishBody(t, buildSet(t, sg))

			return probe(t, f, http.MethodPut, versionTarget, body)
		},
		"listProviderReleases": func(t *testing.T) headertest.Probe {
			t.Helper()

			f, _ := released(t)

			return probe(t, f, http.MethodGet, base+"/providers/"+providerName+"/versions", nil)
		},
		"getProviderRelease": func(t *testing.T) headertest.Probe {
			t.Helper()

			f, _ := released(t)

			return probe(t, f, http.MethodGet, versionTarget, nil)
		},
		"addRegistryKey": func(t *testing.T) headertest.Probe {
			t.Helper()

			body := js(t, map[string]string{"armoredPublicKey": string(pgpfixture.NewSigner(t).Key.Armored)})

			return probe(t, newFixture(t), http.MethodPost, base+"/keys", body)
		},
		"listRegistryKeys": func(t *testing.T) headertest.Probe {
			t.Helper()

			f, _ := trusted(t)

			return probe(t, f, http.MethodGet, base+"/keys", nil)
		},
		"removeRegistryKey": func(t *testing.T) headertest.Probe {
			t.Helper()

			f, sg := trusted(t)

			return probe(t, f, http.MethodDelete, base+"/keys/"+sg.Key.Fingerprint, nil)
		},
	}

	f := newFixture(t)

	ops := f.handler.Operations()
	if len(ops) != len(scenarios) {
		t.Errorf("the handler has %d operations and the test has %d scenarios", len(ops), len(scenarios))
	}

	for _, op := range ops {
		t.Run(op.ID, func(t *testing.T) {
			t.Parallel()

			scenario, ok := scenarios[op.ID]
			if !ok {
				t.Fatalf("%s has no scenario", op.ID)
			}

			// The registry adapter answers 400 to a key on a route that does not
			// take it.
			headertest.Check(t, op, scenario, headertest.Options{RefusesKey: true})
		})
	}
}
