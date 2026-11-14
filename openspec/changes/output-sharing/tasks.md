## 1. Failing tests first

- [ ] 1.1 Add failing cross-tool read tests for Pulumi consumer and OpenTofu consumer with a naming rule. Check: TestJobReadsProducerOutputs_PulumiReadsOpenTofu fails on main, passes after the fix.
- [ ] 1.2 Add failing refusal tests for missing rule and for secret output without starter read-secrets. Check: TestJobReadDenied_NoRule_403 and TestJobReadSecretDenied_NoReadSecrets_403 fail on main, pass after the fix.
- [ ] 1.3 Add failing tests for decrypt scope, mid-job revocation, and stable-ID rename. Check: TestDecrypt_OutsideOutputs_403 and TestRead_RevokedMidJob_403 fail on main, pass after the fix.

## 2. Build and prove

- [ ] 2.1 Implement Checker with both read checks at each read against rules stored by the depended change. Check: `go test -race ./pkg/outputshare/... ./internal/outputsharesvc/...` passes.
- [ ] 2.2 Wire tool-neutral outputs route, TFE-compatible output routes, and the terraform_remote_state path with unit:read-outputs on the producer. Check: `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/outputs`, the TFE-compatible output routes, and the terraform_remote_state path each return 200 with a rule and 403 without one for a job token.
- [ ] 2.3 Extend the e2e step for Pulumi job reads OpenTofu outputs and OpenTofu job reads Pulumi outputs, plus the 403 path. Check: e2e/output_sharing_test.go passes against SQLite and PostgreSQL through the real binary over TLS.
- [ ] 2.4 Write the docs page in the same pull request. Check: docs/guides/output-sharing.md shows both tool reads and the 403 error shape with its one commands.
- [ ] 2.5 Run usability check and full check. Check: Developer follows docs/guides/output-sharing.md alone and each error names its one command; `make check` passes with zero findings.
