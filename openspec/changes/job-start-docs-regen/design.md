## Context

Generated files come from the generators and are never edited by hand. Docs follow the code of the follow-ups. No new type is added (nothing is marked NEW).

## Decisions

1. **Generate, do not edit.** Run make clients-generate and make generate, and commit the output.

## Structure

No new type. The existing clients-check and the API description test keep the files in step with the generators.

## Risks

- The docs may describe behavior that the follow-ups did not ship; the docs test finds this.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| Generated file drift | 0 | stale generated files | API description and both clients after make clients-generate |
