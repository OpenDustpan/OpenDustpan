package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Lifetime of the self-signed certificate of dev, and the time before its end
// from which a start makes a new one.
const (
	devCertLifetime = 30 * 24 * time.Hour
	devCertRenewAt  = 7 * 24 * time.Hour
	// devCertBackdate keeps a client with a slow clock from refusing a new
	// certificate.
	devCertBackdate = time.Minute
	devSerialBits   = 128
)

// errDevCert reports a certificate file of dev that dev cannot use.
var errDevCert = errors.New("the certificate of dev is not valid")

// devCert is the self-signed certificate of dev: the paths of its files and
// the end of its life.
type devCert struct {
	certFile string
	keyFile  string
	// bundleFile is the file that SSL_CERT_FILE names in dev.env, or empty
	// when dev writes none.
	bundleFile string
	notAfter   time.Time
}

// devCertInput holds what the certificate of dev needs. Names are the DNS
// names and IP addresses that the certificate holds. RootFiles lists the
// files that hold the roots that the bundle starts with, or is empty for the
// leaf alone. WriteBundle says whether dev writes a bundle file.
type devCertInput struct {
	now         func() time.Time
	dir         string
	names       []string
	roots       []byte
	writeBundle bool
}

// devCertNames answers the names that the certificate of dev holds, sorted
// and with no repeat: localhost, 127.0.0.1, ::1, the host of the listen
// address when it is not a wildcard, the name of the host and each name of
// --hostname.
func devCertNames(listenHost, hostName string, extra []string) []string {
	names := []string{"localhost", "127.0.0.1", "::1"}

	if listenHost != "" {
		if ip := net.ParseIP(listenHost); ip == nil || !ip.IsUnspecified() {
			names = append(names, listenHost)
		}
	}

	if hostName != "" {
		names = append(names, hostName)
	}

	for _, name := range extra {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}

	slices.Sort(names)

	return slices.Compact(names)
}

// ensureDevCert answers the certificate of dev in dir. It reuses the files
// of an earlier start when the leaf holds the names that this start needs and
// at least 7 days remain. Else it makes a new leaf with a new key. It writes
// the bundle file again at each start, so a change of the roots of the host
// reaches it.
func ensureDevCert(in devCertInput) (devCert, error) {
	cert := devCert{
		certFile: filepath.Join(in.dir, devCertName), keyFile: filepath.Join(in.dir, devTLSKeyName),
		bundleFile: "", notAfter: time.Time{},
	}

	if err := checkDevFile(cert.keyFile, privateSecret); err != nil {
		return devCert{}, err
	}

	if err := checkDevFile(cert.certFile, plainFile); err != nil {
		return devCert{}, err
	}

	leaf, leafPEM, ok := reuseDevLeaf(cert, in)
	if !ok {
		var err error

		leaf, leafPEM, err = makeDevLeaf(cert, in)
		if err != nil {
			return devCert{}, err
		}
	}

	cert.notAfter = leaf.NotAfter

	if in.writeBundle {
		cert.bundleFile = filepath.Join(in.dir, devBundleName)

		bundle := slices.Concat(in.roots, newlineIfNeeded(in.roots), leafPEM)
		if err := writePrivateFile(cert.bundleFile, bundle); err != nil {
			return devCert{}, err
		}
	}

	return cert, nil
}

// newlineIfNeeded answers a line feed when roots does not end with one, so
// the leaf starts on its own line.
func newlineIfNeeded(roots []byte) []byte {
	if len(roots) == 0 || bytes.HasSuffix(roots, []byte("\n")) {
		return nil
	}

	return []byte("\n")
}

// reuseDevLeaf reads the leaf of an earlier start. It answers false when the
// files are missing or do not match, when the leaf does not hold exactly the
// names of this start, or when less than 7 days remain.
func reuseDevLeaf(cert devCert, in devCertInput) (*x509.Certificate, []byte, bool) {
	certPEM, err := os.ReadFile(cert.certFile)
	if err != nil {
		return nil, nil, false
	}

	if _, keyErr := os.Stat(cert.keyFile); keyErr != nil {
		return nil, nil, false
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, nil, false
	}

	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, false
	}

	if !in.now().Add(devCertRenewAt).Before(leaf.NotAfter) || !slices.Equal(leafNames(leaf), in.names) {
		return nil, nil, false
	}

	return leaf, certPEM, true
}

