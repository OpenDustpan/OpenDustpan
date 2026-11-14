## 1. Failing tests first

- [ ] 1.1 Add failing publish test for plugin archive set, registry-safe name, and upload checks. Check: TestPluginPublish_ChecksSignatureAndName fails on main, passes after fix.
- [ ] 1.2 Add failing download test for valid name, version, OS, and arch. Check: TestPluginDownload_StreamsStoredArchive fails on main, passes after fix.
- [ ] 1.3 Add failing test that a bad signature, bad checksum, or revoked version returns 403 with no bytes. Check: TestPluginDownload_BadVerdictRefused fails on main, passes after fix.
- [ ] 1.4 Add failing tests for 401 on missing token, 403 on no read access, and 404 on cross-namespace URL. Check: TestPluginDownload_AuthAndIsolation fails on main, passes after fix.
- [ ] 1.5 Add failing tests for stream SHA-256 mismatch and for audit entries on success and refusal. Check: TestPluginDownload_StreamIntegrityAndAudit fails on main, passes after fix.

## 2. Build

- [ ] 2.1 Wire publish record to plugin download route with registry read actions and audit entries. Check: GET plugin download URL streams bytes; bad or revoked version gives 403 that names the artifact.
- [ ] 2.2 Add e2e step against real Pulumi CLI on SQLite and PostgreSQL. Check: e2e step with real Pulumi CLI installs a private plugin from the registry over TLS.
- [ ] 2.3 Write docs page for publish and install of a private Pulumi plugin. Check: Docs page command sequence runs as written in a test.

## 3. Definition of done

- [ ] 3.1 Run wiring check, route authorization check, e2e step, docs page, usability walk, and full check set. Check: `make check` with zero findings.
