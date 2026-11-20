package firstadmin_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"opendustpan.org/opendustpan/pkg/firstadmin"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
)

// zeroReader gives the same byte for each read, so a code is the same each time.
type zeroReader struct{ b byte }

func (z zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = z.b
	}

	return len(p), nil
}

// TestNewCodeShape checks the code: 32 bytes of the source, 43 characters of
// base64url with no padding, and no code from a source that fails or that is
// too short.
func TestNewCodeShape(t *testing.T) {
	t.Parallel()

	t.Run("form", testCodeForm)
	t.Run("two sources differ", testCodesDiffer)
	t.Run("text never prints the code", testCodeHidden)

	failing := []struct {
		name string
		src  io.Reader
	}{
		{name: "reader that fails", src: iotest.ErrReader(errNoEntropy)},
		{name: "reader with too few bytes", src: bytes.NewReader(make([]byte, firstadmin.CodeBytes-1))},
		{name: "empty reader", src: bytes.NewReader(nil)},
		{name: "nil reader", src: nil},
	}

	for _, tc := range failing {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			code, err := firstadmin.NewCode(tc.src)
			if err == nil {
				t.Fatal("NewCode error = nil, want an error")
			}

			if code != "" {
				t.Errorf("code = %q, want no code with an error", string(code))
			}
		})
	}
}

func testCodeForm(t *testing.T) {
	t.Parallel()

	src := bytes.NewReader(bytes.Repeat([]byte{0xfb, 0xff, 0x01, 0x02}, 8))

	code, err := firstadmin.NewCode(src)
	if err != nil {
		t.Fatalf("NewCode: %v", err)
	}

	if len(code) != firstadmin.CodeLength {
		t.Errorf("len(code) = %d, want %d", len(code), firstadmin.CodeLength)
	}

	if strings.ContainsAny(string(code), "+/=") {
		t.Error("code has a character outside base64url or padding")
	}

	raw, err := base64.RawURLEncoding.DecodeString(string(code))
	if err != nil || len(raw) != firstadmin.CodeBytes {
		t.Errorf("decoded %d bytes, err %v, want %d bytes", len(raw), err, firstadmin.CodeBytes)
	}

	if !code.ValidForm() {
		t.Error("ValidForm() = false, want true for a made code")
	}
}

func testCodesDiffer(t *testing.T) {
	t.Parallel()

	a, errA := firstadmin.NewCode(zeroReader{b: 1})
	b, errB := firstadmin.NewCode(zeroReader{b: 2})

	if errA != nil || errB != nil {
		t.Fatalf("NewCode: %v, %v", errA, errB)
	}

	if a == b || a.Digest() == b.Digest() {
		t.Error("two different sources made the same code or digest")
	}
}

func testCodeHidden(t *testing.T) {
	t.Parallel()

	code, err := firstadmin.NewCode(zeroReader{b: 7})
	if err != nil {
		t.Fatalf("NewCode: %v", err)
	}

	for _, got := range []string{code.String(), fmt.Sprintf("[%v]", code), fmt.Sprintf("%#v", code)} {
		if strings.Contains(got, string(code)) {
			t.Errorf("formatted code %q holds the code", got)
		}
	}
}

// TestCodeValidForm checks the form check of a code from outside.
func TestCodeValidForm(t *testing.T) {
	t.Parallel()

	good, err := firstadmin.NewCode(zeroReader{b: 3})
	if err != nil {
		t.Fatalf("NewCode: %v", err)
	}

	cases := []struct {
		name string
		code firstadmin.Code
		want bool
	}{
		{name: "made code", code: good, want: true},
		{name: "empty", code: "", want: false},
		{name: "one short", code: good[:len(good)-1], want: false},
		{name: "one long", code: good + "A", want: false},
		{name: "standard alphabet", code: firstadmin.Code(strings.Repeat("+", firstadmin.CodeLength)), want: false},
		{name: "padding", code: firstadmin.Code(strings.Repeat("A", firstadmin.CodeLength-1) + "="), want: false},
		{name: "space", code: firstadmin.Code(strings.Repeat("A", firstadmin.CodeLength-1) + " "), want: false},
		// The last character of a 43-character code carries 4 bits that a
		// strict decoder demands to be 0: "B" sets a trailing bit.
		{
			name: "non-zero trailing bits",
			code: firstadmin.Code(strings.Repeat("A", firstadmin.CodeLength-1) + "B"),
			want: false,
		},
		{name: "new line", code: good[:len(good)-1] + "\n", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.code.ValidForm(); got != tc.want {
				t.Errorf("ValidForm() = %t, want %t", got, tc.want)
			}
		})
	}
}

