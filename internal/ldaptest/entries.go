package ldaptest

import (
	"slices"
	"strings"

	ber "github.com/go-asn1-ber/asn1-ber"
	"github.com/go-ldap/ldap/v3"
)

// entry is one entry of the directory, built at each search from the users
// and the groups.
type entry struct {
	dn    *ldap.DN
	attrs map[string][][]byte
	names map[string]string
	text  string
}

// newEntry returns the entry of dn with no attribute.
func newEntry(dn string) entry {
	parsed, err := ldap.ParseDN(dn)
	if err != nil {
		// Each DN of the directory comes from UserDN, GroupDN or a constant.
		parsed = &ldap.DN{RDNs: nil}
	}

	return entry{dn: parsed, attrs: map[string][][]byte{}, names: map[string]string{}, text: dn}
}

// add adds the values to the attribute name.
func (e entry) add(name string, values ...string) {
	key := strings.ToLower(name)
	if _, ok := e.names[key]; !ok {
		e.names[key] = name
	}

	for _, v := range values {
		e.attrs[key] = append(e.attrs[key], []byte(v))
	}
}

// values returns the values of the attribute name.
func (e entry) values(name string) [][]byte {
	return e.attrs[strings.ToLower(name)]
}

// packet returns the SearchResultEntry of e with the attributes attrs, or
// each attribute when attrs is empty.
func (e entry) packet(attrs []string) *ber.Packet {
	p := ber.Encode(ber.ClassApplication, ber.TypeConstructed, ber.Tag(appSearchEntry), nil, "entry")
	p.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, e.text, "DN"))

	list := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "attributes")

	keys := make([]string, 0, len(e.attrs))
	for k := range e.attrs {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	for _, k := range keys {
		if len(attrs) > 0 && !slices.ContainsFunc(attrs, func(a string) bool { return strings.EqualFold(a, k) }) {
			continue
		}

		a := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "attribute")
		a.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, e.names[k], "type"))

		set := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSet, nil, "values")
		for _, v := range e.attrs[k] {
			set.AppendChild(
				ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, string(v), "value"),
			)
		}

		a.AppendChild(set)
		list.AppendChild(a)
	}

	p.AppendChild(list)

	return p
}

// entries returns each entry of the directory: the tree, the service
// account, the users and the groups, with the member and memberOf values
// of each link.
func (s *Server) entries() []entry {
	s.mu.Lock()
	defer s.mu.Unlock()

	base := newEntry(BaseDN)
	base.add("objectClass", "top", "domain")

	people := newEntry(PeopleDN)
	people.add("objectClass", "top", "organizationalUnit")

	groups := newEntry(GroupsDN)
	groups.add("objectClass", "top", "organizationalUnit")

	service := newEntry(ServiceDN)
	service.add("objectClass", "top", "person")

	out := make([]entry, 0, len(s.users)+len(s.groups)+treeEntries)
	out = append(out, base, people, groups, service)
	memberOf := s.memberOf()

	for _, u := range s.users {
		e := newEntry(UserDN(u.UserName))
		e.add("objectClass", "top", "person", "inetOrgPerson")
		e.add("uid", u.UserName)

		for name, values := range u.Attributes {
			e.add(name, values...)
		}

		e.add("memberOf", memberOf[strings.ToLower(UserDN(u.UserName))]...)
		out = append(out, e)
	}

	for _, g := range s.groups {
		e := newEntry(GroupDN(g.Name))
		e.add("objectClass", "top", "groupOfNames")
		e.add("cn", g.Name)

		if g.Link != LinkMemberOf {
			for _, u := range g.Users {
				e.add("member", UserDN(u))
			}

			for _, m := range g.Groups {
				e.add("member", GroupDN(m))
			}
		}

		e.add("memberOf", memberOf[strings.ToLower(GroupDN(g.Name))]...)
		out = append(out, e)
	}

	return out
}

// memberOf returns the memberOf values of each member DN, in lowercase,
// from the groups whose link shows them.
func (s *Server) memberOf() map[string][]string {
	out := map[string][]string{}

	for _, g := range s.groups {
		if g.Link == LinkMember {
			continue
		}

		for _, u := range g.Users {
			key := strings.ToLower(UserDN(u))
			out[key] = append(out[key], GroupDN(g.Name))
		}

		for _, m := range g.Groups {
			key := strings.ToLower(GroupDN(m))
			out[key] = append(out[key], GroupDN(g.Name))
		}
	}

	return out
}

// treeEntries is the count of the entries of the tree and the service
// account.
const treeEntries = 4
