## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Side effects (computed from main)

- [ ] 1.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 2. Spike

- [ ] 2.1 Probe local Forgejo and Gitea from throwaway code in NEW `/var/tmp/forge-spike/` kept outside `pkg/forge`, record pins and versions, use fake credentials only, then delete the directory and remove containers before close. Check: No route: the check lists the files of NEW `/var/tmp/forge-spike/`, shows pins and versions and fake credentials only, then shows the directory and containers removed.
- [ ] 2.2 Write the file NEW `docs/research/forge-small-forges-spike.md` with per-method Forgejo and Gitea support and API call, the five gaps of webhooks, check runs or commit statuses, review comments, app auth, and in-place comment edit, the bridge sketch with same-port or second-port verdict, pins, versions, fake credential kind, and the air-gapped bridge input that links #928. Check: No route: the check reads the NEW `docs/research/forge-small-forges-spike.md` note and shows every method of `pkg/forge/forge.go` marked supported, partly supported or unsupported with its API call, the five gaps, the bridge sketch with same-port or second-port verdict, and the air-gapped link.
- [ ] 2.3 Write the proposed port changes into the file NEW `docs/research/forge-small-forges-spike.md`, post them on #886 before #886 merges, and record the post link in the file NEW `docs/research/forge-small-forges-spike.md`. Check: No route: the check reads the NEW `docs/research/forge-small-forges-spike.md` note and shows the recorded post link to #886 before it merges.
