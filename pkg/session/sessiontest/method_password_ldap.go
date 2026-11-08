package sessiontest

import (
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/session"
)

// DirectorySubject names the subject attribute of a test LDAP method.
type DirectorySubject int

// Subject attributes of a test LDAP method.
const (
	// SubjectEntryUUID reads entryUUID.
	SubjectEntryUUID DirectorySubject = iota
	// SubjectObjectGUID reads objectGUID.
	SubjectObjectGUID
)

// DirectoryGroups names the group source of a test LDAP method.
type DirectoryGroups int

// Group sources of a test LDAP method.
const (
	// DirectoryGroupsOff reads no groups.
	DirectoryGroupsOff DirectoryGroups = iota
	// DirectoryGroupsSearch searches for the groups whose member attribute
	// holds the person.
	DirectoryGroupsSearch
	// DirectoryGroupsMemberOf reads the memberOf attribute.
	DirectoryGroupsMemberOf
)

// DirectoryTransport names the connection of a test LDAP method.
type DirectoryTransport int

// Connections of a test LDAP method.
const (
	// TransportTLS is an ldaps:// connection.
	TransportTLS DirectoryTransport = iota
	// TransportPlainLoopback is a plain ldap:// connection to a loopback
	// IP address.
	TransportPlainLoopback
	// TransportPlainName is a plain ldap:// connection to a host name.
	TransportPlainName
)

// DirectoryFault is a wrong answer of the test directory.
type DirectoryFault int

// Faults of the test directory.
const (
	// FaultNone answers as a directory does.
	FaultNone DirectoryFault = iota
	// FaultSlow answers no search in time.
	FaultSlow
	// FaultSizeLimit answers each search with the result sizeLimitExceeded.
	FaultSizeLimit
	// FaultSearchReference answers a search with only a continuation
	// reference.
	FaultSearchReference
	// FaultSearchReferral answers a search with the result referral.
	FaultSearchReferral
	// FaultBindReferral answers the bind of a user with the result
	// referral.
	FaultBindReferral
)

// DirectoryLink says where the test directory shows the members of a group.
type DirectoryLink int

// Links of a group.
const (
	// LinkBoth shows a member in the member attribute of the group and the
	// group in the memberOf attribute of the member.
	LinkBoth DirectoryLink = iota
	// LinkMember shows a member only in the member attribute.
	LinkMember
	// LinkMemberOf shows the group only in the memberOf attribute.
	LinkMemberOf
)

// DirectorySettings are the settings of a test LDAP method.
type DirectorySettings struct {
	// OperationTimeout is the time for one operation. Zero is the default
	// of the method.
	OperationTimeout time.Duration
	// Subject is the subject attribute.
	Subject DirectorySubject
	// Groups is the group source.
	Groups DirectoryGroups
	// Transport is the connection.
	Transport DirectoryTransport
	// AllowPlain is the value of the plain-connection setting that the
	// AllowPlain hook of the method returns.
	AllowPlain bool
	// EgressRefused makes the egress rules refuse the address of the
	// directory.
	EgressRefused bool
}

// DirectoryGroup is a group of the test directory.
type DirectoryGroup struct {
	// Name is the name of the group.
	Name string
	// Users are the user names of its user members.
	Users []string
	// Groups are the names of its group members.
	Groups []string
	// Link says where the directory shows its members.
	Link DirectoryLink
}

// Directory is the test directory of an LDAP method. RunPasswordMethod
// needs it for a method of the kind session.MethodLDAP: the cases of the
// LDAP protocol use it.
type Directory struct {
	// Configure returns a fixture over the same test directory whose
	// method has the settings s.
	Configure func(t *testing.T, s DirectorySettings) PasswordFixture
	// AddUserWithSubject adds the person p with the password and the raw
	// value of each subject attribute.
	AddUserWithSubject func(t *testing.T, p TestPerson, password string, raw []byte)
	// AddGroup adds a group.
	AddGroup func(t *testing.T, g DirectoryGroup)
	// SetFault makes the directory answer with the fault.
	SetFault func(t *testing.T, f DirectoryFault)
	// UserDN returns the DN of the entry of the user name.
	UserDN func(userName string) string
	// Binds returns the DN of each bind that the directory received.
	Binds func() []string
	// Connections returns the count of the connections that the directory
	// accepted.
	Connections func() int
}

