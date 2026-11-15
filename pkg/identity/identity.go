// Package identity defines who is calling (authentication) and what they may
// do (authorization) as two separate ports.
//
// OpenDustpan always issues its own access tokens; single sign-on providers
// (OIDC, SAML, CAS, LDAP, local users) only authenticate humans so that a token
// can be minted. Requests from tools are therefore always token-based.
package identity

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Sentinel errors returned by Authenticator and Authorizer implementations.
var (
	// ErrUnauthenticated reports a missing, malformed or unknown credential.
	ErrUnauthenticated = errors.New("unauthenticated")
	// ErrRateLimited reports a request that the server refuses before any
	// credential check, because its source failed too many sign-ins. The
	// client can try again later. Adapters answer 429.
	ErrRateLimited = errors.New("too many failed sign-ins from this address")
	// ErrDenied reports an authenticated principal without permission.
	ErrDenied = errors.New("permission denied")
	// ErrWeakToken reports a configured token that is too short to be safe.
	ErrWeakToken = errors.New("token too short")
	// ErrInvalidObject reports a malformed namespace or unit reference.
	ErrInvalidObject = errors.New("invalid object reference")
)

// minTokenLength is the shortest static token accepted, in bytes.
const minTokenLength = 32

// Principal is an authenticated caller.
type Principal struct {
	// Subject uniquely identifies the caller, for example a user name.
	Subject string
	// DisplayName is a human-readable name.
	DisplayName string
	// Admin grants every action on every object.
	Admin bool
	// TokenID names the stored access token that authenticated the caller
	// (ADR 53). It is empty for a bootstrap admin token (ADR 6), which has
	// no stored record.
	TokenID string
	// TokenExpiresAt is the expiry of the stored access token that
	// authenticated the caller, so a response can warn before it (ADR 53,
	// decision 11). It is the zero time for a bootstrap admin token.
	TokenExpiresAt time.Time
	// TemporaryAdministrator reports a temporary administrator: a person
	// the host command made for recovery, with a fixed expiry. The API
	// answers it, so the console shows a banner for the principal. It is
	// false for every other caller, including the bootstrap administrators.
	TemporaryAdministrator bool
	// TemporaryExpiresAt is the fixed end of a temporary administrator.
	// It is the zero time for every other caller.
	TemporaryExpiresAt time.Time
	// Kind is the kind of the principal record that acted: person, service
	// or workload. The empty value is an unknown kind, for example for a
	// bootstrap admin token, which has no record. An authenticator outside
	// this repository that does not fill it gets the stricter treatment of
	// a person.
	Kind Kind
	// Credential is the kind of the credential of the request. The empty
	// value counts as CredentialAccessToken.
	Credential Credential
	// JobID is the job whose token authenticated the request. It is empty
	// for every other credential.
	JobID string
}

// Kind is the kind of a principal record. Its values equal the kinds of a
// principal record of the principal package.
type Kind string

// The kinds of a principal. A new kind goes into Kinds too, so that the
// audit log and the record of a refusal know it.
const (
	KindPerson   Kind = "person"
	KindService  Kind = "service"
	KindWorkload Kind = "workload"
)

// Kinds returns every kind of a principal, in a new slice.
func Kinds() []Kind {
	return []Kind{KindPerson, KindService, KindWorkload}
}

// Known reports whether k is one of Kinds. The comparison is exact, so a
// kind in another case or with a space is not known.
func (k Kind) Known() bool {
	return slices.Contains(Kinds(), k)
}

// Credential names the kind of credential that authenticated a request.
type Credential string

// The credentials of a request.
const (
	// CredentialSession is a console session. A session belongs to a person.
	CredentialSession Credential = "session"
	// CredentialAccessToken is a personal or service access token.
	CredentialAccessToken Credential = "access-token"
	// CredentialJobToken is the token of a job.
	CredentialJobToken Credential = "job-token"
	// CredentialTrustToken is a token from a trust rule of a workload.
	CredentialTrustToken Credential = "trust-token"
)

// JobTokenID returns the job whose token authenticated the request, or the
// empty string for every other credential. A manual lock lets a request pass
// only on this answer, so a principal that carries a job ID with another
// credential gets no pass.
func (p Principal) JobTokenID() string {
	if p.Credential != CredentialJobToken {
		return ""
	}

	return p.JobID
}

// ActsAsPerson reports whether a person acts through the request: the
// credential is a session or an access token, and the kind is person or
// unknown. An empty credential counts as an access token, and an unknown
// kind counts as a person, so the answer fails closed.
func (p Principal) ActsAsPerson() bool {
	if p.Credential == CredentialJobToken || p.Credential == CredentialTrustToken {
		return false
	}

	return p.Kind == "" || p.Kind == KindPerson
}

// StateOrigin names the writer of a state version: the job whose token made
// the write, and the commit that the tool reported with it. Both are empty
// for a write that no job made, for example from a laptop or from a restore.
type StateOrigin struct {
	// Job is the job whose token made the write. It is empty for every
	// other credential.
	Job string
	// Commit is the commit that the tool reported with the write. It is
	// never an authenticated claim, and no decision may trust it. It is
	// empty when the tool reported none.
	Commit string
}

// Action is something a principal wants to do.
type Action string

