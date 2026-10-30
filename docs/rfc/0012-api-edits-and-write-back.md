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

## Proposal for Milestone 5 (2026-10-30)

This section is ready to accept. It covers two parts of RFC 12 that
Milestone 5 needs: the code location in the unit settings, and the commit
of an API edit. It does not decide again what an ADR accepted. The facts
about other products are in the research note
[m5-precedents](../research/m5-precedents.md). Each fact there has a
mark that says whether its source was read on 2026-10-30.

### What is already decided

| Source | Decision |
| --- | --- |
| ADR 100 decision 5 | The server fixes the code location, the commit, the pool and the steps at the start of a job. |
| ADR 101 decisions 3 to 6 | Write-back runs in the server, through the `Forge` port, never with a git client. It edits only config files of the unit, does `apply` only, and the team's CI closes the job with the next final state version. |
| ADR 101 decision 3 | The trailer `Dustpan-Job: <job ID>`, the branch `dustpan/<job ID>`, and a lookup before each commit after a takeover. |
| ADR 103 decision 3 | OpenDustpan approvals add to the approvals of the Git host. |
| ADR 213 decisions 7 and 8, ADR 271 | Sealed credentials, many connections with one default, the `Forge` port with capabilities, the action `namespace:use-git-host-connection`. |
| ADR 270 decision 2 | Unit settings are not kept in the repository. Teams manage them through the API and the providers of OpenDustpan. |
| `job.Planner` (code) | The port that reads the settings of a unit at start exists. No adapter exists, and no store holds unit settings. |

### Part A: the code location in the unit settings

**The model.** A **unit settings** record belongs to one unit, by its stable
unit ID. It holds what the `job.Planner` reads at the start of a job:

| Field | Meaning | Default |
| --- | --- | --- |
| `executor` | `runner` or `write-back` | `runner` when the namespace has a runner pool for jobs, else `write-back` |
| `pool` | the runner pool for jobs | none: a runner job fails at start with a message that names the field |
| `codeLocation.connection` | the Git host connection | the default connection of the namespace (ADR 213 decision 8) |
| `codeLocation.repository` | the repository, as `owner/name` for GitHub | none |
| `codeLocation.ref` | the branch that the unit deploys from, the default branch of a review request | none |
| `codeLocation.directory` | the working directory, relative and clean | `.` |
| `writeBack.delivery` | `review-request` or `direct-push` | `review-request` |
| `writeBack.files` | the config files of the unit that a write-back can edit, each checked by the `Editable` rule of the step planner of its tool | the default file of the tool: `Pulumi.<stack>.yaml`, or `<workspace>.auto.tfvars.json` |
| `reviewRequests` | the settings of pull request automation (RFC 27) | off |
| `steps` | the custom steps (ADR 101 decision 8) | none: the default steps of the tool |

Each answer of the API shows the effective value of each field and where it
comes from: `unit`, `namespace default` or `built-in default` (CLAUDE.md,
"No magic").

**Questions and recommendations.**

1. **One code location, or many for one unit?**
   - (a) One code location for each unit. A module outside the directory is
     part of the commit, and the source snapshot holds the whole commit
     (ADR 135), so a plan reads it.
   - (b) A list of code locations, for a unit that reads two repositories.

   Precedent: HCP Terraform, Spacelift and env zero each give a workspace,
   a stack or an environment one repository, one branch and one directory.
   Atlantis gives a project one `dir` (all from the research note).
   *Recommendation: (a).* A module of another repository is a module
   source, which the git credential mode of the pool serves (ADR 130
   decision 11).
2. **Where settings live, and defaults above the unit.**
   - (a) A record for each unit, and namespace defaults for `pool` and the
     connection only.
   - (b) Settings at the namespace, the collection and the unit, merged
     like profile attachments.
   - (c) Settings in a file of the repository (`.spacelift/config.yml`
     style).

   Precedent: Spacelift and env zero read some settings from a file in the
   repository. HCP Terraform keeps them in the workspace and in project
   defaults. ADR 270 decision 2 refuses (c) for OpenDustpan.
   *Recommendation: (a).* A merge of three levels for each field is a rule
   that a person must learn to explain one job. The Catalog (blueprints)
   already gives shared settings to many units.
3. **A link of many units at once (RFC 12, "Existing projects").**
   - (a) The bulk call of ADR 213 decision 5 on the settings route, and the
     providers of OpenDustpan (ADR 270). A naming rule is a loop in the
     caller.
   - (b) A server-side naming rule that links each new unit.

   *Recommendation: (a).* A server-side rule is a hidden rule. The read
   route `GET .../settings/suggestions` answers the repository and the
   commit that the git details of the last Pulumi updates name, as a
   suggestion that a person accepts. It never sets a field.

**Actions and routes** (prefix of ADR 210, `/api/dustpan/v1`):

| Method and path | Actions |
| --- | --- |
| `GET /namespaces/{ns}/units/{tool}/{name}/settings` | `unit:read` |
| `PUT /namespaces/{ns}/units/{tool}/{name}/settings` | `unit:manage`, plus `namespace:use-git-host-connection` on a connection that the body names, plus `unit:define-steps` when `steps` changes, plus `namespace:read-runners` on the pool |
| `GET /namespaces/{ns}/units/{tool}/{name}/settings/suggestions` | `unit:read` |
| `PUT /namespaces/{ns}/settings/unit-defaults` | `namespace:manage` |