// ldapCase is one case of the LDAP protocol.
type ldapCase struct {
	run  func(t *testing.T, f PasswordFixture, d Directory)
	name string
}

// runLDAPCases runs the cases of the LDAP protocol. Each case runs on a
// fixture of its own.
func runLDAPCases(t *testing.T, factory func(t *testing.T) PasswordFixture) {
	t.Helper()

	for _, c := range ldapCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			f := factory(t)
			if f.Directory == nil {
				t.Fatal("the fixture of an LDAP method has no Directory")
			}

			c.run(t, f, *f.Directory)
		})
	}
}

// ldapCases are the cases of the LDAP protocol.
func ldapCases() []ldapCase {
	return []ldapCase{
		{name: "AnObjectGUIDSubjectHasTheCanonicalTextForm", run: ldAnObjectGUIDSubjectHasTheCanonicalTextForm},
		{name: "AnEntryUUIDSubjectIsLowercase", run: ldAnEntryUUIDSubjectIsLowercase},
		{name: "GroupsComeFromTheConfiguredSource", run: ldGroupsComeFromTheConfiguredSource},
		{name: "NestedGroupsAreFlat", run: ldNestedGroupsAreFlat},
		{name: "FilterCharactersInTheUserNameAreEscaped", run: ldFilterCharactersInTheUserNameAreEscaped},
		{name: "DNCharactersInTheUserNameAreEscaped", run: ldDNCharactersInTheUserNameAreEscaped},
		{name: "AReferralIsNeverFollowed", run: ldAReferralIsNeverFollowed},
		{name: "TheConnectionGoesThroughTheEgressRules", run: ldTheConnectionGoesThroughTheEgressRules},
		{name: "ASearchOverTheSizeLimitIsErrMethodAnswer", run: ldASearchOverTheSizeLimitIsErrMethodAnswer},
		{name: "AGroupSearchOverTheLimitIsErrMethodAnswer", run: ldAGroupSearchOverTheLimitIsErrMethodAnswer},
		{
			name: "ADirectoryThatDoesNotAnswerInTimeIsErrMethodUnavailable",
			run:  ldADirectoryThatDoesNotAnswerInTimeIsErrMethodUnavailable,
		},
		{
			name: "APlainConnectionIsRefusedUnlessTheSettingAllowsIt",
			run:  ldAPlainConnectionIsRefusedUnlessTheSettingAllowsIt,
		},
		{name: "TwoUsersWithOneUserNameAreErrMethodAnswer", run: ldTwoUsersWithOneUserNameAreErrMethodAnswer},
		{name: "GroupsOnAndNoGroupsIsErrGroupsMissing", run: ldGroupsOnAndNoGroupsIsErrGroupsMissing},
	}
}

// settings returns the default settings of a test LDAP method.
func settings() DirectorySettings {
	return DirectorySettings{
		OperationTimeout: 0, Subject: SubjectEntryUUID, Groups: DirectoryGroupsOff, Transport: TransportTLS,
		AllowPlain: false, EgressRefused: false,
	}
}

// ldAnObjectGUIDSubjectHasTheCanonicalTextForm is the case AnObjectGUIDSubjectHasTheCanonicalTextForm.
func ldAnObjectGUIDSubjectHasTheCanonicalTextForm(t *testing.T, _ PasswordFixture, d Directory) {
	t.Helper()

	s := settings()
	s.Subject = SubjectObjectGUID
	f := d.Configure(t, s)

	raw := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}
	d.AddUserWithSubject(t, person("alice"), suitePassword, raw)

	const want = "04030201-0605-0807-090a-0b0c0d0e0f10"

	if a := mustVerify(t, f, newGate(), "alice", suitePassword); a.Subject != want {
		t.Fatalf("subject = %q, want %q", a.Subject, want)
	}
}