// Actions known to the core. Adapters may define their own.
const (
	ActionNamespaceRead  Action = "namespace:read"
	ActionNamespaceAdmin Action = "namespace:admin"
	// ActionNamespaceReadAudit allows reading the audit entries of a
	// namespace. Its object is a namespace.
	ActionNamespaceReadAudit Action = "namespace:read-audit"
	ActionUnitRead           Action = "unit:read"
	ActionUnitWrite          Action = "unit:write"
	ActionUnitManage         Action = "unit:manage"
	ActionUnitEncrypt        Action = "unit:encrypt"
	ActionUnitDecrypt        Action = "unit:decrypt"
	// ActionUnitWriteForced allows a forced state write (state push -force)
	// and an unfenced force-unlock of a client-encrypted state. Its object
	// is a unit. The unit and namespace override switches must allow it too
	// (ADR 44, decisions 4 and 6).
	ActionUnitWriteForced Action = "unit:write-forced"
	// ActionUnitWriteUnlocked allows a write to an unlocked unit, as with
	// apply -lock=false. Its object is a unit. The override switches must
	// allow it too (ADR 44, decisions 5 and 6).
	ActionUnitWriteUnlocked Action = "unit:write-unlocked"
	// ActionNamespaceSetOverrides allows changing the override switches of a
	// namespace. Its object is a namespace.
	ActionNamespaceSetOverrides Action = "namespace:set-overrides"
	// ActionNamespaceManageIPAllowlist allows reading, setting and removing
	// the IP allowlist of a namespace. Its object is a namespace. One action
	// covers the read: the list shows the network layout of the operator, so
	// only a manager may read it.
	ActionNamespaceManageIPAllowlist Action = "namespace:manage-ip-allowlist"
	// ActionNamespaceReadGates allows reading the approval defaults and the
	// approval rules of a namespace, its collections and its units. Its
	// object is a namespace.
	ActionNamespaceReadGates Action = "namespace:read-gates"
	// ActionNamespaceManageGates allows setting and removing the approval
	// defaults and the approval rules of a namespace, its collections and its
	// units. Its object is a namespace. Of the namespace roles, only
	// the administrator holds it. The account administrator and the site
	// administrator hold it as data actions.
	ActionNamespaceManageGates Action = "namespace:manage-gates"
	// ActionUnitSetOverrides allows changing the override switches of a unit.
	// Its object is a unit.
	ActionUnitSetOverrides Action = "unit:set-overrides"
	// ActionUnitReadSecrets allows reading secret values in plain text. Its
	// object is a unit.
	ActionUnitReadSecrets Action = "unit:read-secrets"
	// ActionUnitReadOutputs allows reading the root outputs of the latest
	// state of a unit, and nothing else. Its object is a unit, a collection
	// or a namespace. A secret output also needs ActionUnitReadSecrets.
	// ActionUnitRead does not imply it: a check of a read of outputs reads
	// grants of this action only. Each built-in role that holds
	// ActionUnitRead also holds it.
	ActionUnitReadOutputs Action = "unit:read-outputs"
	// ActionUnitManageSharing allows saving and removing the sharing rules of
	// a unit, a collection or a namespace, and changing the output sharing
	// setting of a namespace. Its object is a unit, a collection or a
	// namespace. Of the namespace roles, only the administrator holds it. The
	// account administrator and the site administrator hold it as data
	// actions.
	ActionUnitManageSharing Action = "unit:manage-sharing"
	// ActionCallerRead allows reading the caller's own record, for example
	// the current-user endpoint of a tool API. Its object is CallerObject.
	ActionCallerRead Action = "caller:read"
	// ActionServerBackup allows starting and reading installation backups.
	// Its object is ServerObject.
	ActionServerBackup Action = "server:backup"
	// ActionServerObserve allows reading installation metrics. Its object
	// is ServerObject.
	ActionServerObserve Action = "server:observe"
	// ActionServerReadInstances allows listing the version rows of the server
	// processes of the installation. Its object is ServerObject.
	ActionServerReadInstances Action = "server:read-instances"
	// ActionServerDeleteInstance allows deleting the version row of a server
	// process, for example of a process that died without a clean stop. Its
	// object is ServerObject. The action does not stop a process.
	ActionServerDeleteInstance Action = "server:delete-instance"
	// ActionServerReadAudit allows reading the audit entries of the
	// installation, the scope @server (ADR 60, decision 7). Its object is
	// ServerObject.
	ActionServerReadAudit Action = "server:read-audit"
	// ActionServerMaintenance allows reading and changing the installation
	// state between normal operation and maintenance mode. Its object is
	// ServerObject.
	ActionServerMaintenance Action = "server:maintenance"
	// ActionServerRestore allows starting and reading installation restores.
	// Its object is ServerObject.
	ActionServerRestore Action = "server:restore"
	// ActionServerReadSettings allows reading the settings resource of the
	// installation: each declared setting with its effective value and its
	// source. Its object is ServerObject.
	ActionServerReadSettings Action = "server:read-settings"

	// ActionInstallationStoreCheck allows starting and reading the store checks
	// of the installation: the deep check of a blob store, which writes
	// throwaway objects under a prefix of its own. Its object is ServerObject.
	ActionInstallationStoreCheck Action = "installation:store-check"
	// ActionServerManageSettings allows changing the installation settings
	// through the settings resource. Its object is ServerObject. The audit
	// entries of the settings name it: the start entry and the entry of a
	// reload of the configuration file.
	ActionServerManageSettings Action = "server:manage-settings"
	// ActionNamespaceReadSettings allows reading the settings resource of a
	// namespace. Its object is the namespace.
	ActionNamespaceReadSettings Action = "namespace:read-settings"
	// ActionNamespaceManageSettings allows changing the settings resource of
	// a namespace. Its object is the namespace.
	ActionNamespaceManageSettings Action = "namespace:manage-settings"
	// ActionServerSetAuditChain allows approving a change of the audit
	// chain to the setting of the server, on or off. A change to off also
	// needs ActionServerDisableAuditChain. Its object is ServerObject.
	ActionServerSetAuditChain Action = "server:set-audit-chain"
	// ActionServerDisableAuditChain is the second action that a change of
	// the audit chain to off needs. It is in another family than
	// ActionServerSetAuditChain, so one family alone cannot turn the chain
	// off. Its object is ServerObject.
	ActionServerDisableAuditChain Action = "server:disable-audit-chain"
	// ActionServerReadPrincipals allows reading the principals and the
	// groups of the installation, and the members of each group (ADR 52).
	// Its object is ServerObject.
	ActionServerReadPrincipals Action = "server:read-principals"
	// ActionServerManagePrincipals allows creating, changing and
	// deleting principals (ADR 52). Its object is ServerObject.
	ActionServerManagePrincipals Action = "server:manage-principals"
	// ActionServerManageGroups allows creating, changing and deleting groups,
	// and adding and removing their members (ADR 52). Its object is
	// ServerObject.
	ActionServerManageGroups Action = "server:manage-groups"
	// ActionServerManageRoles allows creating, changing and deleting custom
	// roles (ADR 52, decision 7). Its object is ServerObject.
	ActionServerManageRoles Action = "server:manage-roles"
	// ActionServerManageAccess allows granting and revoking roles on the
	// installation (ADR 52). Its object is ServerObject.
	ActionServerManageAccess Action = "server:manage-access"
	// ActionServerCreateNamespace allows creating a namespace. Its object is
	// ServerObject. The creator gets the namespace-admin role on the new
	// namespace in the same transaction.
	ActionServerCreateNamespace Action = "server:create-namespace"
	// ActionNamespaceManageAccess allows granting and revoking roles on a
	// namespace. Its object is a namespace.
	ActionNamespaceManageAccess Action = "namespace:manage-access"
	// ActionCollectionManageAccess allows granting and revoking roles on a
	// collection. Its object is a collection.
	ActionCollectionManageAccess Action = "collection:manage-access"
	// ActionUnitManageAccess allows granting and revoking roles on a unit.
	// Its object is a unit.
	ActionUnitManageAccess Action = "unit:manage-access"
	// ActionUnitRestore allows bringing back a deleted unit before its
	// retention period ends (ADR 47). Its object is a unit.
	ActionUnitRestore Action = "unit:restore"
	// ActionUnitLock allows putting a manual lock with a reason on an object,
	// and removing a manual lock that the caller holds. Its object is a unit,
	// a collection or a namespace. Removing the lock of another principal
	// needs ActionUnitManage on the same object.
	ActionUnitLock Action = "unit:lock"
	// ActionUnitPurge allows the final delete of a deleted unit before its
	// retention period ends (ADR 47). It cannot be undone. Its object is a
	// unit.
	ActionUnitPurge Action = "unit:purge"
	// ActionServerReadAccess allows reading the access-review export of the
	// whole installation: every grant (ADR 52, decision 10). Its object is
	// ServerObject.
	ActionServerReadAccess Action = "server:read-access"
	// ActionNamespaceManagePrincipals allows creating, changing and
	// deleting the service principals of a namespace. Its object is a
	// namespace. Reading them needs ActionNamespaceReadAccess.
	ActionNamespaceManagePrincipals Action = "namespace:manage-principals"
	// ActionNamespaceManagePrincipalTokens allows making, rotating, listing
	// and revoking the access tokens of the service principals of a
	// namespace. Its object is a namespace. It reaches only the principals
	// of that namespace, through principal:manage-tokens on each of them.
	ActionNamespaceManagePrincipalTokens Action = "namespace:manage-principal-tokens"
	// ActionNamespaceManageTrustRules allows creating and deleting the
	// workload trust rules of a namespace. Its object is a namespace. A rule
	// lets a workload act as a principal of the namespace, so a create also
	// needs principal:manage-tokens on that principal. Reading the rules
	// needs ActionNamespaceReadAccess.
	ActionNamespaceManageTrustRules Action = "namespace:manage-trust-rules"
	// ActionNamespaceReadAccess allows reading the access-review export of a
	// namespace, and its principals. Its object is a namespace.
	ActionNamespaceReadAccess Action = "namespace:read-access"
	// ActionCollectionReadAccess allows reading the access-review export of
	// a collection. Its object is a collection.
	ActionCollectionReadAccess Action = "collection:read-access"
	// ActionUnitReadAccess allows reading the access-review export of a unit.
	// Its object is a unit.
	ActionUnitReadAccess Action = "unit:read-access"
	// ActionRoleRead allows reading the role catalog. Every signed-in
	// principal holds it, so a person who grants a role can see its
	// actions. Its object is ServerObject.
	ActionRoleRead Action = "role:read"
	// ActionServerReadTokens allows reading the access tokens of every
	// principal, never their secrets (ADR 53). Its object is ServerObject.
	ActionServerReadTokens Action = "server:read-tokens"
	// ActionServerManageTokens allows revoking the personal tokens of other
	// persons. Its object is ServerObject. No action allows creating or
	// rotating a personal token of another person: each person makes and
	// rotates its own.
	ActionServerManageTokens Action = "server:manage-tokens"
	// ActionServerManageServiceTokens allowed creating and rotating service
	// tokens. Its object is ServerObject.
	//
	// Deprecated: no route declares it. Use ActionPrincipalManageTokens,
	// which the built-in roles that hold this action also hold.
	ActionServerManageServiceTokens Action = "server:manage-service-tokens"
	// ActionPrincipalManageTokens allows creating, rotating and revoking the
	// access tokens of a service principal. Its object is the principal
	// (PrincipalObject) or ServerObject, which holds every principal. A
	// holder can act as the principal, with every grant that the principal
	// has now or gets later.
	ActionPrincipalManageTokens Action = "principal:manage-tokens"
	// ActionTokenManageOwn allows a person to list, create, rotate and
	// revoke its own personal tokens (ADR 53, decisions 3 and 10). Its
	// object is CallerObject. Every authenticated person holds it with no
	// grant.
	ActionTokenManageOwn Action = "token:manage-own"
	// ActionCallerManagePreferences allows a person to change its own
	// preferences. Its object is CallerObject. Every authenticated person
	// holds it with no grant. A request that acts as a person acts on its
	// subject. No agent tool holds it.
	ActionCallerManagePreferences Action = "caller:manage-preferences"
	// ActionCallerManageGitHostLinks allows a person to start, accept and
	// delete the links from accounts of a Git host to its own principal. Its
	// object is CallerObject. Every authenticated person holds it with no grant.
	// The routes that change a link accept a console session only, never an
	// access token, so that an agent that holds the token of its person cannot
	// give an account the actions of that person. No agent tool holds it.
	ActionCallerManageGitHostLinks Action = "caller:manage-git-host-links"
	// ActionCallerManageSignIn allows a person to change its own sign-in
	// credentials, for example the password of its local user. Its object is
	// CallerObject. Every authenticated person holds it with no grant. A
	// request that acts as a person acts on its subject. No agent tool holds
	// it.
	ActionCallerManageSignIn Action = "caller:manage-sign-in"
	// ActionCollectionRead allows reading a collection and listing the
	// collections of a namespace. Its object is a collection, or the namespace
	// for a list (ADR 51, decision 11).
	ActionCollectionRead Action = "collection:read"
	// ActionCollectionCreate allows making a collection. Its object is the
	// namespace of the collection.
	ActionCollectionCreate Action = "collection:create"
	// ActionCollectionUpdate allows putting a unit into a collection, or
	// taking one out of it. Its object is the collection (ADR 51, decision 6).
	ActionCollectionUpdate Action = "collection:update"
	// ActionCollectionDelete allows deleting an empty collection. Its object is
	// the collection.
	ActionCollectionDelete Action = "collection:delete"
	// ActionCollectionCreateUnit allows making a unit in a collection, or
	// moving a unit into it. Its object is the collection. A placement rule
	// never grants it (ADR 61, decision 4).
	ActionCollectionCreateUnit Action = "collection:create-unit"
	// ActionNamespaceReadPlacementRules allows listing and reading the placement
	// rules of a namespace. Its object is the namespace.
	ActionNamespaceReadPlacementRules Action = "namespace:read-placement-rules"
	// ActionNamespaceManagePlacementRules allows making, changing, ordering and
	// deleting the placement rules of a namespace. A rule only names a
	// collection for a new unit. It never grants collection:create-unit, so the
	// caller who makes the unit still needs that right on the collection. Its
	// object is the namespace.
	ActionNamespaceManagePlacementRules Action = "namespace:manage-placement-rules"
	// ActionUnitMove allows moving a unit to another collection. A move needs
	// exactly these checks: unit:move on the unit (the route), unit:manage-access
	// on the unit, collection:create-unit on the target, collection:manage-access
	// on the target, and collection:manage-access on the collection the unit is
	// in now, when it has one (ADR 61, decision 4; RFC 4).
	ActionUnitMove Action = "unit:move"
	// ActionServerManageAudit allows changing how the audit log protects
	// itself, for example starting a new epoch of the signed audit
	// checkpoints. Its object is ServerObject.
	ActionServerManageAudit Action = "server:manage-audit"
	// ActionServerManageTokenPolicy allows changing the token life cycle
	// policy and running the inactivity disable (ADR 53, decisions 2, 4 and
	// 6). Its object is ServerObject.
	ActionServerManageTokenPolicy Action = "server:manage-token-policy"
	// ActionServerManageSignIn allows changing the sign-in settings of the
	// installation: the session policy, the MFA freshness settings, and later
	// the sign-in methods, SCIM and MFA. Its object is ServerObject. Of the
	// built-in roles, only the site administrator holds it.
	ActionServerManageSignIn Action = "server:manage-sign-in"
	// ActionServerRestoreAccess allows a restore to replace the access
	// tables: principals, groups, memberships, roles, grants, access tokens
	// and trust rules. Its object is ServerObject. Of the built-in roles,
	// only the site administrator holds it.
	ActionServerRestoreAccess Action = "server:restore-access"

	// The actions below belong to features that have no routes yet. The
	// access package classifies none of them, so the policy check denies
	// each one to every principal that is not an administrator, until its
	// feature lands.

	// ActionUnitRunPlan allows starting a runner job that only plans. Its
	// object is a unit.
	ActionUnitRunPlan Action = "unit:run-plan"
	// ActionUnitRunApply allows starting a runner job that applies or
	// refreshes. Its object is a unit.
	ActionUnitRunApply Action = "unit:run-apply"
	// ActionUnitRunDestroy allows starting a runner job that destroys the
	// resources of a unit. Its object is a unit.
	ActionUnitRunDestroy Action = "unit:run-destroy"
	// ActionUnitWriteBack allows starting a write-back job: a commit to the
	// code location of a unit. Its object is a unit.
	ActionUnitWriteBack Action = "unit:write-back"
	// ActionUnitDefineSteps allows changing the custom steps of a unit in
	// its settings, or of a blueprint. Custom steps run on the runners of
	// the namespace, so their change is its own action, apart from the
	// start of a job. Its object is a unit, or the blueprint.
	ActionUnitDefineSteps Action = "unit:define-steps"
	// ActionJobRead allows reading the jobs of a unit: the spec, the state,
	// the phases, the steps, the approvals and the metadata of plans and
	// logs. Its object is a unit, or the namespace for a list.
	ActionJobRead Action = "job:read"
	// ActionJobReadLogs allows reading the content of job logs. Its object
	// is a unit. A redaction of a plan or of a log, also by substitution,
	// needs ActionUnitRedact: the job model has no redact action of its own.
	ActionJobReadLogs Action = "job:read-logs"
	// ActionJobReadPlans allows reading the content of stored plans, with
	// the values that the tool marks as sensitive masked. The unmasked plan
	// also needs ActionUnitReadSecrets. Its object is a unit.
	ActionJobReadPlans Action = "job:read-plans"
	// ActionJobCancel allows canceling a job. Its object is a unit.
	ActionJobCancel Action = "job:cancel"
	// ActionJobApprove allows approving or rejecting a job that waits for
	// approval. Its object is a unit. The gate can also limit the approvers
	// to groups.
	ActionJobApprove Action = "job:approve"
	// ActionNamespaceManageFreezes allows creating and ending the change
	// freezes of a namespace. Its object is a namespace.
	ActionNamespaceManageFreezes Action = "namespace:manage-freezes"
	// ActionNamespaceBreakFreeze allows starting a job through an active
	// change freeze. Its object is a namespace. Its family must differ from
	// the family of ActionNamespaceManageFreezes, so a principal that can end
	// a freeze does not also get break-glass, and the reverse.
	ActionNamespaceBreakFreeze Action = "namespace:break-freeze"
	// ActionNamespaceManageRunners allows creating and deleting the runner
	// pools of a namespace, and registering and removing runners. Its
	// object is a namespace.
	ActionNamespaceManageRunners Action = "namespace:manage-runners"
	// ActionRunnerClaim allows a runner to claim the phases of its pool and
	// to report on them. Only a runner credential holds it. Its object is a
	// runner pool.
	ActionRunnerClaim Action = "runner:claim"
	// ActionAgentUse allows the agent and outside agents to act for the
	// holder in a namespace. Every tool call that touches an object in a
	// namespace checks it on that namespace, in addition to the action of
	// the tool. Its object is a namespace.
	ActionAgentUse Action = "agent:use"
	// ActionAgentStartRun allows a person to start a run of the built-in
	// agent and to read, continue and cancel its own runs. Its object is
	// CallerObject.
	ActionAgentStartRun Action = "agent:start-run"
	// ActionNamespaceManageAgentRuns allows reading and canceling the runs
	// of other persons that started in a namespace. Its object is a
	// namespace.
	ActionNamespaceManageAgentRuns Action = "namespace:manage-agent-runs"
	// ActionNamespaceManageAgent allows changing the agent settings of a
	// namespace: the opt-in for direct changes, the opt-in for secret
	// values, the default change mode and lower limits. Its object is a
	// namespace.
	ActionNamespaceManageAgent Action = "namespace:manage-agent"
	// ActionServerManageAgent allows connecting and disconnecting a model,
	// setting the limits of the installation and reading the token use of
	// every run. Its object is ServerObject.
	ActionServerManageAgent Action = "server:manage-agent"
	// ActionServerAcceptAgentSecretAccess allows the acceptance that opens
	// the secret gate after a start with the secret access flag. Its object
	// is ServerObject.
	ActionServerAcceptAgentSecretAccess Action = "server:accept-agent-secret-access"
	// ActionCallerManageAgentOptIns allows a person to set its own opt-ins:
	// direct changes and secret values. Its object is CallerObject.
	ActionCallerManageAgentOptIns Action = "caller:manage-agent-opt-ins"
	// ActionUnitObserve allows reading the live status of the resources of a
	// unit from its cloud: events of workloads and the health of targets. Its
	// object is a unit. It is a read, but it reaches the cloud account of the
	// unit with short-lived read-only credentials, so it has its own action.
	ActionUnitObserve Action = "unit:observe"
	// ActionNamespaceReadRunners allows reading the pools, the runners and
	// the join tokens of a namespace, with no secret. Its object is a
	// namespace.
	ActionNamespaceReadRunners Action = "namespace:read-runners"
	// ActionRunnerJoin allows registering a runner key in a pool. Only a
	// join token holds it. Its object is a runner pool.
	ActionRunnerJoin Action = "runner:join"
	// ActionNamespaceReadUnmanaged allows reading the findings, the sweeps,
	// the sweep targets and the ignore rules of a namespace. Its object is
	// the namespace. A finding names only the units that the caller can
	// read, and counts the others.
	ActionNamespaceReadUnmanaged Action = "namespace:read-unmanaged"
	// ActionNamespaceTriageUnmanaged allows the actions on a finding:
	// ignore, stop ignoring, choose an owner, mark for delete, cancel a
	// pending action, and start an import or code generation. It also
	// allows the ignore rules. Its object is the namespace. An action that starts a job also needs the
	// action of the job on its unit.
	ActionNamespaceTriageUnmanaged Action = "namespace:triage-unmanaged"
	// ActionNamespaceManageSweeps allows creating, changing and deleting the
	// sweep targets of a namespace, with their schedule, their runner pool
	// and the command that their runner starts. Its object is the namespace.
	ActionNamespaceManageSweeps Action = "namespace:manage-sweeps"
	// ActionNamespaceStartSweep allows starting and canceling a sweep of a
	// sweep target, or a state check, now. Its object is the namespace.
	ActionNamespaceStartSweep Action = "namespace:start-sweep"
	// ActionServerLinkNamespaces allows linking and unlinking two
	// namespaces, so that the classification of each one reads the units of
	// the other. Its object is ServerObject.
	ActionServerLinkNamespaces Action = "server:link-namespaces"
	// ActionUnitSetContext allows setting and deleting the API context of a
	// unit and of its resources. Its object is the unit.
	ActionUnitSetContext Action = "unit:set-context"
	// ActionCollectionSetContext allows setting and deleting the API context
	// of a collection. Its object is the collection.
	ActionCollectionSetContext Action = "collection:set-context"
	// ActionNamespaceSetContext allows setting and deleting the API context
	// of a namespace. Its object is the namespace.
	ActionNamespaceSetContext Action = "namespace:set-context"
	// ActionBlueprintRead allows reading a blueprint and its context. Its
	// object is the blueprint.
	ActionBlueprintRead Action = "blueprint:read"
	// ActionBlueprintSetContext allows setting and deleting the API context
	// of a blueprint. Its object is the blueprint.
	ActionBlueprintSetContext Action = "blueprint:set-context"
	// ActionUnitRedact allows a redaction of an old history item of a unit:
	// the server destroys its content and keeps its metadata. It cannot be
	// undone. Its object is a unit.
	ActionUnitRedact Action = "unit:redact"

	// ActionProfileRead allows reading the metadata, the revisions and the
	// documents of a profile with sealed secrets, and listing profiles. Its
	// object is the namespace that holds the profile.
	ActionProfileRead Action = "profile:read"
	// ActionProfileWrite allows creating a profile and saving a revision.
	// Its object is the namespace that holds the profile.
	ActionProfileWrite Action = "profile:write"
	// ActionProfileOpen allows reading the secret values of a profile and
	// saving a routing change. Its object is the namespace that holds the
	// profile.
	ActionProfileOpen Action = "profile:open"
	// ActionProfileManage allows deleting a profile. Its object is the
	// namespace that holds the profile.
	ActionProfileManage Action = "profile:manage"

	// ActionNamespaceManageNotifications allows creating, changing, deleting
	// and testing the notification sinks of a namespace. Its object is the
	// namespace. The value equals notify.ActionManage.
	ActionNamespaceManageNotifications Action = "namespace:manage-notifications"

	// ActionViewCreate allows a person to make a saved view of their own.
	// Its object is the namespace that holds the view.
	ActionViewCreate Action = "view:create"
	// ActionViewRead allows reading the saved views of the caller, and
	// running one. A view of another person is never readable. Its object is
	// the namespace that holds the view.
	ActionViewRead Action = "view:read"
	// ActionViewDelete allows deleting a saved view of the caller. Its object
	// is the namespace that holds the view.
	ActionViewDelete Action = "view:delete"

	// ActionNamespaceReadGitHostConnections allows listing and reading the
	// connections of a namespace to a Git host, and answers their settings and
	// never a credential. Its object is the namespace.
	ActionNamespaceReadGitHostConnections Action = "namespace:read-git-host-connections"
	// ActionNamespaceManageGitHostConnections allows making, deleting and
	// checking a connection of a namespace to a Git host, and making one the
	// default. Its object is the namespace.
	ActionNamespaceManageGitHostConnections Action = "namespace:manage-git-host-connections"
	// ActionNamespaceUseGitHostConnection allows naming a connection of a
	// namespace to a Git host in a code location, in addition to the action on
	// the unit. It lets the holder point a unit at the credentials of the
	// connection, and it answers no setting or credential of the connection.
	// Its object is the namespace that holds the connection.
	ActionNamespaceUseGitHostConnection Action = "namespace:use-git-host-connection"
	// ActionServerReadGitHost allows reading the registration of the GitHub
	// App of the installation, and the map from its installations to
	// namespaces. No read answers a key or an event secret. Its object is the
	// installation.
	ActionServerReadGitHost Action = "server:read-git-host"
	// ActionServerManageGitHost allows registering the GitHub App of the
	// installation with its key and its event secret, and assigning each of its
	// installations to a namespace. Its object is the installation.
	ActionServerManageGitHost Action = "server:manage-git-host"
	// ActionRegistryRead allows listing the releases of the private registry
	// of a namespace and getting their files. Its object is the namespace that
	// holds the registry.
	ActionRegistryRead Action = "registry:read"
	// ActionRegistryPublish allows staging the files of a release and
	// publishing it. A published version never changes. Its object is the
	// namespace that holds the registry. No built-in role below the
	// administrator holds it.
	ActionRegistryPublish Action = "registry:publish"
	// ActionNamespaceManageIssuer allows making, rotating and checking the
	// signing keys of the OIDC issuer of a namespace, changing its settings
	// and exporting its two files. Its object is a namespace.
	ActionNamespaceManageIssuer Action = "namespace:manage-issuer"
	// ActionRegistryManageKeys allows registering and removing the trusted
	// signing keys of a namespace, and listing them. Its object is the
	// namespace that holds the registry.
	ActionRegistryManageKeys Action = "registry:manage-keys"
)