// TestDigestConstantTime checks the digest: lowercase hex of SHA-256, the
// compare of two digests, and the form check of a stored digest. The compare
// has no early exit on the first byte: it gives the same answer for a digest
// that differs in the first or in the last character.
func TestDigestConstantTime(t *testing.T) {
	t.Parallel()

	// SHA-256 of the empty text, to pin the algorithm and the hex form.
	const emptyDigest = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	if got := firstadmin.Code("").Digest(); got != emptyDigest {
		t.Errorf("Digest of the empty text = %s, want %s", got, emptyDigest)
	}

	code, err := firstadmin.NewCode(zeroReader{b: 9})
	if err != nil {
		t.Fatalf("NewCode: %v", err)
	}

	digest := code.Digest()
	if !firstadmin.ValidDigest(digest) {
		t.Fatalf("ValidDigest(%q) = false, want true", digest)
	}

	if strings.Contains(digest, string(code)) {
		t.Error("digest holds the code")
	}

	other := firstadmin.Code("A" + string(code)[1:]).Digest()

	compare := []struct {
		name string
		a, b string
		want bool
	}{
		{name: "same", a: digest, b: digest, want: true},
		{name: "other code", a: digest, b: other, want: false},
		{name: "first character differs", a: digest, b: flip(digest, 0), want: false},
		{name: "last character differs", a: digest, b: flip(digest, len(digest)-1), want: false},
		{name: "shorter", a: digest, b: digest[:len(digest)-1], want: false},
		{name: "longer", a: digest, b: digest + "0", want: false},
		{name: "empty and digest", a: "", b: digest, want: false},
		{name: "empty and empty", a: "", b: "", want: true},
	}

	for _, tc := range compare {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := firstadmin.DigestsEqual(tc.a, tc.b); got != tc.want {
				t.Errorf("DigestsEqual = %t, want %t", got, tc.want)
			}
		})
	}

	forms := []struct {
		name string
		d    string
		want bool
	}{
		{name: "digest", d: digest, want: true},
		{name: "empty", d: "", want: false},
		{name: "one short", d: digest[:63], want: false},
		{name: "one long", d: digest + "0", want: false},
		{name: "upper case", d: strings.ToUpper(digest), want: false},
		{name: "not hex", d: strings.Repeat("g", firstadmin.DigestLength), want: false},
		{name: "space", d: " " + digest[1:], want: false},
	}

	for _, tc := range forms {
		t.Run("form "+tc.name, func(t *testing.T) {
			t.Parallel()

			if got := firstadmin.ValidDigest(tc.d); got != tc.want {
				t.Errorf("ValidDigest = %t, want %t", got, tc.want)
			}
		})
	}
}

func flip(s string, i int) string {
	b := []byte(s)
	if b[i] == '0' {
		b[i] = '1'
	} else {
		b[i] = '0'
	}

	return string(b)
}

// TestValidateReason checks that the reason rule is the rule of a temporary
// administrator and maps to ErrReasonInvalid.
func TestValidateReason(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		reason string
		ok     bool
	}{
		{name: "text", reason: "first administrator of the new installation", ok: true},
		{name: "500 characters", reason: strings.Repeat("a", 500), ok: true},
		{name: "blank", reason: "   ", ok: false},
		{name: "empty", reason: "", ok: false},
		{name: "501 characters", reason: strings.Repeat("a", 501), ok: false},
		{name: "not UTF-8", reason: "a\xffb", ok: false},
		{name: "control", reason: "a\x00b", ok: false},
		{name: "new line", reason: "a\nb", ok: false},
		{name: "bidirectional override", reason: "a\u202eb", ok: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := firstadmin.ValidateReason(tc.reason)
			if tc.ok && err != nil {
				t.Fatalf("ValidateReason error = %v, want nil", err)
			}

			if !tc.ok && !errors.Is(err, firstadmin.ErrReasonInvalid) {
				t.Fatalf("ValidateReason error = %v, want ErrReasonInvalid", err)
			}
		})
	}
}