// ldAnEntryUUIDSubjectIsLowercase is the case AnEntryUUIDSubjectIsLowercase.
func ldAnEntryUUIDSubjectIsLowercase(t *testing.T, _ PasswordFixture, d Directory) {
	t.Helper()

	f := d.Configure(t, settings())
	d.AddUserWithSubject(t, person("alice"), suitePassword, []byte("6BA7B810-9DAD-11D1-80B4-00C04FD430C8"))

	const want = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"

	if a := mustVerify(t, f, newGate(), "alice", suitePassword); a.Subject != want {
		t.Fatalf("subject = %q, want %q", a.Subject, want)
	}
}

// ldGroupsComeFromTheConfiguredSource is the case GroupsComeFromTheConfiguredSource.
// Each group shows its member in one source only, so each source finds its
// own group.
func ldGroupsComeFromTheConfiguredSource(t *testing.T, _ PasswordFixture, d Directory) {
	t.Helper()

	d.AddUserWithSubject(t, person("alice"), suitePassword, nil)
	d.AddGroup(t, DirectoryGroup{Name: "by-member", Users: []string{"alice"}, Groups: nil, Link: LinkMember})
	d.AddGroup(t, DirectoryGroup{Name: "by-memberof", Users: []string{"alice"}, Groups: nil, Link: LinkMemberOf})
	d.AddGroup(t, DirectoryGroup{Name: "by-both", Users: []string{"alice"}, Groups: nil, Link: LinkBoth})

	for source, want := range map[DirectoryGroups][]string{
		DirectoryGroupsSearch:   {"by-both", "by-member"},
		DirectoryGroupsMemberOf: {"by-both", "by-memberof"},
	} {
		s := settings()
		s.Groups = source

		a := mustVerify(t, d.Configure(t, s), newGate(), "alice", suitePassword)
		if !a.GroupsPresent || !slices.Equal(sortedCopy(a.Groups), want) {
			t.Fatalf("source %d: groups = %v (present %v), want %v", source, a.Groups, a.GroupsPresent, want)
		}
	}
}

// ldNestedGroupsAreFlat is the case NestedGroupsAreFlat. The groups have a
// cycle, which the method reads once.
func ldNestedGroupsAreFlat(t *testing.T, _ PasswordFixture, d Directory) {
	t.Helper()

	d.AddUserWithSubject(t, person("alice"), suitePassword, nil)
	d.AddGroup(t, DirectoryGroup{Name: "team", Users: []string{"alice"}, Groups: []string{"org"}, Link: LinkBoth})
	d.AddGroup(t, DirectoryGroup{Name: "dept", Users: nil, Groups: []string{"team"}, Link: LinkBoth})
	d.AddGroup(t, DirectoryGroup{Name: "org", Users: nil, Groups: []string{"dept"}, Link: LinkBoth})

	for _, source := range []DirectoryGroups{DirectoryGroupsSearch, DirectoryGroupsMemberOf} {
		s := settings()
		s.Groups = source

		a := mustVerify(t, d.Configure(t, s), newGate(), "alice", suitePassword)
		if want := []string{"dept", "org", "team"}; !slices.Equal(sortedCopy(a.Groups), want) {
			t.Fatalf("source %d: groups = %v, want %v", source, a.Groups, want)
		}
	}
}

// ldFilterCharactersInTheUserNameAreEscaped is the case FilterCharactersInTheUserNameAreEscaped.
func ldFilterCharactersInTheUserNameAreEscaped(t *testing.T, f PasswordFixture, _ Directory) {
	t.Helper()

	f.AddUser(t, person("alice"), suitePassword)
	odd := f.AddUser(t, person(`o(b)*\x`), otherSuitePassword)

	for _, name := range []string{"*", "*)(uid=*", "alice)(uid=*", "*)(|(uid=*", `\2a`, "a*", "alice)(&"} {
		gate := newGate()

		a, err := verify(t, f, gate, name, suitePassword)
		if !errors.Is(err, session.ErrMethodRejected) && !errors.Is(err, session.ErrMethodInput) {
			t.Fatalf("Verify(%q) = %v, want ErrMethodRejected or ErrMethodInput", name, err)
		}

		if !zeroAssertion(a) || len(gate.seen()) != 0 {
			t.Fatalf("Verify(%q) = %+v, gate %q, want the zero Assertion and no BeforeBind", name, a, gate.seen())
		}
	}

	if a := mustVerify(t, f, newGate(), `o(b)*\x`, otherSuitePassword); a.Subject != odd {
		t.Fatalf("subject = %q, want %q", a.Subject, odd)
	}
}

