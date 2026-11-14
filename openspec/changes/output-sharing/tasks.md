## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing cross-tool read tests for Pulumi consumer and OpenTofu consumer with a naming rule. Check: TestJobReadsProducerOutputs_PulumiReadsOpenTofu fails on main, passes after the fix.
- [ ] 1.2 Add failing refusal tests for missing rule and for secret output without starter read-secrets. Check: TestJobReadDenied_NoRule_403 and TestJobReadSecretDenied_NoReadSecrets_403 fail on main, pass after the fix.
- [ ] 1.3 Add failing tests for Decrypt scope, mid-job revocation, stable-ID rename, TFE routes, terraform_remote_state path, audit fail-closed, and undriven version. Check: TestDecrypt_OutsideOutputs_403, TestRead_RevokedMidJob_403, TestRead_StableID_Rename, TestTFEOutputs_Check, TestRemoteState_Check, TestAuditFailClosed_500, and TestUndrivenVersion_Error fail on main, pass after the fix.

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Build and prove

- [ ] 3.1 Implement Checker with both read checks at each read against rules stored by the depended change. Check: `go test -race ./pkg/outputshare/... ./internal/outputsharesvc/...` passes.
- [ ] 3.2 Wire tool-neutral outputs route, TFE-compatible output routes, and the terraform_remote_state path with unit:read-outputs on the producer. Check: `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/outputs`, the TFE-compatible output routes, and the terraform_remote_state path each return 200 with a rule and 403 without one for a job token.
- [ ] 3.3 Add mutation test for the nil-port constructor guard. Check: `go test -run TestNewChecker_NilPort ./pkg/outputshare/...` fails when the nil guard is removed.
- [ ] 3.4 Extend the e2e step for Pulumi job reads OpenTofu outputs and OpenTofu job reads Pulumi outputs through the tool-neutral outputs route with a job token, plus the 403 path for a missing rule. Check: e2e/output_sharing_test.go with `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/outputs` and a job token passes against SQLite and PostgreSQL through the real binary over TLS.
- [ ] 3.5 Write the docs page in the same pull request. Check: docs/guides/output-sharing.md shows both tool reads and the 403 error shape with its one commands.
- [ ] 3.6 Run usability check and full check. Check: Developer follows docs/guides/output-sharing.md alone and each error names its one command; `make check` passes with zero findings.
