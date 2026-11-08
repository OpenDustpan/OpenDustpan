// Package ldaptest is an in-process LDAPv3 directory for the tests of an
// LDAP sign-in method. It serves simple binds, searches with filters,
// StartTLS and referrals, over TLS (ldaps) and over a plain listener that
// offers StartTLS. A test can make it answer slowly, answer a size-limit
// result, or answer only a referral. It holds people and groups in memory.
// It is test infrastructure: the binary never links it.
package ldaptest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"math/big"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-ldap/ldap/v3"
)

// The tree of the directory.
const (
	// BaseDN is the root entry of the directory.
	BaseDN = "dc=example,dc=org"
	// PeopleDN holds each user.
	PeopleDN = "ou=people," + BaseDN
	// GroupsDN holds each group.
	GroupsDN = "ou=groups," + BaseDN
	// ServiceDN is the DN of the service account.
	ServiceDN = "cn=service," + BaseDN
)

// Fault is a wrong answer that the directory gives.
type Fault int

// Faults of the directory.
const (
	// FaultNone answers each request as a directory does.
	FaultNone Fault = iota
	// FaultSlow answers no search until the server stops.
	FaultSlow
	// FaultSlowGroups answers no search below GroupsDN until the server
	// stops. Each other search gets its answer.
	FaultSlowGroups
	// FaultSizeLimit answers each search with at most one entry and the
	// result sizeLimitExceeded.
	FaultSizeLimit
	// FaultSearchReference answers each search with only a search
	// continuation reference to the directory itself.
	FaultSearchReference
	// FaultSearchReferral answers each search with the result referral.
	FaultSearchReferral
	// FaultBindReferral answers each bind that is not the bind of the
	// service account with the result referral.
	FaultBindReferral
	// FaultUserOutsideBase answers each user entry with a DN below
	// OutsideDN instead of its own DN.
	FaultUserOutsideBase
	// FaultGroupOutsideBase answers each group entry with a DN below
	// OutsideDN instead of its own DN.
	FaultGroupOutsideBase
	// FaultGroupOtherEntry answers the read of one group entry with the DN
	// of another group below GroupsDN.
	FaultGroupOtherEntry
)

// DiagnosticPrefix starts the diagnostic message of each result of the
// directory that is not a success, so a test can find the text of the
// directory in an error or a log.
const DiagnosticPrefix = "ldaptest-diagnostic: "

// OutsideDN is the base of the DNs of FaultUserOutsideBase and
// FaultGroupOutsideBase. It is outside the tree of the directory.
const OutsideDN = "ou=elsewhere,dc=other,dc=org"

// Link says where the directory shows the members of a group.
type Link int

// Kinds of link.
const (
	// LinkBoth shows a member in the member attribute of the group and the
	// group in the memberOf attribute of the member.
	LinkBoth Link = iota
	// LinkMember shows a member only in the member attribute of the group.
	LinkMember
	// LinkMemberOf shows the group only in the memberOf attribute of the
	// member.
	LinkMemberOf
)

// User is a person of the directory. Its DN is
// "uid=<UserName>,ou=people,dc=example,dc=org".
type User struct {
	// Attributes are the other attributes of the entry, by name. A value
	// can hold any bytes.
	Attributes map[string][]string
	// UserName is the uid of the entry.
	UserName string
	// Password is the password of a bind as the user.
	Password string
}

// Group is a group of the directory. Its DN is
// "cn=<Name>,ou=groups,dc=example,dc=org".
type Group struct {
	// Name is the cn of the entry.
	Name string
	// Users are the user names of its user members.
	Users []string
	// Groups are the names of its group members.
	Groups []string
	// Link says where the directory shows its members.
	Link Link
}

// BindHook is a function that the directory calls before it answers a
// bind. It receives the DN of the bind. A test uses it to hold the answer
// of a bind until the test releases it.
type BindHook func(dn string)

// Server is a running directory. Its methods are safe for concurrent use.
type Server struct {
	pool            *x509.CertPool
	tlsCfg          *tls.Config
	tlsLn           net.Listener
	plainLn         net.Listener
	release         chan struct{}
	servicePassword string
	conns           map[net.Conn]struct{}
	closeErrs       []error
	cache           []entry
	index           map[string]int
	users           []User
	groups          []Group
	binds           []string
	wg              sync.WaitGroup
	once            sync.Once
	mu              sync.Mutex
	fault           Fault
	bindHook        BindHook
	accepts         int
	stopped         bool
}