// leafNames answers the sorted names that a certificate holds, as
// devCertNames does.
func leafNames(leaf *x509.Certificate) []string {
	names := slices.Clone(leaf.DNSNames)
	for _, ip := range leaf.IPAddresses {
		names = append(names, ip.String())
	}

	slices.Sort(names)

	return slices.Compact(names)
}

// makeDevLeaf makes the key and the self-signed leaf certificate of dev and
// writes both with mode 0600. The leaf is no certificate authority, and
// nothing in the trust store changes.
func makeDevLeaf(cert devCert, in devCertInput) (*x509.Certificate, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("make the key of the certificate of dev: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), devSerialBits))
	if err != nil {
		return nil, nil, fmt.Errorf("make the serial number of the certificate of dev: %w", err)
	}

	now := in.now()
	//nolint:exhaustruct_v5 // The standard library fills the other fields.
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: "opendustpan dev",
		}, //nolint:exhaustruct_v5 // Only the name is set.
		NotBefore:             now.Add(-devCertBackdate),
		NotAfter:              now.Add(devCertLifetime),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
	}

	for _, name := range in.names {
		if ip := net.ParseIP(name); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, name)
		}
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("make the certificate of dev: %w", err)
	}

	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", errDevCert, err)
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("encode the key of the certificate of dev: %w", err)
	}

	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Headers: nil, Bytes: keyDER})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Headers: nil, Bytes: der})

	if writeErr := writePrivateFile(cert.keyFile, keyPEM); writeErr != nil {
		return nil, nil, writeErr
	}

	if writeErr := writePrivateFile(cert.certFile, certPEM); writeErr != nil {
		return nil, nil, writeErr
	}

	return leaf, certPEM, nil
}

// rootFileCandidates are the files that Go reads for the roots of the system
// on Linux, in the order of Go.
//
//nolint:gochecknoglobals // A frozen list, as Go keeps it.
var rootFileCandidates = []string{
	"/etc/ssl/certs/ca-certificates.crt",
	"/etc/pki/tls/certs/ca-bundle.crt",
	"/etc/ssl/ca-bundle.pem",
	"/etc/pki/tls/cacert.pem",
	"/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem",
	"/etc/ssl/cert.pem",
}

// rootCertDirs are the directories that Go reads for the roots of the system
// on Linux when no root file exists.
//
//nolint:gochecknoglobals // A frozen list, as Go keeps it.
var rootCertDirs = []string{"/etc/ssl/certs", "/etc/pki/tls/certs"}

// devRootsInput holds what the choice of the roots reads. The fields let a
// test replace the system.
type devRootsInput struct {
	lookup func(string) (string, bool)
	files  []string
	dirs   []string
}

// devRoots answers the PEM text of the roots that the shell of dev uses at
// the start: the file in SSL_CERT_FILE, else each directory in SSL_CERT_DIR,
// else the first root file that Go reads on Linux, else the system
// directories. It answers nothing when no root is found, and the bundle then
// holds the certificate of dev alone.
func devRoots(in devRootsInput) []byte {
	if path, ok := in.lookup("SSL_CERT_FILE"); ok && path != "" {
		if data, err := os.ReadFile(path); err == nil { //nolint:gosec // The shell of the user names the file.
			return data
		}
	}

	if dirs, ok := in.lookup("SSL_CERT_DIR"); ok && dirs != "" {
		return readCertDirs(strings.Split(dirs, string(os.PathListSeparator)))
	}

	for _, path := range in.files {
		if data, err := os.ReadFile(path); err == nil { //nolint:gosec // A fixed system path.
			return data
		}
	}

	return readCertDirs(in.dirs)
}

// readCertDirs joins the PEM files in each directory, in name order. A file
// that cannot be read is skipped, as Go does.
func readCertDirs(dirs []string) []byte {
	var out []byte

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			data, readErr := os.ReadFile(filepath.Join(dir, entry.Name())) //nolint:gosec // A directory of roots.
			if readErr != nil {
				continue
			}

			out = append(out, data...)

			if !bytes.HasSuffix(data, []byte("\n")) {
				out = append(out, '\n')
			}
		}
	}

	return out
}
