## Person steps by source change

Each task keeps its original number and its original check. The source
path in parentheses names the change the task moved from. Complete them
in any order; tick each task here when it is done.

## From 100-103 (3)

- [ ] (100-103 2.3) 2.3 Write docs page NEW docs/jobs.md for jobs: create, list, cancel, phases, and errors. Check: Docs page commands run as written in a fresh install, verified with access token reads on `GET /api/dustpan/v1/namespaces/{namespace}/units?reference=`; BLOCKED by NEW jobs routes for job create examples. Confirmed by: CI (an e2e step in the NEW file e2e/100_103_docs_test.go runs each command of the docs page against the e2e test server). A person's step.
- [ ] (100-103 4.1) 4.1 Run usability check of the user path and fix error help text. Check: Developer completes create to write-back run from docs page alone, verified with access token on `GET /api/dustpan/v1/namespaces/{namespace}/units?reference=`; BLOCKED by NEW jobs routes for job calls. A person's step.
- [ ] (100-103 4.2) 4.2 Run full definition of done: wiring, route, docs, usability, make check. Check: `make check` with zero findings after wiring; acceptance baseline uses access token on `GET /api/dustpan/v1/namespaces/{namespace}/units?reference=` and BLOCKED by NEW jobs routes for job calls. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From 130-138-launcher-steps-execution (1)

- [ ] (130-138-launcher-steps-execution 4.2) 4.2 Run e2e step with real launcher, usability check, and full checks. Check: E2E job on route ANY /runner/v1 with credential kind handoff token runs a real launcher step with no shell, streams logs, and reports one exit; persona completes path from docs page; `make check` reports zero findings. A person's step. Confirmed by: CI (the agent writes the e2e test). Confirmed by: CI (the agent runs the fast checks only).

## From 130-138-runner-pools-management (1)

- [ ] (130-138-runner-pools-management 4.3) 4.3 Run usability check, route action test TestActionsWithoutRoutesAreUnclassified, and make check. Check: `make check` with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From 130-138-runner-protocol-claims (1)

- [ ] (130-138-runner-protocol-claims 4.2) 4.2 Run usability check of the runner operator path. Check: Runner operator completes poll and heartbeat from the docs page alone. A person's step.

## From 130-138-source-filter-sweeps (1)

- [ ] (130-138-source-filter-sweeps 4.4) 4.4 Run the usability check from the guide alone. Check: Each error names its one command. A person's step.

## From 150-153 (1)

- [ ] (150-153 4.3) 4.3 Usability check Check: a developer completes the path from the docs page alone. A person's step.

## From 160-165-context-graph-focus (1)

- [ ] (160-165-context-graph-focus 4.3) 4.3 Run usability check from docs page alone plus full checks. Check: `make check` passes with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From 160-165-report-intake-findings (1)

- [ ] (160-165-report-intake-findings 4.2) 4.2 Write the NEW docs page docs/guides/sweep-reports.md and run the usability check. Check: `make docs-lint`. A person's step.

## From 160-165-schedule-state-check (1)

- [ ] (160-165-schedule-state-check 4.3) 4.3 Usability check, e2e step on the real store path, and full check. Check: E2E calls existing route `GET /api/audit/entries` with an access token and lists state-check rows; `make check` reports zero findings. A person's step. Confirmed by: CI (the agent writes the e2e test). Confirmed by: CI (the agent runs the fast checks only).

## From 160-165-sweep-targets-lifecycle (1)

- [ ] (160-165-sweep-targets-lifecycle 4.3) 4.3 Usability check from the docs page alone, including the busy and pool errors. Check: Namespace administrator completes the path from the docs. A person's step.

## From 37-namespace-placement-routing-1 (1)

- [ ] (37-namespace-placement-routing-1 4.1) 4.1 Wire the guard into every namespace write path. Run an e2e step with the real Pulumi CLI and OpenTofu, with an access token, against GET /api/dustpan/v1/namespaces/{namespace}/settings and the write routes of each tool, each with its authorization action. Run the usability check from the docs page. Check: `make e2e` passes, then `make check` reports zero findings. A person's step. Confirmed by: CI (the agent writes the e2e test). Confirmed by: CI (the agent runs the fast checks only).

## From 37-namespace-placement-routing-move (1)

- [ ] (37-namespace-placement-routing-move 4.3) 4.3 Do the usability check: an operator completes a move from the docs page alone. Check: Each error names the one command that helps. A person's step.

## From 39-reading-state-contents (2)

- [ ] (39-reading-state-contents 3.7) 3.7 Run each command of the NEW docs page `docs/guides/reading-state-contents.md` by hand from a clean install. Check: each command runs as written and gives the output that the page shows. A person's step.
- [ ] (39-reading-state-contents 3.8) 3.8 Run usability check Check: Developer completes the path from the docs page alone, each error names one command. A person's step.

## From 39-reveal-state-value (1)

- [ ] (39-reveal-state-value 3.6) 3.6 Run usability check Check: Developer completes the path from the docs page alone, each error names one command. A person's step.

## From accept-uploaded-plans-team-s (1)

- [ ] (accept-uploaded-plans-team-s 3.1) 3.1 Wire the route with its authorization actions, the e2e step with the real OpenTofu CLI, the docs page and the usability check. The upload route is NEW; it does not exist on main. ADR 378 sets the hash binding. Declare the route in `internal/api/apidesc/openapi.json` and write its adapter in a NEW file under `internal/api/`. The e2e step is BLOCKED by the missing upload route and the missing credential kind for it: no existing route and no existing credential kind on main run the acceptance path. Check: `make check` with zero findings and `make e2e`. A person's step. Confirmed by: CI (the agent writes the e2e test). Confirmed by: CI (the agent runs the fast checks only).

## From add-access-limits-per-scope (1)

- [ ] (add-access-limits-per-scope 4.1) 4.1 Run the e2e step with the real server over TLS: a session credential calls PUT /api/dustpan/v1/namespaces/{namespace}/access-limits/{id}, then GET /api/dustpan/v1/namespaces/{namespace}/units is refused with gate.ErrLimited. Add docs page and usability check. Check: `make e2e` passes the access-limit step. A person's step. Confirmed by: CI (the agent writes the e2e test).

## From add-e2e-step-runs-real-launcher-binary-modes (1)

- [ ] (add-e2e-step-runs-real-launcher-binary-modes 3.3) 3.3 Usability check: BLOCKED by the sibling part 3 change, which adds the real runner e2e step and the route and credential kind for it. Check: BLOCKED by add-e2e-step-runs-real-runner. A person's step. Confirmed by: CI (the agent writes the e2e test).

## From add-e2e-step-runs-real-runner (2)

- [ ] (add-e2e-step-runs-real-runner 4.2) 4.2 Add the operator docs page for pool setup, join, drain and retire in NEW file docs/guides/runner-pools.md. Check: Docs page commands run as written in a clean installation. Confirmed by: CI (an e2e step in the NEW file e2e/add_e2e_step_runs_real_runner_docs_test.go runs each command of the docs page against the e2e test server). A person's step. `make docs-lint` passes on each edited guide.
- [ ] (add-e2e-step-runs-real-runner 4.3) 4.3 Run usability check, route test, e2e step against ANY /runner/v1 with a runner key and full check gate. Check: `make check` reports zero findings after the e2e step against ANY /runner/v1 with a runner key and ANY /api/runners/{}/pools with an access token. A person's step. Confirmed by: CI (the agent writes the e2e test). Confirmed by: CI (the agent runs the fast checks only).

## From add-e2e-step-runs-real-runner-pool-route-move (1)

- [ ] (add-e2e-step-runs-real-runner-pool-route-move 4.3) 4.3 Run the usability check Check: A runner operator completes pool and token work from the docs page; each error names one command. A person's step.

## From add-external-cedar-service-adapter (1)

- [ ] (add-external-cedar-service-adapter 2.3) 2.3 Run the usability check and the full check Check: make check reports zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From add-gate-simulate-route-starter (1)

