## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing service test for refusal of an unmatched public release with close host, wide prefix, and out of range version cases. Check: `go test ./pkg/registry/ -run TestCuratedUpstreamRefusesUnlisted -count=1` fails on main.
- [ ] 1.2 Add failing service test for serving of a matched public release for tofu init provider, tofu init module, and Pulumi CLI plugin paths. Check: `go test ./pkg/registry/ -run TestCuratedUpstreamServesAllowed -count=1` fails on main.
- [ ] 1.3 Add failing fetch test for single-flight cache miss fetch with checksum verify, size and time limits, and partial-store discard. Check: `go test ./pkg/registry/ -run TestCuratedUpstreamFetchCache -count=1` fails on main.
- [ ] 1.4 Add failing handler test for encoded traversal and absolute URL addresses. Check: `go test ./internal/api/registry/ -run TestCurationHostileAddress -count=1` fails on main.
- [ ] 1.5 Add failing fetch test for stop on redirect to an unlisted host and HTTPS-only fetch. Check: `go test ./pkg/registry/ -run TestCuratedUpstreamRedirect -count=1` fails on main.
- [ ] 1.6 Add failing test for audit entry before serve, fail-closed audit failure, and no curation entry while off. Check: `go test ./pkg/registry/ -run TestCurationAuditBeforeServe -count=1` fails on main.
- [ ] 1.7 Add failing test for curation-enabled default off with effective value, rule validation including IP literal, manage 403, request snapshot, and last-write-wins. Check: `go test ./pkg/registry/ -run TestCurationSettingAndRules -count=1` fails on main.

## 2. Build

- [ ] 2.1 Build curator type, all-field exact host match with OR combination and path boundary, per-request snapshot, off switch with rule retention, single-flight upstream fetch with verify and limits, namespace-keyed cache with re-check, missing upstream 404 after curation check, upstream 5xx 502, and audit before serve with fail-closed log. Check: `go test ./pkg/registry/ -count=1` passes.
- [ ] 2.2 Wire setting and rule routes under /namespaces/{namespace}/curation with namespace curation manage and read actions, and refusal text that names the registry address. Check: `go test ./internal/api/registry/ -run TestCurationRefusalNamesAddress -count=1` passes.
- [ ] 2.3 Wire the curation gate into the registry and tool-compatible fetch endpoints for tofu init and Pulumi CLI plugin fetch. Check: `go test ./internal/api/tfe/ ./internal/api/pulumi/ -run TestCurationGateWired -count=1` passes.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Add e2e step with real tofu init and real Pulumi CLI against the real binary over TLS with curation on, using the registry provider and module routes and a personal token, and show allowed serve and refused fetch naming the address. Check: `make e2e` shows tofu init and Pulumi CLI refusal naming the address through the real binary over TLS. Route: GET /api/dustpan/v1/namespaces/{namespace}/registry/providers/{}/versions/{} with a personal token; rules are set through POST /api/dustpan/v1/namespaces/{namespace}/curation/rules with the same personal token.
- [ ] 4.2 Write docs page for curation rules in the same pull request. Check: Docs page commands run as written in a clean installation.
- [ ] 4.3 Run usability check of the rule list and refusal path. Check: Namespace administrator completes the path from the docs page alone.
- [ ] 4.4 Run full check suite. Check: `make check` reports zero findings.
