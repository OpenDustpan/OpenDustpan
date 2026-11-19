//go:build e2e

package e2e_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"testing"
	"time"

	"opendustpan.org/opendustpan/e2e"
)

// trustBundlesPath is the trust bundle route of the e2e namespace.
const trustBundlesPath = "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/trust-bundles"

// TestTrustBundleAndRuleUpdateDocs runs the requests of the trust rules page
// that concern the trust bundle and the update of a rule against a server of
// each store: save, read, rotate, delete of a version, and the update of a
// rule with If-Match. Each refusal that the page lists answers its code.
func TestTrustBundleAndRuleUpdateDocs(t *testing.T) {
	t.Parallel()

	stores, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, store := range stores {
		t.Run(string(store), func(t *testing.T) {
			t.Parallel()
			skipWithoutStore(t, store)

			env := e2e.NewEnvWithRealTimeSignIn(t.Context(), t, e2e.NoCLI(), store)

			trustBundleScenario(t, env)
		})
	}
}

// caPEM returns the PEM text of a new self-signed CA certificate called cn.
func caPEM(t *testing.T, cn string) string {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	// The template sets only the fields that a CA needs, so it is not a
	// literal that names every field of the type.
	tmpl := new(x509.Certificate)
	tmpl.SerialNumber = big.NewInt(1_700_000_000)
	tmpl.Subject.CommonName = cn
	tmpl.NotBefore = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	tmpl.NotAfter = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	tmpl.IsCA = true
	tmpl.BasicConstraintsValid = true
	tmpl.KeyUsage = x509.KeyUsageCertSign

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Headers: nil, Bytes: der}))
}

// jsonText returns s as a JSON string literal.
func jsonText(t *testing.T, s string) string {
	t.Helper()

	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	return string(b)
}

// captureRevision returns a check that stores the revision of a JSON answer
// in dst, as the text that an If-Match header holds.
func captureRevision(dst *string) func(string) error {
	return func(body string) error {
		var doc struct {
			Revision int64 `json:"revision"`
		}

		if err := json.Unmarshal([]byte(body), &doc); err != nil || doc.Revision < 1 {
			return fmt.Errorf("%w: %s", errNoID, body)
		}

		*dst = `"` + strconv.FormatInt(doc.Revision, 10) + `"`

		return nil
	}
}

// trustStep is one request of a trust rule scenario: the request, the status
// that it must answer and the optional check of the body.
type trustStep struct {
	name  string
	req   e2e.HeaderedSessionRequest
	want  int
	check func(string) error
}

// trustSessionStep sends the request of st as a site administrator with a
// fresh console session, because a bundle write and a rule write need a fresh
// sign-in and an access token never gives one. It reports a step that answers
// another status than st.want, or whose body check fails, and goes on with the
// next.
func trustSessionStep(ctx context.Context, t *testing.T, env *e2e.Env, st *trustStep) {
	t.Helper()

	status, body := env.FreshAdminHeaderedStatus(ctx, t, st.req)
	if status != st.want {
		t.Errorf("%s: %s %s answers %d, want %d: %s", st.name, st.req.Method, st.req.Path, status, st.want, body)

		return
	}

	if st.check != nil {
		if err := st.check(body); err != nil {
			t.Errorf("%s: %v", st.name, err)
		}
	}
}

func trustBundleScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()

	step := func(name, method, path, body string, headers map[string]string, want int, check func(string) error) {
		trustSessionStep(ctx, t, env, &trustStep{
			name: name, want: want, check: check,
			req: e2e.HeaderedSessionRequest{
				Method: method, Path: path, Body: body, IfMatch: headers["If-Match"], UserAgent: trustRuleAgent,
			},
		})
	}

	var worker e2ePrincipal

	principalCall(ctx, t, env, principalStep{
		http.MethodPost, "/api/server/namespaces/" + e2e.Namespace + "/principals", `{"subject":"mtls-deployer"}`,
		http.StatusCreated,
	}, &worker)

	var bundle, first, second, rule, ruleRev string

	step("a namespace with no bundle lists none", http.MethodGet, trustBundlesPath, "", nil, http.StatusOK,
		bodyHolds(`{"bundles":[]}`))
	step("a bundle that is not PEM is refused", http.MethodPost, trustBundlesPath, `{"pem":"not a certificate"}`, nil,
		http.StatusBadRequest, bodyHolds("trust-rule.bundle-invalid"))
	step("save the bundle", http.MethodPost, trustBundlesPath, `{"pem":`+jsonText(t, caPEM(t, "first ca"))+`}`, nil,
		http.StatusCreated, func(body string) error {
			if err := captureID(&bundle)(body); err != nil {
				return err
			}

			return captureRevision(&first)(body)
		})
	step("read the bundle", http.MethodGet, trustBundlesPath+"/"+bundle, "", nil, http.StatusOK,
		bodyHolds(`"active":1`))
	step("the active version is not deleted", http.MethodDelete, trustBundlesPath+"/"+bundle+"/versions/1", "", nil,
		http.StatusConflict, bodyHolds("trust-rule.bundle-invalid"))
	step("a rotation with no If-Match is refused", http.MethodPatch, trustBundlesPath+"/"+bundle,
		`{"pem":`+jsonText(t, caPEM(t, "second ca"))+`}`, nil, http.StatusPreconditionRequired,
		bodyHolds("request.precondition-required"))
	step("rotate the bundle", http.MethodPatch, trustBundlesPath+"/"+bundle,
		`{"pem":`+jsonText(t, caPEM(t, "second ca"))+`,"overlapSeconds":3600}`,
		map[string]string{"If-Match": first}, http.StatusOK, func(body string) error {
			if err := bodyHolds(`"active":2`)(body); err != nil {
				return err
			}

			return captureRevision(&second)(body)
		})
	step("a rotation with the old revision is refused", http.MethodPatch, trustBundlesPath+"/"+bundle,
		`{"pem":`+jsonText(t, caPEM(t, "third ca"))+`}`, map[string]string{"If-Match": first},
		http.StatusPreconditionFailed, bodyHolds("request.precondition-failed"))
	step("delete the retired version", http.MethodDelete, trustBundlesPath+"/"+bundle+"/versions/1", "", nil,
		http.StatusNoContent, nil)
	const absentBundle = "trb-00000000000000000000000000000000"

	ruleBody := trustRuleBody("mtls", worker.ID, trustRuleSubject)

	step("a bundle with another ID is not found", http.MethodGet, trustBundlesPath+"/"+absentBundle,
		"", nil, http.StatusNotFound, bodyHolds("trust-rule.bundle-not-found"))
	step("a rule names a bundle that does not exist", http.MethodPost, trustRulesPath,
		ruleBody[:len(ruleBody)-1]+`,"bundle":"`+absentBundle+`"}`, nil,
		http.StatusBadRequest, bodyHolds("trust-rule.bundle-invalid"))
	step("create a rule", http.MethodPost, trustRulesPath, ruleBody, nil,
		http.StatusCreated, func(body string) error {
			if err := captureID(&rule)(body); err != nil {
				return err
			}

			return captureRevision(&ruleRev)(body)
		})
	step("an update with no If-Match is refused", http.MethodPatch, trustRulesPath+"/"+rule,
		`{"name":"mtls renamed"}`, nil, http.StatusPreconditionRequired, bodyHolds("request.precondition-required"))
	step("update the rule", http.MethodPatch, trustRulesPath+"/"+rule,
		`{"name":"mtls renamed","bundle":"`+bundle+`","spiffePattern":"spiffe://example.org/ci/deploy"}`,
		map[string]string{"If-Match": ruleRev}, http.StatusOK, bodyHolds(`"name":"mtls renamed"`))
	step("an update with the old revision is refused", http.MethodPatch, trustRulesPath+"/"+rule,
		`{"name":"again"}`, map[string]string{"If-Match": ruleRev}, http.StatusPreconditionFailed,
		bodyHolds("request.precondition-failed"))
}
