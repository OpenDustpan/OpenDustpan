## ADDED Requirements

### Requirement: set-tag

The server SHALL create, move and delete revision tags through SetTag with latest fixed on the newest revision that is not retracted.

#### Scenario: Create and delete ordinary tag
- **WHEN** A caller with profile:write creates a tag with SetTag and then deletes it.
- **THEN** The tag names the revision, then the tag is gone and the audit entries hold each change.

#### Scenario: Move tag to older revision
- **WHEN** A caller with profile:write moves a tag with SetTag to an older revision that is not a routing change.
- **THEN** The tag names the older revision and the audit entry holds the revision before and after.

#### Scenario: Routing tag move needs open
- **WHEN** A caller without profile:open moves a tag with SetTag where the own-document taint reaches a secret source in any export key.
- **THEN** The server refuses with profile.routing-change-needs-open.

#### Scenario: Tag to retracted or unknown fails
- **WHEN** A caller moves a tag with SetTag to a retracted or unknown revision.
- **THEN** The server refuses with profile.conflict for a retracted target and with profile.not-found for an unknown revision or tag.

#### Scenario: Tag limits and names
- **WHEN** A caller creates a 33rd tag or a badly named tag with SetTag.
- **THEN** The server refuses with profile.limit for the 33rd tag and with profile.invalid for a bad name, a tag named latest, or a tag that reads as a revision number.

#### Scenario: Latest is fixed
- **WHEN** A caller creates, moves or deletes the tag latest with SetTag.
- **THEN** The server refuses with profile.invalid.

#### Scenario: Concurrent tag move
- **WHEN** Two callers move the same tag with SetTag at the same time.
- **THEN** One move wins and the other refuses with profile.conflict.

### Requirement: labels

The server SHALL store 0 to 32 labels per profile through SetLabels and answer each key with its values.

#### Scenario: Set and list by label
- **WHEN** A caller with profile:write sets 0 to 32 pairs with SetLabels on a profile.
- **THEN** GET profile-labels answers each key with its values and GET profiles with query labelKey and labelValue answers only profiles that hold every named pair.

#### Scenario: Labels replace and validate
- **WHEN** A caller sets labels with SetLabels where each key and value has 1 to 63 characters of a-z 0-9 . _ and - and starts with a letter.
- **THEN** The set replaces the old set, and a bad key, bad value, or overlong entry is refused with profile.invalid.

#### Scenario: Too many labels
- **WHEN** A caller sets a 33rd pair with SetLabels, where the count counts pairs.
- **THEN** The server refuses with profile.limit.

### Requirement: rollback

The server SHALL save a rollback through SaveRollback as a new revision under the rules of a save.

#### Scenario: Rollback saves new revision
- **WHEN** A caller rolls back with SaveRollback to an earlier revision that is not retracted.
- **THEN** A new revision holds the copied document and rotator state stays current, with no change rule so the save applies directly.

#### Scenario: Rollback routing change needs open
- **WHEN** A caller without profile:open rolls back with SaveRollback where the own-document taint reaches a secret source in any export key, and referrer taint is not checked in this slice.
- **THEN** The server refuses with profile.routing-change-needs-open.

#### Scenario: Stale rollback
- **WHEN** A caller rolls back with SaveRollback on a stale newest revision read.
- **THEN** The server refuses with profile.conflict.

### Requirement: retract

The server SHALL retract a revision through Retract only with a reason and only when it is not the newest revision.

#### Scenario: Retract moves tags
- **WHEN** A caller retracts with Retract a revision that is not the newest and not already retracted with a reason of 1 to 500 characters.
- **THEN** The revision is closed to open and import and its tags move to the named replacement that is not retracted, or to the newest revision that is not retracted when no replacement is named, while latest still names the newest revision that is not retracted.

#### Scenario: Retract newest fails
- **WHEN** A caller retracts with Retract the newest revision.
- **THEN** The server refuses with profile.conflict.

#### Scenario: Retract already retracted fails
- **WHEN** A caller retracts with Retract a revision that is already retracted.
- **THEN** The server refuses with profile.conflict.

#### Scenario: Bad replacement fails
- **WHEN** A caller retracts with Retract with an unknown or retracted replacement.
- **THEN** The server refuses with profile.not-found for an unknown replacement and with profile.conflict for a retracted replacement.

#### Scenario: Open retracted fails
- **WHEN** A caller opens or imports a retracted revision.
- **THEN** The server refuses with profile.not-found and answers no secret value.

#### Scenario: Retract races save
- **WHEN** A retract with Retract races a save on the same profile.
- **THEN** One write wins and the other refuses with profile.conflict.

### Requirement: diff

Where two revisions are compared through DiffRevisions, the server SHALL answer changed or unchanged for each secret path with no secret value unless showSecrets with profile:open.

#### Scenario: Diff hides secrets
- **WHEN** A caller with profile:read diffs with DiffRevisions two revisions that are not retracted without showSecrets.
- **THEN** The answer marks each secret path changed or unchanged with no secret value and no hash value and marks each non-secret entry changed or unchanged by value.

#### Scenario: Diff with secrets needs open
- **WHEN** A caller without profile:open asks for a diff with DiffRevisions with showSecrets.
- **THEN** The server refuses with profile.routing-change-needs-open and answers no secret value.

#### Scenario: Diff same revision
- **WHEN** A caller diffs with DiffRevisions a revision against itself.
- **THEN** The answer holds no changed path.

#### Scenario: Diff retracted fails
- **WHEN** A caller diffs with DiffRevisions a revision that is retracted.
- **THEN** The server refuses with profile.conflict.

### Requirement: fence-audit-backup-notify

The server SHALL carry the namespace fence on each tag, label, rollback and retract write and append its audit entries in the same transaction.

#### Scenario: Namespace object check
- **WHEN** A caller calls SetTag, SetLabels, SaveRollback, or Retract.
- **THEN** The server checks profile:write on the namespace object until #1008 ships the profile object.

#### Scenario: Fence and audit refusal
- **WHEN** A caller writes with a stale fence or without the needed grant.
- **THEN** The write is refused and the audit entry records the refusal with actor, action and reason.

#### Scenario: Backup and notify
- **WHEN** A caller completes a tag, label, rollback or retract write.
- **THEN** New tag, label, rollback and retract rows are in the namespace backup and portable spec, and the server emits profile.tag-changed, profile.label-changed and profile.revision-retracted.
