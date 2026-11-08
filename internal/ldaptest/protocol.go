package ldaptest

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	ber "github.com/go-asn1-ber/asn1-ber"
	"github.com/go-ldap/ldap/v3"
)

// Application tags of LDAPv3 (RFC 4511 section 4.2 and on).
const (
	appBindRequest     = 0
	appBindResponse    = 1
	appSearchRequest   = 3
	appSearchEntry     = 4
	appSearchDone      = 5
	appAbandonRequest  = 16
	appSearchReference = 19
	appExtendedRequest = 23
	appExtendedResp    = 24
)

// Field counts and tags of the requests.
const (
	// bindFields is the count of the fields of a bind request.
	bindFields = 3
	// searchFields is the count of the fields of a search request.
	searchFields = 8
	// referralTag is the context tag of the referral of an LDAPResult.
	referralTag = 3
	// handshakeTimeout bounds the TLS handshake of StartTLS.
	handshakeTimeout = 10 * time.Second
)

// Result codes of LDAPv3.
const (
	resultSuccess            = 0
	resultProtocolError      = 2
	resultSizeLimit          = 4
	resultAuthNotSupported   = 7
	resultReferral           = 10
	resultNoSuchObject       = 32
	resultInvalidCredentials = 49
)

// startTLSOID is the name of the StartTLS extended operation.
const startTLSOID = "1.3.6.1.4.1.1466.20037"

// slowFor is the longest time of a slow answer. The server stops sooner in
// each test.
const slowFor = 30 * time.Second

// handle serves the requests of one connection until it closes.
func (s *Server) handle(c net.Conn) {
	defer func() { s.forget(c) }()

	for {
		p, err := ber.ReadPacket(c)
		if err != nil || len(p.Children) < 2 {
			return
		}

		id, ok := p.Children[0].Value.(int64)
		if !ok {
			return
		}

		op := p.Children[1]

		next, err := s.serveOp(c, id, op)
		if err != nil {
			return
		}

		if next != c {
			s.track(c, next)
			c = next
		}
	}
}

// serveOp answers one operation. It returns the connection to read next:
// a TLS connection after StartTLS.
func (s *Server) serveOp(c net.Conn, id int64, op *ber.Packet) (net.Conn, error) {
	if op.ClassType != ber.ClassApplication {
		return c, errStop
	}

	switch uint64(op.Tag) {
	case appBindRequest:
		return c, write(c, id, s.bind(op))
	case appSearchRequest:
		return c, s.search(c, id, op)
	case appExtendedRequest:
		if _, isTLS := c.(*tls.Conn); isTLS {
			return c, write(c, id, result(appExtendedResp, resultProtocolError, "TLS is on already", ""))
		}

		return s.extended(c, id, op)
	case appAbandonRequest:
		return c, nil
	default:
		// An unbind request, and each operation that the directory does
		// not serve, ends the connection.
		return c, errStop
	}
}

// write sends the operation op of the message id.
func write(c net.Conn, id int64, op *ber.Packet) error {
	msg := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "LDAP message")
	msg.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagInteger, id, "message ID"))
	msg.AppendChild(op)

	if _, err := c.Write(msg.Bytes()); err != nil {
		return fmt.Errorf("write: %w", err)
	}

	return nil
}

// result returns an LDAPResult of the tag, with the referral URL when it
// is not empty.
func result(tag uint64, code int64, diagnostic, referral string) *ber.Packet {
	if code != resultSuccess {
		diagnostic = DiagnosticPrefix + diagnostic
	}

	p := ber.Encode(ber.ClassApplication, ber.TypeConstructed, ber.Tag(tag), nil, "result")
	p.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagEnumerated, code, "code"))
	p.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", "matched DN"))
	p.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, diagnostic, "message"))

	if referral != "" {
		r := ber.Encode(ber.ClassContext, ber.TypeConstructed, referralTag, nil, "referral")
		r.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, referral, "URI"))
		p.AppendChild(r)
	}

	return p
}

// octets returns the content of a primitive packet.
func octets(p *ber.Packet) []byte {
	if p == nil {
		return nil
	}

	if p.Data != nil {
		return p.Data.Bytes()
	}

	if v, ok := p.Value.(string); ok {
		return []byte(v)
	}

	return nil
}

// referralURL is the URL of each referral: the directory itself.
func (s *Server) referralURL() string {
	return s.LDAPSURL() + "/ou=elsewhere," + BaseDN
}

