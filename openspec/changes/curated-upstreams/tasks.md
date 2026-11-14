## 1. Failing tests first

- [ ] 1.1 Add failing service test for refusal of an unmatched public release with close host, wide prefix, and out of range version cases. Check: `go test ./pkg/registry/ -run TestCuratedUpstreamRefusesUnlisted -count=1` fails on main.
- [ ] 1.2 Add failing service test for serving of a matched public release for tofu init provider, tofu init module, and Pulumi CLI plugin paths. Check: `go test ./pkg/registry/ -run TestCuratedUpstreamServesAllowed -count=1` fails on main.
- [ ] 1.3 Add failing fetch test for cache miss fetch from matched upstream host and store with checksum for provider, module, and plugin addresses. Check: `go test ./pkg/registry/ -run TestCuratedUpstreamFetchCache -count=1` fails on main.
- [ ] 1.4 Add failing handler test for encoded traversal and absolute URL addresses. Check: `go test ./internal/api/registry/ -run TestCurationHostileAddress -count=1` fails on main.
- [ ] 1.5 Add failing fetch test for stop on redirect to an unlisted host. Check: `go test ./pkg/registry/ -run TestCuratedUpstreamRedirect -count=1` fails on main.

## 2. Build

- [ ] 2.1 Build curator type, all-field match with OR combination and path boundary, per-request snapshot, off switch with rule retention, upstream fetch with cache, missing upstream 404 after curation check, and audit before serve with fail-closed log. Check: `go test ./pkg/registry/ -count=1` passes.
- [ ] 2.2 Wire setting and rule routes under /namespaces/{namespace}/curation with namespace curation manage and read actions, rule checks including IP literal rejection, and refusal text that names the registry address. Check: `go test ./internal/api/registry/ -run TestCurationRefusalNamesAddress -count=1` passes.

## 3. Definition of done

- [ ] 3.1 Add e2e step with real tofu init and real Pulumi CLI against the real binary over TLS. Check: `make e2e` runs tofu init and Pulumi CLI plugin fetch through the registry over TLS and shows the refusal naming the address.
- [ ] 3.2 Write docs page for curation rules in the same pull request. Check: Docs page commands run as written in a clean installation.
- [ ] 3.3 Run usability check of the rule list and refusal path. Check: Namespace administrator completes the path from the docs page alone.
- [ ] 3.4 Run full check suite. Check: `make check` reports zero findings.
