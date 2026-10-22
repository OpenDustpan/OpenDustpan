// Package outputsharetest is the conformance suite of the ports of package
// outputshare: Store, Checker, Attachments and States, and the rule that the
// SQL adapter of a tool store writes when it makes a unit. A new adapter runs Run with a Factory.
//
// The SQL adapters serve the Settings port. Each other case names one
// behavior and is skipped with the reason Pending until the slice that adds
// the first adapter of its port writes it. That slice writes the case before
// the adapter code, and removes the skip of each case that it writes.
package outputsharetest

import (
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/outputshare"
)

// Pending is the skip reason of a case that no adapter can run yet.
const Pending = "no adapter implements this output sharing port yet; " +
	"the slice that adds the first adapter writes this case"

// Harness is one fresh, empty set of ports for one subtest.
type Harness struct {
	// Store keeps the rules and the setting. A case of the rules uses it.
	Store outputshare.Store
	// Settings keeps the setting. A case of the setting uses it, so that an
	// adapter that serves the setting and no rule runs those cases. An
	// adapter that serves Store gives Store here too.
	Settings outputshare.Settings
	// Checker decides each read. It reads the attachments and the latest
	// states through Attachments and States.
	Checker outputshare.Checker
	// Attachments reads the saved attachments of the profile block
	// outputs/opendustpan.
	Attachments outputshare.Attachments
	// States tells the latest state of a unit.
	States outputshare.States
	// Namespace makes the namespace name and returns its current fence.
	Namespace func(t *testing.T, name string) namespace.Fence
	// Unit makes a unit of the tool in the namespace, in the collection when
	// it is not empty, and returns its placement with the stable IDs. The
	// tool store writes the rule of the setting of the namespace in the
	// transaction that makes the unit.
	Unit func(t *testing.T, tool string, ns namespace.Name, name, collection string) outputshare.Placement
	// UnitFault is an optional hook: it makes a unit as Unit does, with a
	// fault that the adapter injects after the write of the first rule and
	// before the commit, and returns the error of the create. A case that
	// needs it skips when it is nil.
	UnitFault func(t *testing.T, tool string, ns namespace.Name, name string) error
	// Authority makes the attachment authority of an open of the profile
	// block outputs/opendustpan and returns its principal ID.
	Authority func(t *testing.T, ns namespace.Name) string
	// Profile saves a new revision of the profile with the stable ID profile
	// in the namespace ns, and makes the profile at its first revision. The
	// revision has one block outputs/opendustpan that names the producer,
	// and imports the profile imports when imports is not empty. The tag
	// latest moves to the new revision. It returns the reference of the
	// block with no attachment ID.
	Profile func(t *testing.T, ns namespace.Name, profile string, producer outputshare.Placement, imports string) outputshare.AttachmentRef
	// Attach attaches the profile at the tag latest to the object (a unit, a
	// collection or a namespace), with the authority as its attachment
	// authority, and returns the stable ID of the attachment. The spec of a
	// job that Job starts later resolves it.
	Attach func(t *testing.T, object identity.Object, profile, authority string) string
	// SaveState saves a new latest state of the unit, encrypted by the client
	// when encrypted is true, and returns its ID as State.ID names it.
	SaveState func(t *testing.T, unit outputshare.Placement, encrypted bool) string
	// Grant gives the principal the action on the object.
	Grant func(t *testing.T, principal string, action identity.Action, object identity.Object)
	// Job starts a job of the unit for the starter principal and returns the
	// principal of its job token. The start resolves the attachments that
	// cover the unit to fixed revisions in the spec of the job.
	Job func(t *testing.T, unit outputshare.Placement, starter string) identity.Principal
	// Disable disables the principal, as a person who leaves the team.
	Disable func(t *testing.T, principal string)
	// Audited is an optional hook: it returns the action of each audit entry
	// of the namespace, oldest first. A case that needs it skips when it is
	// nil.
	Audited func(t *testing.T, ns string) []string
	// Calls is an optional hook: it returns the port calls that the checker
	// made since the last call of Calls, in order, each as the port and the
	// method with no argument. A case that needs it skips when it is nil.
	Calls func(t *testing.T) []string
	// StoreRaw is an optional hook: it writes the rule into the store with
	// no Validate, as a row of another version or a restore would. A case
	// that needs it skips when it is nil.
	StoreRaw func(t *testing.T, r outputshare.Rule)
	// SettingRaw is an optional hook: it writes value as the stored setting
	// of the namespace ns with no Validate, as a row of another version or a
	// restore would. A case that needs it skips when it is nil.
	SettingRaw func(t *testing.T, ns namespace.Name, value string)
}

