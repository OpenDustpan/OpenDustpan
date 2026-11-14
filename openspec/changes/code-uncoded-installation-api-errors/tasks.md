## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Migrate in slices

- [ ] 1.1 Slice 1 classifyAccess plus classifyBackup: reuse the codes from 403-codes-refusals-labeled-access and restore-documents-give-failed-restore, add missing errcode.Spec entries in internal/errcode, move onto Classify built by problemWriter in internal/api/server/server.go:552, shrink server.txt for the two classes. Check: `go test ./internal/errcode/... -run TestUncodedPathsNeedsTheSkippedFunctions -count=1` fails before, passes after slice
- [ ] 1.2 Slice 2 classifyGateConfig plus classifyIPAllowlist plus classifySessionPolicy plus classifyProviders: add errcode.Spec entries in internal/errcode, move onto Classify in internal/api/server/server.go:552 with chain order fixed, shrink server.txt for the four classes. Check: `go test ./internal/api/server/... -count=1` fails before, passes after slice
- [ ] 1.3 Slice 3 classifyPrincipal plus classifyToken plus classifySignIn plus git-host paths: reuse the 409 no-registration code from git-host-slice-1-follow, add missing errcode.Spec entries in internal/errcode, move onto Classify in internal/api/server/server.go:552, shrink server.txt for the classes. Check: `go test ./internal/api/server/... -count=1` fails before, passes after slice
- [ ] 1.4 Slice 4 registry paths plus audit chain plus ErrNoPaging plus errCheckpointsOff plus ErrNoWorker: reuse the registry codes from error-codes-g6-follow-up, add missing errcode.Spec entries in internal/errcode, move onto Classify in internal/api/server/server.go:552 until server.txt holds no line for the named classes. Check: `go test ./pkg/dustpan/... -run TestSDKAnswersCarryACode -count=1` fails before, passes after slice

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Run wiring through problemWriter in internal/api/server/server.go:552 for ANY /api/audit/entries with an access token, refresh the API description in internal/api/apidesc/document.go:96, update the docs page, run e2e step against the installation API, and run `make check` with zero findings. Confirmed by: CI (make check). Check: `go test ./internal/errcode/... -count=1` plus `curl -s http://127.0.0.1:8080/api/audit/entries` with an access token shows errorCode on refusal
