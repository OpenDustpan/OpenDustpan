package backup_test

import (
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/job"
)

// devDecision says what the strip does with a column or a field that names a
// principal, and why.
type devDecision struct {
	strip  bool
	reason string
}

// principalColumn matches the name of a column that can name a principal, by
// ID, by subject or as text. The coverage test fails for a column that
// matches and has no decision, so a new column forces its author to decide.
var principalColumn = regexp.MustCompile(
	`(?i)(principal|subject|owner|author|actor|user|holder|approver|_by$|^by_|granted|requested|member|identity|email|display_name)`,
)

// devColumnDecisions lists every column that names a principal, with the
// decision of the strip. The strip reads the first group in the staged data
// and removes the rows. The keep decisions say why the text stays: it is
// history or a label, and no code grants authority from it, because the
// sign-in refusal stops a dev person from coming back and the approval code
// counts no signature of a dev person on a production installation.
//
//nolint:gochecknoglobals // A frozen decision table shared by the coverage tests.
var devColumnDecisions = map[string]devDecision{
	"principals.subject": {strip: true, reason: "the dev principal itself"},
	"grants.subject": {
		strip:  true,
		reason: "text form of the subject of a grant of a removed principal",
	},
	"grants.subject_id":                {strip: true, reason: "ID of the principal that holds the grant"},
	"group_members.principal_id":       {strip: true, reason: "membership of a removed principal"},
	"member_removals.members_before":   {strip: false, reason: "a count of members, not a principal"},
	"sessions.principal_id":            {strip: true, reason: "session of a removed principal"},
	"person_preferences.principal_id":  {strip: true, reason: "preferences of a removed principal"},
	"access_tokens.principal_id":       {strip: true, reason: "token of a removed principal"},
	"temp_admins.principal_id":         {strip: true, reason: "temporary administrator that is a removed principal"},
	"temp_admins.subject":              {strip: true, reason: "temporary administrator with the dev prefix"},
	"forge_account_links.principal_id": {strip: true, reason: "Git host account link of a removed principal"},
	"forge_link_requests.principal_id": {strip: true, reason: "link request of a removed principal"},
	"trust_rules.principal_id":         {strip: true, reason: "trust rule that acts as a removed principal"},
	"local_users.principal_id":         {strip: true, reason: "user name and password of a removed principal"},
	"local_users.user_name":            {strip: false, reason: "the strip removes the row by its principal ID"},
	"local_users.created_by":           {strip: false, reason: "history text of who made the local user"},
	"principal_session_generations.principal_id": {
		strip: true, reason: "session generation of a removed principal",
	},
	"passkeys.principal_id":        {strip: true, reason: "passkey of a removed principal"},
	"trust_rules.created_by":       {strip: false, reason: "history text of who made the trust rule"},
	"group_mappings.created_by":    {strip: false, reason: "history text of who made the group mapping"},
	"grants.subject_kind":          {strip: false, reason: "a kind word, not a subject"},
	"grants.created_by":            {strip: false, reason: "history text of who made the grant"},
	"grants.revoked_by":            {strip: false, reason: "history text of who revoked the grant"},
	"access_tokens.created_by":     {strip: false, reason: "history text of who made the token"},
	"access_tokens.replaced_by":    {strip: false, reason: "ID of a token, not of a person"},
	"temp_admins.created_by":       {strip: false, reason: "history text of the host operator"},
	"audit_entries.subject":        {strip: false, reason: "the audit chain stays whole"},
	"audit_entries.idp_subject":    {strip: false, reason: "the audit chain stays whole"},
	"audit_entries.principal_kind": {strip: false, reason: "the audit chain stays whole"},
	"audit_entries.user_agent":     {strip: false, reason: "the audit chain stays whole"},
	"audit_sink_leases.holder":     {strip: false, reason: "name of a process, not of a person"},
	"gate_references.owner_id":     {strip: false, reason: "ID of a gate object, not of a person"},
	"gate_references.owner_kind":   {strip: false, reason: "kind of a gate object, not of a person"},
	"imports.created_by":           {strip: false, reason: "history text of who started the import"},
	"inventory.display_name":       {strip: false, reason: "label of a unit"},
	"inventory.lock_holder": {
		strip:  false,
		reason: "history text of a lock; an unlock needs its own authorization",
	},
	"inventory_changes.display_name": {
		strip: false, reason: "label of a unit",
	},
	"manual_locks.holder": {
		strip:  false,
		reason: "history text of a lock; an unlock needs its own authorization",
	},
	"object_context.updated_by": {strip: false, reason: "history text of who changed the context"},
	"object_context_owners.owner": {
		strip:  false,
		reason: "context text; the approval code counts no dev signature",
	},
	"principals.display_name": {
		strip:  false,
		reason: "label of a principal that the strip removes with its row",
	},
	"principal_groups.closed_to_identity_provider": {
		strip:  false,
		reason: "a flag of the group; it names no principal",
	},
	"profile_revisions.author":   {strip: false, reason: "history text of who wrote the revision"},
	"profiles.created_by":        {strip: false, reason: "history text of who made the profile"},
	"issuer_settings.updated_by": {strip: false, reason: "history text of who changed the settings"},
	"issuer_settings.subject_template": {
		strip: false, reason: "a list of subject key names, such as namespace and unit; it names no principal",
	},
	"issuer_settings.allow_block_subjects": {strip: false, reason: "a flag; it names no principal"},
	"issuer_settings.include_actor_name":   {strip: false, reason: "a flag; it names no principal"},
	"saved_views.owner": {
		strip:  false,
		reason: "ID of a principal, not a subject: a view of a missing principal matches no reader",
	},
	"triggers.last_saver_subject": {
		strip: false,
		reason: "subject of the last saver; a start checks the current grants of that principal and " +
			"refuses when the principal is gone",
	},
	"registry_keys.added_by": {
		strip:  false,
		reason: "ID of a principal, not a subject: history text of who registered the key",
	},
	"self_service_blueprint_versions.published_by": {
		strip:  false,
		reason: "history text of who published the version; a published version never changes",
	},
	"registry_releases.published_by": {
		strip:  false,
		reason: "ID of a principal, not a subject: history text of who published the release",
	},
	"job_freezes.created_by":        {strip: false, reason: "history text of who made the freeze"},
	"notification_sinks.created_by": {strip: false, reason: "history text of who made the sink"},
	"placement_rules.created_by":    {strip: false, reason: "history text of who made the placement rule"},
	"notifications.subject":         {strip: false, reason: "history text of who caused the notification"},
	"notification_subscriptions.created_by": {
		strip: false,
		reason: "subject of the last writer; a delivery checks the current grants of that principal " +
			"and skips when the principal is gone",
	},
	"notification_deliveries.principal": {
		strip: false,
		reason: "ID of the principal whose access decides the delivery; the check before each attempt " +
			"skips the delivery when the principal is gone",
	},
	"notification_deliveries.owner": {
		strip:  false,
		reason: "name of an instance that holds the claim, not of a person",
	},
	"job_freezes.ended_by":                {strip: false, reason: "history text of who ended the freeze"},
	"runner_join_tokens.created_by":       {strip: false, reason: "history text of who made the join token"},
	"runner_join_tokens.used_by":          {strip: false, reason: "ID of a runner, not of a person"},
	"sessions.user_agent":                 {strip: false, reason: "text of a client"},
	"sign_in_providers.authorization_url": {strip: false, reason: "address of a provider"},
	"sign_in_providers.display_name":      {strip: false, reason: "label of a provider"},
	"sign_in_providers.email_claim":       {strip: false, reason: "name of a claim of a provider"},
	"sign_in_providers.subject_claim":     {strip: false, reason: "name of a claim of a provider"},
	"sign_in_providers.userinfo_url":      {strip: false, reason: "address of a provider"},
	"sign_in_providers.username_claim":    {strip: false, reason: "name of a claim of a provider"},
	"state_versions.fenced_by":            {strip: false, reason: "history text of who fenced the version"},
	"state_versions.holder": {
		strip:  false,
		reason: "history text of a lock; an unlock needs its own authorization",
	},
	"updates.created_by":                     {strip: false, reason: "history text of who started the update"},
	"workspace_resource_changes.by_job_id":   {strip: false, reason: "ID of a job, not of a person"},
	"workspace_resource_changes.by_subject":  {strip: false, reason: "history text of who made the change"},
	"workspace_resource_changes.by_token_id": {strip: false, reason: "ID of a token, not of a person"},
	"workspaces.lock_holder": {
		strip:  false,
		reason: "history text of a lock; an unlock needs its own authorization",
	},
	"workspaces.last_release_holder": {
		strip:  false,
		reason: "history text of a released lock; a repeated unlock needs its own authorization",
	},
	"workspaces.last_release_by": {
		strip:  false,
		reason: "history text of a released lock; a repeated unlock needs its own authorization",
	},
	"inventory.last_release_holder": {
		strip:  false,
		reason: "copy of the history text of a released lock; a repeated unlock needs its own authorization",
	},
	"inventory.last_release_by": {
		strip:  false,
		reason: "copy of the history text of a released lock; a repeated unlock needs its own authorization",
	},
	"break_glass_reviews.use_subject": {
		strip: false,
		reason: "history text of who broke the glass; a review records a use that already happened and " +
			"grants no authority",
	},
}

