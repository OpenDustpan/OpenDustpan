## Context

A developer runs plans and applies in the CI the team already uses and wants that history in OpenDustpan without moving to hosted runners.

This change records RFC 29 as waiting on the run model of milestone 5. It makes no behavior change: the server answers the tool APIs as today and adds no error code. It changes no server code, no route, and no schema now.

## Decisions

1. **No code now.** Existing CI keeps driving the Pulumi CLI or OpenTofu against the server as today. History reads through the tool-neutral API as today.
2. **Runs land first.** The contract between existing CI and OpenDustpan (what CI posts, what the server records, and where approval steps sit) is the one open question of the proposal. It stays open until the run model ships, since naming the contract before the run object exists would fix names the run model may refuse.
3. **No new error code.** CI failures surface through the tool CLIs and the existing unit history, not through a new server error.

## Structure

No new structure now. The bug class is a CI-posted record that the run model cannot carry; waiting for the run model before fixing the contract is the structure that removes it. When runs ship, the CI record is an unexported value built only through its constructor, which checks the run link and the fence.

No custom analyzer and no new tool.

## Risks

- Teams wait for CI-posted history until milestone 5 ships. The docs page says CI runs work as today and history reads as today.
- A contract drafted early hardens around the wrong run shape. The open question stays open, so no code depends on a draft.
