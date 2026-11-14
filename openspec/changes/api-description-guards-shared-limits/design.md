## Context

Small follow-ups to the API description guard and the server wiring for the profiles handler. The shared sink test limit waits for outbox and dispatcher work and stays out of scope.

## Decisions

1. **Guard covers each handler type.** Widen the guard to each field whose type implements the handler interface, checked on both value and pointer form, including embedded struct fields and interface-typed fields whose interface includes ServeHTTP.
2. **Single profiles declaration.** Keep the profiles handler in neutralHandlers at pkg/dustpan/dustpan.go:1216 and remove the adapters copy at pkg/dustpan/dustpan.go:1108. Fix wiring to read the kept field and fail server startup with a plain error when it is nil.
3. **Exclusion list rule.** The exclusion list in pkg/dustpan/apidesc_internal_test.go holds each handler-typed field that serves no route. The dustpan package owns it in the same test file. A change that adds a handler-typed field adds one entry in the same pull request when the field never reaches the server mux, else it adds an API description row.

## Structure

No new type. The fix narrows existing structs and widens an existing test guard. No unexported type makes this bug class impossible, so a guard with a test stays.

## Risks

- Wider match lists a non-route field; mitigated by the exclusion list rule and a negative guard case.
- Removed adapters copy breaks wiring; mitigated by fixing readers and a startup nil check covered by a test.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | Small guard and wiring follow-ups with no latency, storage, or limit change |  |  |
