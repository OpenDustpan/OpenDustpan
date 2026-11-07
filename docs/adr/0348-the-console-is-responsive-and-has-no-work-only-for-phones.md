# 348. The console is responsive, and no work is only for phones

Status: accepted · 2026-11-07. Decided by the project owner on 7 November 2026

Changes:

- Extends ADR 180 decision 4: the rule covers each console design, not only the themes.
- Extends ADR 183 decision 6: the mobile checks apply to responsive designs.

## Context

ADR 180 decision 4 says that the themes get no design for small screens of their own. ADR 183 decision 6 gives acceptance checks for iOS Safari and Android Chrome. The rule is not written for console designs in general.

On 7 November 2026 the project owner decided the rule for designs.

## Decision

Decided by the project owner on 7 November 2026.

1. **Responsive designs (owner, 2026-11-07).** A console design is responsive. One design works on all screen sizes.
2. **No phone-only work (owner, 2026-11-07).** The project does not do design or build work that serves only phones.
3. **The server stays neutral about the device.** A server API does not depend on the device of the client. The console applies decisions 1 and 2 in its own repository (ADR 8).

## Consequences

- The console repository applies this ADR. The server repository has no code change.
- This ADR does not decide which actions a phone can do. A phone can do an action only if the action is possible on all sizes, with the same checks, for example a fresh sign-in.