// Factory returns a fresh harness for one subtest.
type Factory func(t *testing.T) Harness

// suiteCase is one behavior of the ports. A nil test is a case that waits for
// the first adapter.
type suiteCase struct {
	test func(t *testing.T, h Harness)
	name string
}

// Run runs every case of the suite.
func Run(t *testing.T, newHarness Factory) {
	t.Helper()

	for _, tc := range allCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.test == nil {
				t.Skip(Pending)
			}

			tc.test(t, newHarness(t))
		})
	}
}

// Names returns the names of the cases of the suite, in the order of Run.
// Each call returns a new slice.
func Names() []string {
	cases := allCases()
	names := make([]string, 0, len(cases))

	for _, tc := range cases {
		names = append(names, tc.name)
	}

	return names
}

// allCases returns the cases of the suite. Each call returns a new slice.
func allCases() []suiteCase {
	return slices.Concat(
		storeCases(), newUnitCases(), checkerCases(), hiddenCases(), latestCases(), attachmentCases(),
		attachmentsCases(), statesCases(),
	)
}

// storeCases are the behaviors of Store.
func storeCases() []suiteCase {
	rules := []suiteCase{
		{nil, "store: put and get round trip a rule on a unit, a collection and a namespace"},
		{nil, "store: put refuses a rule that fails Validate and changes nothing"},
		{nil, "store: put refuses a rule that names a principal"},
		{nil, "store: put refuses a rule that names a whole other namespace"},
		{nil, "store: put keeps a rule that names a unit or a collection of another namespace"},
		{nil, "store: put refuses a producer or a consumer of the namespace with no stable ID"},
		{nil, "store: put refuses a producer unit that does not exist with ErrUnknownObject"},
		{nil, "store: put refuses a consumer unit or collection of the namespace that does not exist"},
		{nil, "store: put refuses a stable ID that names another object than the name"},
		{nil, "store: put refuses a consumer of another namespace with a stable ID"},
		{nil, "store: put of a consumer of another namespace answers the same whether it exists or not"},
		{nil, "store: put refuses a second rule with the same producer and consumer"},
		{nil, "store: put refuses the rule past the limit of the producer"},
		{nil, "store: put refuses a stale fence and a fence of another namespace"},
		{nil, "store: a rule across namespaces carries the fence of the producer only"},
		{nil, "store: get and delete of an unknown rule"},
		{nil, "store: get and delete of a rule of another unit answer ErrNotFound and change nothing"},
		{nil, "store: get and delete on a unit never reach the rule of its collection or its namespace"},
		{nil, "store: get and delete on the namespace never reach the rule of a unit"},
		{nil, "store: get and delete on a unit made again with the old name never reach the rule of the old unit"},
		{nil, "store: get and delete refuse an invalid ID and an invalid namespace"},
		{nil, "store: delete refuses a stale fence"},
		{nil, "store: list returns the rules of one producer ordered by ID"},
		{nil, "store: a rule is invisible to another namespace"},
		{nil, "store: a rename of the producer or of a consumer in the namespace keeps the rule"},
		{nil, "store: a collection of the namespace made again with the old name does not get the rules"},
		{nil, "store: a collection of another namespace made again with the old name matches the rule"},
		{nil, "store: the purge of a unit removes the rules that name it in its namespace"},
		{nil, "store: find matches a rule on the unit, its collection and its namespace"},
		{nil, "store: find matches a consumer unit, its collection and the namespace of the producer"},
		{nil, "store: find never matches a namespace rule for a consumer of another namespace"},
		{nil, "store: find matches a consumer of another namespace by its reference"},
		{nil, "store: find after a move of the consumer to another collection"},
		{nil, "store: find returns ErrNoRule when no rule matches"},
		{nil, "store: find of a producer with no stable ID runs the same query and returns ErrNoRule"},
		{nil, "store: find never matches a stored row that fails Validate"},
		{nil, "store: find refuses an invalid placement"},
	}

	return slices.Concat(rules, settingCases(), []suiteCase{
		{nil, "store: set setting changes no rule of a unit that exists"},
		{nil, "store: each write appends one audit entry in its transaction"},
	})
}

