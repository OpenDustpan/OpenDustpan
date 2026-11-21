## Context

A developer tries to judge the cost of a planned change before apply, but the server gives no estimate. The approach waits on research.

This change holds the place for the estimate until the research result of RFC 67 decides the approach. It is gated on the research result, not on the integration. It changes no server code, no route, and no schema now.

## Decisions

1. **No code now.** The estimate shape ships after the spend and cost tool research (from 67-spend-cost-tools) ends. No estimate field appears before its source is trusted.
2. **Research result gates, not integration.** The approach is decided by what the research finds, not by when collection lands. A negative research result closes this change with no estimate instead of a weak one.
3. **No estimate beside the plan.** Until the refinement ships, plans carry no cost figure. A number without a named source and time would mislead more than no number.
4. **Drift and the run queue stay out.** They refine on their own paths; estimation reads plans but changes neither.

## Structure

No new structure now. The bug class is an estimate the plan cannot explain; gating the estimate on the research result plus a constructor that requires source and time is the structure that removes it. When the research ends, an estimate is an unexported value built only through its constructor, which refuses a figure without a trusted source and timestamp.

No custom analyzer and no new tool.

## Risks

- Developers judge cost without server help until the research ends. Preview output is unchanged, so the current review path keeps working.
- A stale price makes a change look cheap. Source and time on each figure let the reader tell stale from fresh.
