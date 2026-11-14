## Context

The registry signature check needs an OpenPGP dependency. Issue 1293 asks for its own ADR with the approved modules, the approval, and the GO-2026-5932 disposition. The pack proves a fixture import of go-crypto openpgp.

## Decisions

1. **Record the dependency in one ADR.** Write one ADR with the confirmed owner approval, the module versions that match go.mod, the reason go-crypto replaces the frozen package, and the pasted fresh govulncheck output with affected symbol count.
2. **Number, list and link.** Use the next free number from docs/adr/INDEX.md at write time, add the index entry, and link the registry docs signature section to the new ADR in the same change.

## Structure

No code change. The structure is one ADR file under docs/adr/, its index entry in docs/adr/INDEX.md, and one docs link from the registry page.

## Risks

- The ADR records versions that differ from go.mod, so readers trust a wrong fact.