// ldDNCharactersInTheUserNameAreEscaped is the case DNCharactersInTheUserNameAreEscaped.
func ldDNCharactersInTheUserNameAreEscaped(t *testing.T, f PasswordFixture, d Directory) {
	t.Helper()

	f.AddUser(t, person("alice"), suitePassword)

	const odd = `alice,ou=admins+cn=root;"#<x>`

	subject := f.AddUser(t, person(odd), otherSuitePassword)

	a := mustVerify(t, f, newGate(), odd, otherSuitePassword)
	if a.Subject != subject {
		t.Fatalf("subject = %q, want %q", a.Subject, subject)
	}

	binds := d.Binds()
	if !slices.ContainsFunc(binds, func(b string) bool { return sameDNText(b, d.UserDN(odd)) }) {
		t.Fatalf("binds = %q, want a bind as %q", binds, d.UserDN(odd))
	}

	for _, name := range []string{"alice,ou=people", d.UserDN("alice"), "cn=service"} {
		a, err := verify(t, f, newGate(), name, suitePassword)
		checkRefused(t, a, err, session.ErrMethodRejected)
	}
}

// sameDNText reports whether two DNs from the directory are the same text,
// with no case.
func sameDNText(a, b string) bool {
	return len(a) == len(b) && (a == b || equalFoldASCII(a, b))
}

// equalFoldASCII compares a and b with no case for ASCII letters.
func equalFoldASCII(a, b string) bool {
	for i := range len(a) {
		x, y := a[i], b[i]
		if x >= 'A' && x <= 'Z' {
			x += 'a' - 'A'
		}

		if y >= 'A' && y <= 'Z' {
			y += 'a' - 'A'
		}

		if x != y {
			return false
		}
	}

	return true
}

// ldAReferralIsNeverFollowed is the case AReferralIsNeverFollowed. Each
// referral points to the directory itself: a method that follows it opens a
// second connection.
func ldAReferralIsNeverFollowed(t *testing.T, f PasswordFixture, d Directory) {
	t.Helper()

	f.AddUser(t, person("alice"), suitePassword)

	for _, fault := range []DirectoryFault{FaultSearchReference, FaultSearchReferral, FaultBindReferral} {
		d.SetFault(t, fault)

		before := d.Connections()
		gate := newGate()

		a, err := verify(t, f, gate, "alice", suitePassword)
		checkRefused(t, a, err, session.ErrMethodAnswer)

		if got := d.Connections() - before; got != 1 {
			t.Fatalf("fault %d: %d connections, want 1", fault, got)
		}
	}
}

// ldTheConnectionGoesThroughTheEgressRules is the case TheConnectionGoesThroughTheEgressRules.
func ldTheConnectionGoesThroughTheEgressRules(t *testing.T, _ PasswordFixture, d Directory) {
	t.Helper()

	s := settings()
	s.EgressRefused = true
	f := d.Configure(t, s)
	f.AddUser(t, person("alice"), suitePassword)

	a, err := verify(t, f, newGate(), "alice", suitePassword)
	checkRefused(t, a, err, session.ErrMethodUnavailable)

	if got := d.Connections(); got != 0 {
		t.Fatalf("connections = %d, want 0", got)
	}
}

// ldASearchOverTheSizeLimitIsErrMethodAnswer is the case ASearchOverTheSizeLimitIsErrMethodAnswer.
func ldASearchOverTheSizeLimitIsErrMethodAnswer(t *testing.T, f PasswordFixture, d Directory) {
	t.Helper()

	f.AddUser(t, person("alice"), suitePassword)
	d.SetFault(t, FaultSizeLimit)

	gate := newGate()

	a, err := verify(t, f, gate, "alice", suitePassword)
	checkRefused(t, a, err, session.ErrMethodAnswer)

	if len(gate.seen()) != 0 {
		t.Fatalf("BeforeBind received %q, want no call", gate.seen())
	}
}

