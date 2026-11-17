package trustrules_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"time"
)

// bundlesURL is the bundle collection of the namespace acme.
const bundlesURL = "/api/dustpan/v1/namespaces/acme/trust-bundles"

// fixtureCAPEM makes a CA certificate with a fixed validity, so that no test
// reads the wall clock.
func (e *env) fixtureCAPEM() string {
	e.t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		e.t.Fatalf("generate key: %v", err)
	}

	var tmpl x509.Certificate

	tmpl.SerialNumber = big.NewInt(1)
	tmpl.Subject.CommonName = "test ca"
	tmpl.NotBefore = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	tmpl.NotAfter = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	tmpl.IsCA = true
	tmpl.BasicConstraintsValid = true
	tmpl.KeyUsage = x509.KeyUsageCertSign

	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		e.t.Fatalf("create certificate: %v", err)
	}

	var block pem.Block

	block.Type = "CERTIFICATE"
	block.Bytes = der

	return string(pem.EncodeToMemory(&block))
}

// mustSaveBundle saves a bundle of acme as the administrator and returns its
// ID.
func (e *env) mustSaveBundle() string {
	e.t.Helper()

	body, err := json.Marshal(map[string]string{"pem": e.fixtureCAPEM()})
	if err != nil {
		e.t.Fatalf("marshal: %v", err)
	}

	status, out := e.admin(http.MethodPost, bundlesURL, string(body))
	if status != http.StatusCreated {
		e.t.Fatalf("save bundle = %d %s, want 201", status, out)
	}

	var doc struct {
		ID string `json:"id"`
	}

	decode(e.t, out, &doc)

	return doc.ID
}