const testWin = 5 * time.Minute

func testNow() time.Time { return time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC) }

func testCode() firstadmin.Code {
	return firstadmin.Code("A" + strings.Repeat("B", firstadmin.CodeLength-1))
}

// errNoEntropy is the error of a random source that fails.
var errNoEntropy = errors.New("random failure")

const testPrincipalID = "prn-abc123"

func person() principal.Principal {
	return principal.Principal{
		CreatedAt: testNow().Add(-time.Hour), UpdatedAt: testNow().Add(-time.Hour), DisabledAt: time.Time{},
		ID: testPrincipalID, Subject: "ada@example.org", DisplayName: "Ada",
		Kind: principal.KindPerson, Source: "", Namespace: "", GroupCount: 0, BreakGlass: false,
	}
}

// goodFacts are facts for which CheckRedeem answers nil. Each row of the
// table changes one fact.
func goodFacts() firstadmin.Facts {
	return firstadmin.Facts{
		Now:         testNow(),
		AuthTime:    testNow().Add(-time.Minute),
		CodeExpires: testNow().Add(firstadmin.Lifetime / 2),
		CodePrin:    testPrincipalID,
		CodeDigest:  testCode().Digest(),
		InDigest:    testCode().Digest(),
		InPrincipal: testPrincipalID,
		Principal:   person(),
		Window:      testWin,
		MarkerSet:   false,
		HaveCode:    true,
		HavePrin:    true,
		TempAdmin:   false,
		HaveSession: true, SessionOwner: testPrincipalID, SessionStatus: session.StatusActive,
	}
}

