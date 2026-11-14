## 1. Failing tests first

- [ ] 1.1 Add failing API tests for valid publish, bad SHA-256 at publish, missing signature, bad signature, and unknown version. Check: TestRegistryProvider_PublishValidListsInMirror fails on main.
- [ ] 1.2 Add failing tests for team protocol discovery, versions list, and download metadata. Check: TestRegistryProvider_TeamProtocolServesVersions fails on main.
- [ ] 1.3 Add failing tests for host match, safe-name rule, traversal names, and cross-namespace isolation. Check: TestRegistryProvider_SafeAddressRefusesForeignHost fails on main.

## 2. Build

- [ ] 2.1 Build publish, key register and removal, mirror index, team protocol, and download with atomic and immutable writes in the active state. Check: `go test -race ./pkg/registry/...` passes.
- [ ] 2.2 Wire routes with authorization actions and audit entries. Check: `make e2e` fetches a private provider with `tofu init` and refuses a bad signature and a bad checksum.

## 3. Definition of done

- [ ] 3.1 Run all checks and fix findings in code. Check: `make check` passes with zero findings.
- [ ] 3.2 Add the docs page and the usability check with the one helping command per error. Check: A Developer completes `tofu init` from the docs page alone against the real binary over TLS.