// Object limits for the tool and unit parts of a unit reference.
const (
	// MaxToolLength is the longest tool code, in bytes.
	MaxToolLength = 100
	// MaxUnitLength is the longest unit name, in bytes.
	MaxUnitLength = 300
)

// Object is the thing an action applies to: a namespace, a unit reference,
// the authenticated caller itself, or the installation itself. A namespace
// reference is the namespace name. A unit reference is the tool, the
// namespace and the tool's own name for the unit, written as
// <tool>:<namespace>/<name>. The core keeps the tool-specific name opaque
// and never parses it. The caller reference names the principal in the
// request and has no namespace or unit. The server reference names the
// installation and has no namespace or unit. Their wire forms start with
// '@', which namespace names never contain, so ParseObject rejects them and
// they never equal a namespace. Use IsCaller and IsServer to test for them;
// String alone also differs, but the methods state the intent.
type Object struct {
	raw      string
	ns       namespace.Name
	tool     string
	unit     string
	isUnit   bool
	isCaller bool
	isServer bool
	// isCollection is true for a collection reference. collection holds the
	// name of the collection as it was written.
	isCollection bool
	collection   string
	// unitID is the stable ID of a unit reference, when the core resolved
	// it (ADR 19). It is not part of the wire form.
	unitID string
	// collectionID is the stable ID of a collection reference, when the core
	// resolved it (ADR 51). It is not part of the wire form.
	collectionID string
	// principal is the ID of the principal that a principal reference
	// names, and empty for every other reference.
	principal string
}

