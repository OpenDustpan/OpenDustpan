# 290. Console themes: the Friendly theme is dropped, and the search shortcut

Status: accepted · 2026-10-31. Decided by the project owner on 31 October 2026. Amends ADR 180 (decision 2)

## Context

ADR 180 decision 1 names two themes: the default theme and Terminal. A draft design also had a third theme, "Friendly". ADR 180 decision 2 says that the default theme has no keyboard shortcut of the console and never shows a shortcut hint.

## Decision

1. **No Friendly theme (owner).** The console has no Friendly theme. Default and Terminal stay (ADR 180 decision 1). (owner)
2. **Search shortcut in the Default theme (owner).** In the Default theme, Ctrl K (Cmd K on macOS) opens search. The console shows no shortcut hint for it. This amends ADR 180 decision 2: the Default theme has one console shortcut, and it has no hint. (owner)
3. **The Terminal theme keeps its rules.** Terminal keeps the shortcuts, the hints and the settings of ADR 180 decisions 2 and 3.
4. **No Maple Mono (owner).** The console does not use the font Maple Mono. The fonts of ADR 180 decision 1 stay. (owner)
5. **Keyboard access.** A person who uses a screen reader or speech input can still reach search by the link in the navigation. The shortcut is not the only way.

## Consequences

- ADR 180 gets a pointer to this ADR.
- `OpenDustpan/console` removes any Friendly theme code and adds the search shortcut to the Default theme.
- A saved preference with the value `friendly` is not valid. The preferences check refuses it. A tagged release has not shipped this value.