// TestDevStripCoversEveryPrincipalColumn lists the columns of the backup that
// can name a principal and fails for a column with no decision, and for a
// decision with no column. A new column forces its author to decide if the
// strip removes it or keeps it, and why.
func TestDevStripCoversEveryPrincipalColumn(t *testing.T) {
	t.Parallel()

	found := map[string]bool{}

	for _, table := range backup.PortableTables() {
		for _, column := range table.Columns {
			if !principalColumn.MatchString(column.Name) {
				continue
			}

			key := table.Table + "." + column.Name
			found[key] = true

			decision, listed := devColumnDecisions[key]
			if !listed {
				t.Errorf("column %s can name a principal and has no decision: add it to devColumnDecisions "+
					"and, when the strip must remove it, to pkg/store/internal/devstrip", key)

				continue
			}

			if strings.TrimSpace(decision.reason) == "" {
				t.Errorf("column %s has a decision with no reason", key)
			}
		}
	}

	for key := range devColumnDecisions {
		if !found[key] {
			t.Errorf("devColumnDecisions lists %s, which is no column of the backup", key)
		}
	}
}

// devFieldDecisions lists the fields of the objects of the policy model that
// name a principal or hold the Cedar text that can name one. The strip edits
// Approvers.Users in the kinds approval-rule, approval-default and
// ownership-rule. It does not edit Cedar text: after the strip no principal
// matches a dev subject, and the sign-in refusal stops a dev person from
// coming back, so the text grants no authority to a dev person.
//
//nolint:gochecknoglobals // A frozen decision table shared by the coverage tests.
var devFieldDecisions = map[string]devDecision{
	"ApprovalRule.Approvers": {
		strip:  true,
		reason: "Users loses the dev subjects; Groups and UnitOwners are not subjects",
	},
	"ApprovalDefault.Approvers": {
		strip:  true,
		reason: "Users loses the dev subjects; Groups and UnitOwners are not subjects",
	},
	"OwnershipRule.Approvers": {strip: true, reason: "Users loses the dev subjects; Groups are not subjects"},
	"ApprovalDefault.Unless": {
		strip:  false,
		reason: "Cedar text; no principal matches a dev subject after the strip",
	},
	"OwnershipRule.When": {
		strip:  false,
		reason: "Cedar text; no principal matches a dev subject after the strip",
	},
	"Gate.Condition": {
		strip:  false,
		reason: "Cedar text; no principal matches a dev subject after the strip",
	},
	"Gate.Unless": {
		strip:  false,
		reason: "Cedar text; no principal matches a dev subject after the strip",
	},
	"AccessLimit.Policy": {
		strip:  false,
		reason: "Cedar text; no principal matches a dev subject after the strip",
	},
	"Requirement.Approvers": {strip: false, reason: "decision record: history, and it grants no authority"},
	"Requirement.Signatures": {
		strip:  false,
		reason: "decision record: history; the approval code counts no dev signature",
	},
}

