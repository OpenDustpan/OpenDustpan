## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing test TestMakerLossDeniesServiceToken: make a service token, remove the maker grant, show use is denied with token.outside-reach. Check: `go test ./pkg/accesstoken/ -run TestMakerLossDeniesServiceToken -count=1` fails on main.

## 2. Implement and definition of done

- [ ] 2.1 Wire the NEW maker-path check into pkg/accesstoken/scope.go with one grant read per use and no cache. Check: `go test ./pkg/accesstoken/ -run TestMakerLossDeniesServiceToken -count=1` passes and BenchmarkCallerGrants shows no allocation regression.
- [ ] 2.2 Show the ended token through the existing route GET /api/server/service-tokens (access token), add the docs page, and run the usability check from the docs page alone. Confirmed by: the driver's gates. Check: `GET /api/server/service-tokens` shows the token as ended.
- [ ] 2.3 Run make check with zero findings. Confirmed by: the driver's gates. Check: `make check` reports zero findings.
