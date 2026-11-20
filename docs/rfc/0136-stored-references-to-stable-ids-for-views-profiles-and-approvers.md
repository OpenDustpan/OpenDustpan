---
rfc: 136
title: Stored references to stable IDs for saved views, profiles and approvers
status: discussing
created: 2026-11-20
---

# RFC 136. Stored references to stable IDs for saved views, profiles and approvers

Status: discussing. The owner has decided the rule (owner, 2026-11-20, see "The rule that this RFC applies"). This RFC does not decide the rule again. It gives the form of the change for three stored references that name an entity by its name: the unit references of a saved view, the profile references, and the `users` of an approver list. It also removes one stale shim. This RFC edits no ADR. The ADR that follows is a separate pull request, and the owner accepts it. The audit of main (`9b0990bd3`) is read-only.

## The rule that this RFC applies

[ADR 362](../adr/0362-owner-decisions-of-8-november-2026-stable-ids-store-order-mysql-baseline-hashes-and-portable-backups.md) decision 1 (owner, 2026-11-08):

> Every stored reference to an entity uses the stable ID of the entity when a user can rename the entity, or delete and recreate it. A name is a label for people only.

The owner applied it again on 20 November 2026 (recorded as ADR 391 in pull request #3883, which is pending, so this RFC cites it as "ADR 391 (PR #3883, pending)"):

> Stable ID always. No entities should be keyed by name.

ADR 391 (pending) decision 2 says: "A name never decides a match, a grant or a rule. The store may keep a name next to the ID for display." ADR 391 (pending) "Not decided here" says that the other stored references by name "need their own ADR or RFC". This RFC is that RFC for the three items below. [RFC 133](0133-the-form-of-the-move-from-names-to-ids-in-limits-and-gates.md) is the same question for limits, gates and exemptions. This RFC does not cover them.

## What this RFC covers

| # | Stored reference | Where it names by name today |
| --- | --- | --- |
| 1 | The unit references of a saved view | `pkg/savedview/savedview.go:105`, stored in `saved_views.query_json` |
| 2 | The references to a profile | six places, listed in "Item 2" |
| 3 | The `users` of an approver list | `pkg/gate/rules.go:156`, stored in the JSON body of the approval objects |
| 4 | The shim for approval-rule groups saved before the IDs existed | `pkg/gate/rules.go:152` and three more lines, listed in "Item 4" |

It does not cover, and only names, these related items: a Cedar policy that names a group (ADR 362 "Follow-ups for decision 1" and [RFC 133](0133-the-form-of-the-move-from-names-to-ids-in-limits-and-gates.md)), the scope key of a collection (see "Not covered"), and the machine approvers of [RFC 134](0134-machine-approvers-for-job-approvals.md).

## Problem

A user can rename a unit or a profile, and can delete one and make it again with the same name. A stored reference by name then follows the name and not the entity.

- A view that names `unit:...web/prod` selects the new unit that someone made after a delete. A rename of the stack ends the match with no error (ADR 266 decision 4 says this on purpose).
- A profile reference by name needs a block (the rename of a profile fails while something names it, ADR 202 decision 9), and a reference by name still follows a deleted-and-recreated profile to other secrets.
- A person that a rule names by subject (`alice`) is a different person when the principal `alice` is deleted and an identity provider makes a new principal with the same subject. The new principal inherits the approver entry. A principal record says: "Subject ... is unique among the principals that exist" (`pkg/principal/principal.go:166`), so a subject can come back.

The three cases are the failure that ADR 362 decision 1 names. ADR 391 (pending) shows that a pending code change had the same failure. OpenDustpan has no users and no installation, and no tag shipped these shapes, so no conversion code is written (ADR 322, "No migrations or compatibility code before the first tag"; `CLAUDE.md`, "SQL").

## The form that all three items share

One form for all items, so a reader learns it once. It is the form that approver groups use on main today (`pkg/gate/config.go:39-59`, `pkg/gate/approvers_check.go:39-70`).

1. **A caller names the entity at the edge.** A request body takes the name that a person writes (a unit reference, a profile name, a user subject). The edge is the HTTP handler and the service that the handler calls. It is the only place that turns a name into an ID.
2. **The edge resolves the name once, and fails closed.** The service looks up the entity that has the name now, in the namespace of the request. It stores the ID. A name that matches no entity is an error, and nothing is stored. The service never keeps the name as the key.
3. **The stored reference holds the ID, and may hold the name that the entity had at the save.** The name is for display only. It is never read for a match, a grant or a rule. A reader that needs the current name asks the entity by its ID.
4. **The answer shows both.** The response holds the ID that the edge chose, and the current name of the entity. A person can see which entity the server bound ("No magic", `CLAUDE.md`). The binding is explicit, not a silent rule.
5. **A match, a read and a check use the ID only.** An ID that no entity has any more matches nothing and grants nothing. The read shows the reference as missing, with the stored ID and the stored name, so a person can see what was lost.
6. **A client can also send the ID.** The request accepts the ID in place of the name. An ID that does not exist is an error. This is how a script that holds the ID avoids a second lookup. See open question 1 for the shape of the field.

Option "ID only, refuse a name at save" is the form that RFC 133 proposes for limits and gates. See "Options" and open question 1: both forms stay open until the owner chooses one rule.

## Item 1. Saved views

### Current shape

- `savedview.Query.References` is `[]string`, "unit references in the namespace of the view" (`pkg/savedview/savedview.go:98-106`). `validateReferences` parses each with `identity.ParseObject` and refuses a reference that is not a unit of the view's namespace (`pkg/savedview/savedview.go:142-170`).
- The JSON form is `{"labels": [...], "references": ["<unit reference>"]}` (`pkg/store/internal/viewstore/viewstore.go:28-31`), in `saved_views.query_json` (`pkg/store/sqlite/migrations/0168_baseline.sql:982-989`; the PostgreSQL baseline has the same table at `pkg/store/postgres/migrations/0168_baseline.sql:1296`). A unit reference has the tool, the namespace and the name of the unit.
- The unit list matches a stored reference by name at each read: `filterOfView` turns each reference into a `UnitName` (`internal/api/inventory/views.go:433-466`), and `unitFilter.matches` compares the tool and the name of an entry (`internal/api/inventory/filters.go:35-49`).
- ADR 266 decision 4 (PM decision): "A reference holds the tool of the unit. A unit that moves to another tool no longer matches the reference". The owner of a view is already an ID (`prn-...`, `pkg/savedview/savedview.go:171-179`), and ADR 266 decision 2 says why.

A unit has a stable ID today. `inventory.Entry.UnitID` is "the unit's stable, opaque identifier, for example `stk-...`. It lets a consumer follow a unit through a rename" (`pkg/inventory/inventory.go:26-38`), and the unit list answer carries it as `unitID` (`internal/api/inventory/inventory.go:311-313`). The unit scope of a gate already keys its storage on it (`pkg/gate/scope.go:76-88` and the `CHECK` at `pkg/store/sqlite/migrations/0168_baseline.sql:1676`).

### The stable-ID shape

- **Stored:** `savedview.Query.References` becomes a list of unit reference entries. Each entry holds `UnitID` (the stable ID, required), and `Reference` (the unit reference text at the save, for display only). The JSON becomes `{"labels":[...],"references":[{"unitId":"stk-...","reference":"<unit reference>"}]}`. `decodeQuery` still refuses a field that it does not know.
- **At the edge:** the create route (`POST .../namespaces/{namespace}/views`) takes a reference entry with a `reference` (a name) or a `unitId`, never both. The service looks up the unit in the namespace of the view, through the inventory port that the unit list uses. Unknown unit: `400` with the code `view.invalid` (the existing code, `errcode.ViewInvalid`) and the text "no unit has the reference <reference> in this namespace: list the units with `GET .../units`". A reference of another namespace stays refused (`validateReferences`), so the check does not tell a reader about a unit of another namespace (ADR 220 decision 11).
- **At the match:** `filterOfView` builds a set of unit IDs and `unitFilter` compares `Entry.UnitID`. It no longer compares a tool and a name.
- **In the answer:** each reference shows `unitId`, the current `reference` of the unit, and `status` with the value `present` or `missing`. A `missing` unit shows the stored reference text. The matching of a view with a `missing` reference selects nothing for it, and a label filter still selects what it selects.
- **Defense in depth:** the store check of a stored row (`viewstore.View`) validates each `unitId` as the ID grammar of a unit (`pkg/gate/scope.go:163-170` has the grammar for the unit scope) and refuses a row that fails (`ErrCorrupt`), since a backup, a restore or another version can write the row.

### Rename and recreate

| Event | Before | After |
| --- | --- | --- |
| The stack is renamed | the view stops selecting it, with no message | the view still selects it, and shows the new name |
| The stack is deleted and made again with the same name | the view selects the new stack | the view shows the old unit as `missing` and selects nothing for it |
| A unit moves between collections | no effect | no effect (ADR 346: units never move between namespaces) |

The sentence in ADR 266 decision 4 about a unit that moves to another tool has no case left to describe: a unit ID belongs to one tool. The ADR that follows changes that sentence.

### Tests

A table of hostile cases for the service, the store codec and the HTTP handler (ADR 250): a reference to a unit of another namespace, a unit ID of another namespace, a unit ID with a hidden character, a forged `unitId` that no unit has, a view whose unit is deleted and made again, and a stored row with a name and no ID. Each guard has a test that fails when the guard is removed. The storetest suite for `saved_views` (`pkg/store/storetest/portable_saved_view_limits.go`) gets the missing-unit row.

## Item 2. Profiles

### Current shape, place by place

A profile has an ID (`prf-...`) and a name today (`pkg/profile/profile.go:82-91`; `profiles.id` and `profiles.name` at `pkg/store/sqlite/migrations/0168_baseline.sql:714-725`; the name is unique among live profiles, `profiles_live_name` at line 1826). A route takes either (`pkg/profile/name.go:99-123`, `Ref`). The references to a profile are these.

| # | Place | Shape today | Stored today? |
| --- | --- | --- | --- |
| a | The `imports:` list and the `profiles[...]` reads of a profile document | The name, with an optional `@tag` (`pkg/profile/document_check.go:90-124`, `pkg/profile/exports.go:133`). It is text that a person or the Pulumi CLI wrote (ADR 209). | Yes, in `profile_revisions.document` |
| b | The scope key of a profile | `profile/<namespace>/<profile>` with the name (`pkg/gate/scope.go:60-70`, `ProfileScope` at line 189, "the name is 1 to 300 bytes of valid UTF-8") | No stored configuration yet: `gate_objects.level` allows `installation`, `namespace`, `collection` and `unit` only (`pkg/store/sqlite/migrations/0168_baseline.sql:1674`), and no route holds a profile scope (`pkg/gate/config.go:322-336`). The key appears in decision records and requirement names. |
| c | The profile of an import | `imports.profile TEXT` (`pkg/store/sqlite/migrations/0168_baseline.sql:648`; `unitimport.Import.Profile`, `internal/unitimport/unitimport.go:472`). The only value that any code writes is a name: the test harness uses `"aws-read"` (`internal/unitimport/unitimporttest/harness.go:108`). `validate.go:131` checks only that it is not empty. ADR 241 decision 1 says "The import names a profile". No code resolves it yet, because the import service is not built. | Yes, a column |
| d | The profile of a blueprint | `Platform.Profile string` of the blueprint document (`internal/selfservice/blueprint.go:188`), for example `"profile": "aws-prod"` (ADR 112 decision 8, `internal/selfservice/testdata/postgres.blueprint.json:30`). No code checks that the profile exists. | Yes, in the version document |
| e | The profile of a job | `job.Spec.Profile string`: "names the profile that supplies the secrets of a runner job. The server fixes it at start" (`pkg/job/job.go:317-320`); it reaches the runner in the assignment (`internal/api/runnerapi/session.go:365`). The test value is an ID (`pkg/job/jobtest/create.go:32`: `prf-default`), and the type does not say. | Yes, in the job body |
| f | A profile attachment and the Catalog attachment | ADR 203 decision 4 says that an attachment stores the principal that made it; ADR 203 line 37 says that a grant, an attachment and a rule "can name a namespace, a collection or a unit". The attachment code is not on main. | Not built |

The rename route does not exist on main either: `pkg/profile/service.go` has `Create`, `Get`, `List`, `Revisions`, `Read`, `Save` and `Delete`, and `profile.ErrInUse` is not defined (`rg ErrInUse pkg/profile` finds nothing). ADR 202 decision 9 describes both. So the block on rename ("`profile.ErrInUse` while a profile, a blueprint or an attachment names the profile by its name") is a decision that no code holds yet.

### The stable-ID shape

- **Profile document (place a).** The text of a document stays names. A person writes `imports: [aws-base]`, and the Pulumi CLI sends names, because the document format and the routes of the tool-compatible API are contracts (ADR 209, ADR 362 "Follow-ups": "the real CLIs send names, so those routes stay"). The server resolves each import at save and stores the result next to the revision: `profile_revisions` gets a column `imports`, a JSON list of `{"profileId": "prf-...", "name": "<name as written>", "revision": <number or null>, "tag": "<tag or empty>"}`. A revision is immutable, so its stored list never changes. An open reads the stored list and never reads the name. A name that matches no live profile at the save fails the save with `profile.ErrInvalid` and the text "the profile <name> does not exist: list the profiles with `GET .../profiles`". The same resolution applies to the `profiles[...]` reads of a secret reference, which the save already checks (`pkg/profile/exports.go:478-560`).
- **Scope key (place b).** The scope carries the profile ID like a unit scope does (`WithUnitID`, `pkg/gate/scope.go:132-160`). The key text keeps the name for the route and the record. A store keys on the ID. When a change adds profile-scope configuration, it adds `profile` to the `level` check and a `CHECK (level <> 'profile' OR scope_key = 'profile/' || profile_id)`, as for a unit. This RFC changes no stored configuration, because none exists.
- **Import (place c).** `imports.profile` holds the profile ID. The column is renamed `profile_id` in both baselines, edited in place. The create of an import takes a profile name or ID and resolves it as in "The form that all three items share". The check of `profile:open` at each run, resume and claim (ADR 241 decision 1) uses the ID, so a profile that someone deleted and made again makes the import pause with `import.denied` and does not open the new profile.
- **Blueprint (place d).** The published version stores `platform.profile` as an ID. The author writes a name or an ID. Publish resolves it in the namespace of the blueprint and the stored document holds the ID. The blueprint document is a stored version that the platform team signs by publishing it, so the hash is over the stored document. See open question 4 for the case that a blueprint of the Catalog crosses namespaces.
- **Job (place e).** `Spec.Profile` holds the profile ID, set at the start from the unit settings or the blueprint. The type comment says so. The runner assignment carries the ID. This fixes the sentence "a later change ... cannot point a job at other secrets" against a delete and recreate too.
- **Attachment (place f).** The code that builds attachments stores the profile ID. No stored shape exists to change.

### The rename block of ADR 202 decision 9 goes away

ADR 202 decision 9: "**Rename** keeps the ID. It fails with `profile.ErrInUse` while a profile, a blueprint or an attachment names the profile by its name. The error names each one."

With IDs, no stored reference breaks at a rename, so the block has no reason. What replaces it:

- A rename needs `profile:manage` and writes an audit entry with the old and the new name, as other renames do. It keeps the ID, the revisions, the tags and the grants.
- The new name must be free among the live profiles of the namespace (`profiles_live_name`). A taken name answers `409`.
- An importer or a blueprint document that still says the old name keeps working, because the stored reference is the ID. The text that a person wrote keeps the old name. The read shows the current name next to it, as the shape above says. A new save of that text fails with "the profile <old name> does not exist", with the one command that helps (the profile list), and the person changes the text. This is the same failure as any other name that does not exist.
- **Delete stays blocked, and the block is by ID.** ADR 202 decision 9 says that a delete "fails with `profile.ErrInUse` while a profile imports it, an attachment names it, or a job that is not terminal reads it". That sentence stays. The referrers list (same decision) finds the referrers by ID, with the name next to each.
- **Restore** within the 30 days brings back the same ID, so every stored reference works again. A profile that someone makes again with the same name has a new ID and does not inherit them.

### Tests

A hostile table for each place: a rename of a profile that an importer names; a delete and recreate under the same name (an old import, a blueprint version, a job and a scope must not apply to the new profile); an ID of a profile of another namespace (answers `404`, ADR 220 decision 11); an import whose stored ID has no profile (fails closed at the open). Each guard has a test that fails when the guard is removed. A table row checks that a document that names a profile that does not exist fails the save.

## Item 3. Approver `users`

### Trace of `Approvers.Users` on main

Question: does `Approvers.Users` hold a subject or a user name, and is it checked at use?

**What it holds.** A `string` that the caller wrote. The comment of the type says "Approvers names who may approve: groups, users, and the owners of the unit" (`pkg/gate/rules.go:149-160`; `Users []string` is on line 156 and has no comment). The code that reads it treats it as a **subject**, the name that an authenticator answers for a principal (`pkg/principal/principal.go:166`, `identity.Principal.Subject`, `pkg/identity/identity.go:45`): `ApproverPolicy.CountsAsSignature(subject)` and `ApproverPolicy.Validate` test each `Users` entry with the dev-subject matcher (`pkg/gate/approvers.go:43-62` and `:143-153`); `NoApprover` is stored as a user (`pkg/gate/approvers.go:72`). The check of the text says "The name of a user from an identity provider can hold characters that an API subject cannot, so the check is not stricter" (`pkg/gate/approvers_check.go:195-215`), which shows that the author took the entry to be an identity-provider user name and a subject, not a principal ID.

**The write path.**

1. The route body: `approversBody.Users []string` (`internal/api/server/gate_config.go:109-115`), copied into `gate.Approvers.Users` (`internal/api/server/gate_config.go:143-145`, `GroupIDs: nil`). The handler refuses a hidden character (`gate_config.go:152-162`, called at `:362` and `:559`).
2. The config service: `ConfigService.checkApprovers` (`pkg/gate/approvers_check.go:39-70`, called at `pkg/gate/config.go:161` and `:273`) calls `countUsers` (`approvers_check.go:105-117`), which calls `checkUserName` (`:199-225`): UTF-8, no white space at the ends, no hidden character. **It does not look up a principal.** An unknown subject is accepted, and the entry counts as one person for the count check (`approvers_check.go:63`).
3. The store: `approverChecker.check` runs `ApproverPolicy.Validate`, which refuses `NoApprover`, a leading `!`, and on a production installation a dev subject (`pkg/store/internal/gatestore/approvers.go:44-51`, `pkg/gate/approvers.go:43-62`). The JSON body of `gate_objects` holds the list (`pkg/store/internal/gatestore/codec.go:35-59`).

**The check at save.** Only the shape checks above. No check that a principal has the subject, none that a person holds the approve action, none for the count against the people that the users name (the count check takes `len(users)` as the people that can approve).

**The check at approval time.** None on main. Three reads prove it:

- `jobgate.gatesOf` refuses each requirement that names a user: "the requirement ... names users or unit owners, and a job enforces groups only; name groups instead" (`pkg/gate/jobgate/jobgate.go:178-180`). So a job never gets a gate with `Users`. `job.Gate` has no field for a user (`pkg/job/gate.go:89-125`).
- `Service.Approve` checks the actor with `checkApprover` (`pkg/job/service_approve.go:209-262`) and `checkGroups` (`:277-320`). It tests the subject of the approver against the group IDs with `MemberOfAny(ctx, subject, g.GroupIDs)` (`:304`; the port at `pkg/job/service.go:112-116`). No code compares the approver with a user entry.
- The state-write gate builds the requirement with `ruleApprovers` (`pkg/gate/statewrite.go:693`, `pkg/gate/approvers.go:155-175`). It strips dev subjects and tests `NamesNobody`. It matches no signer against `Users`.

**Result.** `Approvers.Users` holds a subject, as free text. It is not revalidated at use, because no code uses it to match an approver. The entry is saved, shown in the decision record (`pkg/gate/record.go:106`, the `Requirement` carries the whole `Approvers`), and refused by the job path. A subject that is deleted and made again is the same string, so any future matcher by subject would admit the new principal. The OpenSpec change `several-approval-requirements-one-change` (Part B, design decision 11, task 5.1) plans the first matcher and designs it by ID, which this RFC supports. The matcher does not exist on main, so no code needs to move.

### The stable-ID shape

- **Stored:** each approver user entry holds the stable principal ID (`prn-...`, `pkg/principal/principal.go:166`) and the subject at the save, for display. `NoApprover` stays the one value for internal use, and stays refused at the write path.
- **At the edge:** the request takes `users` as subjects (the field that exists), and the config service resolves each to the principal that has the subject now, through a new method on the port that already resolves groups (`ApproverDirectory`, `pkg/gate/config.go:48-60`): `ResolvePrincipal(ctx, subject) (PrincipalRef, error)`. The service stores the ID and the subject, and the answer shows both. A subject with no principal: `422` with a new reason `ApproversUserMissing` and the text "the user <subject> does not exist: make the principal first". A subject of a disabled principal is accepted and counts no one while it is disabled, as a disabled group member does (`approvers_check.go:61-67`).
- **At the approval:** the matcher maps the subject of the approver to its principal ID (the port `PrincipalIDs` of the OpenSpec change) and tests the ID against the stored IDs. It never compares the subject string. The approval-time check is the same check that a group member passes: the principal exists, is enabled, and has the ID in the entry.
- **At the read:** `currentNames`-style refresh: the answer shows the current subject of each ID (`approvers_check.go:73-100` does this for groups).
- **The record:** a decision record stores the IDs and the subjects at the time of the decision, so the record stays true after a rename or a delete.
- **Security note.** Who may a rule author learn exists? Group names already answer "the group does not exist" at save (`approvers_check.go:140-150`). A subject lookup tells a namespace administrator whether a principal exists in the installation. See open question 3. This RFC gives a recommendation only. It does not decide an approver semantic.

### Entry form: parallel lists or entries

`Groups` and `GroupIDs` are two lists that must be the same length (`pkg/gate/rules.go:193-205`). The shim of item 4 exists because they can differ. Adding `UserIDs` as a third list repeats the class. See "Options" for entries `{id, name}`.

## Item 4. The stale shim for groups saved before the IDs existed

On main the domain can hold a group name with no ID:

- `pkg/gate/rules.go:152-154`: "An entry of Groups with no ID here was saved before the IDs existed, and it counts no one."
- `pkg/gate/rules.go:193-198`: `validateGroupIDs` accepts an empty `GroupIDs` ("give one ID for each group, or none").
- `pkg/gate/approvers_check.go:73-80`: `currentNames` returns the approvers unchanged when `len(a.GroupIDs) != len(a.Groups)`: "A group with no ID, from a rule saved before the IDs existed, keeps its stored name too".
- `pkg/gate/jobgate/jobgate.go:186-190`: refuses a requirement "saved without group IDs ... save the rule again".
- `pkg/job/service_approve.go:298-299`: denies with "the gate names groups with no group IDs".

No tag shipped a rule saved without group IDs (ADR 322 decision 1; `CLAUDE.md`: "Do not write migration, upgrade, alias or data-conversion code for a shape that no tag shipped"). The config service always writes both lists (`approvers_check.go:48-52`). So the only source of a name with no ID is the request, before the service resolves it. That is input, not stored data.

**Change.** Split input from stored shape:

- The request type holds names only. The service turns it into the stored type, which holds IDs, and the stored type cannot hold a name with no ID.
- A read of a stored row whose lists differ in length is corrupt data: the store returns `ErrCorrupt` (as `viewstore` does) and the gate fails closed. It is never "counts no one" in silence.
- The five lines above lose their special case. `validateGroupIDs` no longer accepts "or none" in a stored value. The text "save the rule again" goes with it.

The entries form (Options, B) makes the corrupt state impossible to represent. The parallel lists form (Options, A) needs the check on read.

## Options

1. **A. The form of the group precedent, with parallel lists.** Name in, ID stored, names in a parallel list for display. This is `GroupIDs` today, design decision 11 of `several-approval-requirements-one-change` (`UserIDs`), and it adds the same form for views and profiles. Cost: parallel lists that can differ in length; the shim class stays and needs a check on read for each list.
2. **B. The form of the group precedent, with entries.** Name in, ID stored, in one entry `{id, name}`. The stored type has no way to hold a name with no ID or a length mismatch. Cost: a wire change of `approvers` (`groups` and `users` become lists of objects in the answer, and the console form changes) and a change of the codec of three stored objects (`approval-rule`, `approval-default`, `ownership-rule`). The tool-neutral APIs and the console may break before 1.0 (ADR 73), and no tag shipped the shape. This follows "Architecture over safeguards".
3. **C. ID only, refuse a name at save** (RFC 133 option 1). A caller sends only IDs. No resolution at the edge, no hidden rule, the smallest server code. Cost: a person must look up an ID before each write, and the Pulumi CLI sends names (profile documents), so item 2 place a cannot use it.
4. **D. Keep names, add a check at use.** The reference keeps the name and the ID that the entity had at the save. Each use refuses a mismatch. Cost: two fields for one reference, and a stale reference fails closed until someone saves it again. This is option 3 of RFC 133.
5. **E. Do nothing.** A rename or a recreate follows the name. This conflicts with the owner decision.

## Recommendation

Recommendation only. The owner decides, and nothing in approver identity is decided here.

- **Form:** B for the approver lists (entries), and the edge form of "The form that all three items share" for all three items. For saved views and profile documents, a name in the request is how a person works, and the answer names the ID that the edge chose. Option C for scripts is the same form with the `unitId` or `profileId` field.
- **Item 1:** entries with `unitId` and `reference`.
- **Item 2:** IDs in the stored places b, c, d, e and f. In place a, the document text keeps names, and the revision stores the resolved IDs. The rename block goes away, and delete stays blocked by ID.
- **Item 3:** entries `{id, name}` for `users`; one `ResolvePrincipal` at save; an ID match at approval time. This agrees with the owner decision as the OpenSpec change reads it ("The owner applied the same rule to users (2026-11-20)"). The change `several-approval-requirements-one-change` adopts this form in its task 5.1 or says why not.
- **Item 4:** split input and stored types, and fail closed on a mismatch.
- **Order:** the three items are independent and land as three code changes after the ADR. Item 4 lands with item 3, because both change the `Approvers` codec once.

## Effects

| Area | Effect |
| --- | --- |
| Baseline | `imports.profile` becomes `imports.profile_id`; `profile_revisions` gets an `imports` JSON column. `saved_views.query_json` and `gate_objects.body` change inside their JSON. Both dialect baselines are edited in place, in name order. No numbered migration and no conversion code. A dev installation runs `opendustpan dev reset` (ADR 324). |
| Pulumi and TFE-compatible APIs | None. Profile document text and the ESC-style routes keep names (ADR 209). |
| Tool-neutral API (views, approval rules, approval defaults, ownership rules, profile and import routes) | Request: unchanged for a name; accepts an ID. Answer: adds the ID and the current name. The `approvers` answer changes to entries under option B. Each break is allowed before 1.0 (ADR 73). |
| Console | The saved view list and the approver form show the name and the ID state (present or missing). The approver form lists principals and groups by name and posts names, as today, or IDs. A console change merges before the next server tag (`CLAUDE.md`, "The tool-compatible APIs are contracts"). |
| Docs | The guides of saved views, profiles and approval rules say that a rename keeps the reference, and that a deleted and recreated entity does not inherit it. `docs/guides/approval-rules.md:83` ("Who may approve") changes. The ADR 266 sentence about a unit that moves to another tool goes. |
| Backup and restore | IDs survive a portable restore, as unit IDs do. A restored reference whose entity is missing shows as missing and matches nothing. The dev strip of `Approvers.Users` (`pkg/store/internal/devstrip/approvers.go`) removes the entry as a whole under option B. |
| Audit and records | A rule save writes the resolved IDs. A decision record names the principal ID and the subject at the time. |

## Hostile cases that all three items share

Each case needs a test per layer that fails when the guard is removed. Layers: the HTTP handler, the service, the store codec and the matcher.

| # | Case | Expected |
| --- | --- | --- |
| 1 | Delete the entity and make it again with the same name | the old reference matches nothing and grants nothing |
| 2 | Rename the entity | the reference still points to it, and shows the new name |
| 3 | A request sends an ID that no entity has | refused at the edge; nothing stored |
| 4 | A request sends both a name and an ID, or neither | refused (`ErrInvalid`) |
| 5 | A request sends the ID of an entity of another namespace | `404`, with no name, ID or count of the other namespace (ADR 220 decision 11) |
| 6 | A stored row holds a name and no ID, or an ID with a hidden character | `ErrCorrupt`; fail closed |
| 7 | A restored backup holds a reference to an entity that the installation lacks | the reference shows as missing; no match |
| 8 | An approver user entry names a subject that a new principal now uses | the new principal fills no slot |

## Plain summary for 3 a.m.

Three places keep a name where they should keep the ID: a saved view lists units by name, profiles are named by name in imports, blueprints, jobs and rule keys, and a rule lists approvers by their login name. A rename or a delete and recreate then breaks the reference, or hands it to the wrong thing. The owner said that every stored reference is the stable ID and a name is a label. This RFC says how. People still type names. The server looks the name up once when you save, stores the ID, and shows you both. After that, only the ID counts. A rename changes nothing. A delete and recreate gives a new ID, so the old reference matches nothing and shows as missing. The block on renaming a profile in ADR 202 goes away, because nothing breaks at a rename. Deleting a profile that something still uses stays blocked. We also remove a leftover rule that lets an approver group without an ID exist in stored data, because no release ever shipped that shape. Nothing in the data needs conversion.

## ADR check

Searched with `docs/adr/INDEX.md`, then `rg` over `docs/adr` and `docs/rfc` for "stable ID", "by name", "profile", "saved view", "approvers" and "users". Owner-tagged decisions are marked.

| ADR | Decision line | Chain (from `INDEX.md`) | Effect |
| --- | --- | --- | --- |
| [362](../adr/0362-owner-decisions-of-8-november-2026-stable-ids-store-order-mysql-baseline-hashes-and-portable-backups.md) decision 1 (owner, 2026-11-08) | "Every stored reference to an entity uses the stable ID of the entity when a user can rename the entity, or delete and recreate it. A name is a label for people only." | Changed by: ADR 363 extends decision 2 only. Current rule for decision 1: ADR 362. | The rule that this RFC applies. "Not decided here" asks for "the form of the migration from names to IDs in each ADR and each code path". This RFC gives the form for three paths, and no migration, because no tag shipped them. |
| ADR 391 (PR #3883, pending) decisions 1 and 2 (owner, 2026-11-20) | "Stable ID always. No entities should be keyed by name." The store may keep a name next to the ID for display. | Pending. It changes ADR 273 decision 3. | The decision that this RFC applies. Cited as pending. If #3883 changes, this RFC follows it. |
| [266](../adr/0266-a-saved-view-is-a-server-object-with-an-api.md) decision 4 (PM decision under delegation, not owner-tagged; decision 1 is owner decision 40.3) | "A view stores labels and references only. A reference holds the tool of the unit. A unit that moves to another tool no longer matches the reference" | No change links. Extends ADR 17. | Changes the sentence about the tool and the name. Decisions 2 (owner of a view is an ID), 3 and 6 stay. |
| [202](../adr/0202-secrets-revisions-tags-drafts-and-rotation.md) decision 9 | "Rename keeps the ID. It fails with `profile.ErrInUse` while a profile, a blueprint or an attachment names the profile by its name." | ADR 222, 224, 225 change decisions 6 and 7; ADR 341 changes decision 2. Decision 9 is not changed. | The rename block ends. The delete block stays, by ID. |
| [225](../adr/0225-approval-rules-approval-defaults-and-ownership-rules.md) decision 1 (field list, line 41; owner-tagged "An approval rule is data (owner)") and decision 7 | "`approvers`: groups, users and `unitOwners`". "An eligible approver ... is in the `approvers` of its rule." | ADR 286 changes; ADR 299 changes decisions 1, 7 and 12; ADR 385 changes decision 5. | The field keeps its meaning: who may approve. The stored form of an entry changes. Decision 7 (who may approve by kind) is not touched. The PM named "decision 8, line 41": line 41 is in decision 1, and decision 8 is "Where requirements live". |
| [229](../adr/0229-decision-records-simulate-and-starter-packs.md) | A decision record holds "the requirements, with the eligible approvers, the count and the signatures so far" | ADR 286 changes. | The record holds IDs and the subjects at the time. A record never changes after it is written. |
| [220](../adr/0220-isolation-between-namespaces-on-shared-infrastructure.md) decision 11 (owner) | "An ID of another namespace answers 404 ... The answer holds no name, ID or count of the other namespace." | ADR 273 changes decision 12; ADR 286 changes; ADR 363 extends decisions 1 to 3. | Each lookup by ID is scoped to the namespace of the request, and a miss answers like an unknown ID. |
| [203](../adr/0203-opening-a-profile-delivery-actions-and-audit.md) decision 4 and line 37 | an attachment "stores that principal"; an attachment "can name a namespace, a collection or a unit" | ADR 210, 222, 225, 241, 286, 300, 353 change or extend other decisions. | The attachment stores the profile ID when the code is built. |
| [112](../adr/0112-blueprints-the-document-versions-and-expr.md) decision 8 | "`platform` holds ... the profile that supplies the secrets of a run" | ADR 222, 224, 228, 286, 360 change or extend others. | `platform.profile` is stored as an ID. |
| [209](../adr/0209-how-each-tool-reads-a-profile.md) | `{project}/{env}` is a profile name; "`@tag` and `@number` in an import name a revision" | Not changed. | The document text and the routes keep names. The server stores the resolved IDs next to the text. |
| [241](../adr/0241-an-import-is-an-api-resource-that-the-server-runs.md) decision 1 | "The import names a profile (ADR 200)" | ADR 248, 313, 343 change or extend others. | The import stores the profile ID. |
| [19](../adr/0019-a-stack-has-a-stable-id.md), [346](../adr/0346-a-unit-moves-between-collections-never-between-namespaces.md) | A stack has a stable ID. A unit never moves between namespaces. | Not changed. | A unit ID is stable through a rename and a collection move. |
| [73](../adr/0073-until-1-0-a-change-can-break-everything-except-the-tool-compatible-apis.md), [322](../adr/0322-no-migrations-or-compatibility-code-before-the-first-tag.md) (owner decisions 1, 2, 4, 5), [324](../adr/0324-a-schema-mismatch-refuses-and-opendustpan-dev-reset-starts-again.md) | APIs other than the tool-compatible ones may break; no migration or compatibility code before the first tag; `dev reset` for a mismatch | Not changed. | The basis for editing the baselines in place and for the answer changes. |
| [250](../adr/0250-defense-in-depth-for-security-code.md) (owner) | One test per layer for security code | Not changed. | Saved views, profile secrets and approvers are security code. The hostile tables follow it, and each of the three code changes needs the Opus security review (rev2). |
| [RFC 133](0133-the-form-of-the-move-from-names-to-ids-in-limits-and-gates.md), [RFC 134](0134-machine-approvers-for-job-approvals.md) | Limits and gates by ID; machine approvers named by stable ID in `machineIds` | draft; discussing | Related, not covered. RFC 134 uses principal IDs for machines (`prn-`). A person in a session uses `users` by this RFC. Both fields use the same ID. |

## Not covered

- A Cedar group-name policy, an access limit that names a group, an exemption by subject: ADR 362 "Follow-ups for decision 1" and [RFC 133](0133-the-form-of-the-move-from-names-to-ids-in-limits-and-gates.md).
- The scope key of a collection (`collection/<namespace>/<collection>`, `pkg/gate/scope.go:100-106`) keys the stored configuration of a collection by its name (`gate_objects.scope_key`, `pkg/store/sqlite/migrations/0168_baseline.sql:1663`). A collection can be renamed or deleted and made again. This is the same failure as the unit case, and ADR 391 (pending) lists collections for output sharing. It needs its own ADR or RFC. Listed so that it does not drop out of sight.
- Principal references in `gate_objects`, `access` grants and runner pools by name: ADR 362 "Follow-ups for decision 1" (ADR 52 decision 5, ADR 130 decision 2, ADR 137).
- Machine approvers: [RFC 134](0134-machine-approvers-for-job-approvals.md).
- How `UnitOwners` resolves owners from the context: design decision 12 of `several-approval-requirements-one-change`.

## Open questions

1. **One form for all items, and the shape of the field.** Does the owner accept "name in at the edge, the edge resolves once, the ID is stored" for views, profiles and approvers, while [RFC 133](0133-the-form-of-the-move-from-names-to-ids-in-limits-and-gates.md) proposes "ID only" for limits and gates? Two forms exist in the code base then (groups resolve a name at save today). The alternative is one rule for all: ID only (option C), with a lookup call that gives the ID. For the field: a request entry with `reference` or `unitId` (exactly one), or a name field and an ID field of its own. Recommendation: the edge resolves, and one field per form, as above.
2. **Parallel lists or entries.** Option A (parallel lists, as `UserIDs` in the OpenSpec change) or option B (entries). Option B changes the wire shape of `approvers`. Recommendation: B, with the OpenSpec change updated before its task 5.1 starts.
3. **Who may learn that a subject exists.** A save that resolves `users` by subject tells the writer whether a principal exists, as the group check does for a group name. Does the save need `principal:read`, or is the answer the same for "unknown" and "not allowed to know"? This is a security question and the RFC does not decide it. Recommendation: require the same read action that lists principals, and give one answer for both cases (ADR 220 decision 11).
4. **A blueprint of the Catalog that names a profile.** The profile belongs to a namespace, and a blueprint can be used by other namespaces (ADR 360). Which namespace resolves `platform.profile`: the namespace of the blueprint, or the namespace of the unit that uses it? The stored ID of the first reading cannot be opened by another namespace. Recommendation: the namespace of the consumer unit, at start, with a stored ID in the job and the name in the blueprint, because the secrets belong to the consumer. The owner or the lead confirms.
5. **A rename of a profile and the text of a document.** After a rename, an importer keeps working, and its text names the old name. Does the read show a warning, or rewrite the display name only, or must a rename list the importers whose text is now stale? Recommendation: show the current name next to the old one in the referrers list, and write nothing to an immutable revision.
6. **Existing dev data.** The baselines change in place. Does the dev docs page need a note ("run `opendustpan dev reset`"), as RFC 133 asks? Recommendation: one line in the dev guide, in the same pull request as the baseline edit.
7. **Where item 4 lands.** With item 3, in the same change of the codec (recommended), or as a separate bug fix with the evidence in the body. A single change avoids a second pass over `Approvers`.

## Checklist

- [x] Tolerates asynchronous replication between installations: each change adds no write path. A rule save, a view create and a profile save stay one write of one namespace, under its fence, with the audit entry in the same transaction. The lookup of a name and of a principal is a read before the write. No write waits for another installation (ADR 248 decisions 3, 6 and 9).

## Outcome

Empty until the RFC is accepted (link the ADR) or dropped (say why).