// newUnitCases are the behaviors of the rule that a tool store writes when it
// makes a unit.
func newUnitCases() []suiteCase {
	return []suiteCase{
		{nil, "new unit: nobody gives no rule"},
		{nil, "new unit: namespace gives one rule that names the namespace of the unit"},
		{nil, "new unit: the rule has its audit entry in the namespace of the unit"},
		{nil, "new unit: a fault before the commit leaves no unit, no rule and no audit entry"},
		{nil, "new unit: a setting that fails Validate refuses the create and writes nothing"},
	}
}

// checkerCases are the behaviors of Checker.
func checkerCases() []suiteCase {
	return []suiteCase{
		{nil, "checker: a principal with unit:read-outputs reads the plain outputs"},
		{nil, "checker: a principal without unit:read-outputs is denied by the grant check"},
		{nil, "checker: a principal with unit:read but no unit:read-outputs is denied"},
		{nil, "checker: a secret output needs unit:read-secrets"},
		{nil, "checker: a rule gives a principal nothing"},
		{nil, "checker: a job with a rule and a starter with the grant reads"},
		{nil, "checker: a job with no rule is denied by the rule check"},
		{nil, "checker: a job with a rule and a starter without the grant is denied by the grant check"},
		{nil, "checker: the job reads the intersection of the rule and the grants of the starter"},
		{nil, "checker: a grant or a rule that changes after the start applies at the next read"},
		{nil, "checker: a starter that is deleted or disabled denies"},
		{nil, "checker: a job of a trigger uses the grants of the last saver of the trigger"},
		{nil, "checker: a job that ended denies"},
		{nil, "checker: a job of another namespace reads only with a rule that names its unit or collection"},
		{nil, "checker: a job of another namespace never matches a namespace rule"},
		{nil, "checker: a job of the producer namespace with no rule answers identity.ErrDenied"},
		{nil, "checker: a principal with unit:read-outputs and unit:read-secrets gets no right to decrypt"},
		{nil, "checker: a stored rule that fails Validate denies"},
		{nil, "checker: an error of a port denies"},
		{nil, "checker: an error of a port whose text wraps identity.ErrDenied unwraps to one sentinel only"},
		{nil, "checker: each denial writes an audit entry with the check, the producer and the unit of the job"},
		{nil, "checker: of several failed checks the denial names the first in the order of the checks"},
		{nil, "checker: an invalid producer placement is refused"},
		{nil, "checker: a producer reference that is not a unit is refused with ErrInvalid"},
		{nil, "checker: an unknown producer for a principal answers ErrUnknownProducer and not identity.ErrDenied"},
		{nil, "checker: an unknown producer for a job of the producer namespace answers ErrUnknownProducer"},
	}
}

// hiddenCases are the behaviors of the checker for a read by a job of another
// namespace: a denial and an unknown producer answer the same, and take the
// same work.
func hiddenCases() []suiteCase {
	return []suiteCase{
		{nil, "hidden: a job of another namespace with no rule answers ErrUnknownProducer, not identity.ErrDenied"},
		{nil, "hidden: a job of another namespace whose starter has no grant answers ErrUnknownProducer"},
		{nil, "hidden: a grant check whose cause wraps identity.ErrDenied unwraps to ErrUnknownProducer only"},
		{nil, "hidden: a secret output without unit:read-secrets answers ErrUnknownProducer only"},
		{nil, "hidden: a job whose unit cannot be read gives a hidden denial"},
		{nil, "hidden: a hidden denial writes its audit entry in the producer namespace only"},
		{nil, "hidden: a hidden denial writes no audit entry in the namespace of the job"},
		{nil, "hidden: an unknown producer answers ErrUnknownProducer only, as a hidden denial"},
		{
			nil,
			"hidden: an unknown producer and a hidden denial each write one audit entry of the same kind " +
				"in the namespace of the route",
		},
		{nil, "hidden: an unknown producer and a hidden denial make the same port calls in the same order"},
		{nil, "hidden: a denial by the rule check and by the grant check make the same port calls"},
		{
			nil,
			"hidden: an unknown producer in a namespace that does not exist writes no audit entry and answers ErrUnknownProducer",
		},
		{nil, "hidden: the audit entry names the unit of the job by its reference and never by a stable ID"},
	}
}

