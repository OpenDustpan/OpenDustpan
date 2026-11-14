## Context

Generated error-variant schemas feed the API description. A duplicate generated name hides one variant.

## Decisions

1. **Refuse duplicate generated name in checkedErrorVariants.** checkedErrorVariants builds a local seen set of exact generated names and returns a duplicate error before it calls errorVariantSchemas. No new type removes the need for the check because the inputs are plain strings from code generation, so a guard with a mutation test takes its place: the test removes the guard and confirms the duplicate passes silently. No new tool. No custom analyzer.

## Structure

Local seen set inside func checkedErrorVariants in internal/api/apidesc/errorvariants.go:79, entered only through func WithErrorVariants in internal/api/apidesc/errorvariants.go:52; no unexported type makes duplicate strings impossible, so a guard with a mutation test takes its place.

## Risks

- A duplicate name reaches the API description and hides one error shape.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | Startup-only guard with no request latency effect |  |  |
