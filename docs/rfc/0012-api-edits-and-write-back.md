---
rfc: 12
title: API edits, and write-back to git
status: draft
created: 2026-10-10
---

# RFC 12. API edits, and write-back to git

Status: draft. The names, the scope and the principle are proposals.

## Problem

Today a team changes a unit in one way: edit files in git, then run the tool.
Some changes do not need a person in an editor: a new instance size, one more
replica, a feature flag, a new region in a list. Teams want to make these
changes from the console, the API or a chat bot ("API ops"). They also want
git to stay the record of what they deploy ("GitOps").

These two wants conflict. If the console writes a value somewhere that git
does not know about, git stops being the record. If git is the only place, the
console cannot change anything.

Other products give part of an answer:

- Pulumi Cloud can keep a stack's config in the service. The stack points to
  an ESC environment (`PUT /api/stacks/{org}/{project}/{stack}/config` with an
  `environment` reference), and the CLI uses that config instead of
  `Pulumi.<stack>.yaml`. `pulumi config env init` moves existing config into
  an environment. Pulumi Deployments and the Automation API can then change
  config and run updates with no edit in git.
- HCP Terraform and Terraform Enterprise keep workspace variables and
  variable sets in the service. They apply only to remote runs.
- Atlantis plans each pull request, applies on a comment, and merges after the
  apply succeeds.

None of them writes a change back to git. So a team chooses: config in the
service and git is not the record, or config in git and no API edits.

## Principle

**Each value has one home.** A value lives in git or in a profile (RFC 5),
never in both. OpenDustpan never keeps two copies and syncs them.

An API edit goes to the value's home:

- If the home is a profile, the edit is a new version of the profile.
- If the home is git, the edit is a commit. OpenDustpan keeps no copy of the
  value. Git stays the record, and the API becomes one more author of
  commits.

This is what makes write-back sane. Two-way sync between a database and a
repository has no correct answer when both sides change. One home and one
writer path has one.

## Proposal

### 1. Unit config in a profile

- Serve the Pulumi CLI's stack config endpoints in the Pulumi
  Cloud-compatible API. The `environment` reference names a profile. A stack
  that uses it needs no config values in `Pulumi.<stack>.yaml`.
- For OpenTofu, serve workspace variables and variable sets in the
  TFE-compatible API, backed by profiles. As RFC 5 says, OpenTofu reads them
  only in a run that a runner does (RFC 6). A workstation run does not.
- Every edit makes a new profile version. A Pulumi update or an OpenTofu run
  records which version it read.

### 2. Forge connections

Remote runs (RFC 6) need git integration anyway, with or without write-back:

- A webhook from the forge starts a job on a push or a review request.
- The backend posts a status and the plan result on the review request.
- In apply before merge, the backend merges the review request.

So a **forge connection** is part of the core: an app installation (a GitHub
App, a GitLab application, a Forgejo token) that the backend holds, one per
namespace or more. The backend uses it to read files and post results. It can also give a runner a
short-lived, read-only token for one repository, so that a runner needs no git
credentials of its own.

**Every forge is an adapter behind a `Forge` port** (ADR 2). OpenDustpan
ships adapters for the most-used forges. For any other forge, a team writes an
adapter and builds its own `main` with the SDK, as for any other port. The
port gets a conformance suite (`forgetest`) before the first adapter, and
every adapter runs it.

**Writes go only through the forge's API.** OpenDustpan never pushes with a
git client, over SSH or HTTPS. A forge without an API for commits and review
requests gets no write-back.

**Push access is optional.** A forge connection without write access to
repository contents gives remote runs and nothing more. Write-back is off for
it.

### 3. Write-back to git

- A **code location** links a unit to a repository on a forge connection, a
  ref and a directory (see "Code location" in `GLOSSARY.md`). "Source" means
  the backend of a unit before an import.
- A **write-back** is an API edit whose home is git. The backend turns it into
  a commit on the unit's code location.
- **The backend commits through the `Forge` port, never through a runner.**
  Writing a data file is not running an infrastructure-as-code tool, so RFC
  6's principle does not apply. The forge APIs create a commit without a clone (GitHub's contents and Git
  data APIs, GitLab's commits API with actions, Forgejo's contents API). So
  the backend needs no git binary, no working copy and no runner. The edit
  lands in seconds, even when no runner is online.