A `PUT` carries `If-Match` with the version of the record, so two writers
cannot overwrite each other. Each write checks the fence and appends one
audit entry with the old and the new value of each changed field. A change
of the code location or of `writeBack.files` needs the connection to hold
the capabilities that the settings use (`forge.Needs`): `files` and
`commit` for write-back, `events` for review request automation.

**Ports.**

- `pkg/unitsettings` (new domain package): `Settings`, `Store` (`Get`,
  `Put` with a version, `Delete` with the unit), `Validate`, and the
  conformance suite `unitsettingstest`. The store adapters run it in both
  dialects.
- The wiring implements `job.Planner` over `unitsettings.Store`, the step
  planner of each tool and the forge service, which resolves the head of
  `ref` to a commit (`Files.Head`).

### Part B: the commit of an API edit

**The flow** (ADR 101 decision 3, made concrete):

1. `job.Service.Start` with the executor `write-back` and the edits. The
   policy input holds the old and new values (ADR 213 decision 4).
2. Gates at `change.propose` and `change.apply` (ADR 224) run before the
   deliver phase.
3. The deliver phase reads each file at the base commit (`Files.ReadFile`),
   applies the edits key by key, and records the blob ID of each file it
   read.
4. It records the start of the step `commit`, then commits through
   `Commits.Commit` with the base commit and the expected blob IDs.
5. For `review-request`, it opens the review request (`Reviews.OpenReview`)
   on `dustpan/<job ID>`.
6. `Recorder.Delivered`, then `awaiting-state` until the merge and the
   final state version (ADR 100 decision 15).

**Questions and recommendations.**

4. **The GitHub API that makes the commit, and its signature (ADR 271
   question 4).**
   - (a) The GraphQL mutation `createCommitOnBranch`. One commit for many
     files, with `expectedHeadOid`, so the commit fails when the branch
     moved. GitHub signs a commit that a GitHub App makes this way and
     shows it as verified. The app is the author. The person goes in a
     `Co-authored-by` trailer.
   - (b) The Git data API (blobs, a tree, a commit, then a ref update with
     `force: false`). The author can be the person. The commit is not
     signed unless the server signs it with a key of its own.
   - (c) The contents API. One file for each commit, with the blob `sha`
     of the file that it replaces.

   Precedent: the research note lists which bots use which API. Branch
   rules that need signed commits refuse (b) and (c) without a key in the
   server.
   *Owner decision.* Recommendation: (a). It works with the rule "require
   signed commits", holds no signing key in the server, and makes one
   commit for many files with a check of the head. The cost: the author on
   GitHub is the app, not the person. The trailer `Co-authored-by` names
   the person, and the job record and the audit entry are the record of who
   asked. This changes the "Authorship" item of this RFC.
5. **Conflict check on the content.** `createCommitOnBranch` checks the head
   of the branch, not the file. For a direct push, the head of the ref
   moves often.
   - (a) Read again and retry: when the head moved, read each file at the
     new head, and apply the edits again only when each edited key has the
     value that it had at the base. Else fail with `job.ErrConflict`, and
     the console shows both values. At most 3 retries.
   - (b) Fail at once when the head moved.

   *Recommendation: (a).* This is the rule of RFC 12 "Conflicts", and the
   retry limit stops a loop on a busy branch.
6. **The YAML of `Pulumi.<stack>.yaml`.** `go.yaml.in/yaml/v3` is a
   dependency of the module already, and its `Node` API keeps comments and
   key order. A secret value is written as the ciphertext of the `secret`
   package, in the `secure:` form that the Pulumi CLI writes.
   *Recommendation:* use it, with no new dependency. A key path that does
   not exist is added at the end of `config:`. A file that does not parse
   fails the job with the line number, and the server never writes a file
   that it did not parse.
7. **An edit of many units (a shared file).**
   - (a) One job for each unit, as ADR 100 decision 2 says. A path that is
     not an editable file of the unit fails at start. A shared file is not
     editable through write-back.
   - (b) One write-back for many units, as one review request.

   Precedent: Renovate and Dependabot open one pull request for each
   update group, and a group is a rule of the user (research note).
   *Recommendation: (a)* for Milestone 5. A shared file is the home of a
   value of many units. Its edit needs a design of its own (a change order
   of write-back jobs), which a later RFC can add.
8. **The merge method (ADR 271 question 11).** The server merges only for
   the pre-merge apply option of RFC 27.
   - (a) A field `mergeMethod` in `reviewRequests` of the unit settings:
     `merge`, `squash` or `rebase`, default `merge`. The merge call names
     the head commit that OpenDustpan applied, so GitHub refuses the merge
     when the head moved.
   - (b) The default method of the repository.

   *Recommendation: (a).* GitHub's merge API takes the method as a
   parameter with the default `merge`. An explicit field shows the value
   in the API.
