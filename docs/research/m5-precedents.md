# Research: precedents for the Milestone 5 designs

Question: For each open question of the Milestone 5 proposals in RFCs 5, 12,
15, 23, 24, 27 and 30, what do HCP Terraform and Terraform Enterprise,
Spacelift, env zero, Atlantis, Digger (OpenTaco), GitHub Actions, GitLab,
Pulumi and other tools do?

Date: 2026-10-30

This note gives facts as input. It decides nothing. Each fact has a mark:

- "fetched: <URL>" means that the page or the source file was read on
  2026-10-30. The pages were read through a tool that summarizes, so a quote
  is short and must be checked against the page before an ADR quotes it.
- "not fetched (from memory)" means that the fact was not checked on that
  day. Check it before a decision relies on it.
- "page silent" means that a fetched page did not answer the question.

The proposals cite this note by section. Product names are the names of
other projects. OpenDustpan is not affiliated with any of them.


## Pull request automation: precedent facts

Research date: 2026-10-30. Input for an OpenDustpan design RFC on pull request
automation (summary comments, comment commands, apply before merge, stale
plans, forks, global apply stop, required reviewers, policy checks).

Marking:

- `fetched: <URL>` means the fact was read on that page in this session. Pages
  were read through a summarizing fetch tool, so quotes are short and should be
  re-checked against the page before they go into an ADR verbatim.
- `not fetched (from memory)` means the fact comes from prior knowledge and is
  not verified in this session.

Products: Atlantis, Digger (now OpenTaco, docs moved from docs.digger.dev to
docs.opentaco.dev with 301 redirects), Spacelift, env zero (docs at
docs.envzero.com, formerly env0), HCP Terraform / Terraform Enterprise,
GitHub Actions, GitLab CI, Pulumi Deployments.

---

### 1. Summary comment, comment noise, statuses and checks

#### Atlantis

- Posts plan/apply results as PR comments, one comment set per command.
  `--max-comments-per-command` limits the comments per command. Default `100`;
  `0` disables truncation. fetched: https://www.runatlantis.io/docs/server-configuration
- `--hide-prev-plan-comments`: hides earlier plan comments. GitHub, GitLab and
  Bitbucket; on Bitbucket they are deleted. fetched: https://www.runatlantis.io/docs/server-configuration
  (On GitHub this uses the "minimize comment" feature, marked as outdated: not fetched (from memory).)
- `--hide-unchanged-plan-comments`: removes plan comments that show no changes.
  fetched: https://www.runatlantis.io/docs/server-configuration
- `--enable-diff-markdown-format`: diff-style code blocks for color.
  fetched: https://www.runatlantis.io/docs/server-configuration
- `silence_pr_comments` (values `plan`, `apply`), in repo or server-side
  config: silences "PR comments from defined stages while preserving PR status
  checks." fetched: https://www.runatlantis.io/docs/repo-level-atlantis-yaml and
  https://www.runatlantis.io/docs/server-side-repo-config
- Silence flags: `--silence-no-projects`, `--silence-vcs-status-no-plans`,
  `--silence-vcs-status-no-projects`, `--silence-allowlist-errors`,
  `--silence-fork-pr-errors`. fetched: https://www.runatlantis.io/docs/server-configuration
- Long output is split into several comments when it is over the VCS comment
  size limit (GitHub 65536 characters). not fetched (from memory)
- Statuses: uses commit statuses named by `--vcs-status-name` (default
  `atlantis`). fetched: https://www.runatlantis.io/docs/server-configuration
  The statuses are aggregate per command (`atlantis/plan`, `atlantis/apply`),
  with an optional per-project status. not fetched (from memory). The command
  requirements page refers to "other projects' Atlantis plan statuses" for
  per-project applies on GitLab, which confirms per-project statuses exist.
  fetched: https://www.runatlantis.io/docs/command-requirements
- `--pending-apply-status` (GitLab only): status stays pending until planned
  changes are applied. fetched: https://www.runatlantis.io/docs/server-configuration

#### Digger / OpenTaco

- Digger "leaves a comment for every plan or apply performed," with a summary
  comment on top. The docs say this is "noisy for anything over 10 impacted
  projects." The documented fix is to turn comments off:
  `reporting: comments_enabled: false`; plans are then seen on the checks page.
  fetched: https://docs.opentaco.dev/ce/howto/noise-reduction
- `comment_render_mode` (`basic` or `detailed`); `reporting.ai_summary`;
  `disable_digger_apply_comment` hides the apply prompt after a plan;
  `disable_digger_apply_status_check` turns off the check that apply ran.
  fetched: https://docs.opentaco.dev/ce/reference/digger.yml.md
- Digger CE is known for a single summary comment that is edited in place in
  newer versions. not fetched (from memory); the fetched noise page contradicts
  a strict single-comment model (one comment per plan/apply plus a summary).

#### Spacelift

- Proposed run results are "always" reported to VCS as commit statuses and as
  pull request comments. fetched: https://docs.spacelift.io/concepts/run/proposed
- Plan commenting is opt-in per stack with the label
  `feature:add_plan_pr_comment`. fetched: https://docs.spacelift.io/concepts/run/pull-request-comments
- Notification policy `pull_request` rule (keys `id`, `commit`, `branch`,
  `body`, `deduplication_key`). Default: each matching run adds a new comment.
  With a constant `deduplication_key` (for example the stack ID) one comment
  per PR is updated in place. The key is scoped per PR. The docs recommend
  targeting by `id` (`run.commit.pull_request_id`) because commit-based
  targeting can fail with GHES or forks. fetched: https://docs.spacelift.io/concepts/policy/notification-policy
- GitHub: uses both Checks (check runs) and Commit statuses. Integration
  setting "VCS checks": "Individual checks (one per stack)" or aggregated.
  "Aggregate VCS checks" posts `spacelift/tracked`, `spacelift/proposed`,
  `spacelift/modules`; the Details link lists the stacks. Aggregation needs
  the Business plan or above. Tracked runs also create GitHub Deployments
  (Pending, Failure, Active); label `ghenv:` sets the environment name,
  `ghenv:-` disables it. fetched: https://docs.spacelift.io/integrations/source-control/github

#### env zero

- The "affected environments" comment is "updated (edited) on every PR commit,
  so only a single comment will be shown in the PR." fetched:
  https://docs.envzero.com/guides/admin-guide/environments/plan-and-apply-from-pr-comments
- PR plan comments are posted when a plan starts, succeeds and fails. One
  commit status check; with several linked environments "you will get the
  summary of all the PR Plans". fetched: https://docs.envzero.com/guides/admin-guide/environments/plan-on-pull-request

#### HCP Terraform / TFE

- No plan text in PR comments: posts a commit status with a link to the
  speculative plan. "A single pull request can include links to multiple
  plans." Skipped plans still post a passing status. fetched:
  https://developer.hashicorp.com/terraform/cloud-docs/run/ui
- Organization setting: aggregated (default) or non-aggregated status checks.
  Aggregated: "one VCS status check for all runs triggered by a VCS event."
  Non-aggregated: a status check "for each triggered workspace and related run
  stage." fetched: https://developer.hashicorp.com/terraform/cloud-docs/users-teams-organizations/organizations/vcs-status-checks
- "HCP Terraform does not update the status checks on a pull request with the
  status of an associated apply." fetched: https://developer.hashicorp.com/terraform/enterprise/run/ui

#### Pulumi (GitHub App, Deployments)

- GitHub App "automatically adds comments to pull requests with the results of
  any stack changes"; "All Pulumi stack updates are reported to the GitHub
  Checks API"; turning comments off keeps check reporting. fetched:
  https://www.pulumi.com/docs/iac/using-pulumi/continuous-delivery/github-app/

#### GitHub (platform)

- Checks: "Detailed output, annotations, and messages", created by GitHub Apps
  (including Actions). Commit statuses: "A simpler status for a commit", from
  external services. fetched: https://docs.github.com/en/pull-requests/collaborating-with-pull-requests/collaborating-on-repositories-with-code-quality-features/about-status-checks
- Only GitHub Apps can write check runs through the Checks API (OAuth apps and
  personal tokens can read only); statuses and check runs are bound to a commit
  SHA. not fetched (from memory)
- A required status check can pin an expected source app: "If any other person
  or integration sets the status, merging is blocked." fetched:
  https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches

---

### 2. Comment commands: syntax, who may run them, security

#### Atlantis

- Syntax: `atlantis plan [-d dir | -p project | -w workspace] [--verbose] -- [terraform flags]`;
  `atlantis apply` (same targeting, plus `--auto-merge-disabled`,
  `--auto-merge-method` GitHub only); `atlantis unlock`; `atlantis approve_policies`;
  `atlantis import ADDRESS ID`; `atlantis state rm ADDRESS...`; `atlantis cancel`
  (queued only); `atlantis version`; `atlantis help`. `-p` cannot combine with
  `-d`/`-w`. apply without targets applies "all unapplied plans for the PR".
  Plan blocks flags that change where Terraform writes (`-out`, `-state`,
  `-chdir`, `-generate-config-out`); apply ignores plan-changing flags
  (`-target`, `-var`, `-var-file`). fetched: https://www.runatlantis.io/docs/using-atlantis
