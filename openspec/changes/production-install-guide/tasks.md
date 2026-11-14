## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add failing persona path test that uses the shared runner on docs run-marked fixture blocks. Check: NEW TestProductionInstallPersonaPath fails on main, passes after fixtures land, and runs through the shared runner from docs-commands-run-in-tests.
- [ ] 1.2 Add failing test for missing TLS file refusal. Check: NEW TestProductionInstallMissingTLSFiles fails on main.
- [ ] 1.3 Add failing test for missing proxy header refusal. Check: NEW TestProductionInstallProxyHeaders fails on main.
- [ ] 1.4 Add failing test for forged proxy headers from outside the NEW allowlist. Check: NEW TestProductionInstallProxySpoof fails on main.
- [ ] 1.5 Add failing test for unreachable PostgreSQL refusal. Check: NEW TestProductionInstallStoreUnreachable fails on main.
- [ ] 1.6 Add failing test for unreachable blob store refusal. Check: NEW TestProductionInstallBlobUnreachable fails on main.
- [ ] 1.7 Add failing test for open key file refusal. Check: NEW TestProductionInstallKeyPermissions fails on main.
- [ ] 1.8 Add failing test for secret redaction in errors and logs. Check: NEW TestProductionInstallSecretRedaction fails on main.
- [ ] 1.9 Add failing test for backup check failure. Check: NEW TestProductionInstallBackupCheck fails on main.
- [ ] 1.10 Add failing test for expired certificate handshake failure. Check: NEW TestProductionInstallExpiredCert fails on main.

## 2. Server guards

- [ ] 2.1 Confirm each guard on main and implement missing refusals for TLS files, key permissions, proxy trust, store, blob store, and backup check. Check: Guard tests pass; missing guards implemented with allowlist of NEW proxy addresses and fail closed behavior.

## 3. Definition of done

- [ ] 3.1 Wire persona path into e2e with install bits from release-artifacts-image-install-docs against real CLI and real directory. Check: Persona path calls ANY /api/server/backups with access token through stored grant gate, then real Pulumi CLI up and real OpenTofu apply through the NEW nginx container proxy pass.
- [ ] 3.2 Publish docs page in same pull request. Check: Docs page for production install merged in same pull request with NEW nginx example, key file backup note, links to install-from-artifacts page, and no-run marks for use by the shared runner.
- [ ] 3.3 Run full check with zero findings. Check: `make check` reports zero findings.