// TestCheckRedeemTable has one row for each rule of CheckRedeem. A row
// changes one fact of the good facts and names the error. Each rule is a
// guard: remove it and its row fails.
func TestCheckRedeemTable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		change func(f *firstadmin.Facts)
		want   error
	}{
		{name: "all facts hold", change: func(*firstadmin.Facts) {}, want: nil},
		{
			name:   "marker set",
			change: func(f *firstadmin.Facts) { f.MarkerSet = true },
			want:   firstadmin.ErrAlreadyMade,
		},
		{
			name: "marker set wins over a wrong code",
			change: func(f *firstadmin.Facts) {
				f.MarkerSet = true
				f.InDigest = firstadmin.Code("other").Digest()
			},
			want: firstadmin.ErrAlreadyMade,
		},
		{
			name:   "no code row",
			change: func(f *firstadmin.Facts) { f.HaveCode = false },
			want:   firstadmin.ErrCodeInvalid,
		},
		{
			// The compare of an absent row runs on the empty digest. An
			// input digest that is empty must not match it.
			name: "no code row and an empty input digest",
			change: func(f *firstadmin.Facts) {
				f.HaveCode = false
				f.CodeDigest = ""
				f.InDigest = ""
			},
			want: firstadmin.ErrCodeInvalid,
		},
		{
			name:   "wrong digest",
			change: func(f *firstadmin.Facts) { f.InDigest = firstadmin.Code("other").Digest() },
			want:   firstadmin.ErrCodeInvalid,
		},
		{
			name:   "code expired at the instant",
			change: func(f *firstadmin.Facts) { f.CodeExpires = testNow() },
			want:   firstadmin.ErrCodeInvalid,
		},
		{
			name:   "code expired before now",
			change: func(f *firstadmin.Facts) { f.CodeExpires = testNow().Add(-time.Second) },
			want:   firstadmin.ErrCodeInvalid,
		},
		{
			name:   "code one nanosecond before expiry",
			change: func(f *firstadmin.Facts) { f.CodeExpires = testNow().Add(time.Nanosecond) },
			want:   nil,
		},
		{
			name:   "code made for another person",
			change: func(f *firstadmin.Facts) { f.CodePrin = "prn-other" },
			want:   firstadmin.ErrCodeInvalid,
		},
		{
			name:   "code row with no principal",
			change: func(f *firstadmin.Facts) { f.CodePrin = ""; f.InPrincipal = "" },
			want:   firstadmin.ErrCodeInvalid,
		},
		{
			name:   "session principal is another person",
			change: func(f *firstadmin.Facts) { f.InPrincipal = "prn-other" },
			want:   firstadmin.ErrCodeInvalid,
		},
		{
			name:   "no live principal row",
			change: func(f *firstadmin.Facts) { f.HavePrin = false },
			want:   firstadmin.ErrPrincipalInvalid,
		},
		{
			name: "live row has another ID",
			change: func(f *firstadmin.Facts) {
				p := person()
				p.ID = "prn-other"
				f.Principal = p
			},
			want: firstadmin.ErrPrincipalInvalid,
		},
		{
			name:   "principal checks run: disabled",
			change: func(f *firstadmin.Facts) { f.Principal.DisabledAt = testNow().Add(-time.Hour) },
			want:   firstadmin.ErrPrincipalInvalid,
		},
		{
			name:   "principal checks run: temporary administrator",
			change: func(f *firstadmin.Facts) { f.TempAdmin = true },
			want:   firstadmin.ErrPrincipalInvalid,
		},
		{
			name:   "window of 0",
			change: func(f *firstadmin.Facts) { f.Window = 0 },
			want:   firstadmin.ErrStaleSignIn,
		},
		{
			// A window of 0 asks at each step: even a sign-in at this
			// instant is not fresh.
			name:   "window of 0 and a sign-in at this instant",
			change: func(f *firstadmin.Facts) { f.Window = 0; f.AuthTime = testNow() },
			want:   firstadmin.ErrStaleSignIn,
		},
		{
			name:   "negative window",
			change: func(f *firstadmin.Facts) { f.Window = -time.Minute },
			want:   firstadmin.ErrStaleSignIn,
		},
		{
			name:   "sign-in older than the window",
			change: func(f *firstadmin.Facts) { f.AuthTime = testNow().Add(-testWin - time.Nanosecond) },
			want:   firstadmin.ErrStaleSignIn,
		},
		{
			name:   "sign-in exactly at the window",
			change: func(f *firstadmin.Facts) { f.AuthTime = testNow().Add(-testWin) },
			want:   nil,
		},
		{
			name:   "sign-in after now",
			change: func(f *firstadmin.Facts) { f.AuthTime = testNow().Add(time.Nanosecond) },
			want:   firstadmin.ErrStaleSignIn,
		},
		{
			name:   "zero sign-in time",
			change: func(f *firstadmin.Facts) { f.AuthTime = time.Time{} },
			want:   firstadmin.ErrStaleSignIn,
		},
		{
			name:   "no session row",
			change: func(f *firstadmin.Facts) { f.HaveSession = false },
			want:   firstadmin.ErrSessionInvalid,
		},
		{
			name:   "session of another person",
			change: func(f *firstadmin.Facts) { f.SessionOwner = "prn-other" },
			want:   firstadmin.ErrSessionInvalid,
		},
		{
			name:   "ended session",
			change: func(f *firstadmin.Facts) { f.SessionStatus = session.StatusEnded },
			want:   firstadmin.ErrSessionInvalid,
		},
		{
			name:   "idle session",
			change: func(f *firstadmin.Facts) { f.SessionStatus = session.StatusIdle },
			want:   firstadmin.ErrSessionInvalid,
		},
		{
			name:   "expired session",
			change: func(f *firstadmin.Facts) { f.SessionStatus = session.StatusExpired },
			want:   firstadmin.ErrSessionInvalid,
		},
		{
			// A wrong code is reported before a stale sign-in, so a stale
			// sign-in never tells whether the code was right.
			name: "wrong code wins over a stale sign-in",
			change: func(f *firstadmin.Facts) {
				f.InDigest = firstadmin.Code("other").Digest()
				f.AuthTime = testNow().Add(-24 * time.Hour)
			},
			want: firstadmin.ErrCodeInvalid,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := goodFacts()
			tc.change(&f)

			err := firstadmin.CheckRedeem(f)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("CheckRedeem error = %v, want nil", err)
				}

				return
			}

			if !errors.Is(err, tc.want) {
				t.Fatalf("CheckRedeem error = %v, want %v", err, tc.want)
			}
		})
	}
}