- [ ] (add-gate-simulate-route-starter 4.3) 4.3 Run the usability check from the docs page. Check: Persona follows the docs page alone and each error names the one command that helps. A person's step.

## From add-job-proxy-runner-contract (1)

- [ ] (add-job-proxy-runner-contract 4.3) 4.3 Run the usability check Check: a runner pool administrator completes the path from docs/guides/job-proxy-pool-settings.md alone. A person's step.

## From add-job-service-routes-start-freeze-manage (1)

- [ ] (add-job-service-routes-start-freeze-manage 4.3) 4.3 Run the usability check of the freeze path. Check: Namespace administrator completes the path from the docs page alone and each error names the one command that helps. A person's step.

## From add-job-session-paths-runner-2 (1)

- [ ] (add-job-session-paths-runner-2 4.3) 4.3 Check the usability of the path from the docs page alone. Check: Each error names one command that helps. A person's step.

## From add-job-session-paths-runner-sweep-claimer-wiring (1)

- [ ] (add-job-session-paths-runner-sweep-claimer-wiring 4.2) 4.2 Check the route actions, the wiring, the docs page and the usability walk, then run the full check. Check: `make check` reports zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From add-job-session-paths-runner-unmanaged-sweep-store (1)

- [ ] (add-job-session-paths-runner-unmanaged-sweep-store 4.2) 4.2 Run the usability check from the docs page alone. Check: An embedder runs the suite on a new adapter from the page. A person's step.

## From add-named-path-sets-paths (1)

- [ ] (add-named-path-sets-paths 4.2) 4.2 Wiring, route, docs page and usability check from the docs page alone. Check: `make check`. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From add-one-generic-settings-api (1)

- [ ] (add-one-generic-settings-api 3.5) 3.5 Run the usability check from the docs page alone. Check: Each error names the one command that helps. A person's step.

## From add-output-sharing-setting-new (2)

- [ ] (add-output-sharing-setting-new 4.3) 4.3 Usability check: a person who reads only the guide sets the setting, makes a stack, lists its rule, saves a collection rule, hits the 409 and the 400 of the error table, and finds each fix by the one command that the error names. A person's step. Check: each error names one command.
- [ ] (add-output-sharing-setting-new 4.4) 4.4 Run the fast checks of the Makefile. No route: the test runs the local Makefile targets only. Check: `make test-changed` reports no finding; CI runs `make check` and the e2e job. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From add-ownership-rules-conditional-auto (1)

- [ ] (add-ownership-rules-conditional-auto 4.3) 4.3 Run the usability check. Check: The persona completes the path from the docs page alone. A person's step.

## From add-schedule-triggers-due-claim (1)

- [ ] (add-schedule-triggers-due-claim 4.3) 4.3 Run a usability check of the docs page, with each error naming one command. Check: The persona follows the page alone. A person's step.

## From ai-pr-links-spec-ready-change (1)

- [ ] (ai-pr-links-spec-ready-change 2.3) 2.3 Add the step to `.github/workflows/ci.yml` for `pull_request` only, after the checkout (with the history of the pull request): write the body from `github.event.pull_request.body` through an environment variable to a file, write `git log --format='%ae' <base>..<head>` to a file, and run `make ai-pr-spec-check`. Check: `make semgrep` reports nothing for the workflow. Confirmed by: CI (the agent runs the fast checks only). The cloud agent cannot push .github/workflows. Write the full diff of the workflow file in the pull request description under the heading 'Workflow patch (apply locally)', and leave the PR as a draft. Confirmed by: A person's step.

## From alternation (1)

- [ ] (alternation 3.2) 3.2 Run full definition of done: wiring through proxy, real CLI traffic, docs page, usability check from docs page, make check Check: `make check` reports zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From app-level-second-factor-totp (1)

- [ ] (app-level-second-factor-totp 4.3) 4.3 Docs page and usability check Check: Docs page commands run as written and each error names the one command that helps. A person's step.

## From apply-after-merge (1)

- [ ] (apply-after-merge 4.3) 4.3 Run usability check for errors and help commands. Check: Persona completes the path from the docs page alone. A person's step.

## From approval-defaults-take-effect-live (1)

- [ ] (approval-defaults-take-effect-live 4.3) 4.3 Run the usability check from the docs page alone and `make check` with zero findings. Check: `make check` reports zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From artifact-registry-lifecycle-cli (1)

- [ ] (artifact-registry-lifecycle-cli 4.1) 4.1 Run full check and the usability walk from the docs page alone. Check: `make check` with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From artifact-registry-private-module (1)

- [ ] (artifact-registry-private-module 4.1) 4.1 Confirm wiring, route actions, e2e, docs, audit entries, and usability. Check: Route GET /api/dustpan/v1/namespaces/{}/registry/modules/{}/{}/versions with an access token passes e2e on SQLite and PostgreSQL; routes declare authorization actions; usability check passes; `make check` passes with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From artifact-registry-private-pulumi-plugins (1)

- [ ] (artifact-registry-private-pulumi-plugins 4.1) 4.1 Run wiring check, route authorization check, docs page, usability walk, and full check set. The routes are the new plugin download route under /api/dustpan/v1/namespaces/{}/registry (declared in internal/api/apidesc/routes_plugins.go (NEW), adapter in internal/registrysvc/plugin_http.go (NEW)) and the existing route PUT /api/dustpan/v1/namespaces/{}/registry/providers/{}/versions/{}; the credential kind is a personal access token. Check: Route test shows registry read actions on the new plugin download route (personal access token); the e2e step above passes; docs test passes; `make check` reports zero findings. A person's step. Confirmed by: CI (the agent writes the e2e test). Confirmed by: CI (the agent runs the fast checks only).

## From audit-entry-has-no-agent (1)

- [ ] (audit-entry-has-no-agent 4.2) 4.2 Run usability check of the agent-block read path. Check: Persona completes the user path from the docs page alone, and each error names the one command that helps. A person's step.

## From audit-list-page-token (2)

- [ ] (audit-list-page-token 4.2) 4.2 Usability check from the docs page alone: a site administrator pages a log of more than 200 entries with the commands of `docs/guides/audit-log.md`, and each error names the one command that helps. Check: the person reads the three errors of 3.1 in the answers of a test server. A person's step.
- [ ] (audit-list-page-token 4.3) 4.3 Opus security review (rev2) on the exact head of the pull request. The review reads the tables of 1.5 and 1.8 and checks that each guard has a test that fails without it, and that the handler checks the action before it reads the token. Check: the review verdict is CLEAN on the head. A person's step.

## From backup-fence-target-writes-attempt (1)

- [ ] (backup-fence-target-writes-attempt 4.3) 4.3 Run the usability check from the docs page and `make check`. Check: `make check` with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From backups-do-not-copy-files (1)

- [ ] (backups-do-not-copy-files 4.3) 4.3 Run the usability check and make check. Check: Usability check: the Operator completes the path from the docs page alone using /api/dustpan/v1/backups with an access token; each error names its one command (`opendustpan backup`); make check with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From blueprints (1)

- [ ] (blueprints 4.4) 4.4 Usability check from the docs page alone Check: Each error names one command. A person's step.

## From break-glass-pass-wiring (2)

- [ ] (break-glass-pass-wiring 3.2) 3.2 BLOCKED by the owner gate until the lead confirms task 3.3. Wire the header X-Dustpan-Break-Glass-Pass on the routes that a gate in error refuses (the state write routes of break-glass-gates-in-error), with their authorization actions; add the e2e step, the docs page and the usability check. Check: make e2e and make check with zero findings. A person's step. Confirmed by: CI (the agent writes the e2e test). Confirmed by: CI (the agent runs the fast checks only).
- [ ] (break-glass-pass-wiring 3.3) 3.3 Confirm that the real OpenTofu CLI, the real Pulumi CLI or the local wrapper can send the header X-Dustpan-Break-Glass-Pass on those routes, and record how in the pull request body. If none can send it, the question goes back to the owner. Check: the pull request body names the client that sends the header. A person's step.

