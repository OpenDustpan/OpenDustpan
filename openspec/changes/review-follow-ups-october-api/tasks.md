## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add NEW test TestServedIndicator in internal/api/registry for served before and after signing-key removal Check: `go test ./internal/api/registry -run TestServedIndicator` fails on main
- [ ] 1.2 Add NEW test TestOperatorFieldPresence in internal/api/server for each operator-owned field Check: `go test ./internal/api/server -run TestOperatorFieldPresence` fails on main
- [ ] 1.3 Add NEW test TestForgeInvalidHidesWrappers in internal/api/server Check: `go test ./internal/api/server -run TestForgeInvalidHidesWrappers` fails on main
- [ ] 1.4 Add NEW test TestRefusedOperationCarriesConnectionId in internal/api/server Check: `go test ./internal/api/server -run TestRefusedOperationCarriesConnectionId` fails on main
- [ ] 1.5 Add NEW test TestFreshAuthIncludesSignInPath in internal/api/pulumi Check: `go test ./internal/api/pulumi -run TestFreshAuthIncludesSignInPath` fails on main
- [ ] 1.6 Add NEW test TestNilMapNullableRule in internal/api/apidesc Check: `go test ./internal/api/apidesc -run TestNilMapNullableRule` fails on main
- [ ] 1.7 Add NEW test TestEmbeddedPointerRequiredRefused in internal/api/apidesc Check: `go test ./internal/api/apidesc -run TestEmbeddedPointerRequiredRefused` fails on main

## 2. Fixes

- [ ] 2.1 Expose served boolean and retain unserved releases in internal/api/registry/handlers.go Check: TestServedIndicator passes
- [ ] 2.2 Preserve field presence and refuse operator-owned fields in internal/api/server/githost_connections.go Check: TestOperatorFieldPresence passes
- [ ] 2.3 Hide wrapper prefixes of forge invalid in internal/api/server/githost.go Check: TestForgeInvalidHidesWrappers passes
- [ ] 2.4 Carry connection ID into refused audit entries in internal/api/server/githost_connections.go Check: TestRefusedOperationCarriesConnectionId passes
- [ ] 2.5 Add sign-in start path to reauthenticate on 403 in internal/api/pulumi/freshness.go Check: TestFreshAuthIncludesSignInPath passes
- [ ] 2.6 Apply nullable map rule in internal/api/apidesc/schema.go and refuse required on embedded-pointer fields in internal/api/apidesc/fields.go Check: TestNilMapNullableRule and TestEmbeddedPointerRequiredRefused pass

## 3. Definition of done

- [ ] 3.1 Wire each fix on its existing route with its stored-grant gate and access-token credential — Confirmed by: CI (package tests) Check: `go test ./internal/api/registry ./internal/api/server ./internal/api/pulumi ./internal/api/apidesc` passes; `make check` reports zero findings
