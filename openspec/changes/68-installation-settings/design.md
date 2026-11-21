## Context

An operator sets up one installation and needs one clear place for installation settings. Main already serves one settings resource for the installation and one for each namespace. The names, defaults, layers, and sensitive marks of installation settings stay open until the shape of the installation API is fixed.

This change records RFC 68 as a coarse change that waits on that shape. It fixes the rule for the direction of a weaker setting so the refinement can test it. It keeps the existing settings resources unchanged and changes no server code, no route, and no schema now.

## Decisions

1. **No code now.** Setting names, defaults, layers, and sensitive marks ship after the installation API shape (RFC 59) is fixed. The current resources answer as today until then.
2. **One direction rule for weaker settings.** A change that makes the installation less secure needs a fresh authentication and writes an audit entry. A change that keeps or raises security applies at once. The refinement tests this rule in both directions.
3. **Existing codes stay.** An unknown key answers 400 with `settings.key-unknown`; an invalid value answers 400 with `settings.value-invalid`; a weaker change without a fresh step answers 403 with `mfa.fresh-step-required`. Each failure changes nothing.
4. **Namespace placement and routing wait on this item.** They refine after the installation settings shape is fixed, not beside it.
5. **No console views in this change.** They follow the fixed shape after it lands.

## Structure

No new structure now. The bug class is a weaker setting applied by an old session; the one direction rule with a fresh-authentication check on the weakening path is the structure that removes it. When the shape is fixed, a settings write is refused or accepted by the direction of the change, checked in the same transaction as the write.

No custom analyzer and no new tool.

## Risks

- Operators wait for named settings until the API shape lands. The current resources with effective value and source cover reads until then.
- A setting reads as weaker when it is not. The direction rule names exactly which changes count as weakening, tested in both directions.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| handler | Refuse a weaker settings change without a fresh authentication with 403 and `mfa.fresh-step-required`; refuse unknown keys and invalid values with 400 and the named code. | Weaken a setting with an old session; the answer is 403 and the value is unchanged. |
| service | Write an audit entry with actor and source for each settings change. | Change a setting; the audit log names the actor, the old value, and the new value. |
| store | Carry the fence and the maintenance check on each settings write. | Write a setting without a fence; the store refuses it. |
