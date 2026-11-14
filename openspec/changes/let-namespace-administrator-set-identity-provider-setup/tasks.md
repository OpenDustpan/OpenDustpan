## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing SaveProviderConfig and FindOrMakeSignInPrincipal isolation tests in `NEW pkg/namespace/signinsettings_test.go` for OIDC and SAML per namespace home. Check: TestNamespaceSignInFindsOnlyHomePrincipal in `NEW pkg/namespace/signinsettings_test.go` fails on main, passes after.
- [ ] 1.2 Add failing SaveProviderConfig tests in `NEW pkg/namespace/signinsettings_test.go` for fresh-auth refusal and namespace audit entry. Check: TestNamespaceProviderSaveNeedsFreshAuth in `NEW pkg/namespace/signinsettings_test.go` fails on main, passes after.

## 2. Build

- [ ] 2.1 Add the NEW sign-in route in `NEW internal/namespacesvc/signin.go` with `namespace:manage-sign-in` check and fresh-auth check. Check: PUT the NEW route `PUT /api/dustpan/v1/namespaces/{namespace}/sign-in` with a session of a namespace admin passes the manage gate and fresh check; stale proof gets 401.
- [ ] 2.2 Store provider config in `NEW pkg/namespace/signinsettings.go` keyed by stable namespace ID and show effective values without secrets. Check: `GET /api/dustpan/v1/namespaces/{namespace}/settings` with a session of a namespace admin shows effective provider state without secrets.
- [ ] 2.3 Add e2e step in `NEW e2e/namespacesignin_test.go` against the real server with hosted namespace principals. Check: `NEW e2e/namespacesignin_test.go` signs in through namespace A provider and asserts no principal of namespace B appears; BLOCKED by NEW sign-in route until it lands.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Add user docs page in `NEW docs/guides/namespace-sign-in.md` for namespace provider setup. Check: Docs page for namespace OIDC and SAML setup in `NEW docs/guides/namespace-sign-in.md` runs as written.
- [ ] 4.2 Run usability check of the user path from `NEW docs/guides/namespace-sign-in.md`. Check: Namespace administrator completes setup from `NEW docs/guides/namespace-sign-in.md` alone; each error names the one command that helps.
- [ ] 4.3 Run wiring and route authorization on `GET /api/dustpan/v1/namespaces/{namespace}/settings` with a session, plus e2e, docs, and `make check`. Check: `GET /api/dustpan/v1/namespaces/{namespace}/settings` with a session of a namespace admin passes; `make check` reports zero findings.