## From break-glass-sign-in-typed (1)

- [ ] (break-glass-sign-in-typed 4.4) 4.4 Run the usability check. Check: The persona completes the path from the docs page and each error names one command. A person's step.

## From ci-service-container-s3-suite (6)

- [ ] (ci-service-container-s3-suite 1.1) 1.1 Record the owner decision in the pull request text (NEW note, no file edit): ADR 376 decision 2 says "CI runs the S3 suite on RustFS 1.x, pinned by digest (owner, 2026-11-14)." Read the digest of the current 1.x release again and pin that digest in .github/workflows/ci.yml. Check: Pull request text cites ADR 376 decision 2 and lists the digest of the current RustFS 1.x release. The cloud agent cannot push .github/workflows. Write the full diff of the workflow file in the pull request description under the heading 'Workflow patch (apply locally)', and leave the PR as a draft. Confirmed by: A person's step.
- [ ] (ci-service-container-s3-suite 2.1) 2.1 In .github/workflows/ci.yml (the go job), add the RustFS service container to the go job and set DUSTPAN_TEST_S3_ENDPOINT, DUSTPAN_TEST_S3_BUCKET, DUSTPAN_TEST_S3_ACCESS_KEY and DUSTPAN_TEST_S3_SECRET_KEY. Check: `rg -n rustfs .github/workflows/ci.yml` shows the digest, ports 9000:9000 and the fixed test-only RUSTFS_ROOT_PASSWORD. The cloud agent cannot push .github/workflows. Write the full diff of the workflow file in the pull request description under the heading 'Workflow patch (apply locally)', and leave the PR as a draft. Confirmed by: A person's step.
- [ ] (ci-service-container-s3-suite 2.2) 2.2 In .github/workflows/ci.yml (the go job, before the go test step), add steps that wait for the S3 API and then make the bucket with aws cli so an existing bucket passes. Check: CI log shows list-buckets readiness retries then create-bucket exits 0, and a rerun with an existing bucket still exits 0 (scenario Existing bucket passes). The cloud agent cannot push .github/workflows. Write the full diff of the workflow file in the pull request description under the heading 'Workflow patch (apply locally)', and leave the PR as a draft. Confirmed by: A person's step (after the workflow patch is applied).
- [ ] (ci-service-container-s3-suite 3.1) 3.1 Show that go test -race ./... runs the S3 suite in CI and passes Check: CI log of `go test -race ./... -v` shows pkg/blob/s3 ran blobtest.Run to pass and the guard step exits 0. The check needs a CI run of the patched .github/workflows/ci.yml. Confirmed by: A person's step (after the workflow patch is applied).
- [ ] (ci-service-container-s3-suite 3.2) 3.2 Prove the no-skip guard fails the job when the suite skips Check: Guard on the -v log exits 1 when the pkg/blob/s3 log holds --- SKIP or the package result is not ok. The check needs a CI run of the patched .github/workflows/ci.yml. Confirmed by: A person's step (after the workflow patch is applied).
- [ ] (ci-service-container-s3-suite 4.1) 4.1 Record the digest check with exact store, version and date in the pull request text for .github/workflows/ci.yml (NEW note, no file edit); the digest is pinned in .github/workflows/ci.yml. Check: Pull request text lists the RustFS digest, its version and the check date with no support claim beyond the test. The cloud agent cannot push .github/workflows. Write the full diff of the workflow file in the pull request description under the heading 'Workflow patch (apply locally)', and leave the PR as a draft. Confirmed by: A person's step.

## From cli-driven-remote-execution (1)

