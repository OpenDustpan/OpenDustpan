## 1. Failing tests first

- [ ] 1.1 Add failing port tests for each invalid proxy rule field Check: `TestSaveNamespaceProxyRuleSetValidation` fails on main
- [ ] 1.2 Add failing port tests for unit save, id rules, port default, and `*` and `**` patterns Check: `TestSaveUnitProxyRuleSetValidation` fails on main
- [ ] 1.3 Add failing service tests for join order, digest, audit entry, empty set, and rendered view Check: `TestJoinNamespaceAndUnitProxyRuleSets` fails on main
- [ ] 1.4 Add failing handler tests for grant, fresh authentication, size limit, content type, and rendered access Check: `TestProxyRulesHandlerRefusals` fails on main

## 2. Build

- [ ] 2.1 Wire namespace get and put routes with `namespace:manage-proxy-rules` and audit with old and new digest Check: `PUT /api/dustpan/v1/namespaces/{namespace}/proxy-rules` returns the digest
- [ ] 2.2 Wire unit get and put routes with `unit:manage-proxy-rules` and audit with old and new digest Check: `PUT /api/dustpan/v1/units/{unit_id}/proxy-rules` returns the digest
- [ ] 2.3 Wire rendered route for debug only with unit read access Check: `GET /api/dustpan/v1/units/{unit_id}/proxy-rules/rendered` returns joined proxy rules

## 3. Definition of done

- [ ] 3.1 Add e2e step against the real binary Check: e2e step saves a namespace set and a unit set, reads the joined set, and renders the plan view through the real binary over TLS
- [ ] 3.2 Write the docs page in the same pull request Check: Docs page shows each route, each field, each error, and the debug only note
- [ ] 3.3 Run the usability check Check: Namespace administrator completes the path from the docs page alone
- [ ] 3.4 Run `make check` with zero findings Check: `make check` passes with zero findings
