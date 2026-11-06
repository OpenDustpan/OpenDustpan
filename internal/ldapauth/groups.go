package ldapauth

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"

	"opendustpan.org/opendustpan/pkg/session"
)

// groupWalk is the state of one read of the groups of a person: the DNs of
// the groups that it found, their names, and the DNs that wait for a read.
type groupWalk struct {
	seen  map[string]struct{}
	names map[string]struct{}
	queue []string
}

// add records the group of the canonical DN dn with the name. It returns
// false for a group that it found before. It refuses more than MaxGroups
// groups.
func (w *groupWalk) add(dn, name string) (bool, error) {
	key := strings.ToLower(dn)
	if _, ok := w.seen[key]; ok {
		return false, nil
	}

	if len(w.seen) >= MaxGroups {
		return false, fmt.Errorf("%w: the person has more than %d groups", session.ErrMethodAnswer, MaxGroups)
	}

	w.seen[key] = struct{}{}
	w.names[name] = struct{}{}

	return true, nil
}

// result returns the names of the groups.
func (w *groupWalk) result() []string {
	out := make([]string, 0, len(w.names))
	for n := range w.names {
		out = append(out, n)
	}

	return out
}

// groupsOf returns the groups of the person of the entry e and the DN
// userDN, nested groups included, as a flat list. It binds again as the
// service account first. It returns nil when the groups are off, and
// session.ErrGroupsMissing when they are on and the person has none.
func (m *Method) groupsOf(conn *ldap.Conn, e *ldap.Entry, userDN string) ([]string, error) {
	if m.groups.Source == GroupsOff {
		return nil, nil
	}

	if err := conn.Bind(m.bindDN, m.bindPassword.Reveal()); err != nil {
		if network(err) {
			return nil, fmt.Errorf("%w: bind again as the service account: %w", session.ErrMethodUnavailable, err)
		}

		return nil, fmt.Errorf("%w: the directory refused the service account", session.ErrMethodUnavailable)
	}

	w := &groupWalk{seen: map[string]struct{}{}, names: map[string]struct{}{}, queue: nil}

	var err error

	switch m.groups.Source {
	case GroupsOff:
		return nil, nil
	case GroupsSearch:
		err = m.searchGroups(conn, w, userDN)
	case GroupsMemberOf:
		err = m.memberOfGroups(conn, w, e)
	default:
		// Fail closed: NewMethod refuses another source.
		err = fmt.Errorf("%w: the group source is not known", session.ErrMethodAnswer)
	}

	if err != nil {
		return nil, err
	}

	groups := w.result()
	if len(groups) == 0 {
		return nil, fmt.Errorf("%w: the directory gives no group for the person", session.ErrGroupsMissing)
	}

	return groups, nil
}

// searchGroups finds the groups whose member attribute holds the DN of the
// person, then the groups whose member attribute holds the DN of each group
// that it found.
func (m *Method) searchGroups(conn *ldap.Conn, w *groupWalk, userDN string) error {
	w.queue = append(w.queue, userDN)

	for len(w.queue) > 0 {
		member := w.queue[0]
		w.queue = w.queue[1:]

		filter := "(&" + m.groups.Filter + "(" + m.groups.MemberAttribute + "=" + EscapeFilter(member) + "))"
		req := ldap.NewSearchRequest(m.groupBaseText, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, MaxGroups+1,
			int(m.operationTimeout/time.Second), false, filter, []string{m.groups.NameAttribute}, nil)

		res, err := conn.Search(req)
		if err != nil {
			return searchError("the group search", err)
		}

		if len(res.Entries) > MaxGroups {
			return fmt.Errorf("%w: the group search found more than %d groups", session.ErrMethodAnswer, MaxGroups)
		}

		for _, g := range res.Entries {
			if err := m.addGroup(w, g, nil); err != nil {
				return err
			}
		}
	}

	return nil
}

// memberOfGroups reads the memberOf attribute of the person, then the
// memberOf attribute of each group that it found.
func (m *Method) memberOfGroups(conn *ldap.Conn, w *groupWalk, e *ldap.Entry) error {
	w.queue = append(w.queue, e.GetEqualFoldAttributeValues(m.groups.MemberOfAttribute)...)

	for len(w.queue) > 0 {
		if len(w.queue) > 2*MaxGroups {
			return fmt.Errorf("%w: the person has more than %d groups", session.ErrMethodAnswer, MaxGroups)
		}

		raw := w.queue[0]
		w.queue = w.queue[1:]

		dn, parsed, err := canonicalDN(raw)
		if err != nil {
			return fmt.Errorf("%w: a memberOf value is not a valid DN", session.ErrMethodAnswer)
		}

		// A group outside the group base DN is not read and not returned.
		if !m.groupBaseDN.AncestorOfFold(parsed) {
			continue
		}

		if _, ok := w.seen[strings.ToLower(dn)]; ok {
			continue
		}

		req := ldap.NewSearchRequest(dn, ldap.ScopeBaseObject, ldap.NeverDerefAliases, 1,
			int(m.operationTimeout/time.Second), false, "(objectClass=*)",
			[]string{m.groups.NameAttribute, m.groups.MemberOfAttribute}, nil)

		res, err := conn.Search(req)
		if err != nil {
			return searchError("the read of a group", err)
		}

		if len(res.Entries) != 1 {
			return fmt.Errorf("%w: the read of a group found %d entries, not 1", session.ErrMethodAnswer,
				len(res.Entries))
		}

		if err := m.addGroup(w, res.Entries[0], &dn); err != nil {
			return err
		}
	}

	return nil
}

// addGroup adds the group entry g to w. With want set, the DN of g must be
// that DN. A new group goes to the queue: its own DN for a group search, its
// memberOf values otherwise.
func (m *Method) addGroup(w *groupWalk, g *ldap.Entry, want *string) error {
	dn, parsed, err := canonicalDN(g.DN)
	if err != nil {
		return fmt.Errorf("%w: the DN of a group is not valid", session.ErrMethodAnswer)
	}

	if want != nil && !strings.EqualFold(dn, *want) {
		return fmt.Errorf("%w: the read of a group answered another entry", session.ErrMethodAnswer)
	}

	// Defense in depth: the group search starts at the group base DN. This
	// check stays so that an entry from another part of the tree is never a
	// group of the person.
	if !m.groupBaseDN.AncestorOfFold(parsed) {
		return fmt.Errorf("%w: a group is not below the group base DN", session.ErrMethodAnswer)
	}

	names := g.GetEqualFoldAttributeValues(m.groups.NameAttribute)
	if len(names) != 1 || names[0] == "" || len(names[0]) > session.MaxAssertionGroup || !validText(names[0]) {
		return fmt.Errorf("%w: a group must have one name of 1 to %d bytes of text", session.ErrMethodAnswer,
			session.MaxAssertionGroup)
	}

	added, err := w.add(dn, names[0])
	if err != nil || !added {
		return err
	}

	if m.groups.Source == GroupsSearch {
		w.queue = append(w.queue, dn)
	} else {
		w.queue = append(w.queue, g.GetEqualFoldAttributeValues(m.groups.MemberOfAttribute)...)
	}

	return nil
}
