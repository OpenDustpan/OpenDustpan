## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing publish test for plugin archive set, registry-safe name, and upload checks. Check: TestPluginPublish_ChecksSignatureAndName fails on main, passes after fix.
- [ ] 1.2 Add failing download test for valid name, version, OS, and arch on the new plugin route. Check: TestPluginDownload_StreamsStoredArchive fails on main, passes after fix.
- [ ] 1.3 Add failing test that a bad signature or bad checksum version returns 403 with no bytes. Check: TestPluginDownload_BadVerdictRefused fails on main, passes after fix.
- [ ] 1.4 Add failing tests for 401 on missing token and 404 on no read access or cross-namespace URL. Check: TestPluginDownload_AuthAndIsolation fails on main, passes after fix.
- [ ] 1.5 Add failing tests for stream SHA-256 mismatch, mid-stream blob failure, and audit entries on success and refusal. Check: TestPluginDownload_StreamIntegrityAndAudit fails on main, passes after fix.

## 2. Build

- [ ] 2.1 Wire publish record to the new plugin download route with registry read actions and audit entries. Check: GET on the new plugin download route under /api/dustpan/v1/namespaces/{}/registry with a personal access token streams bytes; bad version gives 403 that names the artifact.
- [ ] 2.2 Add e2e step against the real Pulumi CLI on SQLite and PostgreSQL, using the new plugin download route under /api/dustpan/v1/namespaces/{}/registry and a personal access token. Check: E2E `pulumi plugin install` against the new plugin download route over TLS with a personal access token.
- [ ] 2.3 Write docs page for publish and install of a private Pulumi plugin. Check: Docs page command sequence runs as written in a test.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run wiring check, route authorization check, docs page, usability walk, and full check set. The e2e step uses the new plugin download route under /api/dustpan/v1/namespaces/{}/registry with a personal access token. Check: Route test shows registry read actions on the new plugin download route (personal access token); the e2e step above passes; docs test passes; `make check` reports zero findings.
