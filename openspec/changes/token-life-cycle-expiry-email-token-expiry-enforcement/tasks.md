## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing authenticator tests for expired, revoked, and idle-disabled tokens Check: NEW TestAuthenticateRefusesExpiredToken in pkg/accesstoken fails on main
- [ ] 1.2 Add failing API tests for 401 on Pulumi, TFE, and tool-neutral routes with expired, revoked, and disabled bearer tokens on `ANY /api/server/tokens` and `POST /token` Check: NEW TestTokenExpiryDeniesPulumiAndTofu e2e fails on main on route `ANY /api/server/tokens` and `POST /token` with bearer access token

## 2. Implementation

- [ ] 2.1 Implement authenticator checks for expiry, revocation, disable, installation and namespace maxima, and last-used update Check: `go test ./pkg/accesstoken/...` passes for installation and namespace maxima with visible effective values
- [ ] 2.2 Wire authenticator into Pulumi, TFE, and tool-neutral handlers with 401 mapping, fresh-step checks, and audit entries Check: `go test ./internal/api/...` passes with 401 mapping and audit entries on `ANY /api/server/tokens` with bearer access token
- [ ] 2.3 Implement 90-day inactivity disable job with break-glass exemption, settings, re-enable path, and 10-minute rotation overlap Check: NEW TestInactivityDisableSweep in pkg/accesstoken passes for 90-day period with same-transaction re-read and break-glass exemption

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Check wiring, route with authorization actions, and audit entries Check: Route test lists `ANY /api/server/tokens`, `ANY /api/server/caller/tokens`, and `POST /token` with actions principal:manage-tokens and installation:manage-settings
- [ ] 4.2 Run e2e step against real CLIs over TLS Check: e2e step on `POST /token` and `ANY /api/server/tokens` with bearer access token runs pulumi up and tofu apply with expired, revoked, and idle-disabled tokens over TLS and checks 401 plus audit entries on SQLite and PostgreSQL
- [ ] 4.3 Write docs page in the same pull request Check: Docs page for token life cycle documents expiry, lifetime, inactivity disable, rotation overlap, and re-enable
- [ ] 4.4 Run usability check for the persona path Check: Developer completes token create with fresh step, rotate, and CLI use from the docs page alone
- [ ] 4.5 Run full check with zero findings Check: `make check` reports zero findings
