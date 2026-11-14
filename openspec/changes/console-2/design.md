## Context

The server already starts jobs, streams logs, stores plans and records approvals. The console repository holds the run page. This change wires the run page to those server records and runs its browser tests against the suite server.

## Decisions

1. **Read through public API only.** The run page reads only the tool-neutral jobs API and the inventory API. It writes only through the job approve route that policy-approvals owns.
2. **Server-side masking only.** The server masks secret values in masked log chunks and in the masked plan diff before send, including values split across chunk boundaries. The console renders the masked text without further processing.
3. **Polling for live updates.** The timeline and the live log poll the job, log and approval routes every 5 seconds with a manual refresh control. The page shows the time of the last poll. No server-sent event channel ships in this slice.

## Structure

No new stored type. The view model uses unexported field runView.source with constructor newRunView that accepts only server API records.

## Risks

- Browser tests flake when live log timing varies.
- Plan diff grows large on big units and slows the page.