// TestDevStripCoversEveryApproverField fails for a field of the objects of the
// policy model that names approvers or holds Cedar text and has no decision.
func TestDevStripCoversEveryApproverField(t *testing.T) {
	t.Parallel()

	checkFieldDecisions(t)
	checkApproverSubjectFields(t)
	checkApproverFieldNames(t)
}

// checkFieldDecisions fails for a field with no decision and for a decision
// with no field.
func checkFieldDecisions(t *testing.T) {
	t.Helper()

	types := []reflect.Type{
		reflect.TypeFor[gate.ApprovalRule](), reflect.TypeFor[gate.ApprovalDefault](),
		reflect.TypeFor[gate.OwnershipRule](), reflect.TypeFor[gate.Gate](),
		reflect.TypeFor[gate.AccessLimit](), reflect.TypeFor[gate.Requirement](),
	}

	approvers := reflect.TypeFor[gate.Approvers]()
	cedar := []string{"Policy", "Condition", "Unless", "When", "Signatures"}
	found := map[string]bool{}

	for _, typ := range types {
		for field := range typ.Fields() {
			if field.Type != approvers && !slices.Contains(cedar, field.Name) {
				continue
			}

			key := typ.Name() + "." + field.Name
			found[key] = true

			if _, listed := devFieldDecisions[key]; !listed {
				t.Errorf("field %s names approvers or holds Cedar text and has no decision", key)
			}
		}
	}

	for key := range devFieldDecisions {
		if !found[key] {
			t.Errorf("devFieldDecisions lists %s, which is no such field", key)
		}
	}
}

// checkApproverSubjectFields proves that Approvers.Users is the only field of
// Approvers that holds subjects.
func checkApproverSubjectFields(t *testing.T) {
	t.Helper()

	subjects := []string{}

	for field := range reflect.TypeFor[gate.Approvers]().Fields() {
		if field.Type == reflect.TypeFor[[]string]() && field.Name == "Users" {
			subjects = append(subjects, field.Name)
		}
	}

	if !slices.Equal(subjects, []string{"Users"}) {
		t.Errorf("Approvers fields of subjects = %v, want [Users]; decide the new field in the strip", subjects)
	}
}

