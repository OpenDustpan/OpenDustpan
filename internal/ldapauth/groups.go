package ldapauth

import (
	"context"
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
func (m *Method) groupsOf(ctx context.Context, conn *ldap.Conn, e *ldap.Entry, userDN string) ([]string, error) {
	if m.groups.Source == GroupsOff {
		return nil, nil
	}

	// One deadline covers the whole read of the groups, the rebind as the
	// service account included. At the deadline the connection closes, so
	// an operation that waits ends too.
	walkCtx, cancel := context.WithTimeout(ctx, m.groupTimeout)
	defer cancel()

	stop := context.AfterFunc(walkCtx, func() { m.closeConn(ctx, conn) })
	defer stop()

	if err := conn.Bind(m.bindDN, m.bindPassword.Reveal()); err != nil {
		return nil, m.serviceBindError(ctx, err)
	}

	w := &groupWalk{seen: map[string]struct{}{}, names: map[string]struct{}{}, queue: nil}

	var err error

	switch m.groups.Source {
	case GroupsOff:
		return nil, nil
	case GroupsSearch:
		err = m.searchGroups(walkCtx, conn, w, userDN)
	case GroupsMemberOf:
		err = m.memberOfGroups(walkCtx, conn, w, e)
	default:
		// Fail closed: NewMethod refuses another source.
		err = fmt.Errorf("%w: the group source is not known", session.ErrMethodAnswer)
	}

	if err != nil {
		if walkCtx.Err() != nil && ctx.Err() == nil {
			return nil, fmt.Errorf("%w: the read of the groups took more than %s", session.ErrMethodUnavailable,
				m.groupTimeout)
		}

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
func (m *Method) searchGroups(ctx context.Context, conn *ldap.Conn, w *groupWalk, userDN string) error {
	w.queue = append(w.queue, userDN)

	for len(w.queue) > 0 {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%w: the read of the groups stopped", session.ErrMethodUnavailable)
		}

		member := w.queue[0]
		w.queue = w.queue[1:]

		filter, err := m.checkGroupFilter(groupFilter(m.groups, EscapeFilter(member)))
		if err != nil {
			return err
		}

		req := ldap.NewSearchRequest(m.groupBaseText, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, MaxGroups+1,
			int(m.operationTimeout/time.Second), false, filter, []string{m.groups.NameAttribute}, nil)

		res, err := conn.Search(req)
		if err != nil {
			return m.searchError(ctx, "the group search", err)
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
func (m *Method) memberOfGroups(ctx context.Context, conn *ldap.Conn, w *groupWalk, e *ldap.Entry) error {
	direct, err := m.directGroups(e)
	if err != nil {
		return err
	}

	w.queue = append(w.queue, direct...)

	for len(w.queue) > 0 {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%w: the read of the groups stopped", session.ErrMethodUnavailable)
		}

		if len(w.queue) > 2*MaxGroups {
			return fmt.Errorf("%w: the person has more than %d groups", session.ErrMethodAnswer, MaxGroups)
		}

		raw := w.queue[0]
		w.queue = w.queue[1:]

		dn, read, err := m.toRead(w, raw)
		if err != nil {
			return err
		}

		if !read {
			continue
		}

		if err := m.readGroup(ctx, conn, w, dn); err != nil {
			return err
		}
	}

	return nil
}

// readGroup reads the one group entry of the canonical DN dn and adds it
// to w.
func (m *Method) readGroup(ctx context.Context, conn *ldap.Conn, w *groupWalk, dn string) error {
	req := ldap.NewSearchRequest(dn, ldap.ScopeBaseObject, ldap.NeverDerefAliases, 1,
		int(m.operationTimeout/time.Second), false, "(objectClass=*)",
		[]string{m.groups.NameAttribute, m.groups.MemberOfAttribute}, nil)

	res, err := conn.Search(req)
	if err != nil {
		return m.searchError(ctx, "the read of a group", err)
	}

	if len(res.Entries) != 1 {
		return fmt.Errorf("%w: the read of a group found %d entries, not 1", session.ErrMethodAnswer,
			len(res.Entries))
	}

	return m.addGroup(w, res.Entries[0], &dn)
}

// toRead returns the canonical DN of the memberOf value raw, and whether
// the method reads it: a group outside the group base DN is not read and
// not returned, and a group that w holds is read once.
func (m *Method) toRead(w *groupWalk, raw string) (string, bool, error) {
	dn, parsed, err := canonicalDN(raw)
	if err != nil {
		return "", false, fmt.Errorf("%w: a memberOf value is not a valid DN", session.ErrMethodAnswer)
	}

	if !m.groupBaseDN.AncestorOfFold(parsed) {
		return dn, false, nil
	}

	_, seen := w.seen[strings.ToLower(dn)]

	return dn, !seen, nil
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

// checkGroupFilter returns f, or ErrMethodAnswer when the count of the
// items of f is not the count of the group filter with a plain value, so
// that a DN of the directory that adds an item never reaches it.
func (m *Method) checkGroupFilter(f string) (string, error) {
	// Defense in depth: EscapeFilter escapes each special byte of the DN.
	// This check stays so that a change of the escape cannot add an item
	// to the filter.
	if !sameShape(f, m.groupFilterNodes) {
		return "", fmt.Errorf("%w: a DN of the directory changes the structure of the group filter",
			session.ErrMethodAnswer)
	}

	return f, nil
}

// directGroups returns the memberOf values of the person. It refuses more
// direct groups below the group base DN than the limit before any read.
func (m *Method) directGroups(e *ldap.Entry) ([]string, error) {
	direct := e.GetEqualFoldAttributeValues(m.groups.MemberOfAttribute)
	if m.inGroupBase(direct) > MaxGroups {
		return nil, fmt.Errorf("%w: the person has more than %d groups", session.ErrMethodAnswer, MaxGroups)
	}

	return direct, nil
}

// inGroupBase returns the count of the DNs of values below the group base
// DN. A value that does not parse counts too: the walk refuses it later.
func (m *Method) inGroupBase(values []string) int {
	n := 0

	for _, v := range values {
		parsed, err := ldap.ParseDN(v)
		if err != nil || m.groupBaseDN.AncestorOfFold(parsed) {
			n++
		}
	}

	return n
}
