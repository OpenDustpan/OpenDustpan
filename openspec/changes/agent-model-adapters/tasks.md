## 0. Confirm against the code first

- [ ] 0.1 Confirm that `agent-connection-document-and-secrets` is on main: `rg -n 'type ConnectionSpec|func CheckModelURL' internal pkg`. BLOCKED by that change.
- [ ] 0.2 Confirm there is no `pkg/modeladapter`: `rg -n 'modeladapter' internal pkg cmd`.
- [ ] 0.3 Read ADR 129 decisions 5 to 8, 13 to 24, 27, 28 and 37 and the model port of ADR 121 in `internal/agent`. Note any change since the spec.

## 1. Failing tests first

- [ ] 1.1 Extend `agenttest.RunModel` with the cases of ADR 129 decision 37: each output token field name, `choice: omit`, the conversation key in each place and at a dotted path, no end-user field, each cache mode, each entry form of `reasoning.keep`, an opaque part that comes back unchanged, an error event in a stream with the status 200, each form of a wait, a 402, each list of `errors`, a canary secret in an opaque part (the readable record holds size and format only).
- [ ] 1.2 Tests of the dial guard and the client: denied addresses, rebinding, no redirect, no proxy, timeouts, body limit.
- [ ] 1.3 Tests of the HMAC key: two instances, no route.
- [ ] 1.4 For each guard of the layers table, a test that fails when the guard is removed. Check: remove the guard, see the test fail, put the guard back.

## 2. Build

- [ ] 2.1 `internal/agent`: `PartOpaque`, `ErrModelQuota`, the three stop reasons, `NewModel`. Move `OwnedPathConflict` to `pkg/modeladapter` and make `ParseConnectionSpec` call it.
- [ ] 2.2 `pkg/modeladapter/chatcompletions` and `pkg/modeladapter/messages`. Both run the `agenttest.RunModel` suite against a local fake server of the format. No provider name in code: `rg -in 'openai|anthropic|azure|bedrock|gemini' pkg/modeladapter` finds nothing in code.
- [ ] 2.3 `pkg/egress`: `DialGuard` and `NewDialGuard`.
- [ ] 2.4 The HMAC key row in `internal/store/{sqlite,postgres}` (one schema file for each dialect) and its start step in `cmd/opendustpan`.
- [ ] 2.5 The loop: the `check` step ends a run with `connection-changed`; the loop stops a run with `model-quota` and `model-rate-limited`.
- [ ] 2.6 The domain rule and the depguard entry for `pkg/modeladapter/...` in `.golangci.yml`.

## 3. Side effects

- [ ] 3.1 `make generate` and, if the API description changes, `go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description` and `make clients-generate`. Never edit a generated file by hand.
- [ ] 3.2 Docs page `docs/guides/agent-model-formats.md`: the two formats, the fields that differ between servers, three generic examples. A test runs each command of the page.

## 4. Definition of done

- [ ] 4.1 e2e step: a fake server of each format serves one agent turn with a tool call; the run records tokens and the cache fields.
- [ ] 4.2 Usability check: an administrator who reads the page only sets `outputTokens.field` for a server that refuses `max_tokens`. The error says which field fixes it.
- [ ] 4.3 `make check` passes with zero findings. Paste the output into the pull request.
