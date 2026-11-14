## 1. Failing tests

- [ ] 1.1 Add failing API tests for the token warning field and levels. Check: API test for warning field inside and outside window fails on main.
- [ ] 1.2 Add failing tests for the CLI warning carriage on both tool APIs. Check: CLI contract test for Pulumi and OpenTofu warning carriage fails on main.
- [ ] 1.3 Add failing tests for expired, no-expiry, boundary, and refusal cases. Check: Tests for expired token, no-expiry, boundary values, and 403 without grant fail on main.

## 2. Build

- [ ] 2.1 Add warning level computation from expiry and the two threshold settings. Check: `GET /api/dustpan/v1/me/tokens` returns expiry and warning level.
- [ ] 2.2 Wire the warning state into both tool-compatible responses. Check: Pulumi and OpenTofu responses carry warning level and time left.
- [ ] 2.3 Wire the warning state into the token list read. Check: Token list returns per-token warning levels with server clock seconds left.

## 3. Definition of done

- [ ] 3.1 Write the user docs page for token expiry warnings. Check: Docs page command run shows banner text and CLI warning text.
- [ ] 3.2 Add the e2e step with the real Pulumi CLI and the real OpenTofu against the real binary over TLS. Check: e2e run with a token near expiry shows the CLI warning and the API warning field, and audit entries pass.
- [ ] 3.3 Run the full check and the usability check from the docs page alone. Check: `make check` reports zero findings.
