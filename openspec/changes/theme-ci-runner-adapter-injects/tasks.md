## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Side effects (computed from main)

- [ ] 1.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 2. Research

- [ ] 2.1 Write NEW docs/research/ci-runner-secrets.md with these named scenarios: injected secret in state warns; expired exchange token mid-apply is refreshed or the run is closed by the reaper; fork job gets no secrets; own-CLI state write with no fingerprints follows the normal gates; derived, encoded or split value is a known boundary of exact matching; each secret fetch and each denial writes an audit entry. Add one hostile case per ADR 250 layer. Limits: token lifetime 15 minutes per run; run record at most 100 names per run; minimum 12 characters per value. Check: `rg -c "injected secret in state warns|expired exchange token|fork job gets no secrets|own-CLI state write with no fingerprints follows the normal gates|known boundary|audit entry|hostile" docs/research/ci-runner-secrets.md` shows each scenario