// bind answers a bind request.
func (s *Server) bind(op *ber.Packet) *ber.Packet {
	if len(op.Children) < bindFields {
		return result(appBindResponse, resultProtocolError, "bad bind request", "")
	}

	dn := string(octets(op.Children[1]))
	auth := op.Children[2]

	s.recordBind(dn)

	if hook := s.currentBindHook(); hook != nil {
		hook(dn)
	}

	if auth.ClassType != ber.ClassContext || auth.Tag != 0 {
		return result(appBindResponse, resultAuthNotSupported, "only simple binds", "")
	}

	if s.currentFault() == FaultBindReferral && !sameDN(dn, ServiceDN) {
		return result(appBindResponse, resultReferral, "bind elsewhere", s.referralURL())
	}

	password := string(octets(auth))

	switch {
	case password == "":
		// An unauthenticated bind (RFC 4513 section 5.1.2): many
		// directories accept it, so this directory does too.
		return result(appBindResponse, resultSuccess, "", "")
	case s.checkPassword(dn, password):
		return result(appBindResponse, resultSuccess, "", "")
	default:
		return result(appBindResponse, resultInvalidCredentials, "invalid credentials", "")
	}
}

// extended answers an extended request on a connection with no TLS:
// StartTLS only.
func (s *Server) extended(c net.Conn, id int64, op *ber.Packet) (net.Conn, error) {
	if len(op.Children) < 1 || string(octets(op.Children[0])) != startTLSOID {
		return c, write(c, id, result(appExtendedResp, resultProtocolError, "not supported", ""))
	}

	if err := write(c, id, result(appExtendedResp, resultSuccess, "", "")); err != nil {
		return c, err
	}

	tc := tls.Server(c, s.tlsCfg)

	ctx, cancel := context.WithTimeout(context.Background(), handshakeTimeout)
	defer cancel()

	if err := tc.HandshakeContext(ctx); err != nil {
		return c, fmt.Errorf("handshake: %w", err)
	}

	return tc, nil
}

// searchRequest is a decoded search request.
type searchRequest struct {
	filter    *ber.Packet
	base      string
	attrs     []string
	scope     int64
	sizeLimit int64
}

// decodeSearch decodes the search request op.
func decodeSearch(op *ber.Packet) (searchRequest, bool) {
	var none searchRequest

	if len(op.Children) < searchFields {
		return none, false
	}

	scope, ok1 := op.Children[1].Value.(int64)
	size, ok2 := op.Children[3].Value.(int64)

	if !ok1 || !ok2 {
		return none, false
	}

	attrs := make([]string, 0, len(op.Children[7].Children))
	for _, a := range op.Children[7].Children {
		attrs = append(attrs, string(octets(a)))
	}

	return searchRequest{
		filter: op.Children[6], base: string(octets(op.Children[0])), attrs: attrs, scope: scope, sizeLimit: size,
	}, true
}

// search answers a search request.
func (s *Server) search(c net.Conn, id int64, op *ber.Packet) error {
	req, ok := decodeSearch(op)
	if !ok {
		return write(c, id, result(appSearchDone, resultProtocolError, "bad search request", ""))
	}

	fault := s.currentFault()

	if fault == FaultSlow || fault == FaultSlowGroups && isGroupBase(req.base) {
		timer := time.NewTimer(slowFor)
		defer timer.Stop()

		select {
		case <-s.release:
		case <-timer.C:
		}

		return errStop
	}

	if fault == FaultSearchReference {
		ref := ber.Encode(ber.ClassApplication, ber.TypeConstructed, ber.Tag(appSearchReference), nil, "reference")
		ref.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, s.referralURL(),
			"URI"))

		if err := write(c, id, ref); err != nil {
			return err
		}

		return write(c, id, result(appSearchDone, resultSuccess, "", ""))
	}

	if fault == FaultSearchReferral {
		return write(c, id, result(appSearchDone, resultReferral, "search elsewhere", s.referralURL()))
	}

	return s.answerSearch(c, id, req)
}

// answerSearch sends the entries that match req.
func (s *Server) answerSearch(c net.Conn, id int64, req searchRequest) error {
	entries, found := s.match(req)
	if !found {
		return write(c, id, result(appSearchDone, resultNoSuchObject, "no such object", ""))
	}

	limit := len(entries)
	code := int64(resultSuccess)

	switch {
	case s.currentFault() == FaultSizeLimit:
		limit, code = min(limit, 1), resultSizeLimit
	case req.sizeLimit > 0 && int64(len(entries)) > req.sizeLimit:
		limit, code = int(req.sizeLimit), resultSizeLimit
	default:
	}

	fault := s.currentFault()

	for _, e := range entries[:limit] {
		e.text = movedDN(fault, e.text, req.scope)

		if err := write(c, id, e.packet(req.attrs)); err != nil {
			return err
		}
	}

	return write(c, id, result(appSearchDone, code, "", ""))
}