// listeners is the count of the listeners of a directory.
const listeners = 2

// Start starts a directory on loopback with a new certificate authority,
// and stops it when the test ends.
func Start(tb testing.TB) *Server {
	tb.Helper()

	cert, pool := newCertificate(tb)
	tlsCfg := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}

	var lc net.ListenConfig

	tlsLn, err := lc.Listen(tb.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("listen: %v", err)
	}

	plainLn, err := lc.Listen(tb.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("listen: %v", err)
	}

	s := &Server{
		pool:            pool,
		tlsCfg:          tlsCfg,
		tlsLn:           tls.NewListener(tlsLn, tlsCfg),
		plainLn:         plainLn,
		release:         make(chan struct{}),
		servicePassword: rand.Text(),
		conns:           map[net.Conn]struct{}{},
		closeErrs:       nil,
		cache:           nil,
		index:           nil,
		users:           nil,
		groups:          nil,
		binds:           nil,
		wg:              sync.WaitGroup{},
		once:            sync.Once{},
		mu:              sync.Mutex{},
		fault:           FaultNone,
		bindHook:        nil,
		accepts:         0,
		stopped:         false,
	}

	s.wg.Add(listeners)

	go s.serve(s.tlsLn)
	go s.serve(s.plainLn)

	tb.Cleanup(func() {
		for _, err := range s.stopAndReport() {
			tb.Logf("ldaptest: %v", err)
		}
	})

	return s
}

// newCertificate returns a self-signed certificate for 127.0.0.1, ::1 and
// localhost, and a pool that trusts it.
func newCertificate(tb testing.TB) (tls.Certificate, *x509.CertPool) {
	tb.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		tb.Fatalf("generate key: %v", err)
	}

	now := time.Now()

	// The template sets only the fields that a self-signed server
	// certificate needs.
	var tmpl x509.Certificate

	tmpl.SerialNumber = big.NewInt(1)
	tmpl.Subject.CommonName = "ldaptest"
	tmpl.NotBefore, tmpl.NotAfter = now.Add(-time.Hour), now.Add(time.Hour)
	tmpl.IsCA, tmpl.BasicConstraintsValid = true, true
	tmpl.KeyUsage = x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign
	tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	tmpl.DNSNames = []string{"localhost"}
	tmpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1"), net.IPv6loopback}

	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		tb.Fatalf("create certificate: %v", err)
	}

	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		tb.Fatalf("parse certificate: %v", err)
	}

	pool := x509.NewCertPool()
	pool.AddCert(leaf)

	var cert tls.Certificate

	cert.Certificate, cert.PrivateKey, cert.Leaf = [][]byte{der}, key, leaf

	return cert, pool
}

// LDAPSURL returns the ldaps:// URL of the TLS listener, with the address
// 127.0.0.1.
func (s *Server) LDAPSURL() string {
	return "ldaps://" + s.tlsLn.Addr().String()
}

// PlainURL returns the ldap:// URL of the plain listener, with the address
// 127.0.0.1. The listener offers StartTLS.
func (s *Server) PlainURL() string {
	return "ldap://" + s.plainLn.Addr().String()
}

// PlainNameURL returns the ldap:// URL of the plain listener with the host
// name localhost.
func (s *Server) PlainNameURL() string {
	addr, ok := s.plainLn.Addr().(*net.TCPAddr)
	if !ok {
		return ""
	}

	return "ldap://localhost:" + strconv.Itoa(addr.Port)
}

// ServicePassword returns the password of the service account. Each
// directory makes a new random password when it starts.
func (s *Server) ServicePassword() string {
	return s.servicePassword
}

// CertPool returns a pool that trusts the certificate of the directory.
func (s *Server) CertPool() *x509.CertPool {
	return s.pool
}

// UserDN returns the DN of the user name.
func UserDN(userName string) string {
	return "uid=" + escapeValue(userName) + "," + PeopleDN
}

// GroupDN returns the DN of the group name.
func GroupDN(name string) string {
	return "cn=" + escapeValue(name) + "," + GroupsDN
}

// AddUser adds the user u and returns its DN. Two users can have the same
// user name: a search for that name then finds both.
func (s *Server) AddUser(u User) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.users = append(s.users, u)
	s.cache, s.index = nil, nil

	return UserDN(u.UserName)
}