// checkApproverFieldNames fails for a field of Approvers that the strip does
// not know.
func checkApproverFieldNames(t *testing.T) {
	t.Helper()

	for field := range reflect.TypeFor[gate.Approvers]().Fields() {
		switch name := field.Name; name {
		case "Groups", "GroupIDs", "Users", "UnitOwners":
		default:
			t.Errorf("Approvers has the new field %s: decide it in the strip and in this test", name)
		}
	}
}

// jobSubjectField matches the name of a field of a job record that can name a
// principal or hold a decision of one. The coverage test fails for a field that
// matches and has no decision.
var jobSubjectField = regexp.MustCompile(
	`(?i)(subject|by$|saver|starter|approv|author|owner|holder|user|actor|principal|agent|auth|requester|creator|signer)`,
)

// devJobFieldDecisions lists the fields of a job record that can name a
// principal. The table jobs keeps them in the JSON column body, so the
// column check does not see them. The strip does not edit the body: the
// subjects of the starter and the approvers stay as history text, like
// job_freezes.created_by. Gate.Status counts an approval only when
// ApproverPolicy.CountsAsSignature allows its subject, so a dev subject fills
// no slot of a gate on a production installation, also when the body holds it.
//
//nolint:gochecknoglobals // A frozen decision table shared by the coverage tests.
var devJobFieldDecisions = map[string]devDecision{
	"Job.StartedBy": {
		strip:  false,
		reason: "history text of who started the job; a confirmation by it counts only when CountsAsSignature allows it",
	},
	"Job.Agent":         {strip: false, reason: "name of an agent, not of a person"},
	"Job.AgentMode":     {strip: false, reason: "a mode word, not a subject"},
	"LogRef.ReplacedBy": {strip: false, reason: "ID of a log version, not of a person"},
	"Gate.Approvals":    {strip: false, reason: "the number of approvals that the gate needs, not a subject"},
	"Phase.Approvals": {
		strip:  false,
		reason: "history of the verdicts; Gate.Status counts no signature that CountsAsSignature refuses",
	},
	"Approval.Subject": {
		strip:  false,
		reason: "history text of who gave the verdict; CountsAsSignature refuses a dev subject on production",
	},
	"Approval.Agent":  {strip: false, reason: "name of an agent, not of a person; an agent verdict never counts"},
	"Approval.Auth":   {strip: false, reason: "kind of fresh authentication, not a subject"},
	"Approval.AuthAt": {strip: false, reason: "time of a fresh authentication, not a subject"},
	"TriggerRef.Saver": {
		strip:  false,
		reason: "subject of the last saver of the trigger at the start; history text, as triggers.last_saver_subject",
	},
}

// TestDevStripCoversEveryJobSubjectField walks the types of a job record and
// fails for a field that can name a principal and has no decision, and for a
// decision with no field. A new subject field in the body of a job forces its
// author to decide what the strip does with it.
func TestDevStripCoversEveryJobSubjectField(t *testing.T) {
	t.Parallel()

	found := map[string]bool{}

	for _, typ := range jobRecordStructs(reflect.TypeFor[job.Job](), map[reflect.Type]bool{}) {
		for field := range typ.Fields() {
			if !jobSubjectField.MatchString(field.Name) {
				continue
			}

			key := typ.Name() + "." + field.Name
			found[key] = true

			decision, listed := devJobFieldDecisions[key]
			if !listed {
				t.Errorf("field %s of a job record can name a principal and has no decision: add it to "+
					"devJobFieldDecisions", key)
			} else if strings.TrimSpace(decision.reason) == "" {
				t.Errorf("field %s has a decision with no reason", key)
			}
		}
	}

	for key := range devJobFieldDecisions {
		if !found[key] {
			t.Errorf("devJobFieldDecisions lists %s, which is no such field of a job record", key)
		}
	}
}

// jobRecordStructs returns the struct types of package job that a record of
// typ reaches through fields, slices, arrays, maps and pointers.
func jobRecordStructs(typ reflect.Type, seen map[reflect.Type]bool) []reflect.Type {
	for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array ||
		typ.Kind() == reflect.Map {
		typ = typ.Elem()
	}

	if typ.Kind() != reflect.Struct || seen[typ] || typ.PkgPath() != reflect.TypeFor[job.Job]().PkgPath() {
		return nil
	}

	seen[typ] = true
	out := []reflect.Type{typ}

	for field := range typ.Fields() {
		out = append(out, jobRecordStructs(field.Type, seen)...)
	}

	return out
}