// ldAGroupSearchOverTheLimitIsErrMethodAnswer is the case AGroupSearchOverTheLimitIsErrMethodAnswer.
func ldAGroupSearchOverTheLimitIsErrMethodAnswer(t *testing.T, _ PasswordFixture, d Directory) {
	t.Helper()

	d.AddUserWithSubject(t, person("alice"), suitePassword, nil)

	for _, g := range manyGroups(session.MaxAssertionGroups + 1) {
		d.AddGroup(t, DirectoryGroup{Name: g, Users: []string{"alice"}, Groups: nil, Link: LinkBoth})
	}

	for _, source := range []DirectoryGroups{DirectoryGroupsSearch, DirectoryGroupsMemberOf} {
		s := settings()
		s.Groups = source

		a, err := verify(t, d.Configure(t, s), newGate(), "alice", suitePassword)
		checkRefused(t, a, err, session.ErrMethodAnswer)
	}
}

// ldADirectoryThatDoesNotAnswerInTimeIsErrMethodUnavailable is the case
// ADirectoryThatDoesNotAnswerInTimeIsErrMethodUnavailable.
func ldADirectoryThatDoesNotAnswerInTimeIsErrMethodUnavailable(t *testing.T, _ PasswordFixture, d Directory) {
	t.Helper()

	const timeout = 300 * time.Millisecond

	s := settings()
	s.OperationTimeout = timeout
	f := d.Configure(t, s)
	f.AddUser(t, person("alice"), suitePassword)
	d.SetFault(t, FaultSlow)

	start := time.Now()

	a, err := verify(t, f, newGate(), "alice", suitePassword)
	checkRefused(t, a, err, session.ErrMethodUnavailable)

	if took := time.Since(start); took > 20*timeout {
		t.Fatalf("Verify took %v, want about %v", took, timeout)
	}
}

// ldAPlainConnectionIsRefusedUnlessTheSettingAllowsIt is the case
// APlainConnectionIsRefusedUnlessTheSettingAllowsIt. A plain connection to
// a host name needs the setting; a plain connection to a loopback IP
// address does not.
func ldAPlainConnectionIsRefusedUnlessTheSettingAllowsIt(t *testing.T, f PasswordFixture, d Directory) {
	t.Helper()

	f.AddUser(t, person("alice"), suitePassword)

	s := settings()
	s.Transport = TransportPlainName

	a, err := verify(t, d.Configure(t, s), newGate(), "alice", suitePassword)
	checkRefused(t, a, err, session.ErrMethodUnavailable)

	if got := d.Connections(); got != 0 {
		t.Fatalf("connections = %d after a refused plain connection, want 0", got)
	}

	s.AllowPlain = true
	mustVerify(t, d.Configure(t, s), newGate(), "alice", suitePassword)

	s.Transport, s.AllowPlain = TransportPlainLoopback, false
	mustVerify(t, d.Configure(t, s), newGate(), "alice", suitePassword)
}

// ldTwoUsersWithOneUserNameAreErrMethodAnswer is the case TwoUsersWithOneUserNameAreErrMethodAnswer.
func ldTwoUsersWithOneUserNameAreErrMethodAnswer(t *testing.T, f PasswordFixture, _ Directory) {
	t.Helper()

	f.AddUser(t, person("alice"), suitePassword)
	f.AddUser(t, person("alice"), otherSuitePassword)

	for _, password := range []string{suitePassword, otherSuitePassword} {
		gate := newGate()

		a, err := verify(t, f, gate, "alice", password)
		checkRefused(t, a, err, session.ErrMethodAnswer)

		if len(gate.seen()) != 0 {
			t.Fatalf("BeforeBind received %q, want no call", gate.seen())
		}
	}
}

// ldGroupsOnAndNoGroupsIsErrGroupsMissing is the case GroupsOnAndNoGroupsIsErrGroupsMissing.
func ldGroupsOnAndNoGroupsIsErrGroupsMissing(t *testing.T, _ PasswordFixture, d Directory) {
	t.Helper()

	d.AddUserWithSubject(t, person("alice"), suitePassword, nil)

	for _, source := range []DirectoryGroups{DirectoryGroupsSearch, DirectoryGroupsMemberOf} {
		s := settings()
		s.Groups = source

		a, err := verify(t, d.Configure(t, s), newGate(), "alice", suitePassword)
		checkRefused(t, a, err, session.ErrGroupsMissing)
	}
}
