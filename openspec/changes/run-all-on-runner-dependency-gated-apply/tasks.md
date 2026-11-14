## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing server test for ordered apply starts on a 3-workspace chain; BLOCKED by the NEW change-order apply-start route. Check: The NEW test `TestRunAllApplyWaitsForDependency` fails on main; BLOCKED by the NEW change-order apply-start route.
- [ ] 1.2 Add failing server test that a failed dependency blocks dependent apply jobs; BLOCKED by the NEW change-order apply-start route. Check: The NEW test `TestRunAllFailedDependencyBlocksDependents` fails on main; BLOCKED by the NEW change-order apply-start route.
- [ ] 1.3 Add failing server test for isolated idempotent retry of one apply job with Idempotency-Key; BLOCKED by the NEW change-order apply-start route. Check: The NEW test `TestRunAllApplyRetryIsIsolated` fails on main; BLOCKED by the NEW change-order apply-start route.
- [ ] 1.4 Add failing server test that an edge write with 101 edges is rejected and stores no edges; BLOCKED by the NEW change-order edge-write route. Check: The NEW test `TestRunAllEdgeLimitRejected` fails on main; BLOCKED by the NEW change-order edge-write route.

## 2. Build the gate

- [ ] 2.1 Build the dependency gate on stable workspace IDs with cycle reject and the 100-edge limit at discovery write. Check: The NEW test `TestRunAllApplyWaitsForDependency` passes with strict order; BLOCKED by the NEW change-order apply-start route.
- [ ] 2.2 Wire the gate to the NEW change-order apply-start route in depends_on with job read and job start authorization actions on the change order. Check: `curl GET /api/dustpan/v1/namespaces/{}/units` with an access token shows queued, blocked, and running states with the blocking dependency named.
- [ ] 2.3 Add e2e step with real Terragrunt and real OpenTofu over 3 workspaces on a runner over TLS on PostgreSQL through `ANY /runner/v1` with a runner token; BLOCKED by the NEW change-order apply-start route. Check: Real Terragrunt run over `ANY /runner/v1` with a runner token shows no workspace applied by the job of another workspace and start times follow A then B then C; BLOCKED by the NEW change-order apply-start route.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run the e2e step against the real binary and the real directory through `ANY /runner/v1` with a runner token; BLOCKED by the NEW change-order apply-start route. Check: The NEW test `TestTerragruntRunAllOnRunnerDependencyOrder` passes on Linux on PostgreSQL through `ANY /runner/v1` with a runner token; BLOCKED by the NEW change-order apply-start route.
- [ ] 4.2 Write the NEW file `docs/guides/terragrunt-run-all.md` with the runner fan-out flow. Check: The NEW file `docs/guides/terragrunt-run-all.md` lists the runner fan-out flow and each command runs as written.
- [ ] 4.3 Do a usability check of the ordered apply path from the guide alone. Check: A Developer follows the guide alone and each error names the one command that helps.
- [ ] 4.4 Run the full check suite. Check: `make check` passes with zero findings.
