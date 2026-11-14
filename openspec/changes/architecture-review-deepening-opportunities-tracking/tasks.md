## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Index

- [ ] 1.1 Write the failing test TestIndexDriftCheck in the NEW file internal/archindex/index_test.go for the scenario Index-drift-check. Check: `go test ./internal/archindex -run TestIndexDriftCheck` fails on main
- [ ] 1.2 Re-run the evidence of every finding on current main, starting with internal/api/tfe/overrides.go:74, and save the drift log in the NEW file docs/guides/architecture-review.md. Check: `rg -n -F 'allowance' internal/api/tfe` runs and the log lists each result
- [ ] 1.3 File follow-ups for F1 to F4, including F1b Cache-Control and errorCode alignment, F2b token-scheme and session-flag and ScopePublic and key-owner drifts, F3b empty TokenID, and F4 journal decode, and list them in docs/guides/architecture-review.md. Check: The index lists F1, F1b, F2, F2b, F3, F3b and F4 with gate labels
- [ ] 1.4 File follow-ups for F5 to F9 and list them in docs/guides/architecture-review.md. Check: The index lists F5 to F9 with gate labels
- [ ] 1.5 File follow-ups for F10 to F15 and list them in docs/guides/architecture-review.md. Check: The index lists F10 to F15 with gate labels
- [ ] 1.6 File follow-ups or RFCs for findings 1 to 8 of #1400, including the finding 2 nil audit store fail-open, and list them in docs/guides/architecture-review.md. Check: The index lists findings 1 to 8 with the audit fail-open interim rule
- [ ] 1.7 Link the #1464 nit groups plus the 4 numbered findings of #3037 to exported-pkg-signatures-name-internal, the four new-services-adapters-land-in changes, and errcode-catalog-imports-domain-packages, confirm the intent for edge 3 in internal/clientip/clientip.go and internal/sessionauth/sessionauth.go, and list them in docs/guides/architecture-review.md. Check: The index links the existing changes for #3037 edges and SDK surface instead of new follow-ups

## 2. Definition of done

- [ ] 2.1 Refine this change when the SDK surface RFC is accepted, in docs/guides/architecture-review.md. Confirmed by: CI (make check). Check: Each finding links to its follow-up and `make check` reports zero findings