- `--allow-commands`: default `version,plan,apply,unlock,approve_policies,cancel`
  (`import`, `state` need opt-in). `--disable-apply-all` requires a target on
  apply. `--enable-regexp-cmd` allows regex in `-p` ("not supposed to be used
  with `--disable-apply-all`"). fetched: https://www.runatlantis.io/docs/server-configuration
- Who may run: "Once requirements pass, anyone who can comment on the PR can
  run `atlantis apply`." fetched: https://www.runatlantis.io/docs/command-requirements
  `--gh-team-allowlist` maps GitHub teams to commands (e.g. `myteam:plan`);
  "By default, any team can plan and apply." `--repo-allowlist`
  (`{hostname}/{owner}/{repo}`, `*`, `!` negation) limits webhook sources.
  fetched: https://www.runatlantis.io/docs/server-configuration
- Comment author verification: the author is taken from the webhook payload,
  whose authenticity rests on `--gh-webhook-secret`: "If not specified,
  Atlantis won't be able to validate that the incoming webhook call came from
  GitHub." fetched: https://www.runatlantis.io/docs/server-configuration
  Security page: without a webhook secret, "attackers could make requests to
  Atlantis posing as a repository that is allowlisted"; HTTP lets "the webhook
  secrets ... be stolen". fetched: https://www.runatlantis.io/docs/security
- Edited comments: the webhook docs do not say. fetched: https://www.runatlantis.io/docs/configuring-webhooks
  Atlantis acts only on `issue_comment` action `created` and ignores edits.
  not fetched (from memory)
- Threat model in the docs: an attacker PR with a malicious provider or
  `external` data source runs code at `terraform plan` (autoplan); `local-exec`
  at apply; "Atlantis uses the `atlantis.yaml` file from the pull request
  branch, not `main`"; "anyone can comment on public pull requests";
  "`terraform apply` approvals are not enough" (protect planning: bake providers
  into the image and deny egress, internal registry, or validate the `plan`
  step in server-side config). `--var-file-allowlist` limits `-var-file` from
  comments. fetched: https://www.runatlantis.io/docs/security
- Custom workflows: default off; if allowed, "anyone that can create a pull
  request to your repo can run arbitrary code on the Atlantis server."
  `allowed_overrides`, `allowed_workflows`, `allow_custom_workflows` in
  server-side config. fetched: https://www.runatlantis.io/docs/repo-level-atlantis-yaml and
  https://www.runatlantis.io/docs/server-side-repo-config
- Pre-workflow hooks: server-side config only; run on every Atlantis command
  by default (the `commands` key limits them); a failing hook does not stop
  plan/apply unless `fail-on-pre-workflow-hook-error` is set. Values like
  `HEAD_BRANCH_NAME`, `PULL_AUTHOR`, `COMMENT_ARGS` are untrusted; they are
  passed as environment variables, and safety is lost with `eval`, nested
  `sh -c` or written-then-run scripts. fetched: https://www.runatlantis.io/docs/pre-workflow-hooks
- Security advisories (list read from GitHub; titles and dates as listed):
  fetched: https://github.com/runatlantis/atlantis/security/advisories and
  https://github.com/runatlantis/atlantis/security/advisories?page=2
  - GHSA-6mjf-4m52-m5cq (Critical, 2026-10-25, fixed 0.49.0): clone URL host
    not validated; fork gate compared owners from webhook `full_name`, so a
    forged webhook (no webhook secret) with a same-named head repo passed
    `--allow-fork-prs=false` and ran plan on attacker code. CWE-346. fetched:
    https://github.com/runatlantis/atlantis/security/advisories/GHSA-6mjf-4m52-m5cq
  - GHSA-5vwh-xc92-8h64 (High, fixed 0.49.0) "Plan-hijack": plan file at a
    deterministic path, apply did not check that the plan came from the
    reviewed PR/commit; a comment argument could write an output path and
    overwrite another approved PR's plan, bypassing `apply_requirements`.
    Recommended fix: bind plan metadata (repo, PR, head SHA, project,
    workspace, plan hash) with a MAC and verify on apply, "rejecting on
    mismatch". fetched: https://github.com/runatlantis/atlantis/security/advisories/GHSA-5vwh-xc92-8h64
  - GHSA-rjh2-4xgw-p2hj (High, 2026-08-25, CVE-2026-116676 as shown, affects
    0.4.0 to 0.47.0, fixed 0.47.1): PR-controlled `workspace` value in
    `atlantis.yaml` reached `sh -c` via `strings.Join(args, " ")`; validation
    rejected only `..`, `/`, `\`. Fix: argument vector (`exec.Command`), strict
    name validation as defense in depth. fetched:
    https://github.com/runatlantis/atlantis/security/advisories/GHSA-rjh2-4xgw-p2hj
  - Other titles in the list (not opened): `state rm` bypasses apply/import
    requirements (GHSA-cxvq-66x3-pwhw); Gitea dismissed approval still counted
    for `approved` (GHSA-fm5f-jg8p-x2vw); `ValidateRepoCfg` break/continue lets
    a later project bypass `allowed_workflows` (GHSA-56q6-8vmp-v2qm); symlink in
    a PR escapes `EnsureSubPath` (GHSA-9q39-mgw7-5789); `/api/locks` without
    API token (GHSA-xgrg-fpp6-q6fm); hidden output fails open and leaks secrets
    into PR comments (GHSA-jxcj-v552-f5q4); `--var-file` allowlist bypass for
    file read from comments (GHSA-chm7-fjpm-927x); repo allowlist wildcard
    matches suffix only (GHSA-fwc4-2f3p-7qm7); non-constant-time secret
    comparison (GHSA-48vm-qqx3-7w83, GHSA-mwfx-fvfc-7q5w); workspace path
    traversal (GHSA-26w5-6g95-gj28); Git credentials in logs
    (GHSA-gppm-hq3p-h4rp, 2024-11-08).

#### Digger / OpenTaco

- Commands: `digger plan` (locks and checks locks, plans all affected
  projects), `digger apply` (checks locks, applies all affected projects,
  unlocks, may merge), `digger lock`, `digger unlock`; `-p <project>` for plan
  and apply. fetched: https://docs.opentaco.dev/ce/features/commentops.md
- Who may run and author verification: not described in the fetched commentops
  or troubleshooting pages. fetched: https://docs.opentaco.dev/ce/troubleshooting/comments.md
  The security page says "Control who can trigger applies and under what
  conditions" through apply requirements and CODEOWNERS, and warns that
  contributors with write access could "exfiltrate CI environment variables";
  it recommends OIDC short-lived credentials. Plan output values are masked
  with `<REDACTED>` in logs and comments. fetched: https://docs.opentaco.dev/ce/security/overview.md
- Digger runs jobs in the user's CI (GitHub Actions `workflow_dispatch`), so
  fork and secret behavior follows GitHub Actions rules. not fetched (from memory)

#### Spacelift

- Comment commands are push-policy rules, not built-in verbs. Only comments
  that begin with `/spacelift` are evaluated. Example `track` rule:
  `input.pull_request.action == "commented"` and
  `input.pull_request.comment == concat(" ", ["/spacelift", "deploy", input.stack.id])`;
  a `/spacelift propose` example exists. Requires `issues` read and the
  `issues:comments` event. fetched: https://docs.spacelift.io/concepts/run/pull-request-comments
- Edited comments: Spacelift "only processes event data for new comments" and
  "will not receive event data for edited or deleted comments." fetched:
  https://docs.spacelift.io/concepts/run/pull-request-comments
- Comment-triggered proposed runs bypass deduplication ("the run will be
  created regardless"). fetched: same page.
- `/spacelift unlock <stack-id>` with the `unlock` rule. fetched:
  https://docs.spacelift.io/concepts/policy/push-policy
- Who may comment: not covered on the comments page. A plan policy can deny
  tracked runs whose trigger source starts with `github/` (format
  `github/$username`). fetched: https://docs.spacelift.io/integrations/source-control/github

#### env zero

- Commands: `env0 plan`, `env0 apply` with `-e {aliases}`, `--all`,
  `--path "<glob>"`, `-v name=value` ("This value will persist in the next
  deployment"); also `approve`, `cancel` (only for deployments triggered from
  the same PR). fetched: https://docs.envzero.com/guides/admin-guide/environments/plan-and-apply-from-pr-comments
- Who may run: "By default, anyone with comment permission on your repository
  can run a Plan or an Apply on your environments." Org policy "Enforce
  commenter permissions on env zero" validates permissions; each user maps
  their VCS user ID in Personal Settings. Applies from comments are
  auto-approved by default; policy "Require approval for PR comment applies".
  Edited comments and forks: not addressed. fetched: same page.

#### GitHub Actions (comment-triggered workflows)

- `issue_comment` types `created`, `edited`, `deleted`; runs from the default
  branch workflow file; fires for issue and PR comments. fetched:
  https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows
  Because it runs in base context with secrets, a workflow that checks out the
  PR head on a comment has the same risk as `pull_request_target`, plus a
  race: the attacker can push a commit between the comment and the checkout.
  Workflows must check out the reviewed SHA and check the commenter's
  `author_association` or permission. not fetched (from memory)
- `pull_request_target`: "Running untrusted code on the `pull_request_target`
  trigger may lead to security vulnerabilities"; "Avoid using this event if
  you need to build or run code from the pull request." fetched: same page.
- GitHub Security Lab "pwn requests": `pull_request_target` plus explicit
  checkout of the PR is "a dangerous practice that may lead to repository
  compromise"; write token stays available and `persist-credentials` defaults
  to `true`; recommended split: unprivileged `pull_request` workflow writes
  artifacts, privileged `workflow_run` reads them (artifacts stay untrusted);
  label gate ("safe to test") is a temporary fix and "is still prone to a race
  condition in which the attacker may push new changes after the workflow was
  approved." fetched: https://securitylab.github.com/resources/github-actions-preventing-pwn-requests/
- Incidents: Ultralytics (Dec 2024) compromise through a `pull_request_target`
  workflow with branch-name injection; tj-actions/changed-files supply-chain
  compromise (Mar 2025, CVE-2025-30066). not fetched (from memory)

#### GitLab

- No native comment commands for Terraform; the Terraform MR widget shows plan
  reports from CI artifacts. not fetched (from memory)

---

### 3. Apply before merge vs apply after merge

#### Atlantis (apply before merge is the model)

- Requirements gate `apply`, `import`, `state rm`: `approved`, `mergeable`,
  `undiverged` (keys `plan_requirements`, `apply_requirements`,
  `import_requirements`). Server flags override repo config; repo
  `atlantis.yaml` may set them only via `allowed_overrides`. fetched:
  https://www.runatlantis.io/docs/command-requirements
- `approved`: one approval by someone other than the author. "It does not
  check whether the approval was given before or after the latest plan or
  push." Mitigations: "dismiss stale reviews on push" branch protection or
  `--discard-approval-on-plan` (GitHub, GitLab). fetched: same page.
- `mergeable`: provider-defined. GitHub with branch protection covers status
  checks, reviewers, CODEOWNERS, up-to-date; the Atlantis user must be in
  "Restrict who can push"; using `atlantis/apply` as a required check needs
  `--gh-allow-mergeable-bypass-apply`. Bitbucket checks only conflicts. The
  docs warn to restrict the base branch (`branch` regex in server config) so
  the mergeable check cannot be bypassed by targeting an unprotected branch.
  fetched: same page.
- Locking rationale: "Applying before merge means your `main` branch no longer
  reflects your current infrastructure"; locks (directory + workspace) are
  taken at plan, held "until the pull request is merged or closed, or the plan
  is manually deleted"; another PR planning the same project gets an error
  linking the holder PR. Release with `atlantis unlock` or UI "Discard Plan and
  Unlock". Atlantis locks sit above Terraform state locking. fetched:
  https://www.runatlantis.io/docs/locking
- `repo_locks` mode: `on_plan` (default), `on_apply`, `disabled`.
  `--disable-repo-locking` server flag. fetched: https://www.runatlantis.io/docs/server-side-repo-config
  and https://www.runatlantis.io/docs/server-configuration
- `--automerge`: merge PR after all plans applied (default `false`);
  `delete_source_branch_on_merge`. Execution order groups apply only to a
  global `atlantis apply`; `abort_on_execution_order_fail`. fetched:
  https://www.runatlantis.io/docs/server-configuration and
  https://www.runatlantis.io/docs/repo-level-atlantis-yaml

#### Digger / OpenTaco (apply before merge by default)

- Default: apply manually "while the PR is still open, then merge when it
  succeeds in all target environments"; "This is a safer way to resolve the
  merge-apply dilemma"; tradeoff "your main falls behind the state of your
  environments". Apply on merge suits "highly sensitive parts of production
  environments that are rarely changed" (for example VPC); drawback: flaky
  applies after merge. Config: `workflow_configuration` keys
  `on_pull_request_pushed`, `on_pull_request_closed` (`digger unlock`),
  `on_commit_to_default` (`digger apply`), plus `disable_digger_apply_comment`
  and `disable_digger_apply_status_check` set to `true`. fetched:
  https://docs.opentaco.dev/ce/howto/apply-on-merge
- `apply_requirements: [mergeable, undiverged, approved]` per project;
  default `mergeable`. A required `digger/apply` check makes the PR
  non-mergeable before apply (circular); fix `skip_merge_check: true` or
  `apply_requirements: []`. fetched: https://docs.opentaco.dev/ce/howto/apply-requirements
- PR-level locks: `pr_locks` (default true); stored in backend table
  `digger_locks`, or DynamoDB / GCP bucket / Azure Storage Tables in
  backendless mode. Purpose: stop operations from different PRs overriding each
  other. fetched: https://docs.opentaco.dev/ce/features/pr-level-locks
- `auto_merge`, `auto_merge_strategy` (`squash` default). fetched:
  https://docs.opentaco.dev/ce/reference/digger.yml.md

#### Spacelift (apply after merge by default; apply before merge via policy)

- Proposed runs on non-tracked branches preview only; tracked runs on the
  tracked branch apply. fetched: https://docs.spacelift.io/concepts/run/proposed
- Push policy example tracks (applies) a PR once it is approved, mergeable,
  undiverged and labeled `deploy`: apply before merge driven by a label.
  `input.pull_request` has `approved`, `mergeable`, `undiverged`. fetched:
  https://docs.spacelift.io/concepts/policy/push-policy
- Approval policies gate queued and unconfirmed runs; examples: two approvals,
  role-based (Director, or DevOps plus Security), self-approval rejection.
  fetched: https://docs.spacelift.io/concepts/policy/approval-policy

#### env zero

- PR plans are plan-only; apply from a PR comment checks mergeability
  (GitHub protected branch; Bitbucket one non-author approval; Azure DevOps
  required branch policies). "Bypass apply mergeability check" policy for the
  circular required `env0/Apply` check (GitHub only). fetched:
  https://docs.envzero.com/guides/admin-guide/environments/plan-and-apply-from-pr-comments
  and https://docs.envzero.com/guides/policies-governance/deployment-controls
- Continuous deployment (redeploy on push to the environment branch) gives
  apply after merge. not fetched (from memory)

#### HCP Terraform / TFE (apply after merge)

- Speculative plans on PRs are plan-only; "runs start automatically when you
  merge or commit changes"; plans need confirmation unless auto-apply is on.
  fetched: https://developer.hashicorp.com/terraform/cloud-docs/run/ui
- Speculative plans cannot be applied; there is no apply-before-merge path in
  VCS-driven workflow. not fetched (from memory)

#### Pulumi Deployments

- Review stacks: "created automatically when a pull request is opened, updated
  on each new commit" and "destroyed when the pull request is merged or
  closed"; settings `pullRequestTemplate`, `reviewStackLabels`,
  `previewPullRequests`, `deployCommits`. Push-to-deploy runs `pulumi up` on
  push to a configured branch (apply after merge). fetched:
  https://www.pulumi.com/docs/pulumi-cloud/deployments/review-stacks/ and
  https://www.pulumi.com/docs/iac/using-pulumi/continuous-delivery/github-app/

#### GitHub (platform precedent)

- Branch protection "Require deployments to succeed before merging" (deploy
  to an environment such as staging before merge) and "Require merge queue"
  (tests PRs against the latest target branch). fetched:
  https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches

---

### 4. Stale plan detection

- Atlantis `undiverged`: blocks apply if the base branch changed since the last
  plan; works only with `--checkout-strategy merge`; transient errors count as
  divergence ("fail safely"); with `when_modified` the check narrows to
  matching files. fetched: https://www.runatlantis.io/docs/command-requirements
- Atlantis autoplan runs on a "new pull request or new commit to an existing
  pull request". fetched: https://www.runatlantis.io/docs/autoplanning
  A new commit makes earlier plans for the PR unusable; apply needs the new
  plan. not fetched (from memory). Release v0.46.0 "hardens apply validation
  against stale head/base plan state". fetched:
  https://newreleases.io/project/github/runatlantis/atlantis/release/v0.46.0
- Atlantis plan files were not bound to PR/commit until 0.49.0 (see
  GHSA-5vwh-xc92-8h64 above): precedent for binding a saved plan to repo, PR,
  head SHA, unit and plan hash.
- Atlantis `import` and `state rm` discard the plan; a new plan is required.
  fetched: https://www.runatlantis.io/docs/using-atlantis
- Atlantis approval is not tied to a plan; `--discard-approval-on-plan`.
  fetched: https://www.runatlantis.io/docs/command-requirements
- Atlantis policy approvals are cleared on re-plan unless
  `sticky_policy_approvals: true`. fetched: https://www.runatlantis.io/docs/policy-checking
- Digger `undiverged` requirement. fetched: https://docs.opentaco.dev/ce/howto/apply-requirements
- Spacelift `undiverged` = "PR branch is not behind the target branch";
  dedup key is commit SHA and run type; `cancel` rule with `input.in_progress`
  pre-empts runs of the same type. fetched: https://docs.spacelift.io/concepts/policy/push-policy
- HCP Terraform: a new commit triggers a new run; a webhook for an already
  processed SHA is ignored; setting "Automatically cancel plan-only runs
  triggered by outdated commits". fetched:
  https://developer.hashicorp.com/terraform/cloud-docs/run/ui
- GitLab "Prevent outdated deployment jobs" stops older deployment jobs after
  a newer one started; `resource_group` runs one deployment job at a time.
  fetched: https://docs.gitlab.com/ci/environments/deployment_safety/
- GitHub "Require branches to be up to date before merging" (strict status
  checks) and "Dismiss stale pull request approvals when new commits are
  pushed". fetched: https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches

---

### 5. Pull requests from forks

- Atlantis: `--allow-fork-prs` default `false`; "Potentially dangerous to
  enable"; runs only when head and base repos are on the same host;
  `--silence-fork-pr-errors`. fetched: https://www.runatlantis.io/docs/server-configuration
  Security page: "Don't use `--allow-fork-prs`". fetched: https://www.runatlantis.io/docs/security
  The fork gate trusted webhook fields until 0.49.0 (GHSA-6mjf-4m52-m5cq).
- Spacelift: "By default, Spacelift doesn't trigger runs when a forked
  repository opens a pull request"; `allow_fork` rule on
  `input.pull_request.head_owner`. fetched: https://docs.spacelift.io/concepts/policy/push-policy
- env zero: GitHub is "the only VCS supporting forks (from private
  repositories only)". fetched: https://docs.envzero.com/guides/admin-guide/environments/plan-on-pull-request
- HCP Terraform: no speculative plans for fork PRs, "To avoid executing
  malicious code or exposing sensitive information"; TFE admins can allow them.
  fetched: https://developer.hashicorp.com/terraform/enterprise/run/ui
- GitHub Actions: fork `pull_request` gets no secrets and a read-only
  `GITHUB_TOKEN`; first-time contributor approval. Settings: "Require approval
  for first-time contributors who are new to GitHub", "Require approval for
  first-time contributors", "Require approval for all external contributors";
  private repos: "Run workflows from fork pull requests", "Send write tokens to
  workflows from pull requests", "Send secrets to workflows from pull
  requests", "Require approval for fork pull request workflows". fetched:
  https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows
  and https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/enabling-features-for-your-repository/managing-github-actions-settings-for-a-repository
- GitLab: fork MR pipelines run in the fork with fork variables; no protected
  variables or runners. A parent-project member can run the pipeline in the
  parent after accepting a warning ("Fork merge requests can contain malicious
  code that tries to steal secrets in the parent project"). Disable with
  project setting `ci_allow_fork_pipelines_to_run_in_parent_project`. fetched:
  https://docs.gitlab.com/ci/pipelines/merge_request_pipelines/
- Digger / Pulumi: not addressed on fetched pages.

---

### 6. Global stop for applies, change freezes

- Atlantis: global apply lock from the UI (fails closed: if the lock backend
  is unreachable, apply is rejected). fetched: https://www.runatlantis.io/docs/locking
  `--disable-global-apply-lock` removes the UI button. fetched:
  https://www.runatlantis.io/docs/server-configuration
  `--disable-apply` (stop all applies at server level) is not on the current
  server-configuration page; it existed in earlier versions. not fetched (from memory).
  Excluding `apply` from `--allow-commands` has the same effect. inferred from
  fetched flag description.
- GitLab: deploy freeze periods: "During a deploy freeze period, GitLab blocks
  all deployments". fetched: https://docs.gitlab.com/ci/environments/deployment_safety/
  (`$CI_DEPLOY_FREEZE` variable used in job rules: not fetched (from memory).)
- GitHub: "Lock branch" makes a branch read-only. fetched:
  https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches
  Environment wait timer and custom deployment protection rules can block
  deploys. fetched: https://docs.github.com/en/actions/reference/workflows-and-actions/deployments-and-environments
- HCP Terraform: per-workspace lock; no org-wide apply stop. not fetched (from memory)
- Spacelift: time-based plan policies as freeze; stack lock. not fetched (from memory)

---

### 7. Code owners and required reviewers before apply

- Atlantis: CODEOWNERS only through GitHub branch protection under
  `mergeable`. fetched: https://www.runatlantis.io/docs/command-requirements
- Digger: no extra config; Digger queries GitHub mergeability before apply;
  needs branch protection "Require review from Code Owners". fetched:
  https://docs.opentaco.dev/ce/howto/codeowners.md
- env zero: GitHub protected branch requirements block comment applies.
  fetched: https://docs.envzero.com/guides/admin-guide/environments/plan-and-apply-from-pr-comments
- GitHub branch protection: require reviews from code owners; "Require approval
  of the most recent reviewable push" (the last pusher cannot be the only
  approver); dismiss stale approvals. fetched:
  https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches
- GitHub environments: up to six required reviewers, "Only one of the required
  reviewers needs to approve"; prevent self-review; wait timer; deployment
  branch and tag rules; custom protection rules; "a job cannot access
  environment secrets until one of the required reviewers approves it";
  administrators can bypass by default. fetched:
  https://docs.github.com/en/actions/reference/workflows-and-actions/deployments-and-environments
- GitLab deployment approvals: rules by user, group or role; "By default, the
  user who triggers a deployment pipeline can't also approve the deployment
  job" (checkbox "Allow pipeline triggerer to approve deployment"). fetched:
  https://docs.gitlab.com/ci/environments/deployment_approvals/
- Spacelift approval policy: N approvals, role-based, self-approval rejection;
  only a user's newest review counts. fetched:
  https://docs.spacelift.io/concepts/policy/approval-policy

---

### 8. Policy checks on pull requests

- Atlantis: `--enable-policy-checks`; conftest on plan output; `policy_sets`
  under `policies:` (`name`, `path`, `source: local`), optional
  `approve_count`, `owners` (users/teams), `prevent_self_approve`,
  `sticky_policy_approvals`, `policy_item_regex`; failures fixed "in a
  successive commit" or approved by owners with `atlantis approve_policies`;
  approvals cleared on re-plan by default; `--clear-policy-approval`.
  `policy_check: false` per repo; `custom_policy_check`. fetched:
  https://www.runatlantis.io/docs/policy-checking and
  https://www.runatlantis.io/docs/server-side-repo-config
  Requirement value `policies_passed` is named in the command-requirements
  page for `state rm`. fetched: https://www.runatlantis.io/docs/command-requirements
- Digger: conftest as a workflow step (`conftest test ./prod.json -p ../policies`);
  failure handling not described. fetched: https://docs.opentaco.dev/ce/howto/using-opa-conftest.md
- Spacelift: plan policy warnings plus approval policy put a run into pending
  review; "A run left in pending review is canceled 30 days after it was
  created." fetched: https://docs.spacelift.io/concepts/run/proposed
- HCP Terraform: Sentinel/OPA policy checks run in speculative plans with
  advisory, soft-mandatory (overridable) and hard-mandatory levels. not fetched (from memory)

---

### Gaps

- Digger: who may run comment commands, author check, fork handling, edited
  comments. Not on fetched pages.
- Spacelift: who may run `/spacelift` comments; whether default plan comments
  update in place.
- Atlantis: edited-comment handling and new-commit plan discard are from memory.

## CLI-driven remote execution over the TFE-compatible API: research facts

Collected 2026-10-30 for an OpenDustpan RFC (remote `tofu plan` / `tofu apply`
with a `cloud` block, runs executed by a customer-hosted runner).

### Sources and how to read the markers

- "fetched: <URL>" means read in this session. For source code the URL names
  the repository, the commit or tag and the file. The code was read from a
  shallow `git clone` of that commit.
- "not fetched (from memory)" means not verified in this session.
- Source snapshots used:
  - OpenTofu `main` at `073d2cd577469b7fd6d587c8e87f7b339b0437f3`
    (2026-10-29, `version/VERSION` = `1.14.0-dev`), plus tag `v1.6.0`.
    Abbreviated below as `OT@073d2cd`.
  - go-tfe tag `v1.101.0` (the version in OpenTofu `go.mod`). go-tfe `main`
    has moved to a new `v2` module. OpenTofu still uses v1. Abbreviated
    `go-tfe@v1.101.0`.
  - go-slug tag `v0.16.8` (OpenTofu `go.mod`, indirect).
  - leg100/otf `main` at `cf7f7836` (2026-07-16).
  - AzBuilder/terrakube `main` at `e38cd055` (2026-10-29).

Code URLs below are written as
`https://github.com/<repo>/blob/<ref>/<path>`.

---

### 0. Summary for the RFC (most design-relevant facts)

1. Remote execution happens only when `GET /organizations/:org/entitlement-set`
   returns `operations: true` AND the workspace `execution-mode` is not
   `"local"`. Any other value (`remote`, `agent`, or something new) means
   remote. The CLI never reads `agent-pool-id`.
2. Plan sequence: create configuration version, `PUT` tar.gz to
   `upload-url` (no Authorization header), poll the configuration version
   every 500 ms for at most 60 polls until `status == "uploaded"`,
   `POST /runs`, `GET /runs/:id/run-events`, `GET /runs/:id?include=task_stages`,
   poll `GET /runs/:id`, stream `log-read-url` with `?limit=&offset=`, read the
   run again, then cost estimate and policy checks only if the run has those
   relationships.
3. `log-read-url` is fetched WITHOUT the bearer token. It must be a
   self-authorizing (signed, short-lived) URL. The stream may be framed with
   STX (0x02) and ETX (0x03). Without the framing the reader falls back to
   polling the plan/apply status until it is terminal.
4. Saved cloud plans exist in OpenTofu since v1.6.0. `tofu plan -out f`
   creates a `provisional` configuration version and a run with
   `save-plan: true`, then writes a JSON bookmark
   `{"remote_plan_format":1,"run_id":"run-…","hostname":"…"}`. `tofu apply f`
   reads the run, checks hostname and workspace and `actions.is-confirmable`,
   then calls `POST /runs/:id/actions/apply`. Run ids must start with `run-`.
5. Apply without `-auto-approve`: run created with `auto-apply: false`, CLI
   prompts, "yes" means `POST /runs/:id/actions/apply`, anything else means
   `POST /runs/:id/actions/discard` (if `is-discardable`). With
   `-auto-approve` the run is created with `auto-apply: true` and the CLI
   never calls `actions/apply`.
6. Ctrl-C: the CLI asks "Do you want to cancel the remote operation?" and on
   "yes" calls `POST /runs/:id/actions/cancel` (only if `is-cancelable`).
   OpenTofu never calls `actions/force-cancel`.
7. Variables (`cloud` block): values from `-var`, `-var-file` AND `TF_VAR_*`
   environment variables are sent as run `variables` (HCL literals).
   `terraform.tfvars` / `*.auto.tfvars` are not sent as run variables; they
   travel inside the uploaded archive. The legacy `remote` backend rejects
   `-var`/`-var-file` with an error and silently drops `TF_VAR_*`.
8. Fatal if missing while a run waits in a queue: `GET /workspaces/:id/runs`,
   `GET /organizations/:org/runs/queue`, `GET /organizations/:org/capacity`.
   Fatal if missing always: `GET /runs/:id/run-events` (OTF stubs it with an
   empty list). Tolerated: an "Invalid include parameter" error for
   `include=task_stages`.
9. The redacted JSON plan (`GET /plans/:id/json-output-redacted`) is fetched
   only if the workspace has `structured-run-output-enabled: true` and the
   server identifies as HCP Terraform or as TFE ≥ `v202302-1`. A server can
   avoid this endpoint and send human-readable plan text in the log stream.
10. The `cloud` block refuses to work if the `TFP-API-Version` response
    header is absent or below `2.5`.

---

### 1. Exact call sequence of the OpenTofu `cloud` block

All paths are relative to the service discovery base for `tfe.v2`
(`/.well-known/terraform.json`), normally `/api/v2/`.

#### 1.1 Configure (every command)

fetched: https://github.com/opentofu/opentofu/blob/073d2cd/internal/cloud/backend.go (lines 255-416),
https://github.com/hashicorp/go-tfe/blob/v1.101.0/tfe.go

- Service discovery ID: `tfe.v2` (`tfeServiceID = "tfe.v2"`). The `remote`
  backend uses `tfe.v2.1` (and `state.v2` when `TF_FORCE_LOCAL_BACKEND` is
  set). fetched: OT@073d2cd `internal/cloud/backend.go:45`,
  `internal/backend/remote/backend.go:44,263-272`.
- Request headers on every API call: `Authorization: Bearer <token>`,
  `Terraform-Version: <tofu version>`, `X-Terraform-Integration: cloud`,
  `User-Agent: <OpenTofu UA>`. fetched: OT@073d2cd `internal/cloud/backend.go:46-47,322-331`;
  `version/version.go` (`const Header = "Terraform-Version"`).
- go-tfe `NewClient` calls `GET ping` once and reads response headers
  `TFP-API-Version`, `X-RateLimit-Limit`, `X-TFE-Version`,
  `TFP-AppName`. `IsCloud()` is true only if `TFP-AppName == "HCP Terraform"`;
  otherwise the client is treated as "Enterprise". fetched: go-tfe@v1.101.0 `tfe.go:36-48,560-590,735-765`.
- `GET organizations/:org/entitlement-set`. A 404 is reported as
  "organization not found". `forceLocal = forceLocal || !entitlements.Operations`.
  So the server must return `operations: true` to get remote runs.
  fetched: OT@073d2cd `internal/cloud/backend.go:348-411`.
- API version gate: if `TFP-API-Version` is unparsable or `< 2.5`, error
  "Unsupported cloud backend version". fetched: same file, 379-400.
- Workspace resolution: `GET organizations/:org/workspaces/:name`
  (`Workspaces.Read`). If 404 and the mapping allows, the CLI creates it
  (`POST organizations/:org/workspaces`, optional `Projects.List`/`Create`,
  then `PATCH workspaces/:id` with `terraform-version` = the OpenTofu
  version, failure only logged). Tag mapping uses
  `POST workspaces/:id/relationships/tags` (`AddTags`). fetched: OT@073d2cd
  `internal/cloud/backend.go:565-765`.

#### 1.2 Operation dispatch

fetched: OT@073d2cd `internal/cloud/backend.go:783-950`

- `Workspaces.Read` again. If `forceLocal` or `execution-mode == "local"`,
  the operation runs locally (cloud is then state storage only).
  `isLocalExecutionMode(execMode) == (execMode == "local")`.
- `plan` maps to `opPlan`; `apply` and `destroy` map to `opApply`;
  `refresh` maps to `opApply` with `PlanMode = RefreshOnlyMode`,
  `PlanRefresh = true`, `AutoApprove = true`. Other operations: error.
- Only one operation at a time per backend instance (`opLock`).

#### 1.3 Pre-checks in `opPlan` / `opApply` (client side)

fetched: OT@073d2cd `internal/cloud/backend_plan.go`, `backend_apply.go`

- Plan requires `workspace.permissions.can-queue-run`, else error
  "Insufficient rights to generate a plan".
- Apply requires `can-update` OR `can-queue-apply`, else error
  "Insufficient rights to apply changes".
- Apply is refused if the workspace has a `vcs-repo` ("Apply not allowed for
  workspaces with a VCS connection"). Plan is allowed.
- `-parallelism` other than the default: error "Custom parallelism values
  are currently not supported".
- `-exclude`: error "The -exclude option is not currently supported for
  remote plans."
- `plan` with a saved plan file as input: error.
- `apply` with a saved LOCAL plan: error "Applying a saved local plan is not
  supported".
- No configuration and not destroy: error.

#### 1.4 Remote plan (`b.plan`) — used by `plan`, `apply`, `destroy`, `refresh`

fetched: OT@073d2cd `internal/cloud/backend_plan.go:119-374`, `backend_common.go`,
`backend_run_warning.go`, `backend_taskStages.go`

1. `POST workspaces/:workspace_id/configuration-versions` with attributes
   `auto-queue-runs: false`, `speculative: <planOnly>`,
   `provisional: <plan -out given>`. `planOnly` = operation is `plan` and no
   `-out`. So: `tofu plan` gives `speculative: true, provisional: false`;
   `tofu plan -out` gives `speculative: false, provisional: true`;
   `tofu apply` gives `speculative: false, provisional: false`.
2. Directory to pack: the absolute config dir with the workspace
   `working-directory` suffix trimmed. So if the workspace has a
   working directory, the PARENT tree is uploaded (the CLI prints a notice).
   For a destroy without config, an empty temp dir with the working
   directory created inside.
3. `PUT <upload-url>` with the tar.gz body (see section 3).
4. Poll `GET configuration-versions/:id` every 500 ms, at most 60 times
   (about 30 s), until `status == "uploaded"`. Otherwise
   "Failed to upload configuration files: operation timed out".
5. `POST runs` (JSON:API `runs` resource) with:
   - `refresh: <op.PlanRefresh>` (always sent)
   - `auto-apply: <op.AutoApprove>` (always sent; the value of
     `-auto-approve`, so false for `tofu plan`; true for `tofu refresh`)
   - `save-plan: <plan -out given>` (always sent)
   - `refresh-only: true` only in refresh-only mode
   - `is-destroy: true` only in destroy mode
   - `target-addrs: [...]` if `-target`
   - `replace-addrs: [...]` if `-replace`
   - `variables: [{"key":..,"value":<HCL literal>}]` (always sent, can be empty)
   - `allow-config-generation: true` if `-generate-config-out`
   - relationships `configuration-version`, `workspace`.
   - NEVER sent by OpenTofu: `plan-only`, `allow-empty-apply`, `message`,
     `terraform-version`, `debugging-mode`, `policy-paths`.
     fetched: grep of OT@073d2cd `internal/cloud` and `internal/backend/remote`.
6. If `-lock-timeout` > 0: a goroutine waits that long, then `GET runs/:id`;
   if `status == "pending"` and `actions.is-cancelable`, it sends SIGINT to
   itself with AutoApprove set (cancel without prompt).
7. Prints `https://<hostname>/app/<org>/<workspace>/runs/<run-id>` (the run
   URL format is fixed in the CLI; the server cannot change it).
8. `GET runs/:id/run-events`. An error here is FATAL (returned). Events with
   `action` in `changed_policy_enforcements`, `changed_task_enforcements`,
   `ignored_policy_sets` are printed as warnings.
9. `GET runs/:id?include=task_stages`. If the error text ends with
   "Invalid include parameter", the CLI continues without task stages;
   any other error is fatal. If a `pre_plan` stage exists, poll
   `GET task-stages/:id?include=task_results,policy_evaluations` with
   backoff 4-12 s.
10. `waitForRun`: poll `GET runs/:id` with backoff
    `min(1000ms * 2^(i/5), 3000ms)` while `status` is `pending` or
    `confirmed`. While waiting (first poll, then every 30 s) it calls:
    - `GET organizations/:org/workspaces/:name` (current run, locked);
    - if the workspace is locked and its current run is `pending`: prints
      "waiting for the manually locked workspace";
    - if this run is not the current run: `GET workspaces/:id/runs`
      (paged) to count runs ahead (skips applied, canceled, discarded,
      errored; skips `planned` for plan operations);
    - then `GET organizations/:org/runs/queue` (paged) to find
      `position-in-queue`, and if > 0 `GET organizations/:org/capacity`
      (`pending`, `running`).
    All of these errors are FATAL ("Failed to retrieve queue" etc).
11. Plan logs: `GET plans/:id`, then read `log-read-url` (section 1.7).
    During the plan phase, JSON log lines of type `outputs`,
    `change_summary` and `planned_change` are skipped; other JSON lines are
    rendered; non-JSON lines are printed as-is.
12. `GET runs/:id?include=workspace,plan`. If run status is not `errored`,
    `canceled`, `discarded` (or config generation applies), and structured
    output applies (section 1.8), `GET plans/:id/json-output-redacted` (raw
    JSON, `Accept: application/json`, bearer token, retried up to 10 times)
    and render it.
13. `GET runs/:id`. If a `post_plan` task stage exists, wait on it.
14. If the run has a `cost-estimate` relationship: poll
    `GET cost-estimates/:id` (statuses `finished`, `pending`, `queued`,
    `skipped_due_to_targeting`, `errored`, `canceled`). Unknown status: error.
15. If `relationships.policy-checks` is non-empty: for each,
    `GET policy-checks/:id` + read its logs; `soft_failed` may prompt
    "override" and `POST policy-checks/:id/actions/override`.
    Unknown status: error.

Plan run then ends. For `tofu plan -out FILE`, the bookmark is written
(section 1.5). The plan never calls apply or discard: a speculative run is
just left.

#### 1.5 `tofu plan -out` and `tofu apply FILE` (saved cloud plan)

fetched: OT@073d2cd `internal/cloud/backend_plan.go:97-112`,
`internal/cloud/cloudplan/saved_plan.go`, `internal/cloud/backend_apply.go`,
`internal/cloud/backend_show.go`; tag v1.6.0 `internal/cloud/backend_plan.go`
and `CHANGELOG.md` line 30.

- Supported since OpenTofu v1.6.0. CHANGELOG v1.6.0: "cloud: Remote plans
  on cloud backends can now be saved using the `-out` flag, referenced in
  the `show` command, and applied by specifying the plan file name."
- The file is JSON: `{"remote_plan_format":1,"run_id":"run-…","hostname":"…"}`,
  mode 0644. Loading fails if `remote_plan_format != 1`, `hostname` empty,
  or `run_id` does not start with `run-`.
  Design impact: OpenDustpan run ids exposed to the TFE API must use the
  `run-` prefix.
- `tofu apply FILE`:
  1. hostname in the file must equal the configured hostname;
  2. `GET runs/:id?include=workspace`; the run's workspace id must equal
     the current workspace;
  3. `actions.is-confirmable` must be true, else a status-specific error
     (`applied`, `applying`/`apply_queued`/`confirmed`, `canceled`,
     `discarded`, `errored`, `planned_and_finished`, `policy_override`);
  4. no prompt; `POST runs/:id/actions/apply` (empty body `{}` options),
     then pre-apply task stage, `waitForRun`, apply logs.
  Observation from the code (not tested): with `tofu apply -auto-approve FILE`,
  `!op.AutoApprove` is false, so the CLI does NOT call `actions/apply` on the
  saved run. Check with e2e.
- `tofu show FILE`: `GET runs/:id?include=plan,workspace`; refuses unless the
  plan is in a terminal state; then `GET plans/:id/json-output-redacted` (if
  redacted) or `GET plans/:id/json-output`.
- TFE semantics (fetched: https://developer.hashicorp.com/terraform/cloud-docs/api-docs/run):
  `save-plan` "plans and checks the configuration without becoming the
  workspace's current run"; run status `planned_and_saved` "is only used for
  saved plan runs"; configuration versions for saved plan runs should be
  `provisional`. HCP docs: saved plans need Terraform CLI ≥ 1.6.0, and can
  only be applied in the workspace that planned them (fetched:
  https://developer.hashicorp.com/terraform/cloud-docs/run/cli).

#### 1.6 Remote apply (`opApply` without a saved plan)

fetched: OT@073d2cd `internal/cloud/backend_apply.go`, `backend_common.go` (`confirm`)

1. Runs `b.plan` (above). Stops (success) if `!has-changes` or status
   `canceled` / `errored`.
2. `GET runs/:id`. If not `-auto-approve` and not `actions.is-confirmable`,
   stops (for example a policy hard-fail).
3. If not `-auto-approve` and input is enabled: prompt
   "Do you want to perform these actions in workspace "<ws>"?" (destroy has
   its own text). Only `yes` approves. In parallel the CLI polls
   `GET runs/:id` every 3 s: if `is-confirmable` turns false the run was
   approved elsewhere (UI/API) and the CLI goes on without calling apply; if
   status is `discarded` the CLI stops with "Apply discarded."
4. Answer not `yes`: `GET runs/:id`; if `actions.is-discardable`,
   `POST runs/:id/actions/discard`; returns "Apply discarded."
5. `-input=false` without `-auto-approve`: error
   (`errApplyNeedsUIConfirmation`).
6. Answer `yes`: `POST runs/:id/actions/apply`.
7. With `-auto-approve`: the run was created with `auto-apply: true`; the CLI
   does NOT call `actions/apply` and waits.
8. `GET runs/:id?include=task_stages`; `pre_apply` stage if present.
9. `waitForRun` ("apply") then `GET applies/:id` and read its
   `log-read-url`. The first 3 lines of the apply log are skipped (they
   repeat the version header), then JSON lines are rendered.
10. After the operation: `GET runs/:id`; `has-changes` drives the
    `PlanEmpty` result; status `canceled` / `errored` means exit failure.

#### 1.7 Log streaming protocol (what the server must serve)

fetched: go-tfe@v1.101.0 `logreader.go`, `plan.go:107-148`, `apply.go`, `tfe.go:232-262,290-302`

- `Plans.Logs`: `GET plans/:id`; error if `log-read-url` is empty. Same for
  `applies/:id`.
- Each read: `GET <log-read-url>?limit=<buffer len>&offset=<bytes read so far>`.
  The client REPLACES the URL's whole query string (`RawQuery = "limit=…&offset=…"`).
  Design impact: a signature cannot live in the query string; put it in the
  path (OTF signs the path; Terrakube encrypts the id into the path).
- Headers: only the client's default headers (`Terraform-Version`,
  `X-Terraform-Integration`, `User-Agent`). NO `Authorization` header.
  The URL must authorize the read by itself.
- Framing: if the first byte of the stream is STX (0x02) the reader strips
  it; a final ETX (0x03) marks the end. Backoff between empty reads:
  `min(500ms * 2^(i/5), 2000ms)`.
- End detection: (STX and ETX seen) OR (STX seen and every 10th empty read)
  OR (no STX and more than one read): then it calls `GET plans/:id` and
  stops when the plan status is `canceled`, `errored`, `finished` or
  `unreachable` (apply: same set). So framing is optional but avoids extra
  status calls and early stop risk.
- Content: OpenTofu tries to JSON-decode each line as a `tofu -json` log
  message (`@level`, `@message`, `type`…). Lines that are not JSON are
  printed raw. So a runner can forward either `-json` output (needed for
  structured rendering) or plain text.
- HCP docs only show `log-read-url` as `https://archivist.terraform.io/v1/object/...`
  and do not document expiry or auth. fetched:
  https://developer.hashicorp.com/terraform/cloud-docs/api-docs/plans

#### 1.8 Structured run output and JSON plans

fetched: OT@073d2cd `internal/cloud/backend_plan.go` (`shouldRenderStructuredRunOutput`),
`backend_common.go` (`readRedactedPlan`)

- The redacted plan is fetched only if
  `workspace.structured-run-output-enabled == true` AND (`IsCloud()` OR
  `X-TFE-Version` parses as a release ≥ `v202302-1`). If the version is
  unknown, false.
- Path `plans/:id/json-output-redacted`, raw JSON (not JSON:API).
  HCP public docs do not list this path (they list `json-output` and
  `sanitized-plan`); fetched: https://developer.hashicorp.com/terraform/cloud-docs/api-docs/plans.
  `GET plans/:id/json-output` "generate[s] a temporary authenticated URL",
  redirect lifetime 1 minute (same page).
- Design option: serve `structured-run-output-enabled: false` and stream
  human-readable `tofu plan` output, or set it true, identify as TFE ≥
  v202302-1 and serve the redacted JSON plan.

#### 1.9 Required vs optional endpoints (summary)

From the code above (fetched):

| Endpoint | Used by | If absent |
|---|---|---|
| `GET ping` (headers only) | client setup | no `TFP-API-Version` → cloud block refuses |
| `GET organizations/:org/entitlement-set` | configure | error; `operations:false` → local only |
| `GET organizations/:org/workspaces/:name` | every op, queue wait | fatal |
| `POST workspaces/:id/configuration-versions` | plan | fatal |
| `PUT <upload-url>` | plan | fatal |
| `GET configuration-versions/:id` | plan (poll) | fatal |
| `POST runs` | plan | fatal |
| `GET runs/:id` (+`include=task_stages`, `include=workspace,plan`, `include=workspace`) | all | fatal; only "Invalid include parameter" tolerated for task_stages |
| `GET runs/:id/run-events` | plan | fatal (OTF stubs empty list) |
| `GET workspaces/:id/runs` | queue wait only | fatal when reached |
| `GET organizations/:org/runs/queue` | queue wait only | fatal when reached |
| `GET organizations/:org/capacity` | queue position > 0 | fatal when reached |
| `GET plans/:id`, `GET applies/:id` | logs | fatal |
| `GET <log-read-url>?limit&offset` | logs | fatal |
| `GET plans/:id/json-output-redacted` | SRO only | skipped if SRO off |
| `POST runs/:id/actions/apply` | apply | fatal |
| `POST runs/:id/actions/discard` | apply, answer ≠ yes | fatal |
| `POST runs/:id/actions/cancel` | Ctrl-C | fatal (error printed) |
| `GET cost-estimates/:id` | only if run has cost-estimate relation | skipped |
| `GET policy-checks/:id` + logs, `.../actions/override` | only if run has policy-checks | skipped |
| `GET task-stages/:id`, `.../actions/override` | only if run includes task stages | skipped |

Run attributes the CLI reads: `status`, `has-changes`,
`actions.is-confirmable`, `actions.is-cancelable`, `actions.is-discardable`,
relationships `plan`, `apply`, `workspace`, `cost-estimate`,
`policy-checks`, `task-stages`. Workspace attributes read:
`execution-mode`, `permissions.can-queue-run`, `permissions.can-update`,
`permissions.can-queue-apply`, `vcs-repo`, `working-directory`, `locked`,
`current-run`, `terraform-version`, `structured-run-output-enabled`.
Plan/apply: `status`, `log-read-url`, `generated-configuration`.
Run statuses the CLI switches on (go-tfe constants): `pending`, `confirmed`,
`planned`, `planned_and_finished`, `planned_and_saved`, `policy_override`,
`post_plan_awaiting_decision`, `apply_queued`, `applying`, `applied`,
`discarded`, `errored`, `canceled`. Plan/apply statuses: `pending`,
`queued`, `running`, `finished`, `errored`, `canceled`, `unreachable`
(plus `created`, `mfa_waiting`). fetched: go-tfe@v1.101.0 `run.go`, `plan.go`, `apply.go`.

### 2. The legacy `remote` backend: differences

fetched: OT@073d2cd `internal/backend/remote/backend.go`, `backend_plan.go`,
`backend_apply.go`, `backend_common.go`

- Discovery service `tfe.v2.1`. Config `workspaces { name | prefix }`.
  Auto-creates a missing workspace with `terraform-version` = the OpenTofu
  version (release or rc builds only).
- StateMgr: if not ignoring version conflicts, the workspace
  `terraform-version` must EQUAL the local OpenTofu version exactly
  (`"latest"` excepted). The `cloud` block uses a looser `~> major.minor`
  check (VerifyWorkspaceTerraformVersion).
- Same remote-vs-local decision: `entitlement-set.operations` and
  `execution-mode != "local"`.
- Plan: configuration version `speculative = (op is plan)`, never
  `provisional`. No `-out` ("Saving a generated plan is currently not
  supported"), no saved plan apply, no `-generate-config-out`.
- Run create sends only `refresh`, `refresh-only`, `is-destroy`,
  `target-addrs`, `replace-addrs`. It does NOT send `auto-apply`,
  `save-plan` or `variables`.
- Variables: `-var` or `-var-file` → error "Run variables are currently not
  supported" (asks for `*.auto.tfvars` or workspace variables). `TF_VAR_*`
  values are silently not sent (only `ValueFromCLIArg` and
  `ValueFromNamedFile` trigger the error).
- API version gates (from `TFP-API-Version`): `-target` needs ≥ 2.3;
  `-refresh=false`, `-replace`, `-refresh-only` need ≥ 2.4.
- Apply: uses the WORKSPACE `auto-apply` attribute. If `auto-apply` false and
  `run.permissions.can-apply` false, it discards the run and errors. If the
  workspace auto-applies, the CLI does not prompt. Else prompt, then
  `actions/apply` (also with `-auto-approve`, which skips only the prompt).
- No `run-events` call and no task stages in the remote backend
  (only the cloud package has `backend_run_warning.go` and
  `backend_taskStages.go`).
- Same queue wait (`workspaces/:id/runs`, `runs/queue`, `capacity`),
  same cost estimate and policy check handling, same cancel prompt.
- Env var `TFE_RUN_ID`: when set (inside a remote run), both backends attach
  the run to state versions they create (`run` relationship), and the remote
  backend disables intermediate state snapshots. fetched:
  `internal/backend/remote/backend.go:615-630`, `internal/cloud/state.go:301-330`.

### 3. Configuration version upload format

fetched: go-tfe@v1.101.0 `configuration_version.go:164-181,306-323`, `tfe.go:232-262,1129-1150`;
go-slug@v0.16.8 `slug.go`, `internal/ignorefiles/terraformignore.go:172-189`;
https://developer.hashicorp.com/terraform/cloud-docs/api-docs/configuration-versions

- Archive: tar inside gzip (`gzip.BestSpeed`), built by `slug.Pack(path, w, true)`:
  `.terraformignore` applied, symlinks dereferenced (`true`).
- Default exclusions when no `.terraformignore`: `**/.terraform/**` but NOT
  `**/.terraform/modules/**` (negated, so installed modules ARE uploaded),
  and `**/.git/**`.
- Symlinks with targets outside the root: dereferenced into the archive by
  the CLI (OpenTofu v1.6.0 fixed nested symlinks, CHANGELOG #686).
  On the server side, OpenDustpan must treat the archive as untrusted
  (path traversal, symlinks, size). go-slug's `Unpack` is one reference.
- Upload: `PUT <upload-url>`, headers `Content-Type: application/octet-stream`,
  `Accept: application/json, */*` plus default headers. NO `Authorization`.
  Retried via retryablehttp. So `upload-url` must be self-authorizing too.
- HCP docs: `upload-url` is returned on create and "After creation, the URL
  is hidden on the resource and no longer available." Statuses: `pending`,
  `fetching`, `uploaded`, `archived`, `errored` (TFE adds
  `backing_data_soft_deleted`, `backing_data_permanently_deleted`).
  Attribute defaults: `auto-queue-runs` true, `speculative` false,
  `provisional` false. No size limit stated on the page.
- Size limit: no official TFE/HCP limit found (web search returned nothing).
  OTF default `max-config-size` = 10 MiB (`DefaultConfigMaxSize = 1024*1024*10`,
  413-style rejection via `http.MaxBytesHandler`). fetched: otf@cf7f7836
  `internal/configversion/configuration_version.go:23`, `internal/configversion/api/tfe.go:31,175-193`.
  HCP limits for state (512 MB TFE default) and policies (10 MB): from search
  snippet only, not fetched.

### 4. TFE agent pools and comparable products

#### 4.1 TFE / HCP Terraform

fetched: https://developer.hashicorp.com/terraform/cloud-docs/api-docs/agents,
https://developer.hashicorp.com/terraform/cloud-docs/agents/agent-pools,
https://developer.hashicorp.com/terraform/cloud-docs/agents/requirements,
https://developer.hashicorp.com/terraform/cloud-docs/api-docs/workspaces

- Endpoints: `GET|POST /organizations/:organization_name/agent-pools`,
  `GET|PATCH /agent-pools/:id`, `DELETE /agent-pools/:agent_pool_id`.
  Tokens: `/agent-pools/:id/authentication-tokens` (seen in OTF's TFE
  routes; HCP page not read for it).
- Pool attributes: `name` (letters, digits, `-`, `_`), `organization-scoped`
  ("If true, all workspaces in the organization can use the agent pool"),
  relationships `allowed-workspaces`, `allowed-projects`,
  `excluded-workspaces`.
- Agent statuses: `idle`, `busy`, `unknown`, `errored`, `exited`; delete only
  when `unknown`, `errored` or `exited`.
- Workspace: `execution-mode` in `remote`, `local`, `agent`;
  `agent-pool-id` "Required when `execution-mode` is set to `agent`";
  `operations` is deprecated in favor of `execution-mode`.
- "HCP Terraform may use different agents for the plan and apply operations."
- Agent network: outbound only. `app.terraform.io` tcp/443 "Polling for new
  workloads, providing status updates"; `archivist.terraform.io` tcp/443
  "Blob Storage"; `registry.terraform.io`, `releases.hashicorp.com`,
  `agents.terraform.io` tcp/7146. No inbound.
- tfc-agent is closed source; its job protocol is not published.
  From memory (not fetched): one run per agent process at a time; agent
  tokens per pool; the job payload carries a run-scoped token; hooks
  (`pre-plan`, `post-plan`, `pre-apply`, `post-apply`) run scripts from the
  agent's hooks directory.
- The OpenTofu CLI does not read `agent-pool-id`; for the CLI, `agent` is
  just "not local". fetched: OT@073d2cd `internal/cloud/backend.go:1114`.
  Design impact: OpenDustpan can map its runner labels to whatever it
  shows as `execution-mode`/`agent-pool-id` without CLI impact; only
  `"local"` changes CLI behavior.

#### 4.2 Others (short)

- Spacelift worker pools (fetched: https://docs.spacelift.io/concepts/worker-pools):
  outbound only (MQTT to a broker by default, or HTTPS long-poll on 443);
  each stack/module must be explicitly attached to a pool; one run per
  worker; worker authenticates with a private key + certificate issued from
  a CSR, delivered as `SPACELIFT_TOKEN` + `SPACELIFT_POOL_PRIVATE_KEY`; the
  private key also encrypts run state so only the pool can read it; labels
  are for sorting/filtering pools.
- env zero self-hosted agent (fetched:
  https://docs.envzero.com/guides/admin-guide/self-hosted-kubernetes-agent):
  Kubernetes (Helm) or Docker; "requires an internet connection but no
  inbound network access"; pools assigned to projects, unassigned projects
  use the organization default agent; secrets stay on customer
  infrastructure.
- Scalr agent pools (fetched: https://docs.scalr.io/scalr/docs/agent-pools.md):
  agent connects out, "Scalr.io will never need network access back to the
  agent"; pools as account default, per environment, or per workspace
  override; "Each agent will have a limit of 5 concurrent runs at time".

### 5. Other implementations

#### 5.1 OTF (leg100/otf)

fetched: otf@cf7f7836 `internal/run/api/tfe.go`, `internal/configversion/api/tfe.go`,
`internal/runner/{api/api.go,service.go,operation.go,job.go,tokens.go,pool.go,allocator.go}`,
`internal/run/{chunk.go,phase_writer.go,run.go}`, `internal/tfeapi/signer.go`

- TFE routes served: `POST /runs`, `GET /runs`, `GET /workspaces/{id}/runs`,
  `GET /runs/{id}`, `POST /runs/{id}/actions/{apply,discard,cancel,force-cancel}`,
  `GET /organizations/{org}/runs/queue`, `GET /plans/{id}`,
  `GET /plans/{id}/json-output`, `GET /applies/{id}`,
  `GET /runs/{id}/run-events` (stub: empty list, comment "as of terraform
  v1.5, the cloud backend makes a call to this endpoint"),
  `GET /runs/{run_id}/logs/{phase}` (signed), configuration versions
  (`POST /workspaces/{id}/configuration-versions`, `GET`, `PUT
  /configuration-versions/{id}/upload`, download), `GET
  /organizations/{name}/entitlement-set`, agent pools and
  `/agent-pools/{id}/authentication-tokens`. No `capacity` route found and
  no `json-output-redacted` found by grep.
- Saved plan: OTF accepts `save-plan` (`RunOperationSavePlan = "save_plan"`).
- Logs: `log-read-url` = absolute URL of `/runs/{run}/logs/{phase}` signed
  with a 1-hour expiry (`surl` signer, path signed). The handler returns the
  chunk at `offset`/`limit`. Phase writer prefixes STX and ends with ETX.
- Upload URL likewise signed (state upload URLs are signed with 1 h).
- Runner protocol (OTF-specific, not TFE): agent `POST /agents/register`,
  `GET /agents/await-allocated-jobs` (long poll: returns allocated jobs
  immediately or waits on a pub/sub event), `POST /jobs/start`,
  `POST /jobs/finish`, `GET /jobs/{id}/await-signal` (cancel / force-cancel
  signal), `POST /agents/status`, `PUT /runs/{id}/logs/{phase}`,
  `GET|PUT /runs/{id}/planfile`, `GET|PUT /runs/{id}/lockfile`.
- Allocation is server-push: a single active allocator assigns jobs to
  runners by capacity (`allocateJob`), runners do not race to claim.
- Pools: `organization_scoped`, `allowed_workspace_ids`, assigned
  `workspace_ids`; errors if a workspace is not allowed to use a pool.
  Workspace execution modes `local`, `remote`, `agent`.
- Job token: per job, expiry 60 min. Authorization (`Job.CanAccess`): same
  organization only; get organization/entitlements; get+list modules;
  same workspace only: state versions get/download, create ONLY in apply
  phase; workspace get; run get+cancel, apply only in plan phase; list
  variables and variable sets; upload log chunks; download config version.
  Token exposed to the engine as a host credential env var.
- Execution: download engine binary, download config, read vars, dynamic
  credentials, SSH key, write `.tfvars`, REMOVE the backend block from the
  config (HCL rewrite), download current state, `init`, `plan`, convert to
  JSON, upload plan file + JSON + lock file; apply phase downloads lock file
  and plan file, `init`, `apply`, then uploads state.
- Force-cancel cool-off: 10 s after cancel (`ForceCancelCoolOff`).
  Cancel = SIGINT to engine; force = SIGKILL.

#### 5.2 Terrakube

fetched: terrakube@e38cd055 `api/src/main/java/io/terrakube/api/plugin/state/RemoteTfeController.java`,
`RemoteTfeService.java`, `.../scheduler/job/tcl/executor/persistent/PersistentExecutorService.java`,
`executor/.../security/WorkspaceSecurityImpl.java`, `executor/.../online/OnlineModeServiceImpl.java`

- TFE routes under `/remote/tfe/v2/`: `ping`, `organizations/{org}/entitlement-set`
  (`operations: true`), `organizations/{org}/capacity`, workspaces, state
  versions, `POST /workspaces/{id}/configuration-versions`,
  `GET|PUT /configuration-versions/{id}`, `POST /runs`,
  `GET /workspaces/{id}/runs`, `GET /organizations/{org}/runs/queue`,
  `GET /runs/{id}/run-events`, `GET /runs/{id}`,
  `POST /runs/{id}/actions/apply`, `POST /runs/{id}/actions/discard`,
  policy-checks (list, show, output, override), `GET /plans/{id}`,
  `GET /applies/{id}`, `GET /plans/logs/{a}/{b}`, `GET /applies/logs/{a}/{b}`.
  No `actions/cancel` route found.
- Reads `is-destroy`, `auto-apply`, `refresh-only`, `variables`
  (`CliRunVariables.parse`), `speculative`; reports workspace
  `auto-apply: false` and its `execution-mode`.
- Logs: `log-read-url` = `https://<host>/remote/tfe/v2/plans/logs/<encrypted plan id>`
  (encrypted id in the path acts as the credential); handler takes
  `offset`/`limit`, reads all records of the job's Redis stream, filters by
  step, slices. Plan step number 100, apply 200.
- Plan object hard-codes `actions: {is-confirmable: true, is-discardable: false}`.
- Execution is server-push: the API `POST`s the job to the executor at
  `<agent url>/api/v1/terraform-rs` (per-workspace agent URL, else org
  global var `TERRAKUBE_DEFAULT_EXECUTOR`, else the default executor URL).
  So the executor must be reachable INBOUND from the API.
- Executor auth: it mints its own JWTs signed with a shared HMAC
  `internalSecret` (60-minute or 1-minute tokens), not run-scoped tokens
  issued by the API.

#### 5.3 OpenTaco (diggerhq)

- Not verified. Search found only secondary sources: a blog post (Jan 2026)
  reports `tofu/terraform login` and a `cloud` block working for state; it
  also shows a go-tfe decode error. No source found that confirms TFE
  remote runs. (WebSearch results, not fetched: https://dev.classmethod.jp/articles/open-taco-terraform-statefile-management/)

### 6. Run-scoped tokens

- HCP docs (fetched: https://developer.hashicorp.com/terraform/cloud-docs/run/run-environment):
  "HCP Terraform generates a unique per-run API token and provides it to the
  Terraform worker in the CLI config file." It "can read and write state
  data for the workspace associated with the run", can download private
  registry modules, "may be granted access to read state from other
  workspaces in the organization" (remote state sharing), "Per-run tokens
  cannot make any other calls to the HCP Terraform API", and "They become
  invalid after the run is completed." HCP "uses the cloud block for runs,
  overriding any existing backend in the configuration."
- Injected env vars (same page): `TFC_RUN_ID`, `TFC_WORKSPACE_NAME`,
  `TFC_WORKSPACE_ID`, `TFC_WORKSPACE_SLUG`, `TFC_PROJECT_NAME`,
  `TFC_PROJECT_ID`, `TFC_CONFIGURATION_VERSION_GIT_BRANCH`,
  `TFC_CONFIGURATION_VERSION_GIT_COMMIT_SHA`, `TFC_CONFIGURATION_VERSION_GIT_TAG`.
  Also usable as input variables of the same name.
- OpenTofu reads `TFE_RUN_ID` (links state versions to the run), `TFC_RUN_ID`
  and `TFC_AGENT_VERSION` (import blocks inside a remote run need API ≥ 2.6
  and agent ≥ 1.10). fetched: OT@073d2cd `internal/cloud/state.go:301-330`,
  `internal/cloud/backend_plan.go` (`AssertImportCompatible`).
- From memory (not fetched): workers also set `ATLAS_TOKEN` / `TFE_TOKEN`
  with the run token and write a backend override file (historically
  `zzz_backend_override.tf`) pointing at the workspace.
- OTF comparison: job token, 60 min, scoped per section 5.1.

### 7. Variables in remote runs

- `cloud` block: `ParseCloudRunVariables` keeps declared variables whose
  source is `ValueFromNamedFile` (`-var-file`), `ValueFromCLIArg` (`-var`)
  or `ValueFromEnvVar` (`TF_VAR_*`), encodes each value as an HCL literal
  (`hclwrite.TokensForValue`), and sends them as run `variables`. Undeclared
  variables produce diagnostics. Same in v1.6.0. fetched: OT@073d2cd
  `internal/cloud/cloud_variables.go`; tag v1.6.0 same file.
- Auto-loaded files (`terraform.tfvars`, `*.auto.tfvars`) are not sent as run
  variables; they are inside the uploaded archive and the remote tofu loads
  them. HCP docs: remote runs use workspace variables, variable sets
  (global, project, workspace) and `*.auto.tfvars`; "Terraform can use shell
  environment variables prefixed with `TF_VAR_` as input variables for the
  run"; provider credentials must be set in the workspace. fetched:
  https://developer.hashicorp.com/terraform/cloud-docs/run/cli
- TFE API: run `variables` "must be expressed as an HCL literal"; run
  variables take priority over workspace variables (go-tfe comment
  "prioritized over variables defined on the workspace"). fetched:
  https://developer.hashicorp.com/terraform/cloud-docs/api-docs/run, go-tfe@v1.101.0 `run.go:404-433`.
- Other environment (provider credentials, `AWS_*`, etc.) is never sent.
- `remote` backend: section 2 (error for `-var`/`-var-file`, silent drop of
  `TF_VAR_*`). There is no warning for `TF_VAR_*` in either backend.

### 8. Cancel behavior

- CLI (fetched: OT@073d2cd `internal/cloud/backend.go:823-950`): the first
  interrupt cancels `stopCtx`; the running step returns `context.Canceled`;
  then `GET runs/:id`; if `actions.is-cancelable`: unless AutoApprove, prompt
  "Do you want to cancel the remote operation?" ("Only 'yes' will be
  accepted to cancel."); on yes `POST runs/:id/actions/cancel` (empty
  comment) and print "operation cancelled"; otherwise print "not
  cancelled" and the remote run goes on. The lock-timeout path cancels
  without prompt. OpenTofu has no call to `actions/force-cancel` (only in
  the test mock).
- HCP docs (fetched: https://developer.hashicorp.com/terraform/cloud-docs/run/cli)
  say Ctrl-C during a remote plan only stops log streaming and during an
  apply cancels it only while pending. The OpenTofu code above does try
  `actions/cancel` whenever `is-cancelable` is true; the server decides by
  setting `is-cancelable`.
- TFE API (fetched: https://developer.hashicorp.com/terraform/cloud-docs/api-docs/run):
  `cancel` "can be used to interrupt a run that is currently planning or
  applying", sends INT like Ctrl-C, 409 otherwise. `force-cancel` ends the
  run at once, sets `canceled`, unlocks the workspace; needs a prior cancel
  and an elapsed cool-off (`is-force-cancelable`,
  `force-cancel-available-at`); may lose in-flight state. `discard` is for
  runs paused for confirmation (pending, needs confirmation, policy
  checked, policy override). `force-execute` cancels prior runs to start a
  pending one. All action endpoints need a user or team token, not an
  organization token. Run states include `force_canceled`.
- OTF: cool-off 10 s; cancel = SIGINT to the engine process, force = SIGKILL.

## Precedent: event subscriptions, webhooks, schedules and dependency-triggered runs

Research date: 2026-10-30. Each fact carries its source mark:
"fetched: <URL>" = read in this session; "not fetched (from memory)" = recall, verify before you cite.
Where a fetched page was silent, the entry says "page silent" so a reader does not take silence as a fact.

---

### Topic A: event subscriptions, dispatcher, job events

#### A1. HCP Terraform / TFE notification configurations

- Destination types: `generic`, `email`, `slack`, `microsoft-teams`. fetched: https://developer.hashicorp.com/terraform/cloud-docs/api-docs/notification-configurations
- "Each workspace can have up to 20 notification configurations, and they apply to all runs for that workspace." fetched: https://developer.hashicorp.com/terraform/cloud-docs/api-docs/notification-configurations/workspace
- Scopes: workspace, project, and team notification configurations each have their own API page. fetched: https://developer.hashicorp.com/terraform/cloud-docs/api-docs/notification-configurations
- Trigger strings (the `triggers` array), workspace scope:
  - Run: `run:created`, `run:planning`, `run:needs_attention`, `run:pending_apply_approval`, `run:cost_estimated`, `run:policy_override_required`, `run:policies_checked`, `run:applying`, `run:completed`, `run:errored`, `run:run_errored`, `run:run_canceled`, `run:policy_soft_failed`
  - Assessment (needs health assessments): `assessment:drifted`, `assessment:check_failure`, `assessment:failed`
  - Workspace: `workspace:auto_destroy_reminder`, `workspace:auto_destroy_run_results`, `workspace:deleted`
  - The page is inconsistent: the table says `workspace:auto_destroy_run_results`, the sample payload says `workspace:auto_destroy_results`.
  fetched: https://developer.hashicorp.com/terraform/cloud-docs/api-docs/notification-configurations/workspace
- UI meaning: Created = run enters Pending; Planning = run acquires the lock and starts; Needs Attention = "A plan has changes and Terraform requires user input to continue" (approval or policy override); Errored = ended early by error or cancellation. Drift detected fires on first drift or when the drift changes. Auto destroy reminder is sent 12 and 24 hours before the auto destroy run. fetched: https://developer.hashicorp.com/terraform/cloud-docs/workspaces/settings/notifications
- "Speculative plans and workspaces configured with Local execution mode do not support notifications." fetched: https://developer.hashicorp.com/terraform/cloud-docs/api-docs/notification-configurations/workspace
- Payload (run): `payload_version` (always `1`), `notification_configuration_id`, `run_url`, `run_id`, `run_message`, `run_created_at`, `run_created_by`, `workspace_id`, `workspace_name`, `organization_name`, `notifications[]` with `message`, `trigger`, `trigger_type`, `run_status`, `run_updated_at`, `run_updated_by`. `trigger` = run status at send time; `trigger_type` = the configured trigger that fired. fetched: same workspace page.
  - Design note: one payload carries an array `notifications[]`, so a delivery can batch several transitions (by shape; batching rule not documented).
- Signing: `generic` only. `token` is the HMAC key. Header `X-TFE-Notification-Signature`. HMAC-SHA512 over the request body. fetched: https://developer.hashicorp.com/terraform/cloud-docs/api-docs/notification-configurations
- Verification: sent on save with `enabled: true` or on `POST /notification-configurations/:notification-configuration-id/actions/verify` (200 on success, 400 if the request to the destination cannot complete). A configuration does not get enabled if verification fails. 2xx = success. Email is verified manually. fetched: workspace page and overview page above.
- `delivery-responses` fields: `body` (may be truncated), `code`, `headers` (lowercased names to arrays), `sent-at`, `successful`, `url`. "Only the most recent response is stored." fetched: workspace page.
- URL: must be HTTP or HTTPS. No other restriction documented. fetched: workspace page.
- Retries, timeouts, auto-disable after failures, ordering: page silent. fetched: https://developer.hashicorp.com/terraform/cloud-docs/workspaces/settings/notifications
- Terraform Enterprise has admin settings that restrict outbound requests (for example a setting to block requests to private/loopback addresses for notifications and VCS). not fetched (from memory) - unverified, check before citing.

#### A2. Pulumi Cloud webhooks

- Scopes: organization, stack, and ESC environment webhooks. REST paths (from the published OpenAPI spec):
  - `GET|POST /api/orgs/{orgName}/hooks`, `GET|PATCH|DELETE /api/orgs/{orgName}/hooks/{hookName}`
  - `GET /api/orgs/{orgName}/hooks/{hookName}/deliveries`
  - `POST /api/orgs/{orgName}/hooks/{hookName}/deliveries/{event}/redeliver`
  - `POST /api/orgs/{orgName}/hooks/{hookName}/ping`
  - Same set under `/api/stacks/{orgName}/{projectName}/{stackName}/hooks...` and `/api/esc/environments/{orgName}/{projectName}/{envName}/hooks...`
  fetched: https://api.pulumi.com/api/openapi/pulumi-spec.json
- `Webhook` schema fields: `active`, `displayName`, `envName`, `filters` ("If empty, all events are delivered"), `format` ("e.g., 'raw', 'slack', 'ms_teams'"), `groups` ("e.g., 'stacks', 'deployments'"), `name` (generated if omitted), `organizationName`, `payloadUrl`, `projectName`, `secret` ("omitted when returned from the service"), `stackName`. fetched: https://api.pulumi.com/api/openapi/pulumi-spec.json
- `WebhookDelivery` schema fields: `duration` (ms), `id`, `kind`, `payload`, `requestHeaders`, `requestUrl`, `responseBody`, `responseCode`, `responseHeaders`, `timestamp` (Unix epoch). The delivery record "merges both model.WebhookEvent and model.WebhookDelivery". fetched: same spec.
- Format values per docs search snippet: `raw` (default), `slack`, `ms_teams`, `pulumi_deployments`. fetched (search result snippet only): https://www.pulumi.com/docs/pulumi-cloud/reference/webhooks
- Event kinds: `stack`, `stack_preview`, `stack_update`, `deployment`, `drift_detection`, `drift_remediation`, `policy_violation`. fetched: https://www.pulumi.com/docs/pulumi-cloud/webhooks/
- Filters: `stack_created`, `stack_deleted` (org only); `preview_succeeded`, `preview_failed`; `update_succeeded`, `update_failed`, `destroy_succeeded`, `destroy_failed`, `refresh_succeeded`, `refresh_failed` (the page writes "refresh failed"); `deployment_queued`, `deployment_started`, `deployment_succeeded`, `deployment_failed`; `drift_detected`, `drift_detection_succeeded`, `drift_detection_failed`, `drift_remediation_succeeded`, `drift_remediation_failed`; `policy_violation_mandatory`, `policy_violation_advisory`. Groups: `stacks` = `stack`, `stack_preview`, `stack_update`; `deployments` = `deployment`, `drift_detection`, `drift_remediation`. fetched: https://www.pulumi.com/docs/pulumi-cloud/webhooks/
- Headers: `Pulumi-Webhook-ID` (unique per delivery), `Pulumi-Webhook-Kind`, `Pulumi-Webhook-Signature` (only when a secret is set; HMAC-SHA256 with the secret, documented as hex digest; the Python sample base64-encodes, which conflicts). fetched: same page.
- "Webhooks do not guarantee event order." fetched: same page.
- Redelivery "resends the original payload unchanged". Stack webhooks: CLI or console; org and environment: console only (CLI `pulumi stack webhook delivery redeliver <webhook-id> <event-id>`, experimental). Failed = non-2xx or no response. fetched: same page.
- Retries, timeouts: page silent. fetched: same page.
- Deployment destination: a stack webhook with format `pulumi_deployments` starts a Pulumi Deployment on another stack (used to chain stacks). not fetched (from memory).

#### A3. Spacelift

- Notification policies route events; rules `inbox`, `slack`, `webhook`, `pull_request`. "If no rules match, no action is taken." Input has `run_updated` (`run`, `stack`, `urls`), `internal_error`, `module_version`, `webhook_endpoints` (`id`, `labels`), `account`, `worker_usage`. fetched: https://docs.spacelift.io/concepts/policy/notification-policy
- A `webhook` rule sets `endpoint_id`, `payload`, `headers`, `method` (default POST). Payload placeholders such as `spacelift::logs::planning` inline run logs. fetched: same page.
- Slack `thread_key` groups messages into a thread; without it Spacelift updates the default message on each run update. fetched: same page.
- Signing: `X-Signature-256` (SHA-256, "same way GitHub does"); `X-Signature` (SHA-1) on non-FedRAMP accounts. "The webhook secret cannot be modified after the webhook is created." Rotation = delete and recreate. fetched: https://docs.spacelift.io/integrations/webhooks
- Retries: optional, "automatically retry webhook delivery (up to 3 times)" on 5xx. Auto-disabled after "10 or more consecutive failures". Backoff and timeout not documented. Delivery log stores status code and timestamp. Per-stack webhooks kept only for backward compatibility. fetched: same page.

#### A4. env zero

- Notification targets: Slack, Microsoft Teams, Email, Webhook (webhook since May 2024). fetched (search snippet): https://docs.envzero.com/guides/integrations/notifications
- Webhook: HTTPS required. Respond "within 10 seconds with a 2XX response". "env zero will not retry to send the event." fetched: https://docs.envzero.com/guides/integrations/notifications/webhooks
- Headers: `User-Agent` `env0-webhook`, `x-env-zero-notification-target-id`, `x-env-zero-event`, `x-env-zero-event-id` (globally unique), `x-env-zero-signature` (HMAC hex, SHA-256 in sample). The page mixes `x-env0-*` and `x-env-zero-*` prefixes. fetched: same page.
- Body is CloudEvents-shaped: `type` (for example `com.env-zero.deploy.succeeded`; test event `com.env0.webhook.test`), `source`, `id`, `datacontenttype`, `data`, plus `time` in the sample. fetched: same page.

#### A5. GitHub webhooks

- "GitHub does not automatically redeliver failed deliveries." Redelivery is possible for deliveries "in the past 3 days" (UI and REST API). fetched: https://docs.github.com/en/webhooks/testing-and-troubleshooting-webhooks/redelivering-webhooks
- Respond with 2xx "within 10 seconds"; otherwise GitHub ends the connection and counts a failure. `X-GitHub-Delivery` lets the receiver make each delivery unique per event; a redelivery keeps the same value. After an outage the receiver should redeliver missed events. fetched: https://docs.github.com/en/webhooks/using-webhooks/best-practices-for-using-webhooks
- Payload cap 25 MB: "If an event generates a larger payload, GitHub will not deliver a payload for that webhook event." Headers: `X-GitHub-Delivery`, `X-GitHub-Hook-ID`, `X-GitHub-Event`, `X-Hub-Signature` (SHA-1, legacy), `X-Hub-Signature-256`, `User-Agent` `GitHub-Hookshot/...`. fetched: https://docs.github.com/en/webhooks/webhook-events-and-payloads
- `X-Hub-Signature-256` value starts with `sha256=`, HMAC hex digest of the body; "Never use a plain `==` operator" (constant-time compare). No documented dual-secret rotation; you replace the secret. fetched: https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries
- Ordering: not guaranteed. not fetched (from memory).

#### A6. GitLab webhooks

- Auto-disable: after 4 consecutive failures (4xx, 5xx, timeouts) the hook is temporarily disabled, first for 1 minute, extended on each later failure up to 24 hours, then re-enabled. After 40 consecutive failures it is permanently disabled. A successful test request re-enables it. fetched: https://docs.gitlab.com/user/project/integrations/webhooks/
- No automatic retry schedule documented; "might be delivered more than once if it times out", so receivers dedupe. Manual "Resend Request" (not possible after the URL changed). Recent events visible for 2 days in UI. fetched: same page.
- Headers: `Idempotency-Key` (legacy; same value as `webhook-id`, stable across retries), `X-Gitlab-Event-UUID`, `X-Gitlab-Webhook-UUID`, `X-Gitlab-Token` (plain secret, not recommended), and Standard Webhooks `webhook-id`, `webhook-timestamp`, `webhook-signature` (`v1,{base64}` HMAC-SHA256 over `{id}.{timestamp}.{body}`) when a signing token is set. fetched: same page.
- API: `signing_token` must be `whsec_<base64>` with a 32-byte key, never returned. Changing the URL resets `token`. `GET /projects/:id/hooks/:hook_id/events` lists the past 7 days, filter by `successful`/`client_failure`/`server_failure`. `POST .../events/:hook_event_id/resend` is limited to 5 per minute per user. Response shows `alert_status`, `disabled_until`. fetched: https://docs.gitlab.com/api/project_webhooks/
- GitLab.com limits: timeout 10 s; max payload 25 MB; 100 hooks per project, 50 per group; rate limit per top-level namespace per minute 500 (Free) up to 13,000 (Ultimate 5,000+ seats); when hit, "all webhooks in the namespace are temporarily disabled" until the next minute. fetched: https://docs.gitlab.com/user/gitlab_com/
- Push events: no webhook at all if a push has more than 3 branches/tags (default `push_event_hooks_limit`). fetched: https://docs.gitlab.com/user/project/integrations/webhooks/
- SSRF: admin setting "Allow requests to the local network from webhooks and integrations" (off by default on self-managed), plus an allowlist of local hosts; DNS rebinding protection. not fetched (from memory).

#### A7. Atlantis

- Events: `apply` and `drift` (drift sent after a successful `POST /api/drift/detect`). Kinds: `slack`, `http`. Keys: `webhooks`, `event`, `kind`, `channel`, `url`, `workspace-regex`, `branch-regex` (apply only). Extra HTTP headers via `--webhook-http-headers`. Slack token via `--slack-token` / `ATLANTIS_SLACK_TOKEN`. Retries: page silent. fetched: https://www.runatlantis.io/docs/sending-notifications-via-webhooks
- No signature on `http` webhooks documented (headers are the only auth hook). fetched: same page (by absence).

#### A8. Standard Webhooks spec

- Headers `webhook-id` (stable across retries; idempotency key), `webhook-timestamp` (Unix seconds), `webhook-signature` (space-separated list of `v1,<base64>`; `v1a` for ed25519). Signed content `id.timestamp.body`, byte-exact body. fetched: https://github.com/standard-webhooks/standard-webhooks/blob/main/spec/standard-webhooks.md
- Secrets: `whsec_` + base64 of 24 to 64 random bytes; asymmetric `whsk_` / `whpk_`. Rotation: sign with old and new key for a limited period, send both signatures; consumer accepts any match. fetched: same.
- Consumer: constant-time compare; reject timestamps outside a tolerance (value not fixed); dedupe by `webhook-id` (example keeps IDs about 5 minutes). fetched: same.
- Payload: `type` (dot-delimited), `timestamp` (ISO 8601), `data`; keep under about 20 KB; thin vs full payloads discussed (thin = IDs, easier to evolve and to audit). fetched: same.
- Retries: exponential backoff with jitter over days; example: immediately, 5s, 5m, 30m, 2h, 5h, 10h, 14h, 20h, 24h (about 3 days total). Then notify by another channel and consider disabling. Timeout 15 to 30 s. fetched: same.
- Status codes: 2xx success; 3xx = failure (do not follow redirects); 410 = disable endpoint; 429/502/504 = throttle; honor `retry-after`. fetched: same.
- SSRF: send through a proxy that blocks internal IPs (smokescreen cited); run workers in an isolated subnet. fetched: same.

#### A9. Cross-product summary (Topic A)

| Product | Auto retry | Timeout | Auto-disable | Delivery log / redeliver | Signature |
|---|---|---|---|---|---|
| HCP Terraform | page silent | page silent | no (only verify-on-enable) | last response only; no redeliver | `X-TFE-Notification-Signature` HMAC-SHA512 |
| Pulumi Cloud | page silent | page silent | page silent | deliveries list + redeliver + ping | `Pulumi-Webhook-Signature` HMAC-SHA256 |
| Spacelift | opt-in, up to 3 on 5xx | page silent | after 10 consecutive failures | delivery log | `X-Signature-256` (GitHub style) |
| env zero | none | 10 s | page silent | page silent | `x-env-zero-signature` HMAC-SHA256 |
| GitHub | none | 10 s | no | 3 days, redeliver | `X-Hub-Signature-256` `sha256=` hex |
| GitLab | none documented; may duplicate | 10 s (.com) | 4 fails temp (1 min to 24 h), 40 permanent | 7 days API, resend 5/min | Standard Webhooks `webhook-signature` |
| Standard Webhooks | backoff ~3 days | 15-30 s | recommends disable on 410 / long failure | n/a | `v1,` HMAC-SHA256 base64 |

All sources fetched as marked above.

---

### Topic B: schedules and dependency-triggered runs

#### B1. HCP Terraform run triggers

- A workspace connects to "up to 20 source workspaces", "within your organization". A successful apply in a source queues a run in the target. Only successful applies trigger. fetched: https://developer.hashicorp.com/terraform/cloud-docs/workspaces/settings/run-triggers
- Triggered runs do not auto-apply unless "Auto-apply run triggers" is on; that setting is independent of the workspace auto-apply setting. fetched: same page.
- The triggered run shows links to the source workspace and the source run; the source run's plan/apply shows which workspaces get runs. fetched: same page.
- Create needs admin on the target plus read-runs on the source; delete needs admin on the target. List filter `filter[run-trigger][type]` = `inbound` | `outbound`. Request `relationships.sourceable` of type `workspaces`. fetched: https://developer.hashicorp.com/terraform/cloud-docs/api-docs/run-triggers
- Identity of the triggered run, dedup of two sources applying at once, behavior with an existing pending run: page silent. fetched: both pages above.
- Run `source` attribute value `tfe-run-trigger` for such runs. not fetched (from memory).
- Queue: "processes each workspace's runs in the order they were queued"; a run stays pending until all earlier runs complete. Discarded = "A user chose not to continue this run." Page silent on newer runs canceling older pending runs and on priority. fetched: https://developer.hashicorp.com/terraform/cloud-docs/run/states
- VCS-triggered: when a new commit arrives while an earlier VCS run is still pending, the pending run may be discarded in favor of the newest (applies to plan-only/pending runs in some settings, for example "Auto cancel plan-only runs" for speculative plans). not fetched (from memory) - unverified.
- Health assessments (drift): next assessment is scheduled a set period after the later of the last apply or last assessment (example 24 h; TFE admins can change it). "A health assessment never interrupts or interferes with runs." A run started during an assessment cancels the assessment, which is rescheduled. Assessments pause when the latest run errored, was canceled or discarded. They do not count against concurrency. Identity: page silent. fetched: https://developer.hashicorp.com/terraform/cloud-docs/workspaces/health

#### B2. Spacelift

- Stack dependencies: only tracked runs trigger dependents (proposed runs and tasks do not). Dependents queue tracked runs and wait for the upstream to finish. fetched: https://docs.spacelift.io/concepts/stack/stack-dependencies
- Output references: a dependent with references runs only when a referenced output was created or changed, unless "Trigger always" is on. Referenceless dependents run whenever the upstream finishes. Outputs come only from a successful tracked run with an apply phase. fetched: same page.
- Multiple upstreams: a dependent waits for all its upstreams (implied by scenarios); dedup not documented. Graph must be a DAG; cycle creation returns an error. fetched: same page.
- Failure: a failed run breaks the chain; later dependent runs are canceled; siblings unaffected. A retry of the failed run does not trigger dependents even on success. fetched: same page.
- Attribution: policy input carries a `trigger` object naming the upstream run and stack, nested `trigger.trigger` up to 20 levels; available to trigger, plan, approval, notification and push policies. fetched: same page.
- Dependent runs inherit the upstream priority when all equal, else normal. fetched: same page.
- Warns not to combine stack dependencies with trigger policies (confusion or infinite loops). fetched: same page.
- Scheduling: scheduled tasks (command at a time or cron, with timezone), scheduled delete (optional destroy run), scheduled runs (named, optional runtime config), drift detection (one or more cron expressions + timezone; Reconcile creates reconciliation runs; with Ignore state off, runs only on stacks in Finished state). Schedules on public workers "will not trigger". Missed runs, identity, overlap: page silent. fetched: https://docs.spacelift.io/concepts/stack/scheduling

#### B3. env zero

- Scheduling: UNIX cron (no year field), UTC; Deploy and/or Destroy; only on an already deployed environment; minimum role Project Deployer; enabling a schedule disables the TTL. Missed runs, overlap, identity: page silent. fetched: https://docs.envzero.com/guides/admin-guide/environments/scheduling
- Workflows (env zero): a workflow environment deploys sub-environments in DAG order from `env0.workflow.yaml` with `needs`. not fetched (from memory).

#### B4. GitHub Actions `schedule`

- "The shortest interval you can run scheduled workflows is once every 5 minutes." "can be delayed during periods of high loads"; "some queued jobs may be dropped"; "High load times include the start of every hour." fetched: https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows
- Runs on the latest commit of the default branch. UTC by default; an IANA timezone can be set. fetched: same page.
- Actor: some repository events change the actor (for example, the user who changes the default branch becomes the actor). "Notifications for scheduled workflows are sent to the user who last modified the cron syntax in the workflow file." fetched: same page.
- "In a public repository, scheduled workflows are automatically disabled when no repository activity has occurred in 60 days." fetched: same page.
- Missed ticks are not caught up. not fetched (from memory).

#### B5. GitLab pipeline schedules

- "When you create a pipeline schedule, you become the schedule owner." Scheduled pipelines run with the owner's permissions (protected environments, job token). A manual "run now" uses the permissions of the person who runs it. fetched: https://docs.gitlab.com/ci/pipelines/schedules/
- If the owner is blocked or removed the schedule can become `Inactive`; a Maintainer or Owner can take ownership to reactivate it. Protected branch targets need merge rights; protected tags need create rights. fetched: same page.
- Frequency is capped by the instance maximum; manual trigger once per minute; per-project schedule limit; up to 20 inputs; schedules stop when the project/group is marked for deletion. fetched: same page.
- Schedules are driven by a Sidekiq cron worker; a schedule fires at the next worker tick after its time; missed ticks during downtime are not replayed one by one. not fetched (from memory).

#### B6. Terramate and Atmos Pro (affected units)

- Terramate: `--changed` selects stacks with changes in a commit, branch or PR (git based; untracked and uncommitted files count since v0.11.0). Dependents are not included by default; flags `--include-direct-dependents`, `--include-all-dependents`, `--only-direct-dependents`, `--only-all-dependents`. Filters use data dependencies only (`input.from_stack_id`, Terragrunt `dependency`); `before`/`after` only order. fetched: https://terramate.io/docs/cli/change-detection/
- Atmos Pro: a GitHub App; "Automatically deploy your infrastructure in the correct order with GitHub Actions"; "No tokens. No secrets. Secure by design with GitHub OIDC."; stack locking for one process per stack. Affected detection via `atmos describe affected` and dispatch of GitHub Actions workflows. Partly fetched: https://atmos-pro.com/docs (the `atmos describe affected` link is not fetched (from memory)).

#### B7. Cross-product summary (Topic B), design questions

| Question | HCP Terraform | Spacelift | env zero | GitHub Actions | GitLab |
|---|---|---|---|---|---|
| Identity of scheduled/triggered run | page silent | page silent (trigger chain in policy input) | page silent | actor rules; notifications to last cron editor | schedule owner |
| Missed runs in outage | page silent | page silent | page silent | delayed or dropped | not replayed (memory) |
| Two upstreams at once | page silent | waits for all upstreams | n/a | n/a | n/a |
| Run already pending on target | FIFO queue, page silent on collapse | page silent | page silent | n/a | n/a |
| Trigger condition | successful apply only | finished tracked run; outputs changed unless "Trigger always" | n/a | n/a | n/a |
| Limit | 20 sources | DAG, 20-level trigger chain in input | n/a | 5 min minimum | instance max frequency |

Gaps that no fetched page answers: retry schedule and timeout of HCP Terraform and Pulumi Cloud; identity of HCP Terraform triggered runs and assessments; dedup/collapse of pending triggered runs in HCP Terraform and Spacelift; missed-run behavior in every product.

## Precedent research: step credentials, git write-back, run-scoped output reads

Collected 2026-10-30. Every fact has a source mark:

- "fetched: <URL>": the page was read in this session. The text is a model summary of that page, with short quotes.
- "not fetched (from memory)": the fact comes from training data. Check it before an RFC relies on it.

When a fetched page did not answer a question, the note says "page silent".

---

### Topic A: delivery of cloud credentials to one step of a run

#### A1. HCP Terraform / Terraform Enterprise dynamic provider credentials

AWS configuration (fetched: https://developer.hashicorp.com/terraform/cloud-docs/workspaces/dynamic-provider-credentials/aws-configuration):

- `TFC_AWS_PROVIDER_AUTH` must be `true`, "or HCP Terraform will not attempt to authenticate to AWS". It must be an environment variable (the "default variable form isn't supported").
- `TFC_AWS_RUN_ROLE_ARN`: "The ARN of the role to assume in AWS." Required unless both plan and apply roles are set.
- `TFC_AWS_PLAN_ROLE_ARN`: "The ARN of the role to use for the plan phase of a run." `TFC_AWS_APPLY_ROLE_ARN`: same, for the apply phase. Both fall back to `TFC_AWS_RUN_ROLE_ARN`. Different permissions per phase need "two separate roles and trust policies".
- `TFC_AWS_WORKLOAD_IDENTITY_AUDIENCE`: optional. Default audience is `aws.workload.identity`.
- Every variable has a `[_TAG]` form (for example `TFC_AWS_RUN_ROLE_ARN_ALIAS1`) and a `TFC_DEFAULT_` form (for example `TFC_DEFAULT_AWS_RUN_ROLE_ARN`, used from variable sets).
- Multiple configurations (provider aliases): declare a Terraform variable `tfc_aws_dynamic_credentials` with `default` and `aliases` fields; each holds a `shared_config_file` string. The provider block uses `shared_config_files = [var.tfc_aws_dynamic_credentials.aliases["ALIAS1"].shared_config_file]`. So the credential is delivered as an AWS shared config file per alias, not as `AWS_ACCESS_KEY_ID` env vars.
- Trust policy `sub`: `organization:ORG_NAME:project:PROJECT_NAME:workspace:WORKSPACE_NAME:run_phase:RUN_PHASE`. RUN_PHASE is "currently one of `plan` or `apply`". One role for both phases: `StringLike` with `run_phase:*`.
- Self-hosted agents: v1.7.0 or later for dynamic credentials, v1.12.0 or later for multiple configurations.
- Page warns not to set other AWS provider auth arguments, "since they may interfere with dynamic credentials".
- Page silent on hooks.

Workload identity token claims and lifetime (fetched: https://developer.hashicorp.com/terraform/cloud-docs/workspaces/dynamic-provider-credentials/workload-identity-tokens):

- Standard claims `jti`, `iss`, `iat`, `nbf`, `aud`, `exp`, `sub`. Custom: `terraform_organization_id`, `terraform_organization_name`, `terraform_project_id`, `terraform_project_name`, `terraform_workspace_id`, `terraform_workspace_name`, `terraform_full_workspace`, `terraform_run_id`, `terraform_run_phase`.
- `terraform_run_phase` is `plan` or `apply` (module test runs: always `plan`).
- Lifetime: `exp` is "based on the timeout of the run phase that it was issued for". So one token per phase, valid for the phase timeout. Module test tokens: default 600 s, range 300 to 1800 s, `nbf` 30 s before `iat`.

Raw token for custom auth (fetched: https://developer.hashicorp.com/terraform/cloud-docs/workspaces/dynamic-provider-credentials/manual-generation):

- Set `TFC_WORKLOAD_IDENTITY_AUDIENCE`; without it "HCP Terraform won't inject a token".
- The run then has the token in env var `TFC_WORKLOAD_IDENTITY_TOKEN`, "available in the run environment" of each plan and apply.
- Several tokens: `TFC_WORKLOAD_IDENTITY_AUDIENCE_[TAG]` gives `TFC_WORKLOAD_IDENTITY_TOKEN_[TAG]`. Tag: letters, digits, underscore; `TYPE` reserved.
- Stated use: "custom agent hooks" for custom auth workflows or for providers that dynamic credentials do not support. This implies that agent hooks see `TFC_WORKLOAD_IDENTITY_TOKEN`.

Agent hooks (fetched: https://developer.hashicorp.com/terraform/cloud-docs/agents/hooks):

- Hooks: `pre-plan`, `post-plan`, `pre-apply`, `post-apply`. Files `terraform-<HOOK>` in `~/.tfc-agent/hooks` (Docker: `/home/tfc-agent/.tfc-agent/hooks`). Hooks are on the agent host, set by the agent operator, not by the workspace.
- `pre-plan` and `pre-apply` run before `terraform init`. `post-*` run after the command, also on failure.
- A hook passes env vars to later Terraform commands by writing `KEY=value` lines to the file at `$TFC_AGENT_ENV`. These apply only to the current operation; they do not carry from `pre-plan` to `post-plan`.
- Timeout default 60 s, max 10 min: `TFC_AGENT_HOOK_TIMEOUT`, `TFC_AGENT_HOOK_[HOOK NAME]_TIMEOUT`. Timeout or non-zero exit fails the run.
- Hook stdout/stderr is shown with the run output.
- Page silent on which variables and credentials the hook process sees.

Not fetched (from memory):

- The agent writes the identity token to a file in the run's temporary directory and the generated AWS shared config uses `web_identity_token_file` and `role_arn`. Exact file path not confirmed.
- Credentials are workspace-wide for a phase: there is no TFE mechanism to give credentials to Terraform but not to a hook in the same phase. Cleanup is by the disposable run directory / worker.
- Equivalent variable families exist for other clouds and Vault: `TFC_GCP_PROVIDER_AUTH`, `TFC_GCP_RUN_SERVICE_ACCOUNT_EMAIL`, `TFC_GCP_WORKLOAD_PROVIDER_NAME`; `TFC_AZURE_PROVIDER_AUTH`, `TFC_AZURE_RUN_CLIENT_ID`; `TFC_VAULT_PROVIDER_AUTH`, `TFC_VAULT_ADDR`, `TFC_VAULT_RUN_ROLE`, each with `_PLAN_`/`_APPLY_` variants.

#### A2. Spacelift

AWS integration (fetched: https://docs.spacelift.io/integrations/cloud-providers/aws):

- A run or task "automatically assume[s] an IAM role in your AWS account". `sts:AssumeRole` uses the run ID as `RoleSessionName`, so CloudTrail maps changes to a run.
- Env vars set: `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SECURITY_TOKEN`, `AWS_SESSION_TOKEN`.
- Credentials are obtained once at run start and not refreshed; long runs can fail with `ExpiredToken`.
- Duration: 15 min to 1 h when Spacelift assumes the role; 15 min to 12 h when the private worker assumes it. "Durations above 1 hour require Assume role on worker".
- Credentials generated on a private worker are "never leaked to us in any shape or form".
- External ID example: `spacelifter@01GE7K9SR2DQBCRQ90DH70JF6Y@stack-1@write` (account@integration@stack@access). The format is inferred from the example.
- Page silent on hooks.

Terraform provider docs (fetched: https://raw.githubusercontent.com/spacelift-io/terraform-provider-spacelift/main/docs/resources/aws_integration_attachment.md and https://raw.githubusercontent.com/spacelift-io/terraform-provider-spacelift/main/docs/resources/aws_integration.md):

- Attachment `read`: "Indicates whether this attachment is used for read operations. Defaults to `true`." `write`: same for write operations, default `true`.
- `generate_credentials_in_worker`: "Generate AWS credentials in the private worker. Defaults to false."
- `duration_seconds`: default 900. `external_id`: "Custom external ID (works only for private workers)."

Not fetched (from memory): "read" covers proposed runs (plans on pull requests) and "write" covers tracked runs (plan and apply on the tracked branch). Two attachments of two integrations with different roles give a read-only role for proposed runs and a write role for tracked runs. This is per run type, not per phase within one run: a tracked run uses the write role for both plan and apply.

OIDC (fetched: https://docs.spacelift.io/integrations/cloud-providers/oidc):

- Env var `SPACELIFT_OIDC_TOKEN` and file `/mnt/workspace/spacelift.oidc`.
- Valid one hour. `aud` is the account hostname (for example `demo.app.spacelift.io`). `nbf` equals `iat`.
- Custom claims: `spaceId`, `spacePath`, `callerType`, `callerId`, `runType`, `runId`, `scope` (`read` or `write`). The `scope` claim lets a trust policy separate proposed (read) and tracked (write) runs.

Stack settings and hooks (fetched: https://docs.spacelift.io/concepts/stack/stack-settings):

- Hooks: `before_init`/`after_init`, `before_plan`/`after_plan`, `before_apply`/`after_apply`, `before_destroy`/`after_destroy`, `before_perform`/`after_perform`, `after_run` (sees `TF_VAR_spacelift_final_run_state`).
- A failed "before" hook stops the phase. Hooks run on the worker.
- Hooks can be overridden by `.spacelift/config.yml` unless the stack has label `feature:ignore_runtime_config`.
- "Enable well-known secret masking" redacts known secret patterns (AWS key IDs, GitHub PATs, JWTs, private keys) from logs; "A value that spans more than one line is not masked."
- "Enable local preview" warns that it "allows anybody with write access to the Stack to execute arbitrary code" with access to the stack environment variables.
- Cloud integrations overview (fetched: https://docs.spacelift.io/integrations/cloud-providers): on self-hosted workers "you can use your cloud providers' ambient credentials".

Not fetched (from memory): integration credentials and `SPACELIFT_OIDC_TOKEN` are plain environment of the run container, so every hook in the run sees them. Spacelift has no per-hook credential scope. Azure integration sets `ARM_CLIENT_ID`, `ARM_CLIENT_SECRET`, `ARM_TENANT_ID`, `ARM_SUBSCRIPTION_ID` per run with read/write attachments; GCP integration uses a per-stack service account and writes an OAuth token (`GOOGLE_OAUTH_ACCESS_TOKEN`).

#### A3. env zero

OIDC (fetched: https://docs.envzero.com/guides/integrations/oidc-integrations):

- Token in env var `ENV0_OIDC_TOKEN`. Enabled by an org admin in policies, or when an OIDC credential is selected.
- Claims: `iss`, `sub`, `aud`, `iat`, `exp`, plus `organizationId`, `projectId`, `deploymentType`, `deployerEmail`; AWS session tags under `https://aws.amazon.com/tags`. `env0Tag` (from `ENV0_OIDC_TAG`) is "Informational context only, not an identity claim".
- v2 audiences per provider: `sts.amazonaws.com`, `api://AzureADTokenExchange`, `api://env0-gcp`, `api://env0-vault`. v1: one `aud` `https://prod.env0.com`.
- Lifetime 24 hours after issue.
- Page silent on per-phase credentials and on custom flow access.

Custom flows (fetched: https://docs.envzero.com/guides/admin-guide/custom-flows):

- Steps under `deploy.steps`: `setupVariables` (after), `terraformInit`, `terraformPlan`, `terraformApply`, `storeState`, `terraformOutput` (before/after); OpenTofu uses `opentofu*` names. Also `onCompletion`, `onSuccess`, `onFailure`.
- Hooks see env zero system variables (`ENV0_ENVIRONMENT_ID`, `ENV0_DEPLOYMENT_TYPE`), UI variables via `env0.env-vars.json`, and values exported with `echo KEY=VALUE >> $ENV0_ENV`.
- "Custom flows execute your shell commands within a dedicated deployment container." Page silent on cloud credential visibility.

Not fetched (from memory): env zero deployment credentials (AWS assume role, OIDC) are set as env vars for the whole deployment container, so custom flow steps see them; there is a per-project "cost credentials" separate from "deployment credentials", but no plan/apply split.

#### A4. GitHub Actions

OIDC reference (fetched: https://docs.github.com/en/actions/reference/security/oidc):

- "Without `id-token: write`, the OIDC JWT ID token cannot be requested." The permission does not grant write elsewhere.
- `ACTIONS_ID_TOKEN_REQUEST_URL`: "The URL for GitHub's OIDC provider." `ACTIONS_ID_TOKEN_REQUEST_TOKEN`: "Bearer token for the request to the OIDC provider."
- Custom audience: `core.getIDToken(audience)`, or `curl -H "Authorization: bearer $ACTIONS_ID_TOKEN_REQUEST_TOKEN" "$ACTIONS_ID_TOKEN_REQUEST_URL&audience=api://AzureADTokenExchange"`.
- `sub` with environment: `repo:ORG-NAME/REPO-NAME:environment:ENVIRONMENT-NAME`; colons in values become `%3A`.

Security hardening overview (fetched: https://docs.github.com/en/actions/security-for-github-actions/security-hardening-your-deployments/about-security-hardening-with-openid-connect):

- Example claims include `environment`, `job_workflow_ref`, `ref`, `sha`, `repository`, `actor`, `workflow`, `event_name`; custom properties appear as `repo_property_*`.
- Token is "unique for each workflow job where it is generated". Example `exp - iat` is 300 s; the page only says "short-lived".
- Repositories created after July 15, 2026 use an immutable default `sub` format with owner and repository IDs (not on GHES).

`aws-actions/configure-aws-credentials` (fetched: https://github.com/aws-actions/configure-aws-credentials):

- Default export as env vars for later steps: `AWS_REGION`, `AWS_DEFAULT_REGION`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`, `AWS_PROFILE`.
- `output-env-credentials` (default true without `aws-profile`); `output-credentials` (default false) gives step outputs instead. With `output-env-credentials: false` the credentials stay out of the environment.
- `aws-profile` writes `~/.aws/credentials` and `~/.aws/config`; these persist after cleanup.
- `role-session-name` default `"GitHubActions"`; `role-duration-seconds` default 3600 (900 to 43200).
- `unset-current-credentials`; a post-job step removes credentials from the environment by default; `AWS_SKIP_CLEANUP_STEP="true"` skips it.

Not fetched (from memory):

- `ACTIONS_ID_TOKEN_REQUEST_*` are present for every step of a job that has `id-token: write`, including third-party actions. The scope is the job, not the step.
- An action makes values visible to later steps by appending to the file at `$GITHUB_ENV`; step outputs go to `$GITHUB_OUTPUT`. A step-level `env:` key sets variables for that step only.
- `echo "::add-mask::$VALUE"` masks a value in later log output. Secrets from `secrets.*` are masked automatically. Masking is per exact string and misses transformed values.
- Installation access tokens of a GitHub App expire after 1 hour.

#### A5. GitLab CI

ID tokens (fetched: https://docs.gitlab.com/ci/secrets/id_token_authentication/):

- `id_tokens:` on a job; each key is the variable name, with `aud`. Usable in `script`, `before_script`, `after_script`.
- `aud` default: the instance domain.
- `exp` is the job timeout if set; otherwise 5 minutes after issue. RS256.
- Claims include `ref_protected`; with an environment: `environment`, `environment_protected`, `deployment_tier`, `environment_action`.
- Per job (claims include `job_id`).

Vault secrets (fetched: https://docs.gitlab.com/ci/secrets/hashicorp_vault/):

- `secrets: { DATABASE_PASSWORD: { vault: production/db/password@ops, token: $ID_TOKEN } }`.
- "When a job has at least one ID token defined, the `secrets` keyword automatically uses that token."
- `file` default `true`: secret goes to a temporary file; the variable holds the path. `file: false` puts the value in the variable.

Variables (fetched: https://docs.gitlab.com/ci/variables/):

- Masked values: single line, no spaces, 8 characters or more, not the name of another variable. "Masking a CI/CD variable is not a guaranteed way to prevent malicious users from accessing variable values."
- "Masked and hidden" only at creation; value "cannot be revealed in the UI again".
- Protected: "available only to pipelines that run on protected branches or protected tags".
- File-type: value in a temp file, variable holds the path; recommended "for tools that need a file as input".

Not fetched (from memory): the scope is the job; `before_script`, `script` and `after_script` of a job all see the same variables. No per-script secret scope.

#### A6. Pulumi Deployments and Pulumi ESC

Deployments OIDC (fetched: https://www.pulumi.com/docs/pulumi-cloud/deployments/oidc/):

- "Every time a deployment runs, Pulumi Cloud issues a new OIDC token specific to that run."
- `sub`: `pulumi:deploy:org:<organization name>:project:<project name>:stack:<stack name>:operation:<operation kind>:scope:write`.
- `aud`: the organization name. `iss`: `https://api.pulumi.com/oidc`.
- Page silent on env vars, lifetime, pre-run commands.

Deployment settings (fetched: https://www.pulumi.com/docs/deployments/concepts/settings/): pre-run commands run "arbitrary shell commands before a deployment starts, for setup or authentication". Page silent on whether OIDC credentials reach them.

`esc run` (fetched: https://www.pulumi.com/docs/esc/cli/commands/esc_run/):

- Top-level `environmentVariables` of the opened environment become env vars of the command.
- No shell: "commands are not run in a subshell".
- Output is filtered to remove secret values unless `-i/--interactive`.
- `-l/--lifetime` default 2h0m0s.

Not fetched (from memory):

- Deployments settings `operationContext.oidc.aws` has `roleArn`, `sessionName`, `duration`, `policyArns`; azure has `clientId`, `tenantId`, `subscriptionId`; gcp has `projectId`, `workloadPoolId`, `providerId`, `serviceAccount`.
- OIDC credentials are exported as env vars for the deployment, so pre-run commands see them. `operation` in `sub` is `update`, `preview`, `refresh` or `destroy`, so a trust policy can give `preview` a read-only role. This is the closest Pulumi precedent for per-phase roles.
- ESC `files:` entries are written to temporary files and the variable holds the path; the files go away when the command exits.
- Deployments give the run a `PULUMI_ACCESS_TOKEN` that is a short-lived deployment token.

#### A7. Summary of scoping (synthesis, not a source)

| Product | Credential unit | Per phase | Hooks see it | Delivery |
|---|---|---|---|---|
| HCP Terraform | run phase (plan, apply) | yes: plan role, apply role, `run_phase` claim | raw token is meant for agent hooks | shared config file per alias; `TFC_WORKLOAD_IDENTITY_TOKEN` env |
| Spacelift | run (read for proposed, write for tracked) | no, per run type | yes (from memory) | env vars; token env + file |
| env zero | deployment | no (from memory) | yes (from memory) | env var |
| GitHub Actions | job | no; a job is the unit | every step of the job | request URL + bearer; actions export to `$GITHUB_ENV` |
| GitLab CI | job | no | all scripts of the job | env var or temp file (`file: true` default for Vault) |
| Pulumi Deployments | deployment; `operation` in `sub` | by operation kind (from memory) | pre-run commands, likely | env vars |

No product found gives credentials to the tool step and hides them from a hook in the same phase. The finest grain in precedent is the phase (HCP Terraform) or the job (CI systems).

---

### Topic B: write-back to git and the code location of a unit

#### B1. Code location settings

TFE workspaces API (fetched: https://developer.hashicorp.com/terraform/cloud-docs/api-docs/workspaces):

- `vcs-repo.identifier` ":org/:repo"; `vcs-repo.branch` (empty means default branch); `vcs-repo.ingress-submodules` (default `false`); `vcs-repo.oauth-token-id`; `vcs-repo.tags-regex` ("A regular expression used to match Git tags").
- `working-directory`: path where Terraform runs; default repository root.
- `trigger-prefixes`: directories that start a run when changed. `trigger-patterns`: glob patterns. `file-triggers-enabled`: "Whether to filter runs based on the changed files in a VCS push"; off means any push starts a run.
- `speculative-enabled`: plans on pull requests.

Spacelift stack (fetched: https://docs.spacelift.io/concepts/stack/stack-settings):

- Repository and branch; "A branch must exist before it's pointed to in Spacelift."
- Project root; can be overridden by `.spacelift/config.yml`.
- Project globs: extra trigger paths (doublestar); "Project globs do not mount the files or directories in your project root."
- Git sparse checkout paths: "Only path values are allowed; glob patterns are not supported."
- Not fetched (from memory): Terraform provider attribute names `repository`, `branch`, `project_root`, `additional_project_globs`.

Atlantis (fetched: https://www.runatlantis.io/docs/repo-level-atlantis-yaml):

- `dir` (required, relative to repo root), `workspace` (default `"default"`), `name`.
- `autoplan.when_modified` default: `"**/*.tf*"`, `"**/*.tofu"`, `"**/*.tofu.json"`, `"**/terragrunt.hcl"`, `"**/.terraform.lock.hcl"`, relative to `dir`; a custom list "overrides these defaults entirely".
- `autodiscover.mode`: `auto` (default; only when no `projects`), `disabled`, `enabled`; `ignore_paths` doublestar.

env zero (not fetched (from memory); the template page URL returned 404): a template has repository, path (folder in the repo), revision (branch or tag), and a VCS connection; an environment can override the revision. Fields in the env zero Terraform provider: `repository`, `path`, `revision`.

Pulumi Deployments (not fetched (from memory)): `sourceContext.git` with `repoUrl`, `branch`, `repoDir`, `commit`, `gitAuth`; GitHub app integration with `repository`, `deployCommits`, `previewPullRequests`, `paths` filters.

#### B2. Committing from an API or a bot

GitHub REST contents API (fetched: https://docs.github.com/en/rest/repos/contents):

- `PUT /repos/{owner}/{repo}/contents/{path}`: `message`, `content` (Base64), `sha` ("required when updating": blob SHA of the replaced file), `branch`, `committer`, `author`.
- Returns 200 / 201 / 404 / 409 Conflict / 422.
- Do not run in parallel with "Delete a file"; concurrent requests conflict.
- One file per commit.
- Not fetched (from memory): a stale `sha` returns 409; this is the optimistic concurrency check per file, not per branch head.

GitHub Git data API (fetched: https://docs.github.com/en/rest/git/refs):

- `PATCH /repos/{owner}/{repo}/git/refs/{ref}` with `sha` and `force` (default `false`). With `false` the update must be a fast-forward: "Leaving this out or setting it to false will make sure you're not overwriting work." 422 on validation failure.
- Not fetched (from memory): the full flow is `POST git/blobs`, `POST git/trees` (with `base_tree`), `POST git/commits` (with `parents`), `PATCH git/refs`. A commit made this way by an installation token, without custom author/committer and without `signature`, is signed by GitHub and verified.

GraphQL `createCommitOnBranch` (fetched: https://docs.github.com/en/graphql/reference/commits):

- "Appends a commit to the given branch as the authenticated user."
- Author and committer cannot be set: the commit is "authored by the owner of the credential which authenticates the API request."
- "Commits made using this mutation are automatically signed by GitHub if supported" "and will be marked as verified".
- Input: `branch` (`CommittableBranch!`), `expectedHeadOid` (`GitObjectID!`, "The git commit oid expected at the head of the branch prior to the commit."), `fileChanges` (`FileChanges`: additions, deletions), `message`.
- Not fetched (from memory): a wrong `expectedHeadOid` fails the mutation; many files in one commit; content is Base64; the branch must exist.

Signature verification (fetched: https://docs.github.com/en/authentication/managing-commit-signature-verification/about-commit-signature-verification):

- Web interface commits are signed by GitHub and verified.
- Bots: verification "will only work if the request is verified and authenticated as the GitHub App or bot" and has "no custom author information, custom committer information, and no custom signature information".
- Verification persists once recorded.
- Vigilant mode: a co-authored commit can show "partially verified".

Not fetched (from memory):

- A GitHub App installation commit shows author `<app-slug>[bot]`. To credit a person, add a `Co-authored-by: Name <email>` trailer at the end of the message; GitHub shows the co-author. Setting `author` to the person removes the GitHub signature.
- A user-to-server token (the app acting for a user) makes the user the author; commits are still signed by GitHub.

Rulesets (fetched: https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/creating-rulesets-for-a-repository):

- Bypass actors: admins and owners, maintain/write roles, teams, GitHub Apps, Dependabot. Deploy keys not listed on the page.
- Bypass modes: "Always allow" or "For pull requests only" ("The selected actor is now required to open a pull request").
- Not fetched (from memory): classic branch protection can list apps under "Allow specified actors to bypass required pull requests"; "Require signed commits" accepts GitHub-signed bot commits.

GitLab commits API (fetched: https://docs.gitlab.com/api/commits/):

- `POST /projects/:id/repository/commits`: `branch`, `commit_message`, `start_branch` or `start_sha` (to create the branch), `start_project`, `author_email`, `author_name`, `force` (default `false`), `stats`.
- `actions[]`: `action` (`create`, `delete`, `move`, `update`, `chmod`), `file_path`, `content`, `encoding` (`text`/`base64`), `last_commit_id` ("Last known file commit ID. Only considered in update, move, and delete actions."), `previous_path`, `execute_filemode`.
- Not fetched (from memory): a stale `last_commit_id` is rejected with 400 ("You are attempting to update a file that has changed since you started editing it"). GitLab has no branch-head precondition for this endpoint. GitLab signs web and API commits with an instance key since GitLab 17.x (uncertain).

Forgejo / Gitea (fetched: https://docs.gitea.com/api/1.22/operations/repo-change-files/):

- `POST /repos/{owner}/{repo}/contents` with `files[]`: `operation` (`create`, `update`, `delete`), `path`, `content` (Base64), `from_path`, `sha` ("required for update or delete").
- Top level: `branch`, `new_branch`, `message`, `author`, `committer`, `dates`, `signoff` (adds a `Signed-off-by` trailer).
- Not fetched (from memory): Forgejo keeps the same API; single-file `PUT /repos/{owner}/{repo}/contents/{filepath}` also needs `sha`; instance can sign with a configured key (`[repository.signing]`).

#### B3. Products that write back to git

Renovate (fetched: https://docs.renovatebot.com/updating-rebasing/, https://docs.renovatebot.com/key-concepts/pull-requests/, partial https://docs.renovatebot.com/configuration-options/):

- `branchPrefix` default `"renovate/"`; onboarding branch `renovate/configure`.
- Branch name and PR title act as the cache key for existing PRs (for example `renovate/lodash-4.x`).
- "Each Renovate branch will always have 1 and only 1 commit." Rebase means a new commit on the head of the base branch.
- `rebaseWhen`: `"auto"` (default), `"behind-base-branch"`, `"never"`.
- If a person pushes to the branch, "Renovate stops all updates of that branch". A rebase checkbox or label overrides this.
- When a PR is no longer needed Renovate "autocloses the PR and deletes the branch". If someone else modified the branch, it does not delete it; it appends "- abandoned" to the title and comments.
- `commitTrailers`: structured `Key: value` trailers. `prConcurrentLimit` default 10.
- Not fetched (from memory): `platformCommit` (`"auto"`/`"enabled"`/`"disabled"`) commits through the platform API so that commits are signed and verified for a GitHub App; autoclosed PR titles get "- autoclosed".

Dependabot (not fetched (from memory)): branch `dependabot/<ecosystem>/<directory>/<dependency>-<version>`; rebases its own PRs on conflict unless someone else pushed to the branch; closes a superseded PR with a comment "Superseded by #N"; commands `@dependabot rebase`, `@dependabot recreate`; commits by `dependabot[bot]` are signed by GitHub.

Products that do not write unit config back to git (not fetched (from memory)):

- Pulumi Cloud: no write-back of program or stack config from the console; `Pulumi.<stack>.yaml` edits stay in the user's repo. ESC holds config outside git.
- Spacelift: stack settings live in Spacelift or in Terraform (the Spacelift provider); no commit to the user repo. Blueprints create stacks, not commits.
- Atlantis: no commit feature; it comments on PRs and runs plan/apply.
- Terramate Cloud: no write-back from the UI; Terramate CLI generates code locally and the user commits it.
- env zero: no commit to the user repo from the UI (from memory, low confidence).

---

### Topic C: output reads with a run-scoped token

#### C1. HCP Terraform / TFE

Per-run token (fetched: https://developer.hashicorp.com/terraform/cloud-docs/workspaces/run/run-environment):

- HCP Terraform "generates a unique per-run API token and provides it to the Terraform worker" "in the CLI config file".
- "The per-run token can read and write state data for the workspace associated with the run", "can download modules from the private registry", and "may be granted access to read state from other workspaces in the organization."
- "Per-run tokens cannot make any other calls to the HCP Terraform API" and "are not considered to be user, team, or organization tokens."
- "They become invalid after the run is completed."
- Run env vars: `TFC_RUN_ID`, `TFC_WORKSPACE_ID`, `TFC_WORKSPACE_NAME`, `TFC_WORKSPACE_SLUG`, `TFC_CONFIGURATION_VERSION_GIT_BRANCH`, `TFC_CONFIGURATION_VERSION_GIT_COMMIT_SHA`, `TFC_CONFIGURATION_VERSION_GIT_TAG`, `TFC_PROJECT_NAME`, `TFC_PROJECT_ID`; also usable as Terraform input variables.
- The docs do not name `TFE_TOKEN` or `ATLAS_TOKEN` (web search, fetched result list for https://developer.hashicorp.com/terraform/cloud-docs/run/run-environment). Not fetched (from memory): the worker also exports the run token as `ATLAS_TOKEN` for older tooling, and `TF_TOKEN_<host>`; treat as unconfirmed.

Remote state sharing (fetched: https://developer.hashicorp.com/terraform/cloud-docs/workspaces/state):

- Options: share with all workspaces in the organization, in the project, or with specific workspaces. New workspaces share with none.
- HCP Terraform "automatically manages API credentials for `terraform_remote_state` access" in remote runs.
- "Remote state access controls do not apply when using the `tfe_outputs` data source." It needs a `tfe` provider token with permission to read state outputs.

Workspace API (fetched: https://developer.hashicorp.com/terraform/cloud-docs/api-docs/workspaces):

- `global-remote-state`: default `false`; when `false` "only specifically approved workspaces can access its state."
- `remote-state-consumers` relationship: POST add, GET list, PATCH replace, DELETE remove.
- Not fetched (from memory): paths are `/workspaces/:workspace_id/relationships/remote-state-consumers`; `terraform_remote_state` reads the whole state file, so sharing state also shares every resource attribute, not only outputs.

State version outputs (fetched: https://developer.hashicorp.com/terraform/cloud-docs/api-docs/state-version-outputs):

- `GET /state-versions/:state_version_id/outputs`, `GET /state-version-outputs/:state_version_output_id`, `GET /workspaces/:workspace_id/current-state-version-outputs`.
- Attributes `name`, `sensitive`, `type`, `value`, `detailed-type` (spelled `detailed_type` in one sample).
- Current endpoint: "Sensitive values are not revealed and will be returned as `null`." It lets organization members without state-version read permission read the latest non-sensitive outputs.
- List endpoint: returns sensitive plaintext today; from November 10, 2026 `value` is `null` for sensitive outputs; use the show endpoint for one value.
- Show: 404 if missing or not authorized.
- Outputs are processed asynchronously: check `resources-processed`; current endpoint can return 503 while processing.

`tfe_outputs` (fetched: https://raw.githubusercontent.com/hashicorp/terraform-provider-tfe/main/website/docs/d/outputs.html.markdown):

- Arguments `workspace`, `organization`. Attributes `values` (Dynamic, Sensitive: all outputs) and `nonsensitive_values` (subset that is not sensitive).
- Page silent on permissions.

#### C2. Spacelift

Stack dependencies (fetched: https://docs.spacelift.io/concepts/stack/stack-dependencies):

- `spacelift_stack_dependency_reference` with `output_name` and `input_name` (for example `TF_VAR_vpc_id`). The value is injected into the downstream run as that env var. The server pushes the value; the run does not read the upstream with a token.
- Creating references needs reader permission on the dependent stack and admin permission on the other stack.
- `trigger_always`: default triggers downstream only when the output was created or changed.
- Sensitive outputs: masked in UI and logs; transfer needs "Transfer sensitive outputs across dependencies" on the stack, and on private workers `SPACELIFT_SENSITIVE_OUTPUT_UPLOAD_ENABLED=true`.
- Only outputs of a successful tracked run with an apply phase count. Missing required input fails with "job assignment failed: the following inputs are missing".
- Not fetched (from memory): Spacelift has no `terraform_remote_state`-style run token for other stacks by default; reading another stack via the Spacelift provider needs a stack role attachment (the old "administrative" flag is now ineffective, fetched: stack-settings page).

#### C3. Pulumi

StackReference (fetched: https://www.pulumi.com/docs/iac/concepts/stacks/):

- `requireOutput` fails if the output is missing; `getOutput` returns undefined. Secret outputs need `.secretValue`/`getOutputDetails`. Outputs keep secret annotations.
- Page silent on permissions.

Not fetched (from memory): a StackReference reads with the caller's `PULUMI_ACCESS_TOKEN`; the caller needs read permission on the referenced stack. In Pulumi Deployments the run token has the permissions of the deployment's identity (the requesting user or the stack's configured team/token), not a per-run least-privilege token. Secret outputs are decrypted for any reader with stack read access.

#### C4. env zero

Not fetched (from memory): env zero has "environment outputs" that a workflow or another environment can consume through `${env0:<environment>:<output>}` references; the value is resolved by the server and injected as a variable, like Spacelift. Sensitive outputs can be marked and are masked. Low confidence on syntax.

---

### Source list

Fetched in this session:

- https://developer.hashicorp.com/terraform/cloud-docs/workspaces/dynamic-provider-credentials/aws-configuration
- https://developer.hashicorp.com/terraform/cloud-docs/workspaces/dynamic-provider-credentials/workload-identity-tokens
- https://developer.hashicorp.com/terraform/cloud-docs/workspaces/dynamic-provider-credentials/manual-generation
- https://developer.hashicorp.com/terraform/cloud-docs/agents/hooks
- https://developer.hashicorp.com/terraform/cloud-docs/workspaces/run/run-environment
- https://developer.hashicorp.com/terraform/cloud-docs/workspaces/state
- https://developer.hashicorp.com/terraform/cloud-docs/api-docs/state-version-outputs
- https://developer.hashicorp.com/terraform/cloud-docs/api-docs/workspaces (first 100,000 characters)
- https://raw.githubusercontent.com/hashicorp/terraform-provider-tfe/main/website/docs/d/outputs.html.markdown
- https://docs.spacelift.io/integrations/cloud-providers/aws
- https://docs.spacelift.io/integrations/cloud-providers/oidc
- https://docs.spacelift.io/integrations/cloud-providers
- https://docs.spacelift.io/concepts/stack/stack-settings
- https://docs.spacelift.io/concepts/stack/stack-dependencies
- https://raw.githubusercontent.com/spacelift-io/terraform-provider-spacelift/main/docs/resources/aws_integration_attachment.md
- https://raw.githubusercontent.com/spacelift-io/terraform-provider-spacelift/main/docs/resources/aws_integration.md
- https://docs.envzero.com/guides/integrations/oidc-integrations
- https://docs.envzero.com/guides/admin-guide/custom-flows
- https://docs.github.com/en/actions/reference/security/oidc
- https://docs.github.com/en/actions/security-for-github-actions/security-hardening-your-deployments/about-security-hardening-with-openid-connect
- https://github.com/aws-actions/configure-aws-credentials
- https://docs.gitlab.com/ci/secrets/id_token_authentication/
- https://docs.gitlab.com/ci/secrets/hashicorp_vault/
- https://docs.gitlab.com/ci/variables/
- https://www.pulumi.com/docs/pulumi-cloud/deployments/oidc/
- https://www.pulumi.com/docs/deployments/concepts/settings/
- https://www.pulumi.com/docs/esc/cli/commands/esc_run/
- https://www.pulumi.com/docs/iac/concepts/stacks/
- https://docs.github.com/en/rest/repos/contents
- https://docs.github.com/en/rest/git/refs
- https://docs.github.com/en/graphql/reference/commits
- https://docs.github.com/en/authentication/managing-commit-signature-verification/about-commit-signature-verification
- https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/creating-rulesets-for-a-repository
- https://docs.gitlab.com/api/commits/
- https://docs.gitea.com/api/1.22/operations/repo-change-files/
- https://www.runatlantis.io/docs/repo-level-atlantis-yaml
- https://docs.renovatebot.com/updating-rebasing/
- https://docs.renovatebot.com/key-concepts/pull-requests/
- https://docs.renovatebot.com/configuration-options/ (first 100,000 characters)

Tried and failed (404 or empty): the env zero template page, the Terraform Registry pages for `tfe_outputs` and the Spacelift attachment (read from GitHub raw instead), the GitHub GraphQL index pages, the GitHub App authentication overview (silent on the asked facts).