- [ ] (cli-driven-remote-execution 4.2) 4.2 Confirm wiring, route actions, e2e step, docs page and usability check. The path uses the existing route /api/v2/workspaces/{} (declared in the route table of its adapter package (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description` then writes internal/api/apidesc/openapi.json; never edit it by hand), adapter in internal/api/tfe/remoteplan.go) and the access token of a Developer. Check: The persona follows the docs page alone from setup to tofu plan with an access token, and each error names the one command that helps. A person's step. Confirmed by: CI (the agent writes the e2e test).

## From closure-test-for-replacements (1)

- [ ] (closure-test-for-replacements 3.3) 3.3 Usability check: the developer finds the reason of a refusal from the docs page alone. Check: each error row names `GET /api/v2/workspaces/{id}/state-versions` or `tofu show -json plan.tfplan`. A person's step.

## From collections-placement (1)

- [ ] (collections-placement 4.3) 4.3 Do the usability check from the docs page alone and run all checks Check: `make check` with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From concurrent-applies-merge-core (1)

- [ ] (concurrent-applies-merge-core 3.4) 3.4 Run usability check of developer path Check: Usability walk from docs page alone passes with exact errors. A person's step.

## From concurrent-applies-merge-history-audit (2)

- [ ] (concurrent-applies-merge-history-audit 3.3) 3.3 Run usability check of the history and audit path via `ANY /api/v2/workspaces/{}/state-versions` and `ANY /api/audit/entries` with an access token. Check: Developer completes history check from the docs page alone. A person's step.
- [ ] (concurrent-applies-merge-history-audit 3.4) 3.4 Run wiring, route, e2e, docs, and usability checks with make check, covering `ANY /api/v2/workspaces/{}/state-versions` and `ANY /api/audit/entries` with an access token. Check: make check passes with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From console (1)

- [ ] (console 3.3) 3.3 Usability check with a site administrator. Check: The persona completes the path from the docs page alone and each error names the one command that helps. A person's step.

## From console-2 (1)

- [ ] (console-2 2.3) 2.3 Run usability check of full run page path and record the result in NEW file docs/console/run-page-usability.md. Check: Developer completes timeline, log, diff and approval from docs page alone. A person's step.

## From console-3 (1)

- [ ] (console-3 4.2) 4.2 Usability check of the full create path Check: Developer completes catalog to unit path from the docs page alone. A person's step.

## From curated-upstreams (2)

- [ ] (curated-upstreams 4.2) 4.2 Write docs page for curation rules in NEW file docs/guides/curated-upstreams.md in the same pull request. Check: Docs page commands run as written in a clean installation. Confirmed by: CI (an e2e step in the NEW file e2e/curated_upstreams_docs_test.go runs each command of the docs page against the e2e test server). A person's step. `make docs-lint` passes on each edited guide.
- [ ] (curated-upstreams 4.3) 4.3 Run usability check of the rule list and refusal path. Check: Namespace administrator completes the path from the docs page alone. A person's step.

## From database-outage-503-shape (1)

- [ ] (database-outage-503-shape 4.1) 4.1 Wiring, route authorization actions unchanged, e2e step deferred, docs page, usability check Check: `make check` with zero findings; full long and short outage e2e is BLOCKED by database-outage-e2e-probe. A person's step. Confirmed by: CI (the agent writes the e2e test). Confirmed by: CI (the agent runs the fast checks only).

## From database-outage-e2e-probe (1)

- [ ] (database-outage-e2e-probe 4.3) 4.3 Run usability check from the docs page alone and full checks Check: `make check` with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From docs-commands-run-in-tests (1)

- [ ] (docs-commands-run-in-tests 3.3) 3.3 Run the usability check of the copied commands from a clean work directory. Check: Operator completes the install path from the docs page alone and each error names `make docs-commands-check`. A person's step.

## From dusty-proposes-change-order (1)

- [ ] (dusty-proposes-change-order 4.2) 4.2 Write the guide in the NEW file `docs/guides/dusty-draft.md` and check the user path from the page alone. Check: `make docs-lint` passes. A person's step.

## From environment-promotion (1)

- [ ] (environment-promotion 4.3) 4.3 Run usability check of full path Check: Developer completes compare and promote from docs page alone. A person's step.

## From ephemeral-units (1)

- [ ] (ephemeral-units 4.4) 4.4 Run the usability check of the user path. Check: A Developer completes create, warning, approval, and destroy from the docs page alone with each error naming one command. A person's step.

## From evaluate-gates-on-pulumi-jobs (1)

- [ ] (evaluate-gates-on-pulumi-jobs 4.2) 4.2 Run the usability check Check: A developer completes the path from the docs page alone. A person's step.

## From events-notifications (2)

- [ ] (events-notifications 4.1) 4.1 Write docs page in the existing file docs/guides/notification-destinations.md: extend it with create sink, subscribe with type filter, rotate signing secret, redeliver, read deliveries, order by sequence. Check: Docs page commands run as written in a fresh install. Confirmed by: CI (an e2e step in the NEW file e2e/events_notifications_docs_test.go runs each command of the docs page against the e2e test server). A person's step. `make docs-lint` passes on each edited guide.
- [ ] (events-notifications 4.2) 4.2 Usability check with namespace administrator and recorded errors. Check: Persona completes sink to signed delivery from the docs page alone. A person's step.

## From every-step-audited (1)

- [ ] (every-step-audited 4.2) 4.2 Run a usability check of the audit read path. Check: the persona completes the path from the docs page alone and each error names the one command that helps. A person's step.

## From external-service-principal (1)

- [ ] (external-service-principal 4.3) 4.3 Usability check: the site administrator completes the path from the docs page alone, and each error names its one command. Check: the check is recorded in the pull request. A person's step.

## From failure-in-one-unit (1)

- [ ] (failure-in-one-unit 4.2) 4.2 Run the usability check of the failure path. Check: A Developer finds the failed unit from the docs page alone and each error names one command. A person's step.

## From first-step-up-key-in (1)

- [ ] (first-step-up-key-in 4.4) 4.4 Usability check from the docs page alone Check: each error names one command. A person's step.

## From fix-auto-regen-push-and-resolve-starvation (7)

- [ ] (fix-auto-regen-push-and-resolve-starvation 1.1) 1.1 Confirm that the regen GitHub App, whose token `.github/workflows/auto-regen-push.yml:103` mints, has the permission Actions read and write. The token step of task 1.2 fails with a clear error when it does not. When it does not, stop and report it to the PM: a person with access to the settings of the App adds the permission. Record the result in the pull request body. Check: the pull request body states the permission of the App. The cloud agent cannot push .github/workflows. Write the full diff of the workflow file in the pull request description under the heading 'Workflow patch (apply locally)', and leave the PR as a draft. Confirmed by: A person's step.
- [ ] (fix-auto-regen-push-and-resolve-starvation 1.2) 1.2 In `.github/workflows/auto-regen.yml` (the dispatch job, `:92-110`), add a step that runs `actions/create-github-app-token` at the same pinned commit as `.github/workflows/auto-regen-push.yml:103`, with the client ID from the secret REGEN_APP_CLIENT_ID, the private key from the secret REGEN_APP_PRIVATE_KEY, and `permission-actions: write` only. Pass its token as `GH_TOKEN` to `./scripts/auto-regen.sh dispatch`. Keep `persist-credentials: false` and the job permissions `contents: read` and `actions: write`. Check: `make semgrep` reports nothing for the workflow. The cloud agent cannot push .github/workflows. Write the full diff of the workflow file in the pull request description under the heading 'Workflow patch (apply locally)', and leave the PR as a draft. Confirmed by: A person's step.
- [ ] (fix-auto-regen-push-and-resolve-starvation 1.3) 1.3 In `.github/workflows/auto-regen-resolve.yml:36-38`, set the group to auto-regen-resolve-<pr>-<head>, built from the workflow inputs pr and head, and set `cancel-in-progress: false`, and change the comment above it to say why. Check: `make semgrep` reports nothing for the workflow. The cloud agent cannot push .github/workflows. Write the full diff of the workflow file in the pull request description under the heading 'Workflow patch (apply locally)', and leave the PR as a draft. Confirmed by: A person's step.
- [ ] (fix-auto-regen-push-and-resolve-starvation 1.5) 1.5 Keep `.github/workflows/auto-regen-push.yml` as it is: every guard of `:26-30`, `cmd_match_run`, the check that main did not move and `check-patch`. Check: `git diff main -- .github/workflows/auto-regen-push.yml` is empty. The cloud agent cannot push .github/workflows. Write the full diff of the workflow file in the pull request description under the heading 'Workflow patch (apply locally)', and leave the PR as a draft. Confirmed by: A person's step.
- [ ] (fix-auto-regen-push-and-resolve-starvation 2.2) 2.2 (`.github/workflows/auto-regen-resolve.yml`) A push to main while a resolve run works does not cancel that run. Check: the run ends `success`, not `cancelled`. The cloud agent cannot push .github/workflows. Write the full diff of the workflow file in the pull request description under the heading 'Workflow patch (apply locally)', and leave the PR as a draft. Confirmed by: A person's step.
- [ ] (fix-auto-regen-push-and-resolve-starvation 2.3) 2.3 (`.github/workflows/auto-regen-push.yml`, `cmd_match_run` in `scripts/auto-regen.sh`) A new head of the pull request during a resolve run: the push workflow refuses the older result (the head moved) and pushes nothing. Check: the log of the push run says that the head does not match. The cloud agent cannot push .github/workflows. Write the full diff of the workflow file in the pull request description under the heading 'Workflow patch (apply locally)', and leave the PR as a draft. Confirmed by: A person's step.
- [ ] (fix-auto-regen-push-and-resolve-starvation 3.1) 3.1 (`.github/workflows/auto-regen-push.yml`) A pull request with a stale generated file gets its regenerated files pushed by `auto-regen-push`. Check: the run list of `.github/workflows/auto-regen-push.yml` shows a `success` run with the push. Confirmed by: the driver's gates. The cloud agent cannot push .github/workflows. Write the full diff of the workflow file in the pull request description under the heading 'Workflow patch (apply locally)', and leave the PR as a draft. Confirmed by: A person's step.

## From fix-destroy-approval-fresh-auth (1)

- [ ] (fix-destroy-approval-fresh-auth 4.3) 4.3 Run the usability check from the docs page and `make check`. Check: `make check` with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From fresh-auth-enabled-setting (1)

- [ ] (fresh-auth-enabled-setting 3.5) 3.5 Usability check from the docs page alone. Check: Site administrator turns the setting on and off from the docs page. A person's step.

## From fresh-authentication-window-0-turns (1)

- [ ] (fresh-authentication-window-0-turns 3.5) 3.5 Usability check from the docs page alone Check: Site administrator completes the path. A person's step.

## From give-dusty-tools-comment-propose (1)

- [ ] (give-dusty-tools-comment-propose 4.2) 4.2 Usability check from the docs page alone. Check: Each error names its one command. A person's step.

## From group-guard-count-forbid-exemptions (1)

- [ ] (group-guard-count-forbid-exemptions 4.2) 4.2 Write the e2e step in NEW file e2e/group_guard_forbid_test.go. It uses POST /api/server/groups/{group}/members with an access token of a person principal that has group rights only, over TLS, then a real Pulumi CLI read and a real OpenTofu read. Check: `make e2e` and `make check` with zero findings. A person's step. Confirmed by: CI (the agent writes the e2e test). Confirmed by: CI (the agent runs the fast checks only).

## From h1-hash-provider-archive-registry (2)

- [ ] (h1-hash-provider-archive-registry 4.2) 4.2 Run usability check of the full publish and install path. Check: Namespace administrator completes publish and tofu init from the docs page alone; each error names its one command. A person's step.
- [ ] (h1-hash-provider-archive-registry 4.3) 4.3 Pass definition of done for the publish route: wiring, route, e2e step, docs page, usability check, make check. Check: `make check` reports zero findings; wiring uses POST /api/dustpan/v1/namespaces/{}/registry/uploads with unit:manage on the namespace and an access token credential. A person's step. Confirmed by: CI (the agent writes the e2e test). Confirmed by: CI (the agent runs the fast checks only).

## From housekeeping-principals (1)

- [ ] (housekeeping-principals 3.3) 3.3 Usability check: the operator finds who ran each sweep from the docs page alone. Check: the check is recorded in the pull request. A person's step.

## From housekeeping-principals-install (1)

- [ ] (housekeeping-principals-install 4.2) 4.2 Usability check: an operator finds the built-in principals and their grants from the page `docs/guides/principals-and-groups.md` alone. Check: the check is recorded in the pull request. A person's step.

## From housekeeping-principals-sweeps (1)

- [ ] (housekeeping-principals-sweeps 3.1) 3.1 Usability check: an operator finds who ran each of these sweeps in the audit log from the entry alone. Check: the check is recorded in the pull request. A person's step.

## From install-guide-container-images (1)

- [ ] (install-guide-container-images 3.1) 3.1 Usability check: an operator follows `docs/guides/install.md` alone, pulls the image by tag and by digest, makes the two Secrets and installs the chart on a cluster. Check: the steps in the pull request. A person's step.

## From ip-allowlists (1)

- [ ] (ip-allowlists 5.2) 5.2 Do the usability check from the docs page alone. Check: the site administrator completes the path of the proposal, from the settings call to the denied sign-in. A person's step.

## From ip-allowlists-token-lists (1)

- [ ] (ip-allowlists-token-lists 4.3) 4.3 Do the usability check from the docs page alone. Check: the site administrator sets a token list and sees the denial from an outside address. A person's step.

## From job-cancel-ends-durable-record (1)

- [ ] (job-cancel-ends-durable-record 3.2) 3.2 Add the docs page in NEW file `docs/guides/job-cancel.md` and run the usability check from the page alone. Check: make docs-lint and make check with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From job-proxy-credential-records-audit (1)

- [ ] (job-proxy-credential-records-audit 4.4) 4.4 Run the usability check. Check: Persona completes the path from the docs page alone. A person's step.

## From job-proxy-rule-sets (1)

- [ ] (job-proxy-rule-sets 4.3) 4.3 Run the usability check Check: a namespace administrator completes the path from docs/guides/job-proxy-rules.md alone. A person's step.

## From job-proxy-runtime-guard (1)

- [ ] (job-proxy-runtime-guard 4.3) 4.3 Run usability check with runner operator path from docs page alone, then full definition of done. Check: `make check` passes with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From job-service-unit-settings-pool (1)

- [ ] (job-service-unit-settings-pool 4.2) 4.2 Write the docs page docs/guides/unit-settings-pool.md (NEW) and the usability check from the page alone. Check: make docs-lint. A person's step.

## From job-start-docs-regen (1)

- [ ] (job-start-docs-regen 3.4) 3.4 Check the usability of the page. Check: The developer completes the job start path from the docs page alone. A person's step.

## From job-start-idempotency-retention-24h (1)

- [ ] (job-start-idempotency-retention-24h 4.4) 4.4 Check that the agent completes the retry path from the docs page. Check: manual usability check of the docs page. A person's step.

## From job-start-poison-skip-alert (1)

- [ ] (job-start-poison-skip-alert 2.2) 2.2 Check that each error names one command. Check: usability check from the docs page alone. A person's step.

## From job-token-rules-state-write (1)

- [ ] (job-token-rules-state-write 4.3) 4.3 Wiring, route actions, docs page and usability check done. Check: make check with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From keep-two-working-break-glass (1)

- [ ] (keep-two-working-break-glass 4.3) 4.3 Check the wiring, the route actions, the audit entries and the usability walk-through. Check: `make check` passes with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From kill-during-backup (1)

- [ ] (kill-during-backup 3.2) 3.2 Run the usability check from the docs page alone. Check: The Operator completes the path from the docs page and each error names one command. A person's step.

## From ldap-sign-in (1)

- [ ] (ldap-sign-in 3.4) 3.4 In docs/guides/ldap-sign-in.md, usability check: a person follows the guide alone against the test directory and signs in. Each error they cause names the setting or the one command that helps. Check: the steps and the errors are in the pull request description. A person's step.

## From let-group-trust-identity-provider (1)

- [ ] (let-group-trust-identity-provider 3.2) 3.2 BLOCKED by add-one-generic-settings-api for the full sync part. For trust: add the e2e step against the real server on the existing route PUT and DELETE /api/server/groups/{id}/trust, with the access token of a site administrator principal. Run the usability check and the full gate. Check: `make e2e` and `make check` with zero findings. A person's step. Confirmed by: CI (the agent writes the e2e test). Confirmed by: CI (the agent runs the fast checks only).

## From list-covers-every-object-ignores (1)

- [ ] (list-covers-every-object-ignores 3.1) 3.1 Wire the gate, run the e2e step with an access token against GET /api/dustpan/v1/namespaces/{namespace}/collections, update the docs page, run the usability check. Check: `make check` with zero findings. A person's step. Confirmed by: CI (the agent writes the e2e test). Confirmed by: CI (the agent runs the fast checks only).

## From local-run-all (1)

- [ ] (local-run-all 4.1) 4.1 Run the usability check for the local path. Check: A developer completes the run from the docs page alone; each error names one command. A person's step.

## From local-run-two-cases (1)

- [ ] (local-run-two-cases 4.3) 4.3 Usability check from the docs page alone Check: A Developer completes both cases from the docs page alone, and each error names the one command that helps. A person's step.

## From local-run-without-wrapper (1)

- [ ] (local-run-without-wrapper 3.4) 3.4 Run usability check of user path. Check: Developer completes plain apply and reads holder from docs page alone; each error names one command. A person's step.

## From machine-approvals (4)

- [ ] (machine-approvals 0.2) 0.2 Check the start conditions: the ADR that follows RFC 134 is on main, and `several-approval-requirements-one-change` task 5.4 is on main. Do not start before both. Check: the pull request body names the ADR number and the commit of task 5.4. A person's step.
- [ ] (machine-approvals 4.3) 4.3 Usability check from the docs page alone. Check: an administrator names a bot and the bot approves a job. A person's step.
- [ ] (machine-approvals 4.4) 4.4 The Opus security review (rev2) is CLEAN on the exact head of each code pull request. Check: the verdict names the head sha. A person's step.
- [ ] (machine-approvals 5.1) 5.1 After each answer to an open question: the PM adds the repository record (the ADR, or an amendment), the week log entry and the answered section of the queue, and moves the question to "answered" in the proposal. Then the proposal reads "Open questions: none" and the tasks that waited start. Check: `scripts/check-ai-pr-spec.sh` accepts the proposal. A person's step.

## From manage-ldap-sign-in-providers (1)

- [ ] (manage-ldap-sign-in-providers 3.3) 3.3 Write the guide page in the NEW file docs/guides/ldap-sign-in-providers.md and run the usability check from the page alone. Check: `make docs-lint` and `make check` report zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From mfa (1)

- [ ] (mfa 3.2) 3.2 Add docs page in NEW file docs/guides/mfa.md and run usability check. Check: Docs commands run as written. A person's step. `make docs-lint` passes on each edited guide.

## From mirror-approvals-between-review-request (1)

- [ ] (mirror-approvals-between-review-request 4.3) 4.3 Run the usability check for the Developer path Check: Developer approves in the forge and in OpenDustpan from the docs page alone. A person's step.

## From move-caller-routes-api-server (1)

- [ ] (move-caller-routes-api-server 4.2) 4.2 Run the full check, and the usability check from the docs page alone. Check: `make check` with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From mtls-mtls-exchange-token (1)

- [ ] (mtls-mtls-exchange-token 3.5) 3.5 Usability check from the docs draft. Check: The persona completes the path and each error names one command. A person's step.

## From mtls-mtls-proof-docs (2)

- [ ] (mtls-mtls-proof-docs 3.3) 3.3 Run a usability check and record where the path costs time. Check: The persona reaches both CLI runs from the docs page alone. A person's step.
- [ ] (mtls-mtls-proof-docs 3.4) 3.4 Pass the definition of done for POST /api/dustpan/v1/namespaces/{namespace}/trust-rules/exchange with the exchanged token (kind `mtls-exchange`, credential `trust-token`): wiring, route actions unchanged, e2e step, docs page, usability check. BLOCKED by mtls-mtls-exchange-token until it merges. Check: make check reports zero findings and make e2e passes. A person's step. Confirmed by: CI (the agent writes the e2e test). Confirmed by: CI (the agent runs the fast checks only).

## From mtls-trust-bundle-rules (1)

- [ ] (mtls-trust-bundle-rules 4.2) 4.2 Usability check from the docs page alone Check: each error names one command. A person's step.

## From nightly-load-soak-fault-job (3)

- [ ] (nightly-load-soak-fault-job 2.4) 2.4 Write the workflow `.github/workflows/nightly-perf.yml` as in the design (decision 1 and the hostile-input table). The cloud session cannot push `.github/workflows`: put its full diff in the pull request body under "Workflow patch (apply locally)" and list it in the workflow patch record of the project. The YAML in the body of the draft pull request of the prior work is the starting point. Check: the pull request body holds the patch. Confirmed by: A person's step (after the workflow patch is applied).
- [ ] (nightly-load-soak-fault-job 2.5) 2.5 After the patch is applied: one trial run of the NEW file `.github/workflows/nightly-perf.yml` with `gh workflow run nightly-perf.yml` and a NEW budget file `perf/budgets.json` with one deliberately low budget. Check: the run ends failed, with one issue titled exactly `[nightly-perf] <name>`, the artifact `measurements.json`, and a comment (not a second issue) after a second run. Remove the low budget in the same pull request. Confirmed by: A person's step (after the workflow patch is applied).
- [ ] (nightly-load-soak-fault-job 4.2) 4.2 Usability check of the failed-run path. Check: an Operator reruns from the docs page alone, and each breach in the job summary names `gh run view <run-id>`. A person's step (after the workflow patch is applied).

## From one-membership-function-identity-provider (1)

- [ ] (one-membership-function-identity-provider 4.3) 4.3 Run the full checks and the usability check from the docs page alone. Check: `make check` reports zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From output-reads-during-journaled-pulumi (1)

- [ ] (output-reads-during-journaled-pulumi 3.3) 3.3 Run definition of done: e2e read during a real journaled update on route GET /api/dustpan/v1/namespaces/{namespace}/units/{unit}/outputs with a personal token, docs page, usability check from the docs page alone. Check: Real Pulumi CLI journaled update, then GET /api/dustpan/v1/namespaces/{namespace}/units/{unit}/outputs with a personal token returns the pinned checkpoint with updateRunning true; `make check` passes with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From output-sharing (1)

- [ ] (output-sharing 3.6) 3.6 Run usability check and full check. Check: Developer follows docs/guides/output-sharing.md alone and each error names its one command; `make check` passes with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From person-confirms (1)

- [ ] (person-confirms 4.3) 4.3 Run the usability check of the confirm path. Check: the persona completes the confirm from the docs page alone and each error names the one command that helps. A person's step.

## From person-delegates-dusty (1)

- [ ] (person-delegates-dusty 4.2) 4.2 Run the usability check of the delegation path from the docs page alone. Check: a person completes create, list, read, renew and revoke, and each error names the one command that helps. A person's step.

## From planner-role-2 (1)

- [ ] (planner-role-2 4.3) 4.3 Complete usability check. Check: Persona completes plan from docs page alone. A person's step.

## From policy-approvals (1)

- [ ] (policy-approvals 3.4) 3.4 Run usability check Check: Namespace administrator completes path from docs page alone; each error names one command. A person's step.

## From pre-merge-apply-option (1)

- [ ] (pre-merge-apply-option 3.4) 3.4 Run usability check and full checks. Check: `make check` with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From publish-container-images-forge-registry (1)

- [ ] (publish-container-images-forge-registry 4.1) 4.1 Tag a release candidate in CI, pull both references by digest, run `helm install` from the default registry, and check that an Operator completes the install page alone and each error names one command. Check: The CI run passes, the digests match, `make docs-lint` is clean and `make check` reports zero findings. Confirmed by: A person's step (after the workflow patch is applied).

## From pull-request-automation-first-slice (1)

- [ ] (pull-request-automation-first-slice 4.1) 4.1 Run the wiring, authorization, e2e, docs and usability checks. Route: GET /api/dustpan/v1/namespaces/{namespace}/units with an access token of a principal that can read units; no new route is added. BLOCKED by the three depends_on changes for the e2e part. Check: `make check` with zero findings; e2e step plus docs usability check pass. A person's step. Confirmed by: CI (the agent writes the e2e test). Confirmed by: CI (the agent runs the fast checks only).

## From pulumi-journaling-in-release-build (1)

- [ ] (pulumi-journaling-in-release-build 5.3) 5.3 Usability check: an operator follows `docs/reference/journaling.md` alone, turns journaling off with `DUSTPAN_DISABLE_PULUMI_JOURNALING=true`, restarts, and a `pulumi up` saves checkpoints. Check: `TestPulumiJournalingBothModes` covers the off run with that variable; the pull request body records the walk of the page. A person's step.

## From read-route-decision-records-change (1)

- [ ] (read-route-decision-records-change 3.4) 3.4 E2e step and usability check. Check: BLOCKED by the e2e fixture: a gate that refuses a write with the real OpenTofu CLI and a personal access token of a principal with the gate read action; the step reads the NEW route. A person's step. Confirmed by: CI (the agent writes the e2e test).

## From record-git-edits-catalog-request (1)

- [ ] (record-git-edits-catalog-request 4.2) 4.2 Run the usability check for the drift path. Check: Developer completes the path from the docs page alone. A person's step.

## From record-names-cause (1)

- [ ] (record-names-cause 4.3) 4.3 Run wiring, authorization with audit-read grant, usability from the docs page alone, and make check with zero findings. Check: `make check` with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From record-openpgp-dependency-registry-in (1)

- [ ] (record-openpgp-dependency-registry-in 2.4) 2.4 Move golang.org/x/crypto in go.mod from the pseudo-version to the tag v0.59.0 on or after 25 November 2026, with the owner's word (ADR 379 decision 3) and a clean make vuln. Check: `go list -m golang.org/x/crypto` shows v0.59.0. A person's step.

## From registry-effective-limits (1)

- [ ] (registry-effective-limits 4.3) 4.3 Run the usability check of the read of the limits from the docs page alone. Check: A namespace administrator reads the limits from the docs page alone; the error of a missing grant names its one command. A person's step.

## From registry-namespace-slug (1)

- [ ] (registry-namespace-slug 4.3) 4.3 Run usability check of the install path under the slug. Check: Namespace administrator completes publish and tofu init from the docs page alone; each error names its one command. A person's step.

## From release-artifacts-binary-archives (1)

- [ ] (release-artifacts-binary-archives 4.1) 4.1 Run definition of done for this part: wiring of both NEW Make targets and usability check from test output; route, e2e install step, and docs page are BLOCKED by part 2 container image and install docs work Check: BLOCKED by part 2 container image, install test route and docs page; `make check` with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From release-tag-archives (2)

- [ ] (release-tag-archives 1.5) 1.5 SECURITY. In pull request B, add the NEW file `internal/releaserecord/release_workflow_archives_test.go` (package `releaserecord_test`). It reads `.github/workflows/release.yml` through `os.Root`, parses it with `go.yaml.in/yaml/v3` and runs one checker, `releaseWorkflowFindings(raw []byte) []string`, that returns one finding for each missing item of the layers "Workflow guard", "Job order", "Permissions", "Pins", "Version as data" and "Authorization at the point of use" of `design.md`. Tests, NEW symbols: `TestReleaseWorkflowArchiveJobs` (the real file has no finding; also checks that `archives-build` and `archives-verify` read no `secrets.`, that the three archive jobs and `release-draft` use the same guard text as `publish`, and that the actor step comes before the download step in `release-draft`); `TestReleaseWorkflowCheckerRefusesWeakWorkflows` (a table of mutations of the real file: a job with no guard, no repository clause, a `pull_request` trigger, `publish` with no `needs`, `release-draft` with no `needs: publish`, `archives-verify` with no `needs: archives-build`, a job with no `permissions`, `contents: write` for the file, `contents: write` in `archives-build`, `packages: write` in `release-draft`, a tag pin `@v7`, a short SHA, a pin with no release comment, a `run:` line with `${{ github.ref_name }}`, a checkout with no `persist-credentials: false` in an archive job, the actor step removed; each mutation yields at least one finding, and the finding names the job and the item). No route: the test reads the workflow file only. Check: NEW tests: `go test ./internal/releaserecord -run TestReleaseWorkflowArchiveJobs -count=1`; `go test ./internal/releaserecord -run TestReleaseWorkflowCheckerRefusesWeakWorkflows -count=1` fail on main because `.github/workflows/release.yml:36` (job `publish`) is the only job and has no `needs`, and passes after the patch of task 2.5 is applied. The existing `go test -tags e2e -run TestReleasePushesOneDigestToBothRegistries ./e2e` passes before and after. Confirmed by: a person's step or the Droid computer (the cloud agent cannot push `.github/workflows`; pull request B stays a draft until the patch is in).
- [ ] (release-tag-archives 4.3) 4.3 Run the usability check of the maintainer section: a maintainer who has only the page names the steps that follow a tag push, finds the draft, reads its five files, publishes it, and knows what to do after a failed draft job. Check: the pull request body of A records the walk-through with the commands that it used, run against the fake `gh` of the test (the real draft needs a tag, which no one makes). A person's step.

## From report-plans-on-review-request (1)

- [ ] (report-plans-on-review-request 2.4) 2.4 Write the docs page docs/guides/review-request-plan-report.md (NEW) and run the usability check from the page alone. Check: make docs-lint passes. A person's step.

## From retention-volume-state-write-decision (1)

- [ ] (retention-volume-state-write-decision 4.2) 4.2 Usability check from the docs page alone. BLOCKED by the follow-up retention-sweep-schedule-lease: no route reaches pruneExpired in this change, so no e2e step with the real tofu CLI can show a prune. The held-lock retry keeps its existing route test. Check: `make docs-lint` passes and the page names the follow-up. A person's step. Confirmed by: CI (the agent writes the e2e test).

## From review-holds (1)

- [ ] (review-holds 4.2) 4.2 Complete usability check of the review path. Check: Developer completes the review path from the docs page alone and each error names the one command that helps. A person's step.

## From revoke-stops-work (1)

- [ ] (revoke-stops-work 4.3) 4.3 Run the usability check of the revoke path from the docs page alone. Check: the person sees what stopped and what finished. A person's step.

## From rolling-deploy-graceful-drain-release (1)

- [ ] (rolling-deploy-graceful-drain-release 4.2) 4.2 Run wiring, route with authorization actions, docs page and usability check. Check: `make check` with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From rolling-deploy-test (1)

- [ ] (rolling-deploy-test 4.3) 4.3 Run usability check of rolling replace path. Check: Operator completes replace from existing high availability guide alone with clear version error. A person's step.

## From run-all-in-ci-team (1)

- [ ] (run-all-in-ci-team 4.3) 4.3 Run the usability check from the docs page alone. Check: The person completes the path from the docs page alone and each error names one command. A person's step.

## From run-all-on-runner-dependency-gated-apply (1)

- [ ] (run-all-on-runner-dependency-gated-apply 4.3) 4.3 Do a usability check of the ordered apply path from the guide alone. Check: A Developer follows the guide alone and each error names the one command that helps. A person's step.

## From run-all-on-runner-discovery (1)

- [ ] (run-all-on-runner-discovery 3.3) 3.3 Run the usability check of the discovery path. Check: Runner operator completes discovery from the docs page alone and each error names the one command that helps. A person's step.

## From run-all-on-runner-fanout-plan-apply-jobs (1)

- [ ] (run-all-on-runner-fanout-plan-apply-jobs 4.3) 4.3 Run the usability check with the Runner operator from the docs page alone and record where the path costs time. Check: usability walkthrough with the Runner operator from the docs page alone. A person's step.

## From run-new-schema-against-last (1)

- [ ] (run-new-schema-against-last 4.3) 4.3 Run usability check from the docs page alone Check: an Operator completes the matrix from docs/guides/backup-restore-upgrade.md alone and each error names the one helping command. A person's step.

## From run-triggers (1)

- [ ] (run-triggers 4.3) 4.3 Confirm the docs page usability check. Check: docs page command runs as written. A person's step.

## From runner-job-overlapping-claim-refused (1)

- [ ] (runner-job-overlapping-claim-refused 4.4) 4.4 Run the usability check of the refusal path. Check: A developer finishes the path from the docs page alone, and each error names one command. A person's step.

## From runner-only-feature-in-local (1)

- [ ] (runner-only-feature-in-local 2.4) 2.4 Run the usability check from the docs page. Check: A developer completes the path from the docs page alone and each error names one command. A person's step.

## From runner-operator-guide (1)

- [ ] (runner-operator-guide 4.1) 4.1 Run a usability check of the full path from the docs page. Check: A runner operator completes the full path from the docs page alone with an access token for pool work and a runner token for ANY /runner/v1 through the existing gates. A person's step.

## From runner-poll-skip-metric (1)

- [ ] (runner-poll-skip-metric 2.3) 2.3 Usability check: an operator finds the counter from the docs page alone. Check: Manual walk of the docs page. A person's step.

## From runners (1)

- [ ] (runners 3.2) 3.2 Usability check: a runner operator completes the join from docs/guides/runner-join.md alone, and each error names its one command. Check: Manual walk-through recorded in the pull request body. A person's step.

## From schedules (1)

- [ ] (schedules 4.3) 4.3 Docs page and usability check with measured figures, date, and no SLA. Check: docs/guides/high-availability.md states the measured tick figures with date and no SLA wording. A person's step.

## From scim (1)

- [ ] (scim 4.3) 4.3 Do the usability check from the docs page alone. Check: Each error names one command. A person's step.

## From sdk-guide-for-embedders (1)

- [ ] (sdk-guide-for-embedders 4.3) 4.3 Run usability check of the guide Check: Embedder completes the path from the docs page alone. A person's step.

## From session-fresh-cookie (1)

- [ ] (session-fresh-cookie 4.3) 4.3 Run the usability check from the docs page and `make check`. Check: `make check` with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From session-key-ring (1)

- [ ] (session-key-ring 4.3) 4.3 Run the usability check from the docs page and `make check`. Check: `make check` with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From several-approval-requirements-one-change (4)

- [ ] (several-approval-requirements-one-change 4.3) 4.3 Mark the change `approval-defaults-take-effect-live` stale in its own pull request (its premise is false on main). Check: that pull request exists. A person's step.
- [ ] (several-approval-requirements-one-change 4.4) 4.4 Usability check from the docs page alone. Check: a developer sets two defaults and runs a job with 3 approvers. A person's step.
- [ ] (several-approval-requirements-one-change 4.5) 4.5 The Opus security review (rev2) is CLEAN on the exact head of each code pull request. Check: the verdict names the head sha. A person's step.
- [ ] (several-approval-requirements-one-change 5.7) 5.7 After the answers: the PM adds the ADR or the amendment, the week log entry and the answered section of the queue. The records are: the five owner decisions of 2026-11-20 (an ADR tagged "(owner, 2026-11-20)", with the amendment of the words of ADR 225 decision 8 for jobs: any holder of `job:approve` may reject). The record of owner decision 7 belongs to the change `machine-approvals`. Then update `docs/guides/approval-rules.md`. Check: `make docs-lint` passes on each edited guide. A person's step.

## From sign-in-methods (1)

- [ ] (sign-in-methods 3.1) 3.1 Route actions, docs pages, usability walk. Check: `make check` has zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From signed-file-urls-registry-need (1)

- [ ] (signed-file-urls-registry-need 3.4) 3.4 Run the usability check and make check. Check: `make check` reports zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From signed-request-recovery (1)

- [ ] (signed-request-recovery 4.3) 4.3 The usability check from the docs page alone. Check: the operator recovers, and each error names one command. A person's step.

## From start-plan-on-push-review (1)

- [ ] (start-plan-on-push-review 3.4) 3.4 Write the NEW docs page docs/guides/push-plans.md about push plans and the fork rule, run the usability check and run make check (the existing Make target in Makefile:14). Check: make check reports zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only). `make docs-lint` passes on each edited guide.

## From state-write-gates-context-principal (1)

- [ ] (state-write-gates-context-principal 4.2) 4.2 Complete the usability check for the namespace administrator path. Check: Usability check: a namespace administrator writes the agent gate from the docs page alone and predicts the outcome for each kind. A person's step.

## From state-write-state-facts-fixed (1)

- [ ] (state-write-state-facts-fixed 3.1) 3.1 Check wiring, route actions and docs page. Run the usability check from the docs page alone with the real tofu CLI and a personal access token. Check: `make check` with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From store-move (1)

- [ ] (store-move 0.1) 0.1 For each open PR that touches `pkg/store/`: merge, close as stale, or mark "port with branch mode". Check: a table of the 23 PRs and their outcome in STATE.md. Confirmed by: A person's step (the lead triages the open store PRs).

## From strict-case-takes-turns (1)

- [ ] (strict-case-takes-turns 3.3) 3.3 Run the usability check from the docs page. Check: each error names `dustpan job show <job-id>`. A person's step.

## From sweep-staged-files-unreferenced-files (1)

- [ ] (sweep-staged-files-unreferenced-files 4.3) 4.3 Write the NEW docs page `docs/guides/registry-sweep.md` and do the usability check from the page alone. The page states the 24-hour age, the reference rule, the 1-hour mark expiry and the pause in maintenance mode. Check: The docs commands run in a test and `make check` reports zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only). `make docs-lint` passes on each edited guide.

## From token-life-cycle-expiry-email-expiry-warning-display (1)

- [ ] (token-life-cycle-expiry-email-expiry-warning-display 4.3) 4.3 Run the usability check. Check: a Developer finds the banner and the token that ends from the page alone. A person's step.

## From token-life-cycle-expiry-email-token-expiry-enforcement (1)

- [ ] (token-life-cycle-expiry-email-token-expiry-enforcement 4.4) 4.4 Run the usability check. Check: a Developer finds the ended token and the audit reason from the guide alone. A person's step.

## From token-life-cycle-expiry-email-warning-email-queue (1)

- [ ] (token-life-cycle-expiry-email-warning-email-queue 4.3) 4.3 Run the usability check. Check: a Developer finds the two emails in the guide and the reason of a skipped delivery. A person's step.

## From track-medium-low-items-several-compare-swap-clock-guards (2)

- [ ] (track-medium-low-items-several-compare-swap-clock-guards 4.4) 4.4 Run usability check of the operator path. Check: Operator reads the path alone and each error names the one command that helps. A person's step.
- [ ] (track-medium-low-items-several-compare-swap-clock-guards 4.5) 4.5 Run definition of done: wiring, routes ANY /api/audit/entries and ANY /api/server/session with authorization actions, e2e step, docs page, usability check, `make check`. Check: `make check` reports zero findings with PostgreSQL cases run against `DUSTPAN_TEST_POSTGRES_URL`. A person's step. Confirmed by: CI (the agent writes the e2e test). Confirmed by: CI (the agent runs the fast checks only).

## From track-medium-low-items-several-durable-fenced-restore (1)

- [ ] (track-medium-low-items-several-durable-fenced-restore 4.4) 4.4 Run usability check and `make check` with zero findings. Check: `make check` reports zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From track-medium-low-items-several-sink-lag-readiness-gate (1)

- [ ] (track-medium-low-items-several-sink-lag-readiness-gate 4.2) 4.2 Run PostgreSQL restore path on ANY /api/dustpan/v1/restores/{} with access token, hostile input tests for constructor and schema guard, usability check, and full check Check: `DUSTPAN_TEST_POSTGRES_URL=$DB go test -timeout 60m ./pkg/store/postgres/ -run Restore -count=1` plus `make check` with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From two-disjoint-claims (1)

- [ ] (two-disjoint-claims 4.3) 4.3 Run usability check. Check: Developer completes the path from the docs page alone. A person's step.

## From upgrade-pull-requests (1)

- [ ] (upgrade-pull-requests 4.1) 4.1 Wire the scanner into the server start. Add the two new upgrade-requests routes, both with action unit:read. The e2e step uses the existing route `PUT /api/dustpan/v1/namespaces/{}/registry/providers/{}/versions/{}` to publish a newer release, with a personal access token that holds unit:manage and unit:read, then reads the request with `GET /api/dustpan/v1/namespaces/{namespace}/upgrade-requests/{id}` against the fake forge. Finish the docs page and the usability check. Check: Route test shows the unit:read action on both routes; e2e step with a personal access token passes against the fake forge; docs complete; `make check` with zero findings. A person's step. Confirmed by: CI (the agent writes the e2e test). Confirmed by: CI (the agent runs the fast checks only).

## From upgrade-test-with-real-data (2)

- [ ] (upgrade-test-with-real-data 2.3) 2.3 Add the CI job in the NEW file `.github/workflows/upgrade-test.yml`, which writes the NEW path `.github/workflows/upgrade-test.yml`. Check: CI job runs `make upgrade-test` once per supported old release. The cloud agent cannot push .github/workflows. Write the full diff of the workflow file in the pull request description under the heading 'Workflow patch (apply locally)', and leave the PR as a draft. Confirmed by: A person's step.
- [ ] (upgrade-test-with-real-data 4.2) 4.2 Write the operator upgrade docs page with backup, stop, start and check steps in the NEW file `docs/guides/upgrade.md` and run the usability check Check: Docs commands run as written in CI; `make check` with zero findings; usability check of the upgrade page. A person's step. Confirmed by: CI (the agent runs the fast checks only). `make docs-lint` passes on each edited guide.

## From wake-long-poll-when-operation (1)

- [ ] (wake-long-poll-when-operation 4.2) 4.2 Publish the docs page update and complete the usability check. Check: Docs page states per-instance count, limits, Retry-After, and the 5 s fallback; usability run from the docs page passes. A person's step.

## From write-back (1)

- [ ] (write-back 3.5) 3.5 Run the usability check from the docs page alone and the full check. Check: `make check` with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From write-job-states-running-queued (1)

- [ ] (write-job-states-running-queued 4.3) 4.3 Run usability check from the docs page alone and make check with zero findings. Check: make check passes with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

## From write-state-version-unit-state-follows-trigger-start (1)

- [ ] (write-state-version-unit-state-follows-trigger-start 4.4) 4.4 Run the usability check Check: persona completes the path from the docs page alone; each error names one command. A person's step.

## From write-state-version-unit-state-outputs-changed-filter (1)

- [ ] (write-state-version-unit-state-outputs-changed-filter 4.3) 4.3 Run the usability check of the path. Check: Developer follows the docs page alone and names the one command that explains a skipped start. A person's step.

## From write-state-version-unit-state-trigger-caps-depth (1)

- [ ] (write-state-version-unit-state-trigger-caps-depth 3.5) 3.5 Run the full definition of done with a usability check from the docs page alone. Check: make check reports zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).
