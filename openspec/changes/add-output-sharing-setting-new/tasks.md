## 1. Failing tests first

- [ ] 1.1 Add failing tests for default nobody deny, collection share allow, and rule removal deny at next read. Check: Failing API test TestOutputSharingDefaultNobodyDenies on main.
- [ ] 1.2 Add failing e2e steps for Pulumi StackReference and OpenTofu remote state read pass and fail by rule. Check: Failing e2e test TestOutputSharingPulumiStackReference on main.

## 2. Build

- [ ] 2.1 Add the namespace setting with default nobody and effective value in the settings read. Check: PATCH then GET /api/dustpan/v1/namespaces/{namespace}/settings shows outputSharing.newUnitDefault and its effective value.
- [ ] 2.2 Add namespace and collection sharing rules on the producer with stable IDs. Check: POST then GET sharing rules on a producer by stable ID; rename of collection keeps rule on same ID.
- [ ] 2.3 Wire the read check on all output paths with fail-closed deny after #897 is merged. Check: Tool-neutral outputs route plus both tool paths enforce rule plus unit:read-outputs at each read with audit entries.

## 3. Definition of done

- [ ] 3.1 Run e2e with real Pulumi CLI and OpenTofu against the real binary over TLS. Check: `make e2e` output-sharing steps pass on SQLite and PostgreSQL over TLS against the real binary.
- [ ] 3.2 Add the docs page in the same pull request. Check: `docs/guides/output-sharing.md` renders and each command runs as written.
- [ ] 3.3 Run the usability check. Check: Namespace administrator completes the path from the docs page alone; each error names the one command that helps.
- [ ] 3.4 Run make check with zero findings. Check: `make check` with zero findings.
