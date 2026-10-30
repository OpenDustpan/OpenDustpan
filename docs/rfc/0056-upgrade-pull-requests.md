---
rfc: 56
title: Upgrade pull requests for providers, modules and blueprints
status: discussing
created: 2026-10-10
---

# RFC 56. Upgrade pull requests for providers, modules and blueprints

Status: discussing. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". The section "Proposal (2026-10-30)" is ready to
accept.

## Problem

Gruntwork Patcher opens pull requests that upgrade module and provider
versions and promotes them across environments. HCP Terraform and Pulumi show
which units use an old version.

## First ideas

- Use the forge connection (RFC 12) to open upgrade review requests for units
  that pin old versions.
- Roll out across units in order (RFC 24).

## Scope (owner, 27 October 2026)

This RFC is "an upgrade review request for units that pin an old blueprint
version". It does not grow into fleet campaigns. Upgrades of providers and
modules in the code of a team, batch upgrade tooling with recipes for code
changes, and server-driven upgrade waves are later work (issue 2037). A team
that wants upgrade review requests for providers and modules today uses
Renovate or Dependabot, which already cover OpenTofu (see the facts).

## What is decided already

- ADR 110 decision 5: a Catalog request in the home `git` is a file
  `catalog/<ns>/<bp>/<name>/request.json` in a repository of the team. A
  submit through the API starts a write-back job that commits the file, as
  a review request or a direct push. The team's CI renders and applies it.
- ADR 110 decision 9: OpenDustpan approvals add to the approvals of the
  forge. A write-back job waits at its gates before its commit.
- ADR 111 decision 8: the write-back job commits the bytes of
  `catalog.Document.Encode`, so the diff shows only the keys that changed.
- ADR 116 decisions 3, 9 and 10: one revision in flight for each request.
  A render from git supersedes a revision in flight. A cancel of a
  write-back job after a merge fails with `job.ErrDelivered`.
- ADR 101: the write-back executor, its review request delivery, and its
  undo of an open review request after a cancel.
- ADR 271: the forge port and the installation-level GitHub App.
- RFC 111 (proposal of 2026-10-30): the rollout call, the upgrade path and
  the migration of answers.

## Facts from other products

Each fact has a tag: **[fetched]** means that the page was read on
2026-10-30, **[not fetched]** means a search snippet only.

- **Renovate.** The `terraform` manager updates module sources, providers and
  `required_version`, and also matches `*.tofu` files. It finds an earlier
  pull request by its branch name (`branchPrefix` plus a topic). It limits new
  pull requests with `prHourlyLimit` (default 2) and `prConcurrentLimit`
  (default 10). `recreateWhen: auto` opens a pull request again after a
  close. <https://docs.renovatebot.com/modules/manager/terraform/> [fetched],
  <https://docs.renovatebot.com/configuration-options/> [fetched]
- **Dependabot.** One pull request for each outdated dependency by default,
  with `groups` to join them. Branches are
  `dependabot/<package-manager>/<dependency>`. `open-pull-requests-limit`
  defaults to 5.
  <https://docs.github.com/en/code-security/dependabot/working-with-dependabot/dependabot-options-reference>
  [fetched]
- **Gruntwork Patcher.** It carries a module update through a chain of pull
  requests, one environment after the other. A merge starts the pull request
  of the next environment. Five grouping strategies (none, per dependency,
  per environment, and others).
  <https://docs.gruntwork.io/2.0/docs/patcher/concepts/promotion-workflows>
  [fetched], <https://docs.gruntwork.io/2.0/docs/patcher/concepts/grouping>
  [fetched]
- **HCP Terraform.** A deprecated module version shows a warning in the runs
  of its consumers. Explorer finds workspaces by provider or module version,
  and a person makes a "change request" record for each one. It opens no
  pull request.
  <https://developer.hashicorp.com/terraform/cloud-docs/registry/manage/module-versions>
  [fetched],
  <https://developer.hashicorp.com/terraform/tutorials/hashiconf/self-service-workflows>
  [fetched]
- **HCP Terraform no-code.** A new no-code version shows a notice on each
  workspace, and nothing changes until a person acts.
  <https://developer.hashicorp.com/terraform/cloud-docs/workspaces/no-code-provisioning/provisioning>
  [fetched]

## Proposal (2026-10-30)

### Answer to the open question

The upgrade review request is a feature of the Catalog (blueprints). It is
not a feature of the registry. It is the half of a rollout (RFC 111) for the
requests in the home `git`. It needs no new executor: it is a write-back job
of an upgrade revision.

### Decisions

1. **An offer starts only by a call.** An upgrade review request (an
   **upgrade offer**) starts through the rollout call of RFC 111, with one
   or more request names, or with an empty list for each request that pins
   a lower version. The call needs `blueprint:roll-out` on the blueprint.
   The server never offers an upgrade on its own, for example at a publish.
   So each offer has a person or a principal that asked for it, and a
   reason, in the audit log.
2. **One upgrade revision for each request.** For each request in the home
   `git`, the server builds the upgrade document with the upgrade path of
   RFC 111, runs the checks of `submit` on it, and stores a revision with
   the origin `rollout`. The revision starts a write-back job, as a submit
   does (ADR 110 decision 5).
3. **Always a review request.** The delivery of an offer is
   `review-request`, also when the request names `direct-push`. The platform
   team does not own the repository of the consumer. The merge in the forge
   is the consent of the consumer.
4. **OpenDustpan gates still apply.** The gates of the blueprint and of the
   namespace apply to the write-back job before its commit, as for any
   submit (ADR 110 decision 9). The built-in rule `blueprint-rollout` of
   RFC 111 does not apply to an offer: the forge review is the consent.
