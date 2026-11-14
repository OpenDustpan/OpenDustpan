## Context

The contract report logs new reads. The fix is test data plus a strict comparison in the existing e2e contract check.

## Decisions

1. **Contract files.** Add the NEW files e2e/testdata/pulumi-3.273.0.contract.json and e2e/testdata/opentofu-1.13.2.contract.json. Each lists a read by method and path, with served or refused and a reason.
2. **Strict check.** An unknown read fails the check in TestCLIs. A read that the file records as refused passes. The contract file wins over any other source.

## Structure

No new type. The contract check compares each read with the file of its CLI version, and an unlisted read fails.

## Risks

- A new CLI release turns e2e red until its contract file exists
- A served route can change the answer for an older CLI

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | Test strictness only |  |  |