9. **The bypass list of a protected branch (RFC 13 open question).**
   *Recommendation:* OpenDustpan never asks for a bypass. A direct push
   that branch protection refuses fails the job with `forge.ErrRefused`,
   and the docs show the review request as the path for a protected
   branch.
10. **Branch cleanup.** *Recommendation:* the server removes the branch
    `dustpan/<job ID>` after the merge or the close, through the API, only
    when the head of the branch is still the commit that the job made. A
    branch with a commit of a person stays.

**Ports.** Methods that the `Forge` port needs for Part B, with conformance
cases first in `forgetest`:

- `Files.ReadFile` returns the blob ID of the file (the method exists
  today, and the GitHub adapter implements it).
- `Commits.Commit` takes the expected head and, for each change, the
  expected blob ID (empty for a new file). The GitHub adapter implements
  question 4.
- `Commits.FindCommit`, `Reviews.OpenReview`, `ReadReview`, `CloseReview`
  (exist as methods).
- `Reviews.DeleteBranch(repo, branch, expectedHead)`: new, for question 10.

### End-to-end steps

The e2e suite cannot reach github.com. It gets a GitHub API fake
(`e2e/githubfake`), a small HTTP server that serves the REST and GraphQL
calls that the adapter makes, keeps a real git repository on disk, and
signs webhooks with the event secret. The adapter unit tests check the same
calls against recorded answers.

1. A `PUT` of the unit settings, then a `GET` that shows each effective
   value and its origin.
2. An API edit of a Pulumi config key: the fake shows one commit on
   `dustpan/<job ID>` with the trailer and the edited key, and one review
   request. The real Pulumi CLI runs `pulumi up` on the merged commit
   against OpenDustpan, and the job ends `succeeded`.
3. The same for an OpenTofu `.auto.tfvars.json` key, with the real
   `tofu apply`.
4. A conflict: a commit of a person changes the edited key after the base.
   The job fails with the conflict, and the fake shows no commit.
5. A takeover: the test stops the instance after the commit and before
   `Delivered`. The new claim finds the commit by its trailer and makes no
   second commit.

### Security tests (ADR 250)

| Step | Test with hostile input sent straight to the step |
| --- | --- |
| Check at the boundary | The server refuses each of these settings and never changes it. The values: `..`, an absolute directory, `.github` in the path, a ref with a leading `-`, a control character, a file outside the directory. |
| Contain | A YAML value holds a newline, an anchor, a tag and a document separator. The written file parses to exactly that value. A JSON key with `__proto__`-like names stays a plain key. |
| Limit | 65 edits, a value of 256 KiB plus one byte, a file of more than the read limit: each refused. The connection token used for the commit has contents write on one repository only (the GitHub App installation). |
| Authorization at the point of use | A `PUT` that names a connection without `namespace:use-git-host-connection` is refused. The deliver phase checks again that the settings still name the connection of the spec. |
| Fail closed | A Git host error that is not a timeout or a rate limit fails the job. A file that does not parse fails the job. Each refusal writes an audit entry. |

### Work items

Size: S is under 400 changed lines, M under 1,000, L under 2,000. A
conflict group names the files that two items must not change at the same
time.

| ID | Item | Size | Conflict group | Depends on |
| --- | --- | --- | --- | --- |
| WB-1 | `pkg/unitsettings`: model, `Validate`, store port, conformance suite, memory adapter | M | unit-settings | none |
| WB-2 | SQL store of unit settings in both dialects, migration, backup table | M | store-migrations | WB-1 |
| WB-3 | Routes, actions, audit, `If-Match`, docs page "Unit settings" | M | api-units | WB-2 |
| WB-4 | `job.Planner` adapter over unit settings and the step planners | M | job-wiring | WB-2, M5-1 |
| WB-5 | GitHub adapter: `Head`, `ReadFile` with blob ID, forgetest cases | M | forge-github | none |
| WB-6 | GitHub adapter: `Commit` (question 4), `FindCommit`, conformance cases | L | forge-github | WB-5, owner answer 4 |
| WB-7 | GitHub adapter: `OpenReview`, `ReadReview`, `CloseReview`, `DeleteBranch` | M | forge-github | WB-6 |
| WB-8 | `e2e/githubfake` with REST, GraphQL, git storage and signed webhooks | L | e2e-harness | none |
| WB-9 | Write-back executor: read, edit (YAML and JSON), commit, deliver, lookup on takeover | L | job-writeback | WB-4, WB-6, WB-7 |
| WB-10 | Review watcher: webhook intake of merge and close, the `job.review-watch` poll | M | forge-events | WB-7, WB-8 |
| WB-11 | e2e steps 2 to 5, docs page "Edit config through the API" | M | e2e-steps | WB-8, WB-9, WB-10 |

## Outcome

Accepted in part as [ADR 213](../adr/0213-job-tokens-no-change-plans-bulk-calls-forge-connections-and-locks.md)
on 2026-10-18, as the project manager decided under the owner's decision rule: the credentials of a forge connection are sealed
credentials of the namespace, not a setting, with rotation and a logged
fallback (decision 7), and a namespace has many forge connections with one
default (decision 8). The other questions stay open.