// NamespaceObject returns the reference to the namespace ns.
func NamespaceObject(ns namespace.Name) Object {
	return Object{
		raw:          ns.String(),
		ns:           ns,
		tool:         "",
		unit:         "",
		isUnit:       false,
		isCaller:     false,
		isServer:     false,
		isCollection: false,
		collection:   "",
		unitID:       "", collectionID: "",
		principal: "",
	}
}

// CallerObject returns the reference to the authenticated caller itself, for
// endpoints that read the caller's own record and act on no namespace or
// unit. Its wire form is "@caller", which ParseObject rejects: '@' is not a
// valid namespace character, so a namespace named "caller" never equals it.
// Every tool API needs it for its current-user endpoint, so it lives in the
// core.
func CallerObject() Object {
	return Object{
		raw:          "@caller",
		ns:           namespace.Name(""),
		tool:         "",
		unit:         "",
		isUnit:       false,
		isCaller:     true,
		isServer:     false,
		isCollection: false,
		collection:   "",
		unitID:       "",
		collectionID: "",
		principal:    "",
	}
}

// ServerObject returns the reference to the installation itself, for
// endpoints that act on no namespace or unit. Its wire form is "@server",
// which ParseObject rejects: '@' is not a valid namespace character, so a
// namespace named "server" never equals it. The installation API needs it,
// so it lives in the core.
func ServerObject() Object {
	return Object{
		raw:          "@server",
		ns:           namespace.Name(""),
		tool:         "",
		unit:         "",
		isUnit:       false,
		isCaller:     false,
		isServer:     true,
		isCollection: false,
		collection:   "",
		unitID:       "",
		collectionID: "",
		principal:    "",
	}
}