- **Delivery.** A write-back goes as a **review request** by default: a new
  branch, and a pull request or merge request. A direct push to the ref of the code
  location is an option per code location. The forge's branch protection decides what the
  connection can push to. OpenDustpan does not go around it. On GitHub, write
  access to contents covers every branch, so only branch protection or
  rulesets keep the connection off `main`.
- **Only structured files.** Write-back edits data files, never program code:
  - Pulumi: `Pulumi.<stack>.yaml` config keys. Secret values are written as
    ciphertext from the `secret` package, which is how the Pulumi CLI keeps
    them in git today.
  - OpenTofu: one variables file per workspace, in JSON
    (`*.tfvars.json`), named by the code location. JSON needs no formatter and loses
    no comments. Editing hand-written HCL `.tfvars` is a later option.
  - OpenTofu has no encrypted form for a variable in a file. So a secret
    OpenTofu variable can never have git as its home. Its home is a profile.
- **Conflicts.** The forge APIs check the base for us: GitHub's contents API
  takes the blob SHA of the file it replaces, and GitLab's commit actions take
  the last commit ID of the file. If the file changed since the edit started,
  the backend reads it again and applies the edit only when the edited keys
  did not change. Otherwise the edit fails with a conflict, and the console
  shows both values. A silent overwrite loses data, and the project blocks
  only on data loss or corruption (RFC 9).
- **Authorship.** The commit author is the principal who made the edit. The
  committer is the forge connection. A trailer links the commit to the
  write-back in OpenDustpan, and the console links back to the commit.
- **Risk.** With push access, the backend can change code that every pipeline
  on that repository runs. The access tokens and the forge app's key then
  guard more than the state. Review requests by default, branch protection,
  and an audit entry for every write-back limit this. They do not remove it.

### 4. What a run applied

Each Pulumi update and OpenTofu run records the commit it ran on, when that is
known:

- A job on a runner always knows its commit.
- The Pulumi CLI sends git details with an update when it runs in a git
  checkout. The server can read the commit from them.
- An OpenTofu run from a workstation sends no git details through the
  TFE-compatible API. In TFE, `ingress-attributes` are filled only for
  VCS-driven configuration versions.

With this, the console can show "main is 3 commits ahead of what is applied"
for each unit.

### 5. Existing projects

Write-back must work on a repository that OpenDustpan did not create. An
operator links each unit to a code location. For Pulumi stacks, the git details of
past updates can suggest the repository. They may not give the directory.
For OpenTofu workspaces, the operator gives everything, or links many
workspaces at once by a naming rule.

## Apply after merge, or apply before merge

When a review request exists, two orders are possible:

1. **Apply after merge** (default). A runner plans on the review request and
   applies after the merge. Git's main branch is the record of what the team
   asked for. If the apply fails, main is ahead of the infrastructure until
   someone fixes it.
2. **Apply before merge** (Atlantis-style, an option). A runner plans on the
   review request, applies on approval, and OpenDustpan merges through the
   forge's API after the apply succeeds. Main only gets changes that applied.
   The costs:
   - Code that is not on main reaches production.
   - Two review requests on one unit must not apply in turn on stale plans. So
     the first one to plan holds the unit until it merges or closes.
   - A revert is a new apply, not a `git revert` alone.

This RFC does not judge which order is right. Teams that use Atlantis chose
option 2 for real reasons. Following RFC 9's posture, OpenDustpan gives the
choice per code location and makes option 1 the default.

The two tools differ in one place that matters:

- **OpenTofu** has a workspace lock that lasts until someone releases it. A
  review request can hold it, and a CLI on a workstation gets "locked by"
  until the merge. `tofu apply` on a saved plan refuses a stale plan.
- **Pulumi** has only a lease, which lasts one update. Nothing in the protocol
  holds a stack between two updates. OpenDustpan would need a hold of its own,
  and would refuse to start other updates on the stack while the hold exists.
  The Pulumi CLI's update plans (`--save-plan`, `--plan`) might give a check
  like OpenTofu's saved plans, but they are experimental in the CLI.

## Options

1. **Profiles, and write-back by the backend through a forge connection,
   with review requests by default** (the proposal).