// match returns the entries in the scope of req that match its filter,
// and false when the base entry does not exist.
func (s *Server) match(req searchRequest) ([]entry, bool) {
	base, err := ldap.ParseDN(req.base)
	if err != nil {
		return nil, false
	}

	all, index := s.entries()

	// A read of one entry finds it in the index. A base with another
	// spelling of the same DN falls back to the scan below.
	if i, ok := index[strings.ToLower(req.base)]; ok && req.scope == ldap.ScopeBaseObject {
		if matches(req.filter, all[i]) {
			return []entry{all[i]}, true
		}

		return []entry{}, true
	}

	found := false

	out := make([]entry, 0, len(all))

	for _, e := range all {
		if e.dn.EqualFold(base) {
			found = true
		}

		if !inScope(base, e.dn, req.scope) || !matches(req.filter, e) {
			continue
		}

		out = append(out, e)
	}

	return out, found
}

// inScope reports whether dn is in the scope of base.
func inScope(base, dn *ldap.DN, scope int64) bool {
	switch scope {
	case ldap.ScopeBaseObject:
		return base.EqualFold(dn)
	case ldap.ScopeSingleLevel:
		return base.AncestorOfFold(dn) && len(dn.RDNs) == len(base.RDNs)+1
	default:
		return base.EqualFold(dn) || base.AncestorOfFold(dn)
	}
}

// Filter tags (RFC 4511 section 4.5.1).
const (
	filterAnd        = 0
	filterOr         = 1
	filterNot        = 2
	filterEquality   = 3
	filterSubstrings = 4
	filterPresent    = 7
)

// matches reports whether the entry e matches the filter f. A filter item
// that the directory does not know matches nothing.
func matches(f *ber.Packet, e entry) bool {
	if f == nil || f.ClassType != ber.ClassContext {
		return false
	}

	switch uint64(f.Tag) {
	case filterAnd:
		return !slices.ContainsFunc(f.Children, func(c *ber.Packet) bool { return !matches(c, e) })
	case filterOr:
		return slices.ContainsFunc(f.Children, func(c *ber.Packet) bool { return matches(c, e) })
	case filterNot:
		return len(f.Children) == 1 && !matches(f.Children[0], e)
	default:
		return matchesItem(f, e)
	}
}

// matchesItem reports whether the entry e matches the filter item f: an
// equality, a substrings or a present item.
func matchesItem(f *ber.Packet, e entry) bool {
	switch uint64(f.Tag) {
	case filterEquality:
		return len(f.Children) == 2 && e.equal(string(octets(f.Children[0])), octets(f.Children[1]))
	case filterSubstrings:
		return len(f.Children) == 2 && e.substrings(string(octets(f.Children[0])), f.Children[1].Children)
	case filterPresent:
		return len(e.values(string(octets(f)))) > 0
	default:
		return false
	}
}

// isDNAttribute reports whether the values of the attribute name are DNs.
func isDNAttribute(name string) bool {
	n := strings.ToLower(name)

	return n == "member" || n == "memberof"
}

// equal reports whether a value of the attribute name of e equals v.
func (e entry) equal(name string, v []byte) bool {
	if isDNAttribute(name) {
		_, ok := e.dnKeys[strings.ToLower(name)][dnKey(string(v))]

		return ok
	}

	for _, have := range e.values(name) {
		if bytes.EqualFold(have, v) {
			return true
		}
	}

	return false
}

// substrings reports whether a value of the attribute name of e matches
// the substrings parts: initial (0), any (1) and final (2).
func (e entry) substrings(name string, parts []*ber.Packet) bool {
	for _, have := range e.values(name) {
		if substringMatch(strings.ToLower(string(have)), parts) {
			return true
		}
	}

	return false
}

// substringMatch reports whether v matches the parts.
func substringMatch(v string, parts []*ber.Packet) bool {
	for _, p := range parts {
		part := strings.ToLower(string(octets(p)))

		switch uint64(p.Tag) {
		case 0:
			if !strings.HasPrefix(v, part) {
				return false
			}

			v = v[len(part):]
		case 1:
			i := strings.Index(v, part)
			if i < 0 {
				return false
			}

			v = v[i+len(part):]
		default:
			if !strings.HasSuffix(v, part) {
				return false
			}

			v = ""
		}
	}

	return true
}

// movedDN returns the DN that the directory answers for the entry dn with
// the fault: a DN below OutsideDN, the DN of another group, or dn.
func movedDN(fault Fault, dn string, scope int64) string {
	lower := strings.ToLower(dn)
	user := strings.HasSuffix(lower, ","+strings.ToLower(PeopleDN))
	group := strings.HasSuffix(lower, ","+strings.ToLower(GroupsDN))
	first, _, _ := strings.Cut(dn, ",")

	switch {
	case fault == FaultUserOutsideBase && user, fault == FaultGroupOutsideBase && group:
		return first + "," + OutsideDN
	case fault == FaultGroupOtherEntry && group && scope == ldap.ScopeBaseObject:
		return first + "-other," + GroupsDN
	default:
		return dn
	}
}

// isGroupBase reports whether the search base dn is GroupsDN or below it.
func isGroupBase(dn string) bool {
	lower := strings.ToLower(dn)
	groups := strings.ToLower(GroupsDN)

	return lower == groups || strings.HasSuffix(lower, ","+groups)
}