// principalObjectPrefix starts the wire form of a principal reference.
const principalObjectPrefix = "principal:"

// maxPrincipalIDLength bounds the ID part of a principal reference, in
// bytes.
const maxPrincipalIDLength = 64

// PrincipalObject returns the reference to the principal with the ID id,
// the object of the actions on one principal, such as the issue of its
// tokens. Its wire form is "principal:<id>": an ID has no '/', and a unit
// reference always has one, so the forms never overlap. The installation
// holds every principal. id must already be valid: ParsePrincipalObject
// checks it.
func PrincipalObject(id string) Object {
	return Object{
		raw: principalObjectPrefix + id, ns: namespace.Name(""), tool: "", unit: "",
		isUnit: false, isCaller: false, isServer: false, isCollection: false, collection: "", unitID: "",
		collectionID: "", principal: id,
	}
}

// ParsePrincipalObject returns the reference to the principal with the ID
// id, or ErrInvalidObject when id is not 1 to 64 letters, digits, '-', '_'
// and '.'.
func ParsePrincipalObject(id string) (Object, error) {
	if !ident.Valid(id, maxPrincipalIDLength) {
		return Object{}, fmt.Errorf("%w: principal %q", ErrInvalidObject, id)
	}

	return PrincipalObject(id), nil
}

// CollectionObject returns the reference to the collection called name in the
// namespace ns. Its wire form is "<namespace>/<name>": a namespace name has no
// '/' and a unit reference has ':', so the forms never overlap. The name must
// already be valid: the collection package checks it and keeps ':' out of it
// (ADR 51).
func CollectionObject(ns namespace.Name, name string) Object {
	return Object{
		raw:          ns.String() + "/" + name,
		ns:           ns,
		tool:         "",
		unit:         "",
		isUnit:       false,
		isCaller:     false,
		isServer:     false,
		isCollection: true,
		collection:   name,
		collectionID: "",
		unitID:       "",
		principal:    "",
	}
}

