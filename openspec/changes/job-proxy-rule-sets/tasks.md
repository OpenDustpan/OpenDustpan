## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing port tests for each invalid proxy rule field in NEW file pkg/proxyrules/proxyrules_test.go (package pkg/proxyrules is NEW). Check: `TestSaveNamespaceProxyRuleSetValidation` fails on main
- [ ] 1.2 Add failing port tests for unit save, id rules, port default, and `*` and `**` patterns in NEW file pkg/proxyrules/unit_test.go. Check: `TestSaveUnitProxyRuleSetValidation` fails on main
- [ ] 1.3 Add failing service tests for join order, digest, audit entry, empty set, and rendered view in NEW file internal/proxysvc/proxysvc_test.go. Check: `TestJoinNamespaceAndUnitProxyRuleSets` fails on main
- [ ] 1.4 Add failing service test for the If-Match digest in NEW file internal/proxysvc/digest_test.go. Check: `TestProxyRuleSetStaleDigestRefused` fails on main
- [ ] 1.5 Add failing handler tests for grant, fresh authentication, size limit, content type, and rendered access in NEW file internal/api/server/proxyrules_test.go. Check: `TestProxyRulesHandlerRefusals` fails on main

## 2. Build

- [ ] 2.1 Wire namespace get and put routes with `namespace:manage-proxy-rules` and audit with old and new digest. Declare them in internal/api/apidesc/openapi.json and add the adapter in NEW file internal/api/server/proxyrules.go. Check: New route `PUT /api/dustpan/v1/namespaces/{namespace}/proxy-rules` returns the digest
- [ ] 2.2 Wire unit get and put routes with `unit:manage-proxy-rules` and audit with old and new digest. Declare them in internal/api/apidesc/openapi.json and add the adapter in NEW file internal/api/server/proxyrules.go. Check: New route `PUT /api/dustpan/v1/units/{unit_id}/proxy-rules` returns the digest
- [ ] 2.3 Wire rendered route for debug only with unit read access. Declare it in internal/api/apidesc/openapi.json and add the adapter in NEW file internal/api/server/proxyrules.go. Check: New route `GET /api/dustpan/v1/units/{unit_id}/proxy-rules/rendered` returns joined proxy rules
- [ ] 2.4 Add the stored proxy rule set tables to the baseline of each dialect in name order, in NEW file pkg/store/internal/proxyrulestore/proxyrulestore.go and the baseline migration of each dialect. Check: `make generate` with clean `sqlc diff` for the edited baseline migration. Confirmed by: the driver's gates.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Add e2e step against the real binary on the new proxy-rules routes Check: BLOCKED by the missing proxy-rules routes: the e2e step calls the new `PUT /api/dustpan/v1/namespaces/{namespace}/proxy-rules` and new `GET /api/dustpan/v1/units/{unit_id}/proxy-rules/rendered` through the real binary over TLS with a personal token, and runs after the Build section. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.2 Write the docs page in the same pull request Check: Docs page shows each route, each field, each error, and the debug only note
- [ ] 4.3 Run the usability check Check: Namespace administrator completes the path from the docs page alone. A person's step.
- [ ] 4.4 Run `make check` with zero findings Check: `make check` passes with zero findings. Confirmed by: CI (the agent runs the fast checks only).
