## 1. Failing tests first

- [ ] 1.1 Write a failing test for the docs link scenario. Check: TestRegistryDocsLinkPresent fails on main and passes after the link ships.
- [ ] 1.2 Write a failing test for the index entry scenario. Check: TestAdrIndexEntryPresent fails on main and passes after the entry ships.
- [ ] 1.3 Write a failing test for the version match scenarios. Check: TestAdrVersionsMatchGomod fails when versions differ from go.mod.
- [ ] 1.4 Write a failing test for the vulnerability disposition scenarios. Check: TestGovulncheckDispositionRecorded fails when the ADR lacks the fresh output.

## 2. Record and check

- [ ] 2.1 Check the ADR versions against go.mod and go.sum. Check: `go list -m github.com/ProtonMail/go-crypto github.com/cloudflare/circl golang.org/x/crypto` matches the ADR, including indirect x/crypto.
- [ ] 2.2 Re-run the vulnerability check and record fresh output in the ADR. Check: `make vuln` output pasted into the ADR with affected symbol count; on a differing result the owner reviews before ship.
- [ ] 2.3 Write the ADR after the owner answers the open questions. Check: The ADR cites the owner answer, the versions, the replacement reason, and the risk owner with review date and trigger.

## 3. Definition of done

- [ ] 3.1 Ship the ADR, the index entry and the docs link with docs page and usability check. Check: ADR file, index entry and registry docs link are wired; `make docs-lint` and `make check` report zero findings; usability check passes from the docs page alone.