// UnitObject returns the reference to the unit called name in the namespace ns
// for the tool tool. The name is opaque to the core. Tool names match exactly:
// "pulumi" and "Pulumi" name different tools.
func UnitObject(tool string, ns namespace.Name, name string) (Object, error) {
	if !ident.Valid(tool, MaxToolLength) {
		return Object{}, fmt.Errorf("%w: tool %q", ErrInvalidObject, tool)
	}

	if !validUnitName(name) {
		return Object{}, fmt.Errorf("%w: unit %q", ErrInvalidObject, name)
	}

	raw := tool + ":" + ns.String() + "/" + name

	return Object{
		raw:          raw,
		ns:           ns,
		tool:         tool,
		unit:         name,
		isUnit:       true,
		isCaller:     false,
		isServer:     false,
		isCollection: false,
		collection:   "",
		unitID:       "", collectionID: "",
		principal: "",
	}, nil
}

// ParseObject parses s as a namespace, collection, unit, principal or
// server reference. It rejects the caller reference form: use CallerObject
// for it.
func ParseObject(s string) (Object, error) {
	if s == ServerObject().String() {
		return ServerObject(), nil
	}

	if id, ok := strings.CutPrefix(s, principalObjectPrefix); ok && !strings.Contains(id, "/") {
		return ParsePrincipalObject(id)
	}

	if !strings.Contains(s, ":") && strings.Contains(s, "/") {
		return parseCollectionObject(s)
	}

	if !strings.Contains(s, ":") {
		ns, err := namespace.ParseName(s)
		if err != nil {
			return Object{}, fmt.Errorf("%w: %q", ErrInvalidObject, s)
		}

		return Object{
			raw:          s,
			ns:           ns,
			tool:         "",
			unit:         "",
			isUnit:       false,
			isCaller:     false,
			isServer:     false,
			isCollection: false,
			collection:   "",
			unitID:       "", collectionID: "",
			principal: "",
		}, nil
	}

	return parseUnitObject(s)
}

// maxCollectionLength bounds the name part of a collection reference. The
// collection package keeps the stricter rules of ADR 61.
const maxCollectionLength = 100

// parseCollectionObject parses s as a collection reference of the form
// <namespace>/<collection>. The name must not be empty, must hold no '/' or
// ':' and no white space at either end.
func parseCollectionObject(s string) (Object, error) {
	nsPart, name, _ := strings.Cut(s, "/")

	ns, err := namespace.ParseName(nsPart)
	if err != nil {
		return Object{}, fmt.Errorf("%w: %q", ErrInvalidObject, s)
	}

	if !ident.Valid(name, maxCollectionLength) {
		return Object{}, fmt.Errorf("%w: %q", ErrInvalidObject, s)
	}

	return CollectionObject(ns, name), nil
}

// parseUnitObject parses s as a unit reference of the form
// <tool>:<namespace>/<name>.
func parseUnitObject(s string) (Object, error) {
	tool, rest, _ := strings.Cut(s, ":")

	if strings.Contains(rest, ":") {
		return Object{}, fmt.Errorf("%w: %q", ErrInvalidObject, s)
	}

	if !ident.Valid(tool, MaxToolLength) {
		return Object{}, fmt.Errorf("%w: %q", ErrInvalidObject, s)
	}

	nsPart, unitPart, ok := strings.Cut(rest, "/")
	if !ok {
		return Object{}, fmt.Errorf("%w: %q", ErrInvalidObject, s)
	}

	ns, err := namespace.ParseName(nsPart)
	if err != nil {
		return Object{}, fmt.Errorf("%w: %q", ErrInvalidObject, s)
	}

	if !validUnitName(unitPart) {
		return Object{}, fmt.Errorf("%w: %q", ErrInvalidObject, s)
	}

	return Object{
		raw:          s,
		ns:           ns,
		tool:         tool,
		unit:         unitPart,
		isUnit:       true,
		isCaller:     false,
		isServer:     false,
		isCollection: false,
		collection:   "",
		unitID:       "", collectionID: "",
		principal: "",
	}, nil
}

// validUnitName reports whether name is a usable opaque unit name.
func validUnitName(name string) bool {
	if name == "" || len(name) > MaxUnitLength {
		return false
	}

	if strings.Contains(name, ":") {
		return false
	}

	if strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") {
		return false
	}

	return !strings.Contains(name, "//")
}

// String returns the reference in its wire form.
func (o Object) String() string {
	return o.raw
}

// IsUnit reports whether the reference names a unit rather than a namespace.
func (o Object) IsUnit() bool {
	return o.isUnit
}

// IsNamespace reports whether the reference names a namespace itself rather
// than a unit, a collection, a principal, the caller or the installation.
func (o Object) IsNamespace() bool {
	return o.ns != "" && !o.isUnit && !o.isCollection && !o.isCaller && !o.isServer && o.principal == ""
}

// IsCaller reports whether the reference names the authenticated caller
// itself rather than a namespace, a unit or the installation.
func (o Object) IsCaller() bool {
	return o.isCaller
}

// IsCollection reports whether the reference names a collection.
func (o Object) IsCollection() bool {
	return o.isCollection
}

// CollectionName returns the name of the collection that the reference names,
// or the empty string when it names no collection.
func (o Object) CollectionName() string {
	return o.collection
}

// IsPrincipal reports whether the reference names a principal.
func (o Object) IsPrincipal() bool {
	return o.principal != ""
}

// PrincipalID returns the ID of the principal that the reference names, or
// the empty string when it names no principal.
func (o Object) PrincipalID() string {
	return o.principal
}

// IsServer reports whether the reference names the installation itself
// rather than a namespace, a unit or the caller.
func (o Object) IsServer() bool {
	return o.isServer
}

// Namespace returns the namespace that owns the object.
func (o Object) Namespace() namespace.Name {
	return o.ns
}

// Tool returns the tool that owns the unit, or "" for a namespace.
func (o Object) Tool() string {
	return o.tool
}

// UnitName returns the tool's own opaque name for the unit, or "" for a namespace.
func (o Object) UnitName() string {
	return o.unit
}

// Authenticator turns an access token into a Principal.
type Authenticator interface {
	// AuthenticateToken returns the principal owning token, or ErrUnauthenticated.
	AuthenticateToken(ctx context.Context, token string) (Principal, error)
}

// Authorizer decides whether a principal may perform an action on an object.
//
// The server builds a namespace service principal itself (see
// NamespaceService). It holds one action, ActionUnitRunPlan, on the units of
// its own namespace. An Authorizer must never allow it more, also with the
// Admin flag set, and must deny every principal whose subject has the form of
// such a subject but for which IsNamespaceService does not hold. The server
// puts NamespaceServiceRule around every Authorizer, so that rule holds also
// for an Authorizer that does not know it. An Authorizer that decides these
// principals itself runs identitytest.RunNamespaceService.
type Authorizer interface {
	// Authorize returns nil if allowed and an error wrapping ErrDenied
	// otherwise. An authorizer that knows the action and the object returns
	// a *DeniedError, so that an answer names the missing action and the
	// kind of the object and never the object itself.
	Authorize(ctx context.Context, p Principal, action Action, obj Object) error
	// ListObjects returns the scope of the objects where p may perform
	// action. Authorize allows p action on each object in the scope. A
	// list filters with the scope in its own query, never with one
	// Authorize call for each object. A principal with no object gets the
	// empty Scope and no error. It returns the store errors of the adapter.
	// A decorator that does not filter the scope says so; its callers then
	// Authorize the namespace of the list first.
	ListObjects(ctx context.Context, p Principal, action Action) (Scope, error)
}

