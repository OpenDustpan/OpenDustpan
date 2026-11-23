## Human run-throughs (paired with agent pre-runs in the changes)

Each change below holds the agent pre-run with the transcript as evidence. Run the step here
only if the transcript leaves doubt; most need no second pass. Tick each task when done.

- [ ] 4.1 Run usability check of the user path and fix error help text. Check: Developer completes create to write-back run from docs page alone, verified with access token on `GET /api/dustpan/v1/namespaces/{namespace}/units?reference=`; BLOCKED by NEW jobs routes for job calls. A person's step.
- [ ] 4.2 Run usability check of the runner operator path. Check: Runner operator completes poll and heartbeat from the docs page alone. A person's step.
- [ ] 4.3 Usability check Check: a developer completes the path from the docs page alone. A person's step.
- [ ] 4.3 Do the usability check: an operator completes a move from the docs page alone. Check: Each error names the one command that helps. A person's step.
- [ ] 3.8 Run usability check Check: Developer completes the path from the docs page alone, each error names one command. A person's step.
- [ ] 3.6 Run usability check Check: Developer completes the path from the docs page alone, each error names one command. A person's step.
- [ ] 4.3 Run the usability check Check: A runner operator completes pool and token work from the docs page; each error names one command. A person's step.
- [ ] 4.3 Run the usability check from the docs page. Check: Persona follows the docs page alone and each error names the one command that helps. A person's step.
- [ ] 4.3 Run the usability check Check: a runner pool administrator completes the path from docs/guides/job-proxy-pool-settings.md alone. A person's step.
- [ ] 4.3 Run the usability check of the freeze path. Check: Namespace administrator completes the path from the docs page alone and each error names the one command that helps. A person's step.
- [ ] 4.2 Run the usability check from the docs page alone. Check: An embedder runs the suite on a new adapter from the page. A person's step.
- [ ] 4.3 Usability check: a person who reads only the guide sets the setting, makes a stack, lists its rule, saves a collection rule, hits the 409 and the 400 of the error table, and finds each fix by the one command that the error names. A person's step. Check: each error names one command.
- [ ] 4.3 Run the usability check. Check: The persona completes the path from the docs page alone. A person's step.
- [ ] 4.3 Run a usability check of the docs page, with each error naming one command. Check: The persona follows the page alone. A person's step.
- [ ] 4.3 Run usability check for errors and help commands. Check: Persona completes the path from the docs page alone. A person's step.
- [ ] 4.4 Run the usability check. Check: The persona completes the path from the docs page and each error names one command. A person's step.
- [ ] 4.2 Confirm wiring, route actions, e2e step, docs page and usability check. The path uses the existing route /api/v2/workspaces/{} (declared in the route table of its adapter package (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description` then writes internal/api/apidesc/openapi.json; never edit it by hand), adapter in internal/api/tfe/remoteplan.go) and the access token of a Developer. Check: The persona follows the docs page alone from setup to tofu plan with an access token, and each error names the one command that helps. A person's step. Confirmed by: CI (the agent writes the e2e test).
- [ ] 3.3 Usability check: the developer finds the reason of a refusal from the docs page alone. Check: each error row names `GET /api/v2/workspaces/{id}/state-versions` or `tofu show -json plan.tfplan`. A person's step.
- [ ] 3.3 Run usability check of the history and audit path via `ANY /api/v2/workspaces/{}/state-versions` and `ANY /api/audit/entries` with an access token. Check: Developer completes history check from the docs page alone. A person's step.
- [ ] 3.3 Usability check with a site administrator. Check: The persona completes the path from the docs page alone and each error names the one command that helps. A person's step.
- [ ] 2.3 Run usability check of full run page path and record the result in NEW file docs/console/run-page-usability.md. Check: Developer completes timeline, log, diff and approval from docs page alone. A person's step.
- [ ] 4.2 Usability check of the full create path Check: Developer completes catalog to unit path from the docs page alone. A person's step.
- [ ] 4.3 Run usability check of the rule list and refusal path. Check: Namespace administrator completes the path from the docs page alone. A person's step.
- [ ] 3.3 Run the usability check of the copied commands from a clean work directory. Check: Operator completes the install path from the docs page alone and each error names `make docs-commands-check`. A person's step.
- [ ] 4.3 Run usability check of full path Check: Developer completes compare and promote from docs page alone. A person's step.
- [ ] 4.4 Run the usability check of the user path. Check: A Developer completes create, warning, approval, and destroy from the docs page alone with each error naming one command. A person's step.
- [ ] 4.2 Run the usability check Check: A developer completes the path from the docs page alone. A person's step.
- [ ] 4.2 Usability check with namespace administrator and recorded errors. Check: Persona completes sink to signed delivery from the docs page alone. A person's step.
- [ ] 4.2 Run a usability check of the audit read path. Check: the persona completes the path from the docs page alone and each error names the one command that helps. A person's step.
- [ ] 4.3 Usability check: the site administrator completes the path from the docs page alone, and each error names its one command. Check: the check is recorded in the pull request. A person's step.
- [ ] 4.2 Run the usability check of the failure path. Check: A Developer finds the failed unit from the docs page alone and each error names one command. A person's step.
- [ ] 4.2 Run usability check of the full publish and install path. Check: Namespace administrator completes publish and tofu init from the docs page alone; each error names its one command. A person's step.
- [ ] 3.3 Usability check: the operator finds who ran each sweep from the docs page alone. Check: the check is recorded in the pull request. A person's step.
- [ ] 4.2 Usability check: an operator finds the built-in principals and their grants from the page `docs/guides/principals-and-groups.md` alone. Check: the check is recorded in the pull request. A person's step.
- [ ] 3.1 Usability check: an operator finds who ran each of these sweeps in the audit log from the entry alone. Check: the check is recorded in the pull request. A person's step.
- [ ] 3.1 Usability check: an operator follows `docs/guides/install.md` alone, pulls the image by tag and by digest, makes the two Secrets and installs the chart on a cluster. Check: the steps in the pull request. A person's step.
- [ ] 4.4 Run the usability check. Check: Persona completes the path from the docs page alone. A person's step.
- [ ] 3.4 Check the usability of the page. Check: The developer completes the job start path from the docs page alone. A person's step.
- [ ] 3.2 Run the usability check from the docs page alone. Check: The Operator completes the path from the docs page and each error names one command. A person's step.
- [ ] 4.3 Usability check from the docs page alone Check: A Developer completes both cases from the docs page alone, and each error names the one command that helps. A person's step.
- [ ] 3.4 Run usability check of user path. Check: Developer completes plain apply and reads holder from docs page alone; each error names one command. A person's step.
- [ ] 3.3 Run a usability check and record where the path costs time. Check: The persona reaches both CLI runs from the docs page alone. A person's step.
- [ ] 4.2 Usability check of the failed-run path. Check: an Operator reruns from the docs page alone, and each breach in the job summary names `gh run view <run-id>`. A person's step (after the workflow patch is applied).
- [ ] 3.6 Run usability check and full check. Check: Developer follows docs/guides/output-sharing.md alone and each error names its one command; `make check` passes with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).
- [ ] 4.3 Run the usability check of the confirm path. Check: the persona completes the confirm from the docs page alone and each error names the one command that helps. A person's step.
- [ ] 4.3 Complete usability check. Check: Persona completes plan from docs page alone. A person's step.
- [ ] 3.4 Run usability check Check: Namespace administrator completes path from docs page alone; each error names one command. A person's step.
- [ ] 5.3 Usability check: an operator follows `docs/reference/journaling.md` alone, turns journaling off with `DUSTPAN_DISABLE_PULUMI_JOURNALING=true`, restarts, and a `pulumi up` saves checkpoints. Check: `TestPulumiJournalingBothModes` covers the off run with that variable; the pull request body records the walk of the page. A person's step.
- [ ] 4.2 Run the usability check for the drift path. Check: Developer completes the path from the docs page alone. A person's step.
- [ ] 4.3 Run the usability check of the read of the limits from the docs page alone. Check: A namespace administrator reads the limits from the docs page alone; the error of a missing grant names its one command. A person's step.
- [ ] 4.3 Run usability check of the install path under the slug. Check: Namespace administrator completes publish and tofu init from the docs page alone; each error names its one command. A person's step.
- [ ] 4.2 Complete usability check of the review path. Check: Developer completes the review path from the docs page alone and each error names the one command that helps. A person's step.
- [ ] 4.3 Run usability check of rolling replace path. Check: Operator completes replace from existing high availability guide alone with clear version error. A person's step.
- [ ] 4.3 Run the usability check from the docs page alone. Check: The person completes the path from the docs page alone and each error names one command. A person's step.
- [ ] 4.3 Do a usability check of the ordered apply path from the guide alone. Check: A Developer follows the guide alone and each error names the one command that helps. A person's step.
- [ ] 3.3 Run the usability check of the discovery path. Check: Runner operator completes discovery from the docs page alone and each error names the one command that helps. A person's step.
- [ ] 2.4 Run the usability check from the docs page. Check: A developer completes the path from the docs page alone and each error names one command. A person's step.
- [ ] 4.1 Run a usability check of the full path from the docs page. Check: A runner operator completes the full path from the docs page alone with an access token for pool work and a runner token for ANY /runner/v1 through the existing gates. A person's step.
- [ ] 2.3 Usability check: an operator finds the counter from the docs page alone. Check: Manual walk of the docs page. A person's step.
- [ ] 3.2 Usability check: a runner operator completes the join from docs/guides/runner-join.md alone, and each error names its one command. Check: Manual walk-through recorded in the pull request body. A person's step.
- [ ] 4.3 Run usability check of the guide Check: Embedder completes the path from the docs page alone. A person's step.
- [ ] 4.3 Run the usability check. Check: a Developer finds the banner and the token that ends from the page alone. A person's step.
- [ ] 4.4 Run the usability check. Check: a Developer finds the ended token and the audit reason from the guide alone. A person's step.
- [ ] 4.4 Run usability check of the operator path. Check: Operator reads the path alone and each error names the one command that helps. A person's step.
- [ ] 4.3 Run usability check. Check: Developer completes the path from the docs page alone. A person's step.
- [ ] 4.4 Run the usability check Check: persona completes the path from the docs page alone; each error names one command. A person's step.
- [ ] 4.3 Run the usability check of the path. Check: Developer follows the docs page alone and names the one command that explains a skipped start. A person's step.

## Strictly human (needs a person, no agent counterpart)

- [ ] 1.1 Confirm that the regen GitHub App, whose token `.github/workflows/auto-regen-push.yml:103` mints, has the permission Actions read and write. The token step of task 1.2 fails with a clear error when it does not. When it does not, stop and report it to the PM: a person with access to the settings of the App adds the permission. Record the result in the pull request body. Check: the pull request body states the permission of the App. The cloud agent cannot push .github/workflows. Write the full diff of the workflow file in the pull request description under the heading 'Workflow patch (apply locally)', and leave the PR as a draft. Confirmed by: A person's step.
- [ ] 4.3 Run the usability check of the maintainer section: a maintainer who has only the page names the steps that follow a tag push, finds the draft, reads its five files, publishes it, and knows what to do after a failed draft job. Check: the pull request body of A records the walk-through with the commands that it used, run against the fake `gh` of the test (the real draft needs a tag, which no one makes). A person's step.
- [ ] 4.3 Mark the change `approval-defaults-take-effect-live` stale in its own pull request (its premise is false on main). Check: that pull request exists. A person's step.
- [ ] 0.1 For each open PR that touches `pkg/store/`: merge, close as stale, or mark "port with branch mode". Check: a table of the 23 PRs and their outcome in STATE.md. Confirmed by: A person's step (the lead triages the open store PRs).

## Archived origin (tick here with evidence; the change is closed)

- [ ] 4.2 Run usability check of the agent-block read path. Check: Persona completes the user path from the docs page alone, and each error names the one command that helps. A person's step.
- [ ] 4.3 Run the usability check and make check. Check: Usability check: the Operator completes the path from the docs page alone using /api/dustpan/v1/backups with an access token; each error names its one command (`opendustpan backup`); make check with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).
- [ ] 4.3 Run the usability check Check: a namespace administrator completes the path from docs/guides/job-proxy-rules.md alone. A person's step.
- [ ] 3.4 In docs/guides/ldap-sign-in.md, usability check: a person follows the guide alone against the test directory and signs in. Each error they cause names the setting or the one command that helps. Check: the steps and the errors are in the pull request description. A person's step.
- [ ] 4.1 Run the usability check for the local path. Check: A developer completes the run from the docs page alone; each error names one command. A person's step.
- [ ] 4.2 Run the full check, and the usability check from the docs page alone. Check: `make check` with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).
- [ ] 4.2 Usability check from the docs page alone Check: each error names one command. A person's step.
- [ ] 4.1 Tag a release candidate in CI, pull both references by digest, run `helm install` from the default registry, and check that an Operator completes the install page alone and each error names one command. Check: The CI run passes, the digests match, `make docs-lint` is clean and `make check` reports zero findings. Confirmed by: A person's step (after the workflow patch is applied).
- [ ] 2.4 Move golang.org/x/crypto in go.mod from the pseudo-version to the tag v0.59.0 on or after 25 November 2026, with the owner's word (ADR 379 decision 3) and a clean make vuln. Check: `go list -m golang.org/x/crypto` shows v0.59.0. A person's step. Owner approved 2026-11-21, explicitly not blocking for agents; run on or after 2026-11-25.
- [ ] 4.1 Run definition of done for this part: wiring of both NEW Make targets and usability check from test output; route, e2e install step, and docs page are BLOCKED by part 2 container image and install docs work Check: BLOCKED by part 2 container image, install test route and docs page; `make check` with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).
- [ ] 4.2 Run wiring, route with authorization actions, docs page and usability check. Check: `make check` with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).
- [ ] 4.3 Run usability check from the docs page alone Check: an Operator completes the matrix from docs/guides/backup-restore-upgrade.md alone and each error names the one helping command. A person's step.
- [ ] 3.5 Usability check from the docs draft. Check: The persona completes the path and each error names one command. A person's step.
- [ ] 4.3 Usability check from the docs page of backup-fence-target-writes-attempt. Check: an Operator completes the path from docs/guides/several-instances.md alone (start a backup, check it, read the target layout and the cleanup step) and each error names the one command that helps. A person's step.
- [ ] 4.3 Usability check from the docs page of add-job-session-paths-runner-2 (docs/reference/runner-api.md) Check: Each error names one command that helps. A person's step.

## Move staged workflows into place

- [ ] Move `.github-workflows-staging/ci.yml` into `.github/workflows/ci.yml` and push. Written by: ai-pr-links-spec-ready-change 2.3, ci-service-container-s3-suite 1.1, 2.1, 2.2, 4.1. Check: the workflows match and CI picks the file up.
- [ ] Move `.github-workflows-staging/auto-regen.yml` into `.github/workflows/auto-regen.yml` and push. Written by: fix-auto-regen-push-and-resolve-starvation 1.2. Check: the workflows match and CI picks the file up.
- [ ] Move `.github-workflows-staging/auto-regen-resolve.yml` into `.github/workflows/auto-regen-resolve.yml` and push. Written by: fix-auto-regen-push-and-resolve-starvation 1.3. Check: the workflows match and CI picks the file up.
- [ ] Move `.github-workflows-staging/nightly-perf.yml` into `.github/workflows/nightly-perf.yml` and push. Written by: nightly-load-soak-fault-job 2.4. Check: the workflows match and CI picks the file up.
- [ ] Move `.github-workflows-staging/upgrade-test.yml` into `.github/workflows/upgrade-test.yml` and push. Written by: upgrade-test-with-real-data 2.3. Check: the workflows match and CI picks the file up.