// latestCases are the behaviors of the checker for the latest state of the
// producer: the state ID of the request and a state that the client
// encrypted.
func latestCases() []suiteCase {
	return []suiteCase{
		{nil, "latest: a request with the ID of the latest state reads"},
		{nil, "latest: a request with no state ID is denied by the latest-state check"},
		{nil, "latest: a request with the ID of an older state is denied by the latest-state check"},
		{nil, "latest: a request with the ID of a state of another unit is denied by the latest-state check"},
		{nil, "latest: a new state between two reads makes the old ID stale"},
		{nil, "latest: an error of the states port denies"},
		{
			nil,
			"client-encrypted: the state refuses a job of the producer namespace with a rule and every grant with ErrClientEncrypted",
		},
		{
			nil,
			"client-encrypted: the state refuses a job of another namespace with a rule and every grant with ErrUnknownProducer only",
		},
		{nil, "client-encrypted: the state refuses an open of the profile block by a job"},
		{
			nil,
			"client-encrypted: each refusal writes an audit entry in the producer namespace with the client-encrypted check",
		},
		{
			nil,
			"client-encrypted: a refusal of a job of another namespace writes no audit entry in the namespace of the job",
		},
		{
			nil,
			"client-encrypted: a principal without the grant is denied by the grant check before the client-encrypted check",
		},
		{
			nil,
			"client-encrypted: a principal with unit:read and unit:read-outputs reads a client-encrypted state as before",
		},
		{
			nil,
			"client-encrypted: a principal with unit:read-outputs and not unit:read is refused with ErrClientEncrypted",
		},
		{nil, "client-encrypted: a plain state after a client-encrypted state reads again"},
	}
}