// DeniedError is the error of a denied action: the principal Subject may not
// perform Action on Object. It unwraps to ErrDenied, so errors.Is(err,
// ErrDenied) holds for it. ObjectKind names the kind of the object, so an
// adapter can answer the missing action and the kind of the object without
// naming the object, which the caller may not see.
type DeniedError struct {
	// Subject is the subject of the principal that the authorizer refused.
	Subject string
	// Action is the action that the principal lacks.
	Action Action
	// Object is the object of the action. The text of the error names it;
	// an answer names its kind only.
	Object Object
}

// Error returns the text of a denial: the sentinel, the subject, the action
// and the object, as the text of an authorizer denial always read.
func (e *DeniedError) Error() string {
	return fmt.Sprintf("%s: %s may not %s on %q", ErrDenied, e.Subject, e.Action, e.Object.String())
}

// Unwrap returns ErrDenied.
func (e *DeniedError) Unwrap() error { return ErrDenied }

// ObjectKind returns the kind of the object of the denial in the words of
// the API: "namespace", "unit", "collection", "principal", "caller" or
// "server". It names no object. It returns the empty string for an object
// that names no kind.
func (e *DeniedError) ObjectKind() string {
	switch {
	case e.Object.IsServer():
		return "server"
	case e.Object.IsCaller():
		return "caller"
	case e.Object.IsPrincipal():
		return "principal"
	case e.Object.IsUnit():
		return "unit"
	case e.Object.IsCollection():
		return "collection"
	case e.Object.IsNamespace():
		return "namespace"
	default:
		return ""
	}
}

// ExactScoper is an optional interface of an Authorizer. ScopeExact reports
// that ListObjects gives a scope whose objects are exactly the objects that
// Authorize allows: Authorize never denies an object that the scope holds,
// for the grants and for every other check of the authorizer. A caller that
// counts objects in its own query may take the scope as the answer only for
// such an authorizer. A decorator that does not filter the scope, for
// example one that checks access limits for each object, does not implement
// it.
type ExactScoper interface {
	ScopeExact() bool
}

// HasExactScope reports whether the scope of a is exact: a implements
// ExactScoper and says so. Any other authorizer is not exact.
func HasExactScope(a Authorizer) bool {
	exact, ok := a.(ExactScoper)

	return ok && exact.ScopeExact()
}

// Scope is the set of objects of one kind where a principal may perform one
// action. A scope covers every object through Every. A consumer reads that
// case only through CoversEveryObject or Contains. Otherwise a
// unit is in the scope when it, its collection or its namespace is in a
// set. A principal object is in the scope when it is in Principals.
type Scope struct {
	Namespaces  []Object
	Collections []Object
	Units       []Object
	Principals  []Object
	every       everyObject
}

// everyObject is the case of a scope that covers every object, except the
// objects of the namespaces in except.
type everyObject struct {
	set    bool
	except []namespace.Name
}

// Every returns a scope that covers every object, except the objects of the
// named namespaces. It keeps each name once, in sorted order. The explicit
// sets are nil.
func Every(except ...namespace.Name) Scope {
	names := slices.Clone(except)
	slices.Sort(names)
	names = slices.Compact(names)

	return Scope{
		Namespaces: nil, Collections: nil, Units: nil, Principals: nil,
		every: everyObject{set: true, except: names},
	}
}

// CoversEveryObject reports whether s covers every object and leaves out no
// namespace. A consumer that cannot apply the left-out namespaces uses this
// method; when the scope leaves out a namespace it reports false, so that the
// consumer uses the explicit sets and fails closed.
func (s Scope) CoversEveryObject() bool {
	return s.every.set && len(s.every.except) == 0
}

// Contains reports whether s holds o. In the every-object case it reports
// false for a namespace, collection or unit object of a namespace that s
// leaves out, and true for every other object. Otherwise it compares the wire
// forms: a namespace object is in Namespaces; a collection object is in
// Collections or its namespace is in Namespaces; a unit object is in Units or
// its namespace is in Namespaces; a principal object is in Principals. A unit
// that the scope holds only through a collection gives false, because an
// Object does not carry the collections of a unit. This is fail closed.
func (s Scope) Contains(o Object) bool {
	if s.every.set {
		ns := o.Namespace()
		if ns == "" {
			return true
		}

		return !slices.Contains(s.every.except, ns)
	}

	switch {
	case o.IsNamespace():
		return containsObject(s.Namespaces, o)
	case o.IsCollection():
		return containsObject(s.Collections, o) ||
			containsObject(s.Namespaces, NamespaceObject(o.Namespace()))
	case o.IsUnit():
		return containsObject(s.Units, o) ||
			containsObject(s.Namespaces, NamespaceObject(o.Namespace()))
	case o.IsPrincipal():
		return containsObject(s.Principals, o)
	default:
		return false
	}
}

// containsObject reports whether objs holds an object with the wire form of o.
func containsObject(objs []Object, o Object) bool {
	for i := range objs {
		if objs[i].String() == o.String() {
			return true
		}
	}

	return false
}

// IsEmpty reports whether the scope holds no object.
func (s Scope) IsEmpty() bool {
	return !s.every.set && len(s.Namespaces) == 0 && len(s.Collections) == 0 && len(s.Units) == 0 &&
		len(s.Principals) == 0
}

// AdminScope returns the scope of an authorizer that allows administrators
// only: every object for an administrator, and no object for any other
// principal.
func AdminScope(p Principal) Scope {
	if p.Admin {
		return Every()
	}

	return Scope{
		Namespaces:  nil,
		Collections: nil,
		Units:       nil,
		Principals:  nil,
		every:       everyObject{set: false, except: nil},
	}
}

// StaticToken authenticates exactly one token as one administrator. It exists
// to bootstrap a fresh install; replace it once real identity is configured.
type StaticToken struct {
	principal Principal
	token     []byte
}

var _ Authenticator = (*StaticToken)(nil)

// NewStaticToken returns an authenticator accepting token for an admin named subject.
func NewStaticToken(subject, token string) (*StaticToken, error) {
	if len(token) < minTokenLength {
		return nil, fmt.Errorf("%w: need at least %d bytes", ErrWeakToken, minTokenLength)
	}

	return &StaticToken{
		principal: Principal{
			TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
			Subject:        subject,
			DisplayName:    subject,
			Admin:          true,
			TokenID:        "",
			TokenExpiresAt: time.Time{},
			Kind:           "",
			Credential:     "",
			JobID:          "",
		},
		token: []byte(token),
	}, nil
}

// AdminSubjects returns the subject of the administrator of the token, so
// an access-review export can show it: it holds every action with no grant.
func (s *StaticToken) AdminSubjects() []string {
	return []string{s.principal.Subject}
}

// AuthenticateToken compares token in constant time.
func (s *StaticToken) AuthenticateToken(_ context.Context, token string) (Principal, error) {
	if subtle.ConstantTimeCompare([]byte(token), s.token) != 1 {
		return Principal{}, ErrUnauthenticated
	}

	return s.principal, nil
}