// principalRow is one case of CheckPrincipal. A row with no text is a person
// that passes. A row with a text names a part of the error of the rule.
type principalRow struct {
	name      string
	change    func(p *principal.Principal)
	text      string
	tempAdmin bool
}

func passes(name string, change func(p *principal.Principal)) principalRow {
	return principalRow{name: name, change: change, text: "", tempAdmin: false}
}

func refused(name, text string, change func(p *principal.Principal)) principalRow {
	return principalRow{name: name, change: change, text: text, tempAdmin: false}
}

func nothing(*principal.Principal) {}

func withID(id string) func(p *principal.Principal) {
	return func(p *principal.Principal) { p.ID = id }
}

func withSubject(s string) func(p *principal.Principal) {
	return func(p *principal.Principal) { p.Subject = s }
}

func withKind(k principal.Kind) func(p *principal.Principal) {
	return func(p *principal.Principal) { p.Kind = k }
}

func principalRows() []principalRow {
	return []principalRow{
		passes("enabled person", nothing),
		refused("empty ID", "ID", withID("")),
		refused("ID is the prefix only", "ID", withID(principal.IDPrefix)),
		refused("ID without the prefix", "ID", withID("abc123")),
		refused("ID of a group", "ID", withID("grp-abc")),
		refused("ID with a slash", "ID", withID("prn-a/b")),
		refused("ID with a new line", "ID", withID("prn-a\nb")),
		refused("ID over 64 bytes", "ID", withID("prn-"+strings.Repeat("a", 61))),
		passes("ID of 64 bytes", withID("prn-"+strings.Repeat("a", 60))),
		refused("service", "person", withKind(principal.KindService)),
		refused("workload", "person", withKind(principal.KindWorkload)),
		refused("no kind", "person", withKind("")),
		refused("namespace principal", "namespace", func(p *principal.Principal) { p.Namespace = "acme" }),
		refused("disabled", "disabled", func(p *principal.Principal) { p.DisabledAt = testNow() }),
		refused("break-glass", "break-glass", func(p *principal.Principal) { p.BreakGlass = true }),
		{name: "temporary administrator", change: nothing, text: "temporary", tempAdmin: true},
		refused("dev subject", "dev:", withSubject("dev:ada")),
		refused("dev subject in upper case", "dev:", withSubject("DEV:ada")),
		refused("bootstrap subject admin", "bootstrap", withSubject("admin")),
		refused("bootstrap subject admin-2", "bootstrap", withSubject("admin-2")),
		refused("bootstrap subject Admin", "bootstrap", withSubject("Admin")),
		passes("subject that only starts with admin", withSubject("administrator")),
	}
}

// TestCheckPrincipalTable has one row for each rule of CheckPrincipal.
func TestCheckPrincipalTable(t *testing.T) {
	t.Parallel()

	for _, tc := range principalRows() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := person()
			tc.change(&p)

			err := firstadmin.CheckPrincipal(p, tc.tempAdmin)
			if tc.text == "" {
				if err != nil {
					t.Fatalf("CheckPrincipal error = %v, want nil", err)
				}

				return
			}

			if !errors.Is(err, firstadmin.ErrPrincipalInvalid) {
				t.Fatalf("CheckPrincipal error = %v, want ErrPrincipalInvalid", err)
			}

			if want := ruleOf(tc.text); !errors.Is(err, want) {
				t.Errorf("CheckPrincipal error = %q, want the sentinel of the rule %q (%v)", err, tc.text, want)
			}
		})
	}
}

// ruleOf maps the label of a row to the sentinel of its rule.
func ruleOf(label string) error {
	switch label {
	case "ID", "person", "namespace":
		return firstadmin.ErrPrincipalNotPerson
	case "disabled":
		return firstadmin.ErrPrincipalDisabled
	case "break-glass":
		return firstadmin.ErrPrincipalBreakGlass
	case "temporary":
		return firstadmin.ErrPrincipalTempAdmin
	case "dev:":
		return firstadmin.ErrPrincipalDevSubject
	case "bootstrap":
		return firstadmin.ErrPrincipalBootstrapSubject
	default:
		return firstadmin.ErrPrincipalInvalid
	}
}
