package outputshare_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/outputshare"
)

const (
	acme = namespace.Name("acme")
	beta = namespace.Name("beta")
)

func unitObject(t *testing.T, tool string, ns namespace.Name, name string) identity.Object {
	t.Helper()

	o, err := identity.UnitObject(tool, ns, name)
	if err != nil {
		t.Fatalf("unit object %s:%s/%s: %v", tool, ns, name, err)
	}

	return o
}

func rule(producer, consumer identity.Object) outputshare.Rule {
	return outputshare.Rule{
		CreatedAt: time.Time{},
		ID:        "r-1",
		CreatedBy: "p-1",
		Producer:  producer,
		Consumer:  consumer,
	}
}

func TestRuleValidate(t *testing.T) {
	t.Parallel()

	network := unitObject(t, "pulumi", acme, "net/prod").WithUnitID("stk-1")
	web := unitObject(t, "opentofu", acme, "web").WithUnitID("ws-1")
	otherWeb := unitObject(t, "opentofu", beta, "web")
	platform := identity.CollectionObject(acme, "platform").WithCollectionID("col-1")
	otherPlatform := identity.CollectionObject(beta, "platform")
	principal := identity.PrincipalObject("p-2")

	for _, tc := range []struct {
		name string
		rule outputshare.Rule
		ok   bool
	}{
		{"unit to unit", rule(network, web), true},
		{"unit to a unit of another tool", rule(web, network), true},
		{"unit to collection", rule(network, platform), true},
		{"collection to namespace", rule(platform, identity.NamespaceObject(acme)), true},
		{"namespace to namespace", rule(identity.NamespaceObject(acme), identity.NamespaceObject(acme)), true},
		{"unit to a unit of another namespace", rule(network, otherWeb), true},
		{"unit to a collection of another namespace", rule(network, otherPlatform), true},
		{"unit to a whole other namespace", rule(network, identity.NamespaceObject(beta)), false},
		{"unit to a principal", rule(network, principal), false},
		{"principal as producer", rule(principal, web), false},
		{"unit to the installation", rule(network, identity.ServerObject()), false},
		{"unit to the caller", rule(network, identity.CallerObject()), false},
		{"unit to the zero object", rule(network, identity.Object{}), false},
		{"zero producer", rule(identity.Object{}, web), false},
		{"empty ID", withID(rule(network, web), ""), false},
		{"ID with an upper case byte", withID(rule(network, web), "R-1"), false},
		{"ID with a slash", withID(rule(network, web), "r/1"), false},
		{"ID too long", withID(rule(network, web), outputshare.ID(strings.Repeat("r", outputshare.MaxIDBytes+1))), false},
		{"no saver", withSaver(rule(network, web), ""), false},
		{"producer unit with no stable ID", rule(unitObject(t, "pulumi", acme, "net/prod"), web), false},
		{"producer collection with no stable ID", rule(identity.CollectionObject(acme, "platform"), web), false},
		{"consumer unit of the namespace with no stable ID", rule(network, unitObject(t, "opentofu", acme, "web")), false},
		{"consumer collection of the namespace with no stable ID", rule(network, identity.CollectionObject(acme, "platform")), false},
		{"consumer unit of another namespace with a stable ID", rule(network, otherWeb.WithUnitID("ws-9")), false},
		{
			"consumer collection of another namespace with a stable ID",
			rule(network, otherPlatform.WithCollectionID("col-9")), false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.rule.Validate()
			if tc.ok && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}

			if !tc.ok && !errors.Is(err, outputshare.ErrInvalid) {
				t.Fatalf("Validate() = %v, want ErrInvalid", err)
			}
		})
	}
}

func withID(r outputshare.Rule, id outputshare.ID) outputshare.Rule {
	r.ID = id

	return r
}

func withSaver(r outputshare.Rule, saver string) outputshare.Rule {
	r.CreatedBy = saver

	return r
}

func TestRuleCrossNamespace(t *testing.T) {
	t.Parallel()

	network := unitObject(t, "pulumi", acme, "net/prod")

	if rule(network, unitObject(t, "opentofu", acme, "web")).CrossNamespace() {
		t.Fatal("a consumer of the same namespace is not across namespaces")
	}

	if !rule(network, identity.CollectionObject(beta, "platform")).CrossNamespace() {
		t.Fatal("a collection of another namespace is across namespaces")
	}
}

