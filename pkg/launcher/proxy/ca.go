package proxy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"sync"
	"time"
)

// noInspectHost is the one name that a CA with no inspect host may sign. A
// CA with an empty list of permitted names has no name constraint at all, so
// the CA gets a name that no real host has (the .invalid top-level domain).
const noInspectHost = "no-inspect-rule.invalid"

// Bounds of the certificates.
const (
	// caBackdate is how long before now the certificates start, for clocks
	// that differ a little.
	caBackdate = time.Minute
	// serialBits is the size of a random serial number.
	serialBits = 128
	// ipv4Bits and ipv6Bits are the sizes of an address.
	ipv4Bits = 32
	ipv6Bits = 128
)

// ErrCA is the error for a CA that cannot be made.
var ErrCA = errors.New("the job proxy cannot make its CA")

// ca is the CA of one claim. The key stays in this struct: no method returns
// it.
type ca struct {
	cert   *x509.Certificate
	pem    []byte
	key    *ecdsa.PrivateKey
	now    func() time.Time
	notAft time.Time

	mu     sync.Mutex
	leaves map[string]*tls.Certificate
}

// newCA makes an ECDSA P-256 key and a CA certificate in memory. The CA is
// constrained to hosts (exact names) and to no IP address, and it signs no
// other CA.
func newCA(hosts []string, lifetime time.Duration, now func() time.Time) (*ca, error) {
	if lifetime <= 0 {
		return nil, fmt.Errorf("%w: the lifetime is not positive", ErrCA)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("%w: key", ErrCA)
	}

	permitted := append([]string(nil), hosts...)
	if len(permitted) == 0 {
		permitted = []string{noInspectHost}
	}

	serial, err := serialNumber()
	if err != nil {
		return nil, err
	}

	end := now().Add(lifetime)

	var tmpl x509.Certificate

	tmpl.SerialNumber = serial
	var subject pkix.Name

	subject.CommonName = "OpenDustpan job proxy CA"
	tmpl.Subject = subject
	tmpl.NotBefore = now().Add(-caBackdate)
	tmpl.NotAfter = end
	tmpl.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature
	tmpl.BasicConstraintsValid = true
	tmpl.IsCA = true
	tmpl.MaxPathLen = 0
	tmpl.MaxPathLenZero = true
	tmpl.PermittedDNSDomainsCritical = true
	tmpl.PermittedDNSDomains = permitted
	tmpl.ExcludedIPRanges = []*net.IPNet{allIPv4(), allIPv6()}

	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("%w: certificate", ErrCA)
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("%w: parse", ErrCA)
	}

	return &ca{
		cert:   cert,
		pem:    pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Headers: nil, Bytes: der}),
		key:    key,
		now:    now,
		notAft: end,
		mu:     sync.Mutex{},
		leaves: map[string]*tls.Certificate{},
	}, nil
}

// allIPv4 is 0.0.0.0/0.
func allIPv4() *net.IPNet {
	return &net.IPNet{IP: net.IPv4zero.To4(), Mask: net.CIDRMask(0, ipv4Bits)}
}

// allIPv6 is ::/0.
func allIPv6() *net.IPNet {
	return &net.IPNet{IP: net.IPv6zero, Mask: net.CIDRMask(0, ipv6Bits)}
}

// serialNumber returns a random 128-bit serial.
func serialNumber() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), serialBits)

	n, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("%w: serial", ErrCA)
	}

	return n, nil
}

// leaf returns the certificate for host, signed by the CA. It makes it once
// and keeps it for the claim.
func (c *ca) leaf(host string) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if l, ok := c.leaves[host]; ok {
		return l, nil
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("%w: leaf key", ErrCA)
	}

	serial, err := serialNumber()
	if err != nil {
		return nil, err
	}

	var tmpl x509.Certificate

	tmpl.SerialNumber = serial
	var subject pkix.Name

	subject.CommonName = host
	tmpl.Subject = subject
	tmpl.DNSNames = []string{host}
	tmpl.NotBefore = c.now().Add(-caBackdate)
	tmpl.NotAfter = c.notAft
	tmpl.KeyUsage = x509.KeyUsageDigitalSignature
	tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	tmpl.BasicConstraintsValid = true

	der, err := x509.CreateCertificate(rand.Reader, &tmpl, c.cert, &key.PublicKey, c.key)
	if err != nil {
		return nil, fmt.Errorf("%w: leaf", ErrCA)
	}

	var l tls.Certificate

	l.Certificate = [][]byte{der}
	l.PrivateKey = key
	c.leaves[host] = &l

	return &l, nil
}