// attachmentCases are the behaviors of the checker for an open of the profile
// block outputs/opendustpan. The reference names the attachment that the spec
// of the job resolved, and the checker reads it for the job of the job token.
func attachmentCases() []suiteCase {
	return []suiteCase{
		{nil, "attachment: a job open with a rule and an authority with the grant reads"},
		{nil, "attachment: a job open with a rule and an authority without the grant is denied by the grant check"},
		{nil, "attachment: a job open uses the grants of the authority, not of the starter"},
		{nil, "attachment: a job open with no rule is denied by the rule check"},
		{nil, "attachment: a secret output needs unit:read-secrets of the authority"},
		{nil, "attachment: an authority that is deleted or disabled denies"},
		{nil, "attachment: a job open of a producer of another namespace is denied and hidden"},
		{nil, "attachment: an unknown attachment, profile, revision or block is denied by the attachment check"},
		{nil, "attachment: an attachment of another unit is denied by the attachment check"},
		{nil, "attachment: a collection attachment that covers the unit of the job reads"},
		{nil, "attachment: a collection attachment of another collection is denied by the attachment check"},
		{
			nil,
			"attachment: a collection attachment after the unit of the job moved out of the collection is denied",
		},
		{nil, "attachment: a namespace attachment that covers the unit of the job reads"},
		{nil, "attachment: a namespace attachment whose object the port answers in another namespace is denied"},
		{nil, "attachment: a reference with the ID of an attachment that the spec of the job holds reads"},
		{
			nil,
			"attachment: a reference with the ID of an attachment that the spec of the job does not hold is denied",
		},
		{nil, "attachment: an attachment ID that the port answers other than the reference is denied"},
		{
			nil,
			"attachment: two attachments of one profile cover the unit and the authority of the attachment " +
				"that the job resolved decides when it holds the grant",
		},
		{
			nil,
			"attachment: two attachments of one profile cover the unit and the attachment that the job resolved " +
				"denies when its authority has no grant, also when the other authority has it",
		},
		{
			nil,
			"attachment: two attachments of one profile cover the unit and a reference to the attachment whose " +
				"block the merge does not keep is denied, also when its authority has the grant",
		},
		{
			nil,
			"attachment: a block of a profile that the attached profile imports reads with the authority of the attachment",
		},
		{
			nil,
			"attachment: a block of an imported profile that the job did not reach through the attachment of the " +
				"reference is denied",
		},
		{nil, "attachment: the revision that the spec of the job fixed reads after the tag moves to a new revision"},
		{
			nil,
			"attachment: an older revision of an attached profile that the spec of the job does not hold is denied",
		},
		{nil, "attachment: the reference of an attachment of the spec of another job of the unit is denied"},
		{nil, "attachment: an attachment whose block names another producer is denied by the attachment check"},
		{
			nil,
			"attachment: an escalation with the attachment of an administrator on another unit is denied and audited",
		},
		{
			nil,
			"attachment: an escalation with the reference of a block that names the producer from a profile of another unit is denied",
		},
		{nil, "attachment: the checker denies a mismatch that the attachments port answers"},
		{nil, "attachment: an error of the attachments port denies"},
		{nil, "attachment: a reference that fails Validate is refused with ErrInvalid"},
		{nil, "attachment: a read by a principal with an attachment reference is refused with ErrInvalid"},
		{nil, "attachment: a direct open is a read by the principal with the grants of the caller"},
		{nil, "attachment: the decision names the kind attachment and the rule"},
	}
}

// attachmentsCases are the behaviors of Attachments.
func attachmentsCases() []suiteCase {
	return []suiteCase{
		{
			nil,
			"attachments: an attachment of the spec of the job answers its ID, its object, its producer and its authority",
		},
		{nil, "attachments: the object of a unit, a collection and a namespace attachment"},
		{nil, "attachments: the producer has the stable ID that the save resolved"},
		{nil, "attachments: a block of an imported profile answers the attachment through which the job reached it"},
		{nil, "attachments: two attachments of one profile answer the attachment whose block the merge keeps"},
		{nil, "attachments: an attachment whose block the merge does not keep answers ErrUnknownAttachment"},
		{nil, "attachments: an unknown attachment, profile, revision or block answers ErrUnknownAttachment"},
		{nil, "attachments: a block of another provider answers ErrUnknownAttachment"},
		{nil, "attachments: an attachment that the spec of the job does not hold answers ErrUnknownAttachment"},
		{nil, "attachments: a revision that the spec of the job does not fix answers ErrUnknownAttachment"},
		{
			nil,
			"attachments: an imported profile that the job did not reach through the attachment answers ErrUnknownAttachment",
		},
		{nil, "attachments: an attachment made after the start of the job answers ErrUnknownAttachment"},
		{nil, "attachments: an unknown job and a job of another namespace answer ErrUnknownAttachment"},
		{nil, "attachments: a profile of another namespace answers ErrUnknownAttachment"},
		{nil, "attachments: a reference that fails Validate and an empty job answer ErrInvalid"},
	}
}

// statesCases are the behaviors of States.
func statesCases() []suiteCase {
	return []suiteCase{
		{nil, "states: latest answers the ID of the latest state of a unit of each tool"},
		{nil, "states: latest after a new state answers the new ID"},
		{nil, "states: latest of a unit with no state answers an empty ID"},
		{nil, "states: latest of a state that the client encrypted is client-encrypted"},
		{nil, "states: latest of an unknown unit answers ErrUnknownProducer"},
		{nil, "states: latest of a reference with no stable ID answers ErrUnknownProducer after the same lookup"},
		{nil, "states: latest of a stable ID that names another unit answers ErrUnknownProducer"},
		{nil, "states: latest of an object that is not a unit answers ErrInvalid"},
	}
}