func TestDefaultValidate(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		value outputshare.Default
		ok    bool
	}{
		{outputshare.DefaultNobody, true},
		{outputshare.DefaultNamespace, true},
		{"", false},
		{"everyone", false},
		{"Nobody", false},
	} {
		err := tc.value.Validate()
		if tc.ok != (err == nil) {
			t.Fatalf("Default(%q).Validate() = %v, want ok %t", tc.value, err, tc.ok)
		}

		if err != nil && !errors.Is(err, outputshare.ErrInvalid) {
			t.Fatalf("Default(%q).Validate() = %v, want ErrInvalid", tc.value, err)
		}
	}
}

func TestPlacementValidate(t *testing.T) {
	t.Parallel()

	web := unitObject(t, "opentofu", acme, "web").WithUnitID("ws-1")
	platform := identity.CollectionObject(acme, "platform").WithCollectionID("col-1")

	for _, tc := range []struct {
		name      string
		placement outputshare.Placement
		ok        bool
	}{
		{"unit with no collection", outputshare.Placement{Unit: web, Collection: identity.Object{}}, true},
		{"unit with its collection", outputshare.Placement{Unit: web, Collection: platform}, true},
		{
			"unit with no stable ID",
			outputshare.Placement{Unit: unitObject(t, "opentofu", acme, "web"), Collection: identity.Object{}},
			false,
		},
		{"namespace as unit", outputshare.Placement{Unit: identity.NamespaceObject(acme), Collection: identity.Object{}}, false},
		{
			"collection with no stable ID",
			outputshare.Placement{Unit: web, Collection: identity.CollectionObject(acme, "platform")},
			false,
		},
		{
			"collection of another namespace",
			outputshare.Placement{Unit: web, Collection: identity.CollectionObject(beta, "platform").WithCollectionID("col-2")},
			false,
		},
		{"namespace as collection", outputshare.Placement{Unit: web, Collection: identity.NamespaceObject(acme)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.placement.Validate()
			if tc.ok != (err == nil) {
				t.Fatalf("Validate() = %v, want ok %t", err, tc.ok)
			}

			if err != nil && !errors.Is(err, outputshare.ErrInvalid) {
				t.Fatalf("Validate() = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestDeniedError(t *testing.T) {
	t.Parallel()

	producer := unitObject(t, "pulumi", acme, "net/prod")
	consumer := unitObject(t, "opentofu", acme, "web")

	byJob := &outputshare.DeniedError{
		Err: outputshare.ErrNoRule, Check: outputshare.CheckRule, Producer: producer, Consumer: consumer, Hidden: false,
	}

	var err error = byJob
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("errors.Is(%v, identity.ErrDenied) = false", err)
	}

	if errors.Is(err, outputshare.ErrNoRule) || errors.Is(err, outputshare.ErrUnknownProducer) {
		t.Fatalf("a denial of a job of the producer namespace %v unwraps to its cause or is hidden", err)
	}

	var denied *outputshare.DeniedError
	if !errors.As(fmt.Errorf("check: %w", err), &denied) || !errors.Is(denied.Err, outputshare.ErrNoRule) {
		t.Fatal("the cause is not in the field Err of a wrapped denial")
	}

	for _, want := range []string{producer.String(), consumer.String(), outputshare.CheckRule} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Error() = %q, want %q in it", err.Error(), want)
		}
	}

	byPrincipal := &outputshare.DeniedError{
		Err: nil, Check: outputshare.CheckGrant, Producer: producer, Consumer: identity.Object{}, Hidden: false,
	}

	if !errors.Is(byPrincipal, identity.ErrDenied) || strings.Contains(byPrincipal.Error(), "job") {
		t.Fatalf("a denial of a principal = %q, want identity.ErrDenied and no job", byPrincipal.Error())
	}
}

// TestDeniedErrorSentinel sends a denial of each check, with a cause that
// wraps each other sentinel, and proves that the denial unwraps to its one
// sentinel only, so that the order of the errors.Is tests of an adapter
// cannot change the answer.
func TestDeniedErrorSentinel(t *testing.T) {
	t.Parallel()

	producer := unitObject(t, "opentofu", acme, "network")
	sameNS := unitObject(t, "opentofu", acme, "web")
	sentinels := []error{identity.ErrDenied, outputshare.ErrUnknownProducer, outputshare.ErrClientEncrypted}

	for _, tc := range []struct {
		name     string
		check    string
		consumer identity.Object
		want     error
	}{
		{"a principal denied by the grant check", outputshare.CheckGrant, identity.Object{}, identity.ErrDenied},
		{"a job of the namespace with no rule", outputshare.CheckRule, sameNS, identity.ErrDenied},
		{"a job of the namespace with a stale state", outputshare.CheckLatest, sameNS, identity.ErrDenied},
		{"a job of the namespace with a bad attachment", outputshare.CheckAttachment, sameNS, identity.ErrDenied},
		{"a principal on a client-encrypted state", outputshare.CheckClientEncrypted, identity.Object{}, outputshare.ErrClientEncrypted},
		{"a job of the namespace on a client-encrypted state", outputshare.CheckClientEncrypted, sameNS, outputshare.ErrClientEncrypted},
		{"a principal on an unknown producer", outputshare.CheckProducer, identity.Object{}, outputshare.ErrUnknownProducer},
		{"a job of the namespace on an unknown producer", outputshare.CheckProducer, sameNS, outputshare.ErrUnknownProducer},
	} {
		for _, cause := range sentinels {
			t.Run(tc.name+" with the cause "+cause.Error(), func(t *testing.T) {
				t.Parallel()

				denied := &outputshare.DeniedError{
					Err: fmt.Errorf("%w: from a port", cause), Check: tc.check, Producer: producer,
					Consumer: tc.consumer, Hidden: false,
				}

				for _, sentinel := range sentinels {
					if got := errors.Is(denied, sentinel); got != errors.Is(sentinel, tc.want) {
						t.Fatalf("errors.Is(%v, %v) = %t", denied, sentinel, got)
					}
				}
			})
		}
	}
}

// TestDeniedErrorHidden sends each denial that must answer as an unknown
// producer, with a cause that wraps identity.ErrDenied or ErrClientEncrypted as
// the error of identity.Authorizer does, and proves that it unwraps only to
// ErrUnknownProducer, so that an adapter cannot answer 403 or 409 for it.
func TestDeniedErrorHidden(t *testing.T) {
	t.Parallel()

	producer := unitObject(t, "pulumi", acme, "net/prod")
	other := unitObject(t, "opentofu", beta, "web")
	denied := fmt.Errorf("%w: no grant of unit:read-outputs", identity.ErrDenied)

	for _, tc := range []struct {
		name   string
		denied *outputshare.DeniedError
	}{
		{
			"a job of another namespace, with no flag",
			&outputshare.DeniedError{
				Err: outputshare.ErrNoRule, Check: outputshare.CheckRule, Producer: producer,
				Consumer: other, Hidden: false,
			},
		},
		{
			"a job of another namespace denied by the grant check",
			&outputshare.DeniedError{
				Err: nil, Check: outputshare.CheckGrant, Producer: producer, Consumer: other, Hidden: true,
			},
		},
		{
			"a grant check whose cause wraps identity.ErrDenied, with no flag",
			&outputshare.DeniedError{
				Err: denied, Check: outputshare.CheckGrant, Producer: producer, Consumer: other, Hidden: false,
			},
		},
		{
			"a grant check whose cause wraps identity.ErrDenied, with the flag",
			&outputshare.DeniedError{
				Err: denied, Check: outputshare.CheckGrant, Producer: producer, Consumer: other, Hidden: true,
			},
		},
		{
			"a secrets check whose cause wraps identity.ErrDenied",
			&outputshare.DeniedError{
				Err: denied, Check: outputshare.CheckSecrets, Producer: producer, Consumer: other, Hidden: false,
			},
		},
		{
			"a client-encrypted state for a job of another namespace",
			&outputshare.DeniedError{
				Err: fmt.Errorf("%w: envelope", outputshare.ErrClientEncrypted), Check: outputshare.CheckClientEncrypted,
				Producer: producer, Consumer: other, Hidden: false,
			},
		},
		{
			"an unknown producer for a job of another namespace",
			&outputshare.DeniedError{
				Err: denied, Check: outputshare.CheckProducer, Producer: producer, Consumer: other, Hidden: true,
			},
		},
		{
			"a job whose unit the checker could not read",
			&outputshare.DeniedError{
				Err: denied, Check: outputshare.CheckCaller, Producer: producer, Consumer: identity.Object{}, Hidden: true,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkOnlyUnknownProducer(t, tc.denied)
		})
	}
}

// checkOnlyUnknownProducer fails t when denied is not hidden, or when it
// unwraps to anything other than ErrUnknownProducer.
func checkOnlyUnknownProducer(t *testing.T, denied *outputshare.DeniedError) {
	t.Helper()

	if !denied.IsHidden() {
		t.Fatal("IsHidden() = false, want true")
	}

	err := fmt.Errorf("read: %w", denied)
	if !errors.Is(err, outputshare.ErrUnknownProducer) {
		t.Fatalf("errors.Is(%v, ErrUnknownProducer) = false", err)
	}

	for _, never := range []error{identity.ErrDenied, outputshare.ErrClientEncrypted, outputshare.ErrNoRule} {
		if errors.Is(err, never) {
			t.Fatalf("errors.Is(%v, %v) = true, want only ErrUnknownProducer", err, never)
		}
	}

	if unwrapped := errors.Unwrap(denied); !errors.Is(unwrapped, outputshare.ErrUnknownProducer) ||
		errors.Unwrap(unwrapped) != nil {
		t.Fatalf("Unwrap() = %v, want ErrUnknownProducer and nothing under it", unwrapped)
	}
}

// TestAttachmentRefValidate sends each attachment reference that is not
// valid, and proves that Validate refuses it and does not change it.
func TestAttachmentRefValidate(t *testing.T) {
	t.Parallel()

	good := outputshare.AttachmentRef{Attachment: "att-7c2d", Profile: "prf-0a1b", Block: "network.vpc", Revision: 3}

	if err := good.Validate(); err != nil {
		t.Fatalf("Validate(%+v) = %v", good, err)
	}

	zero := outputshare.AttachmentRef{Attachment: "", Profile: "", Block: "", Revision: 0}
	if good.IsZero() || !zero.IsZero() {
		t.Fatal("IsZero is wrong")
	}

	// with returns good with one part changed.
	with := func(change func(r *outputshare.AttachmentRef)) outputshare.AttachmentRef {
		r := good
		change(&r)

		return r
	}

	for _, tc := range []struct {
		name string
		ref  outputshare.AttachmentRef
	}{
		{"zero", zero},
		{"no attachment", with(func(r *outputshare.AttachmentRef) { r.Attachment = "" })},
		{"attachment with a slash", with(func(r *outputshare.AttachmentRef) { r.Attachment = "att-1/../2" })},
		{"attachment with a dot", with(func(r *outputshare.AttachmentRef) { r.Attachment = "att.1" })},
		{"attachment with an upper case byte", with(func(r *outputshare.AttachmentRef) { r.Attachment = "ATT-1" })},
		{"attachment with a CR LF", with(func(r *outputshare.AttachmentRef) { r.Attachment = "att\r\n1" })},
		{
			"attachment too long",
			with(func(r *outputshare.AttachmentRef) { r.Attachment = strings.Repeat("a", outputshare.MaxIDBytes+1) }),
		},
		{"no profile", with(func(r *outputshare.AttachmentRef) { r.Profile = "" })},
		{"no block", with(func(r *outputshare.AttachmentRef) { r.Block = "" })},
		{"revision 0", with(func(r *outputshare.AttachmentRef) { r.Revision = 0 })},
		{"negative revision", with(func(r *outputshare.AttachmentRef) { r.Revision = -1 })},
		{"profile with a slash", with(func(r *outputshare.AttachmentRef) { r.Profile = "prf-1/../2" })},
		{"profile with a dot", with(func(r *outputshare.AttachmentRef) { r.Profile = "prf.1" })},
		{"profile with an upper case byte", with(func(r *outputshare.AttachmentRef) { r.Profile = "PRF-1" })},
		{
			"profile too long",
			with(func(r *outputshare.AttachmentRef) { r.Profile = strings.Repeat("p", outputshare.MaxIDBytes+1) }),
		},
		{"block with a slash", with(func(r *outputshare.AttachmentRef) { r.Block = "a/b" })},
		{"block with a CR LF", with(func(r *outputshare.AttachmentRef) { r.Block = "a\r\nb" })},
		{"block with a NUL", with(func(r *outputshare.AttachmentRef) { r.Block = "a\x00" })},
		{
			"block too long",
			with(func(r *outputshare.AttachmentRef) { r.Block = strings.Repeat("b", outputshare.MaxBlockBytes+1) }),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			before := tc.ref
			if err := tc.ref.Validate(); !errors.Is(err, outputshare.ErrInvalid) {
				t.Fatalf(
					"Validate(%q) = %v, want ErrInvalid",
					tc.ref.Attachment+"|"+tc.ref.Profile+"|"+tc.ref.Block,
					err,
				)
			}

			if tc.ref != before {
				t.Fatal("Validate changed the reference")
			}
		})
	}
}

// TestAttachmentCovers proves that the object of an attachment covers the
// unit of a job only as the same unit, the collection of the unit or the
// namespace of the unit, each by stable ID, and that each other object, as
// a port could answer it, does not.
func TestAttachmentCovers(t *testing.T) {
	t.Parallel()

	web := unitObject(t, "opentofu", acme, "web").WithUnitID("u-web")
	apps := identity.CollectionObject(acme, "apps").WithCollectionID("c-apps")
	job := outputshare.Placement{Unit: web, Collection: apps}
	noCollection := outputshare.Placement{Unit: web, Collection: identity.Object{}}

	for _, tc := range []struct {
		name   string
		object identity.Object
		unit   outputshare.Placement
		want   bool
	}{
		{"the unit of the job", web, job, true},
		{"the unit of the job after a rename", unitObject(t, "opentofu", acme, "web2").WithUnitID("u-web"), job, true},
		{"the collection of the unit", apps, job, true},
		{"the collection of the unit after a rename", identity.CollectionObject(acme, "apps2").WithCollectionID("c-apps"), job, true},
		{"the namespace of the unit", identity.NamespaceObject(acme), job, true},
		{"the namespace of a unit with no collection", identity.NamespaceObject(acme), noCollection, true},
		{"another unit", unitObject(t, "opentofu", acme, "db").WithUnitID("u-db"), job, false},
		{"a unit with the same name and another stable ID", unitObject(t, "opentofu", acme, "web").WithUnitID("u-old"), job, false},
		{"a unit with no stable ID", unitObject(t, "opentofu", acme, "web"), job, false},
		{"a unit of another namespace with the same stable ID", unitObject(t, "opentofu", beta, "web").WithUnitID("u-web"), job, false},
		{"another collection", identity.CollectionObject(acme, "data").WithCollectionID("c-data"), job, false},
		{"a collection with the same name and another stable ID", identity.CollectionObject(acme, "apps").WithCollectionID("c-old"), job, false},
		{"a collection with no stable ID", identity.CollectionObject(acme, "apps"), job, false},
		{"a collection with no stable ID and a unit with no collection", identity.CollectionObject(acme, "apps"), noCollection, false},
		{"a collection and a unit that moved out of it", apps, noCollection, false},
		{"a collection of another namespace with the same stable ID", identity.CollectionObject(beta, "apps").WithCollectionID("c-apps"), job, false},
		{"another namespace", identity.NamespaceObject(beta), job, false},
		{"a principal", identity.PrincipalObject("p-admin"), job, false},
		{"the caller", identity.CallerObject(), job, false},
		{"the installation", identity.ServerObject(), job, false},
		{"the zero object", identity.Object{}, job, false},
		{"a unit of the job with no stable ID", identity.NamespaceObject(acme), outputshare.Placement{Unit: unitObject(t, "opentofu", acme, "web"), Collection: apps}, false},
		{"a unit of the job with a collection of another namespace", identity.NamespaceObject(acme), outputshare.Placement{Unit: web, Collection: identity.CollectionObject(beta, "apps").WithCollectionID("c-apps")}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a := outputshare.Attachment{ID: "att-1", Object: tc.object, Producer: identity.Object{}, Authority: "p-1"}
			if got := a.Covers(tc.unit); got != tc.want {
				t.Fatalf("Covers(%q) of the object %q = %v, want %v", tc.unit.Unit, tc.object, got, tc.want)
			}
		})
	}
}