2. **Do nothing.** Edits happen in git only. The console only reads. This
   stays valid, and every part of the proposal is a feature that a team can
   turn off (RFC 7).
3. **Profiles only.** API edits go to profiles, never to git. This is the
   Pulumi Cloud model. It is simple, and it moves the record out of git for
   every value the console edits.
4. **Write-back with a git client.** The backend clones and pushes over SSH
   or HTTPS. It works with any git server, but it needs a git client and a
   working copy in the backend, and a plain git server has no review requests.
5. **Write-back by a runner.** A runner with its own git credentials clones,
   edits and pushes. The backend then needs no push access. But an edit waits
   for a runner, the runner needs a git client and a working copy, and the
   backend still needs a forge connection for remote runs.
6. **Two-way sync.** The server keeps a copy of the config and syncs it with
   git in both directions. This breaks the principle above. It is listed to
   record that it was considered and rejected.

## Proposed glossary entries

**Forge**:
A git hosting service with an API, such as GitHub, GitLab or Forgejo.
_Avoid_: VCS provider, SCM, git host

**Forge connection**:
The credentials that let the backend use one forge for a namespace: read
files, take webhooks, post results and, if allowed, push.
_Avoid_: VCS integration, app (alone)

**Code location**:
The repository on a forge connection, the ref and the directory that hold the
program and config of a unit. `GLOSSARY.md` now holds this term.
_Avoid_: Source (that is the backend of a unit before an import), repo (in docs), VCS connection, origin

**Write-back**:
An edit made through the API to a value whose home is git. The backend
delivers it as a commit through a forge connection.
_Avoid_: Sync, push, commit-back

**Review request**:
A proposed change in a forge that people review before a merge: a GitHub pull
request or a GitLab merge request.
_Avoid_: Pull request, merge request (except when naming one forge's feature),
PR, MR

**Hold**:
A claim on a unit that stops other updates or runs from starting until the
holder releases it. It lasts longer than a lease.
_Avoid_: Lock (for Pulumi stacks), reservation

## Open questions

- Which forges come first: GitHub, GitLab, Forgejo? Each one is an adapter
  behind a port (ADR 2).
- Which methods does the `Forge` port need first? A small port is easier to
  implement: read a file at a ref, commit file changes on a base, open a
  review request, post a status, merge, check a webhook's signature.
- Does a team ever need a forge adapter without building its own binary? That
  needs a runtime plugin mechanism (for example a separate process over
  RPC), which ADR 2 does not have. It would be a new RFC.
- How does the backend edit `Pulumi.<stack>.yaml` and keep comments and key
  order? This may need a YAML library with a node API, which is a new
  dependency.
- Does a namespace have one forge connection, or many (one per forge, or one
  per group of repositories)?
- Where does the forge app's private key live: in the database, encrypted by
  the key-encryption key, or outside it like the key-encryption key itself?
- Can a write-back touch more than config? Pulumi YAML programs and OpenTofu
  `import`, `moved` and `removed` blocks are structured. Each is a later RFC.
- Is a hold its own concept in the core, or a feature of each tool package?
  OpenTofu already has workspace locks, and Pulumi has nothing like it.
- When one edit changes many units (a value in a shared file), is that one
  write-back or many?
- Does the console show a value's home next to the value, so that people know
  whether an edit makes a commit?

## Gaps from the competitor review

Added 2026-10-10 from the research doc "IaC and TACOS landscape". Not yet
worked into the proposal above.

- More forges behind the Forge port: Bitbucket Cloud and Server, Azure DevOps,
  GitHub Enterprise (Atlantis, Terrakube, Terramate).
- Unit settings kept in the repository per branch, such as
  `.spacelift/config.yml` (Spacelift, env zero), within the one-home rule.
- Pull request details are RFC 27; stored plans are RFC 28.

## Outcome

Accepted in part as [ADR 213](../adr/0213-job-tokens-no-change-plans-bulk-calls-forge-connections-and-locks.md)
on 2026-10-18, as the project manager decided under the owner's decision rule: the credentials of a forge connection are sealed
credentials of the namespace, not a setting, with rotation and a logged
fallback (decision 7), and a namespace has many forge connections with one
default (decision 8). The other questions stay open.