5. **The review request.** The branch is
   `dustpan/upgrade/<request ID>/v<N>`, so the server finds its own offer
   again. The title is "Upgrade <blueprint> from version <v> to <N>". The
   body lists each input that the migration set or dropped, with the old
   and the new value, a link to the blueprint version, and the line "This
   change was offered by <principal> through a rollout. Merge it to accept
   the upgrade." The body holds no secret value: no answer is secret (ADR
   111 decision 3). The forge shows the diff of `request.json`, which holds
   the version and the answers that changed (ADR 111 decision 8).
6. **No duplicates.** One revision in flight for each request (ADR 116
   decision 3) already gives at most one open offer for each request. A
   rollout skips a request with a revision in flight, with the reason
   `in-flight`.
7. **A newer offer replaces an older one.** When a rollout of version N+1
   finds an open offer of version N for the same request, which a rollout
   made and the forge did not merge, the server cancels that revision
   through `job.Service.Cancel`. The write-back executor closes its review
   request with the comment "Replaced by the offer of version <N+1>". Then
   the server makes the new offer. A revision that a consumer submitted is
   never canceled by a rollout.
8. **A closed offer is a refusal.** When a person closes an offer review
   request without a merge, its job ends `rejected` (ADR 116 decision 2).
   A later rollout skips that request for the same version, with the reason
   `declined`. The rollout body can name the request in `offerAgain` to
   offer it again.
9. **Apply.** After the merge, the team's CI renders the commit and applies
   it, as for each request in git (ADR 116 decision 9). The state version
   at the commit makes the revision `applied`. An offer that nobody merges
   ends `expired` at the deadline of its revision (ADR 116 decision 13),
   and the executor closes the review request.
10. **Limits.** A rollout makes at most 100 offers in one call. A namespace
    has at most 500 open offers. The write-back queue of the job model
    paces the calls to the forge (ADR 100 decision 16). A call over a limit
    fails with `catalog.ErrLimit`, which names the limit, and makes no offer.
11. **Which requests pin which version.** The list of Catalog requests takes
    the filter `blueprintVersion` (with `lt:`, for example `lt:4`), and each
    item shows its home and the state of its last revision. The console
    shows the count for each version on the blueprint page.

### Ports

No new port. The work uses `catalog.Store`, `job.Service`, the write-back
executor and `forge.Forge`. The write-back executor takes one new input: a
fixed branch name and a title and body for the review request
(`job.ReviewRequest`), which the Catalog sets and the job REST API refuses,
as for the Catalog origin (ADR 110, change 1 of the job model).

### End-to-end steps (real CLIs)

The e2e suite extends the fake GitHub API of `e2e/githost.go` with the
review request routes that the write-back executor calls, behind a bare git
repository on the disk of the test.

1. Publish version 1 of an OpenTofu blueprint and of a Pulumi blueprint.
   Submit one request of each in the home `git`. The suite merges the review
   request, runs the render command, and runs `tofu apply` and `pulumi up`
   on the rendered files with the job token of a CI principal.
2. Publish version 2 with a renamed input. Start a rollout as a platform
   principal. Check one open review request for each request, with the
   fixed branch name, and a diff that changes the version and the renamed
   input only.
3. Publish version 3, start a rollout again, and check that the offer of
   version 2 is closed and an offer of version 3 is open.
4. Merge the offer. Run the render command and apply with both CLIs. Check
   that the revision is `applied` and the request pins version 3.
5. Close an offer without a merge. Start a rollout of the same version, and
   check the skip reason `declined`.

### Security tests (ADR 250)

1. **Boundary.** A rollout body with an unknown field, more than 100 names,
   and a request of another blueprint. The branch name is built from the
   request ID and the version only, and a test checks it against the
   branch-name rules of git with hostile request names.
2. **Contain.** The review request title and body: each value from a person
   (the blueprint title, the principal name, the answers) is escaped for
   Markdown, and CR and LF are removed from the title. A test uses a
   blueprint title with Markdown links, HTML and a new line.
3. **Limit.** The 101st offer of a call and the 501st open offer of a
   namespace.
4. **Authorize at use.** A rollout without `blueprint:roll-out`. The job
   REST API refuses `job.ReviewRequest` from a caller. A cancel of a
   revision that a consumer submitted, through a rollout, is refused.
5. **Fail closed.** A forge that does not answer leaves the revision in its
   state and the job in the queue, and writes no partial commit. A
   migration failure makes no offer and writes an audit entry.

### Work items

Sizes, conflict groups and the IDs M6-1 and M6-2 as in RFC 111.

| ID | Item | Size | Conflict group | Depends on |
| --- | --- | --- | --- | --- |
| U1 | `job.ReviewRequest` (fixed branch, title, body) in the write-back executor, refused by the job REST API, with `jobtest` cases | M | `job-writeback` | the write-back executor |
| U2 | Offers in the rollout service: the forced delivery, the replace rule, the `declined` skip, the limits | M | `catalog-core` | B1, B4, U1 |
| U3 | The `blueprintVersion` filter of the request list | S | `api-catalog` | Catalog routes, M6-2 |
| U4 | The e2e steps above, the review request routes of the fake GitHub API, and the docs page "Upgrade offers in git" | M | `e2e`, `docs` | U2 |

Order: U1 and U3 in parallel, then U2 (after B1 and B4), then U4.

### Proposed glossary line

**Upgrade offer**:
A review request that a rollout opens in the repository of a Catalog request
in git. It changes the blueprint version and the answers of the request
file. The merge by the consumer team is its consent.
_Avoid_: Upgrade pull request (copy may say "pull request" for GitHub),
bump, Renovate PR

## Open questions

- Is this a feature of the registry (RFC 10), of blueprints (RFC 14), or
  separate? Answered by the proposal: of the Catalog, as the half of a
  rollout for requests in git.

## Outcome

None yet.
