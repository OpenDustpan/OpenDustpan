package stepup_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/stepup"
)

// keyAddNow is the time of the add in the tests.
func keyAddNow() time.Time {
	return time.UnixMilli(1_800_000_000_000)
}

// goodAddition returns an addition that passes the argument checks of AddKey.
func goodAddition() stepup.KeyAddition {
	return stepup.KeyAddition{
		Key: stepup.Key{
			ID: "stk-1", PersonID: "person-1", Factor: stepup.FactorSSH, Type: "ssh-ed25519",
			Fingerprint: "SHA256:abc", PublicKey: []byte{1}, Name: "laptop", Counter: 0, Suspended: false,
			EnrolledBy: "", CreatedAt: keyAddNow(),
		},
		ChallengeID: "stc-1", At: keyAddNow(),
	}
}

// goodChallenge returns the open key-add challenge that matches goodAddition,
// made by the person.
func goodChallenge() *stepup.Challenge {
	return &stepup.Challenge{
		ID:        "stc-1",
		Nonce:     nil,
		PersonID:  "person-1",
		Holder:    stepup.Holder{Kind: "", ID: ""},
		CreatedBy: "person-1",
		Requester: stepup.Requester{TokenName: "", ClientIP: ""},
		Origin:    "",
		Scope:     stepup.ScopeKeyAdd,
		State:     stepup.StateOpen,
		Action:    "",
		Object:    "",
		Binding: stepup.Binding{
			Method:         "",
			Target:         "",
			ContentType:    "",
			BodySHA256:     [32]byte{},
			IfMatch:        "",
			IdempotencyKey: "",
		},
		JobID:         "",
		PlanDigest:    "",
		Namespace:     "",
		Actions:       nil,
		KeyAdd:        stepup.KeyAddProof{Type: "ssh-ed25519", Fingerprint: "SHA256:abc"},
		PlanSummary:   "",
		Summary:       "",
		SummarySHA256: [32]byte{},
		Failures:      0,
		CreatedAt:     time.Time{},
		ExpiresAt:     keyAddNow().Add(time.Minute),
		AcceptedAt:    time.Time{},
	}
}

// argCase is one case of TestCheckKeyAdditionArguments.
type argCase struct {
	mutate  func(a *stepup.KeyAddition)
	name    string
	adminID string
	enroll  bool
	wantErr bool
}

// argCases lists the cases of TestCheckKeyAdditionArguments.
func argCases() []argCase {
	asAdd := func(name string, wantErr bool, mutate func(a *stepup.KeyAddition)) argCase {
		return argCase{name: name, mutate: mutate, adminID: "", enroll: false, wantErr: wantErr}
	}
	asEnroll := func(name, adminID string, wantErr bool, mutate func(a *stepup.KeyAddition)) argCase {
		return argCase{name: name, mutate: mutate, adminID: adminID, enroll: true, wantErr: wantErr}
	}
	enrolledBy := func(id string) func(a *stepup.KeyAddition) {
		return func(a *stepup.KeyAddition) { a.Key.EnrolledBy = id }
	}
	none := func(*stepup.KeyAddition) {}

	return []argCase{
		asAdd("add is valid", false, none),
		asEnroll("enrollment is valid", "admin-1", false, enrolledBy("admin-1")),
		asAdd("empty key ID", true, func(a *stepup.KeyAddition) { a.Key.ID = "" }),
		asAdd("empty person", true, func(a *stepup.KeyAddition) { a.Key.PersonID = "" }),
		asAdd("empty type", true, func(a *stepup.KeyAddition) { a.Key.Type = "" }),
		asAdd("empty fingerprint", true, func(a *stepup.KeyAddition) { a.Key.Fingerprint = "" }),
		asAdd("empty public key", true, func(a *stepup.KeyAddition) { a.Key.PublicKey = nil }),
		asAdd("empty name", true, func(a *stepup.KeyAddition) { a.Key.Name = "" }),
		asAdd("zero creation time", true, func(a *stepup.KeyAddition) { a.Key.CreatedAt = time.Time{} }),
		asAdd("factor totp", true, func(a *stepup.KeyAddition) { a.Key.Factor = stepup.FactorTOTP }),
		asAdd("factor empty", true, func(a *stepup.KeyAddition) { a.Key.Factor = "" }),
		asAdd("empty challenge ID", true, func(a *stepup.KeyAddition) { a.ChallengeID = "" }),
		asAdd("zero time of the add", true, func(a *stepup.KeyAddition) { a.At = time.Time{} }),
		asAdd("add with EnrolledBy", true, enrolledBy("admin-1")),
		{name: "add with an administrator", mutate: none, adminID: "admin-1", enroll: false, wantErr: true},
		asEnroll("enrollment without an administrator", "", true, enrolledBy("admin-1")),
		asEnroll("enrollment by the person", "person-1", true, enrolledBy("person-1")),
		asEnroll("enrollment with another EnrolledBy", "admin-1", true, enrolledBy("admin-2")),
		asEnroll("enrollment with no EnrolledBy", "admin-1", true, none),
	}
}