// StaticTokens authenticates a fixed set of bootstrap tokens, one
// administrator per token. It exists to bootstrap a fresh install; replace
// it once real identity is configured. It stores only the SHA-256 of each
// token, so the comparison never handles raw tokens of different lengths.
type StaticTokens struct {
	principals []Principal
	hashes     [][32]byte
}

var _ Authenticator = (*StaticTokens)(nil)

// NewStaticTokens returns an authenticator accepting each of tokens as a
// distinct administrator. The first token signs in as admin, the next ones
// as admin-2, admin-3, and so on. Each token must hold at least 32 bytes.
// Empty and duplicate entries fail with ErrWeakToken, as does an empty
// list. White space around an entry is ignored.
func NewStaticTokens(tokens ...string) (*StaticTokens, error) {
	if len(tokens) == 0 {
		return nil, fmt.Errorf("%w: need at least one token", ErrWeakToken)
	}

	principals := make([]Principal, 0, len(tokens))
	hashes := make([][32]byte, 0, len(tokens))
	seen := make(map[[32]byte]struct{}, len(tokens))

	for i, raw := range tokens {
		token := strings.TrimSpace(raw)
		if len(token) < minTokenLength {
			return nil, fmt.Errorf("%w: token %d needs at least %d bytes", ErrWeakToken, i+1, minTokenLength)
		}

		sum := sha256.Sum256([]byte(token))
		if _, dup := seen[sum]; dup {
			return nil, fmt.Errorf("%w: token %d is a duplicate", ErrWeakToken, i+1)
		}

		seen[sum] = struct{}{}

		subject := "admin"
		if i > 0 {
			subject = fmt.Sprintf("admin-%d", i+1)
		}

		principals = append(
			principals,
			Principal{
				TemporaryAdministrator: false,
				TemporaryExpiresAt:     time.Time{},
				Subject:                subject,
				DisplayName:            subject,
				Admin:                  true,
				TokenID:                "",
				TokenExpiresAt:         time.Time{},
				Kind:                   "",
				Credential:             "",
				JobID:                  "",
			},
		)
		hashes = append(hashes, sum)
	}

	return &StaticTokens{principals: principals, hashes: hashes}, nil
}

// AdminSubjects returns the subjects of the administrators of the tokens,
// in token order, so an access-review export can show them: each holds
// every action with no grant.
func (s *StaticTokens) AdminSubjects() []string {
	out := make([]string, 0, len(s.principals))
	for i := range s.principals {
		out = append(out, s.principals[i].Subject)
	}

	return out
}

// AuthenticateToken compares the SHA-256 of token against every stored
// token in constant time, without an early exit.
func (s *StaticTokens) AuthenticateToken(_ context.Context, token string) (Principal, error) {
	sum := sha256.Sum256([]byte(token))

	found := 0

	var principal Principal

	for i := range s.hashes {
		match := subtle.ConstantTimeCompare(sum[:], s.hashes[i][:])
		found |= match

		if match == 1 {
			principal = s.principals[i]
		}
	}

	if found != 1 {
		return Principal{}, ErrUnauthenticated
	}

	return principal, nil
}

// Chain authenticates a token with each of its authenticators in order and
// answers the first principal. It lets the bootstrap admin tokens (ADR 6)
// keep working next to the stored access tokens (ADR 53).
type Chain []Authenticator

var _ Authenticator = Chain(nil)

// AuthenticateToken returns the principal of the first authenticator that
// accepts token. An authenticator that answers ErrUnauthenticated passes the
// token to the next one. Any other error stops the chain and is returned, so
// a failed store read fails closed. With no authenticator left, it answers
// the last ErrUnauthenticated.
func (c Chain) AuthenticateToken(ctx context.Context, token string) (Principal, error) {
	last := ErrUnauthenticated

	for _, a := range c {
		p, err := a.AuthenticateToken(ctx, token)
		if err == nil {
			return p, nil
		}

		if !errors.Is(err, ErrUnauthenticated) {
			return Principal{}, err
		}

		last = err
	}

	return Principal{}, last
}

// AdminSubjects returns the subjects of the bootstrap administrators of the
// authenticators of the chain that name them, in chain order, so an
// access-review export shows them behind the chain too.
func (c Chain) AdminSubjects() []string {
	var out []string

	for _, a := range c {
		if named, ok := a.(interface{ AdminSubjects() []string }); ok {
			out = append(out, named.AdminSubjects()...)
		}
	}

	return out
}

// SelfAllowed allows each of Actions on CallerObject to every authenticated
// principal, with no grant, and asks Next for every other check. It carries
// the self actions until the grants of ADR 52 can, for example
// token:manage-own (ADR 53). The domain service of each action checks
// whether the caller can use it, such as the kind of the principal.
type SelfAllowed struct {
	Next    Authorizer
	Actions []Action
}

var _ Authorizer = SelfAllowed{Next: nil, Actions: nil}

// Authorize allows a self action on the caller, and asks Next otherwise.
func (s SelfAllowed) Authorize(ctx context.Context, p Principal, action Action, obj Object) error {
	if obj.IsCaller() && slices.Contains(s.Actions, action) {
		return nil
	}

	return s.Next.Authorize(ctx, p, action, obj)
}

// ListObjects asks Next. A self action gives an object only on the caller
// itself, which no list holds, so it adds nothing to the scope.
func (s SelfAllowed) ListObjects(ctx context.Context, p Principal, action Action) (Scope, error) {
	scope, err := s.Next.ListObjects(ctx, p, action)
	if err != nil {
		return Scope{
			Namespaces:  nil,
			Collections: nil,
			Units:       nil,
			Principals:  nil,
			every:       everyObject{set: false, except: nil},
		}, fmt.Errorf(
			"list objects: %w",
			err,
		)
	}

	return scope, nil
}

// ScopeExact reports that the scope is exact when the scope of Next is exact:
// a self action adds nothing to the scope and no check to the other actions.
func (s SelfAllowed) ScopeExact() bool {
	return HasExactScope(s.Next)
}

// AdminOnly allows administrators and denies everyone else. It is the safe
// default until a real authorizer (roles, teams) is configured.
type AdminOnly struct{}

var _ Authorizer = AdminOnly{}

// Authorize allows the action only for administrators.
func (AdminOnly) Authorize(_ context.Context, p Principal, action Action, obj Object) error {
	if p.Admin {
		return nil
	}

	return &DeniedError{Subject: p.Subject, Action: action, Object: obj}
}

// ScopeExact reports that the scope is exact: Authorize and ListObjects
// decide on the administrator flag alone.
func (AdminOnly) ScopeExact() bool {
	return true
}

// ListObjects gives an administrator every object and any other principal
// none.
func (AdminOnly) ListObjects(_ context.Context, p Principal, _ Action) (Scope, error) {
	return AdminScope(p), nil
}

// WithUnitID returns o with the stable ID id of its unit (ADR 19). A grant
// on a unit follows this ID, not the name, so a new unit with the name of a
// deleted one does not get its grants. On an object that is not a unit
// reference, it returns o unchanged.
func (o Object) WithUnitID(id string) Object {
	if o.isUnit {
		o.unitID = id
	}

	return o
}

// WithCollectionID returns the reference with the stable ID id of a collection.
// It has no effect on any other reference.
func (o Object) WithCollectionID(id string) Object {
	if o.isCollection {
		o.collectionID = id
	}

	return o
}

// CollectionID returns the stable ID of a collection reference, or "" when the
// core has not resolved one.
func (o Object) CollectionID() string {
	return o.collectionID
}

// UnitID returns the stable ID of a unit reference, or "" when the core has
// not resolved it or o is not a unit.
func (o Object) UnitID() string {
	return o.unitID
}
