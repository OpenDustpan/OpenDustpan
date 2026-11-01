# 311. Console: the first screens, the header of the hosted service and the look of Dusty

Status: accepted · 2026-11-01. Records decisions of the project owner of 17 and 18 October 2026

Changes:

- Extends ADR 180: sentence case and underlined navigation in the console, no friendly locale, no achievements.
- Extends ADR 51: the first screen of a namespace.
- Extends ADR 77: the header of OpenDustpan Cloud and the account switcher.
- Extends ADR 120: the mark and the avatar of Dusty.
- Extends ADR 183: the on-call view at phone width.

## Context

The project owner made these decisions in the owner decision file 2026-10-17. Some entries are dated 18 October. A check on 1 November 2026 found no ADR that states them. The console is in its own repository. The decisions are here because the server ADRs hold the decisions of the project.

This ADR writes the words of the owner. It adds no decision of its own.

## Decision

1. **Sentence case and underlined navigation (owner, 2026-10-17).** The owner said: "sentence case, underlined navigation".
2. **A namespace switcher in the top bar (owner, 2026-10-17).** The owner said: "Namespace switcher in the top bar". The owner said also: "the URL carries the namespace".
3. **No friendly locale (owner, 2026-10-17).** The owner said: "No friendly locale".
4. **No achievements (owner, 2026-10-18).** The owner said: "No achievements". The owner said also: "This replaces the earlier 'achievements such as Night owl' idea".
5. **The header of OpenDustpan Cloud (owner, 2026-10-17).** The owner said: "a Cloud tag and the region in the header".
6. **The account switcher (owner, 2026-10-17).** The owner said: "The account switcher appears only for members of several accounts".
7. **The landing page (owner, 2026-10-17).** The owner said: "The landing page after sign-in is a personal standup ("Home"), not the timeline". The owner said also: "The timeline stays as its own view".
8. **The order of Home (owner, 2026-10-18).** The owner said: "Home order: failed runs first, then approvals waiting on me". The owner said also: "locks I hold, changes since my last visit".
9. **The first screen of a namespace (owner, 2026-10-18).** The owner said: "First screen of a namespace: collections with counts, facet search". The owner said also: "needs-attention list and activity, all on one page". The owner approved the layout at 19:40Z: "the badge list and the one-page first screen" are "approved as a layout". The condition: "the build follows the console directions canvas" (terminal language, serious pass R4, home R5, mobile R6, themes R7, big plans R8). The build also "uses the design system's real icons, glyphs and assets".
10. **On-call at phone width (owner, 2026-10-18).** The owner said: "On-call at phone width: answered by the approved mobile designs (canvas R6)".
11. **The mark of Dusty (owner, 2026-10-18).** The owner said: "Mark: option A, the three square specks from the OpenDustpan mark". The owner said also: "On dark: yellow specks". The owner said also: "On light: ink specks with a yellow border".
12. **The avatar of Dusty (owner, 2026-10-18).** The owner said: "Avatar: option B, the logo tile with the pan removed". The owner said also: "wherever Dusty needs a square (audit log, chat integrations)".

## Consequences

- ADR 51, ADR 77, ADR 120, ADR 180 and ADR 183 get a pointer to this ADR.
- The console repository builds these decisions. A server route is not part of them.