// TestCheckKeyAdditionArguments proves the rules of step 1 of AddKey and
// EnrollFirstKey, each on its own, with no stored challenge.
func TestCheckKeyAdditionArguments(t *testing.T) {
	t.Parallel()

	for _, c := range argCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			a := goodAddition()
			c.mutate(&a)

			err := stepup.CheckKeyAdditionArguments(a, c.adminID, c.enroll)
			if c.wantErr && !errors.Is(err, stepup.ErrInvalid) {
				t.Fatalf("CheckKeyAdditionArguments() = %v, want ErrInvalid", err)
			}

			if !c.wantErr && err != nil {
				t.Fatalf("CheckKeyAdditionArguments() = %v, want nil", err)
			}
		})
	}
}

// chalCase is one case of TestCheckKeyAdditionChallenge.
type chalCase struct {
	mutate  func(c *stepup.Challenge)
	wantErr error
	name    string
	creator string
}

// chalCases lists the cases of TestCheckKeyAdditionChallenge. The cases that
// name two faults show which one decides.
func chalCases() []chalCase {
	person := func(name string, want error, mutate func(c *stepup.Challenge)) chalCase {
		return chalCase{name: name, mutate: mutate, wantErr: want, creator: "person-1"}
	}
	otherPerson := func(c *stepup.Challenge) { c.PersonID = "person-2" }
	none := func(*stepup.Challenge) {}
	expired := func(c *stepup.Challenge) { c.ExpiresAt = keyAddNow().Add(-time.Hour) }

	return []chalCase{
		person("valid", nil, none),
		person("another person", stepup.ErrNotFound, otherPerson),
		person("not made by the creator", stepup.ErrNotFound, func(c *stepup.Challenge) { c.CreatedBy = "person-9" }),
		{
			name:    "empty creator",
			mutate:  func(c *stepup.Challenge) { c.CreatedBy = "" },
			wantErr: stepup.ErrNotFound,
			creator: "",
		},
		{
			name: "enrollment with a challenge that the person made", mutate: none, wantErr: stepup.ErrNotFound,
			creator: "admin-1",
		},
		person("another person and another scope: not found", stepup.ErrNotFound, func(c *stepup.Challenge) {
			otherPerson(c)
			c.Scope = stepup.ScopeWrite
		}),
		person("another person and another key: not found", stepup.ErrNotFound, func(c *stepup.Challenge) {
			otherPerson(c)
			c.KeyAdd.Fingerprint = "SHA256:other"
		}),
		person("another scope", stepup.ErrInvalid, func(c *stepup.Challenge) { c.Scope = stepup.ScopeWrite }),
		person("another type", stepup.ErrInvalid, func(c *stepup.Challenge) { c.KeyAdd.Type = "ssh-rsa" }),
		person("another fingerprint", stepup.ErrInvalid, func(c *stepup.Challenge) { c.KeyAdd.Fingerprint = "x" }),
		person("answered", stepup.ErrUsed, func(c *stepup.Challenge) { c.State = stepup.StateAnswered }),
		person("void", stepup.ErrUsed, func(c *stepup.Challenge) { c.State = stepup.StateVoid }),
		person("accepted", stepup.ErrUsed, func(c *stepup.Challenge) { c.State = stepup.StateAccepted }),
		person("answered and expired: used decides", stepup.ErrUsed, func(c *stepup.Challenge) {
			c.State = stepup.StateAnswered
			expired(c)
		}),
		person("expired before the add", stepup.ErrExpired, expired),
		person("expires at the add", stepup.ErrExpired, func(c *stepup.Challenge) { c.ExpiresAt = keyAddNow() }),
	}
}

// TestCheckKeyAdditionChallenge proves the rules and the order of steps 2 to
// 5.
func TestCheckKeyAdditionChallenge(t *testing.T) {
	t.Parallel()

	for _, c := range chalCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			ch := goodChallenge()
			c.mutate(ch)

			err := stepup.CheckKeyAdditionChallenge(goodAddition(), c.creator, ch)
			if c.wantErr == nil {
				if err != nil {
					t.Fatalf("CheckKeyAdditionChallenge() = %v, want nil", err)
				}

				return
			}

			if !errors.Is(err, c.wantErr) {
				t.Fatalf("CheckKeyAdditionChallenge() = %v, want %v", err, c.wantErr)
			}
		})
	}
}

// TestCheckKeyAdditionChallengeRefusesAMissingChallenge proves that a nil
// challenge, as an adapter passes for a missing one, is ErrNotFound.
func TestCheckKeyAdditionChallengeRefusesAMissingChallenge(t *testing.T) {
	t.Parallel()

	if err := stepup.CheckKeyAdditionChallenge(goodAddition(), "person-1", nil); !errors.Is(err, stepup.ErrNotFound) {
		t.Fatalf("CheckKeyAdditionChallenge(nil) = %v, want ErrNotFound", err)
	}
}
