## Context

Reporting sits above jobs and change orders. The Forge port gets check and comment methods behind capabilities. A pure renderer builds the comment. Timing resolves from the widest scope.

## Decisions

1. **Pure renderer.** The renderer takes unit status, counts, addresses with their kind of change and links. It has no input for a value.
2. **Fixed check name.** The summary check name is one constant.
3. **Summary rule.** The summary check passes only when every unit plan has succeeded. A failed plan, an approval wait, an unfinished plan and a Git host error each give a failing or pending summary.
4. **Comment repair and serialization.** One lock for each review request serializes edits. The server records the comment ID in the same step as the post. A deleted comment gets one new comment.
5. **Timing resolution.** Timing resolves from the widest level that forbids overrides. Else the narrowest set value wins. An unset level inherits. The API shows the effective value and its level. Each write makes an audit entry.

## Structure

An unexported type planComment with the constructor NewPlanComment. It sorts units by stable unit ID, refuses negative counts and has no field for a value. Only the constructor makes a valid value.

## Risks

- A second push posts a second comment.
- The summary stays green while a unit waits.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| webhook intake | Check the signature, refuse a replay, never decide on the payload alone. | A payload with a bad signature reports nothing. |
| renderer and Forge adapter | The renderer type has no value field. The adapter edits the stored comment ID only. | A plan with secret values renders no value. |