// AddGroup adds the group g. Its members do not need to exist yet.
func (s *Server) AddGroup(g Group) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.groups = append(s.groups, g)
	s.cache, s.index = nil, nil
}

// SetFault makes the directory answer with the fault f.
func (s *Server) SetFault(f Fault) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.fault = f
}

// SetBindHook sets the function that the directory calls before it answers
// each bind. A nil function answers each bind at once. A hook that waits
// holds the answer of the bind.
func (s *Server) SetBindHook(f BindHook) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.bindHook = f
}

// Binds returns the DN of each bind that the directory received, in order.
// It never holds a password.
func (s *Server) Binds() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.binds)
}

// Connections returns the count of the connections that the directory
// accepted.
func (s *Server) Connections() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.accepts
}

// Stop closes the listeners and each connection, and waits for each
// goroutine of the directory. A second call does nothing. The test logs the
// errors of each close when it ends.
func (s *Server) Stop() {
	s.once.Do(func() {
		s.mu.Lock()
		s.stopped = true
		close(s.release)

		for c := range s.conns {
			s.closeErrs = append(s.closeErrs, c.Close())
		}
		s.mu.Unlock()

		tlsErr := s.tlsLn.Close()
		plainErr := s.plainLn.Close()

		s.wg.Wait()

		s.mu.Lock()
		s.closeErrs = append(s.closeErrs, tlsErr, plainErr)
		s.mu.Unlock()
	})
}

// stopAndReport calls Stop and returns the errors of each close, except the error of
// a connection that was closed before.
func (s *Server) stopAndReport() []error {
	s.Stop()

	s.mu.Lock()
	defer s.mu.Unlock()

	var out []error

	for _, err := range s.closeErrs {
		if err != nil && !errors.Is(err, net.ErrClosed) {
			out = append(out, err)
		}
	}

	return out
}

// closeConn closes c and records the error.
func (s *Server) closeConn(c net.Conn) {
	err := c.Close()

	s.mu.Lock()
	defer s.mu.Unlock()

	s.closeErrs = append(s.closeErrs, err)
}

// serve accepts the connections of ln until it closes.
func (s *Server) serve(ln net.Listener) {
	defer s.wg.Done()

	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}

		s.mu.Lock()
		if s.stopped {
			s.mu.Unlock()
			s.closeConn(c)

			return
		}

		s.accepts++
		s.conns[c] = struct{}{}
		s.wg.Add(1)
		s.mu.Unlock()

		go func() {
			defer s.wg.Done()

			s.handle(c)
		}()
	}
}

// track replaces the connection old with c in the list of open
// connections, after StartTLS.
func (s *Server) track(old, c net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.conns, old)
	s.conns[c] = struct{}{}
}

// forget removes c from the list of open connections and closes it.
func (s *Server) forget(c net.Conn) {
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()

	s.closeConn(c)
}

// currentFault returns the fault of the directory.
func (s *Server) currentFault() Fault {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.fault
}

// currentBindHook returns the function that the directory calls before it
// answers a bind.
func (s *Server) currentBindHook() BindHook {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.bindHook
}

// recordBind records the DN of a bind.
func (s *Server) recordBind(dn string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.binds = append(s.binds, dn)
}

// checkPassword reports whether password is the password of the entry dn.
func (s *Server) checkPassword(dn, password string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sameDN(dn, ServiceDN) {
		return password == s.servicePassword
	}

	for _, u := range s.users {
		if sameDN(dn, UserDN(u.UserName)) {
			return password == u.Password
		}
	}

	return false
}

// sameDN reports whether a and b name the same entry.
func sameDN(a, b string) bool {
	pa, err := ldap.ParseDN(a)
	if err != nil {
		return false
	}

	pb, err := ldap.ParseDN(b)
	if err != nil {
		return false
	}

	return pa.EqualFold(pb)
}

// escapeValue escapes s for an attribute value of a DN (RFC 4514). It is
// the directory's own escape, so a test of a method does not test the
// escape of the method against itself.
func escapeValue(s string) string {
	var b strings.Builder

	for i, r := range s {
		switch {
		case strings.ContainsRune(`"+,;<>\=`, r),
			i == 0 && (r == ' ' || r == '#'),
			i == len(s)-1 && r == ' ':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}

	return b.String()
}

// errStop ends the handling of a connection.
var errStop = errors.New("the connection ends")
