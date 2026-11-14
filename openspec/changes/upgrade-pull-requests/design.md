## Context

A unit pins a fixed provider or module version. The registry holds a newer release. A scanner finds the stale pin, opens one review request on the forge that changes the pin, and starts one plan through the same plan path that milestone 5 uses. RFC 56 is a draft, so scan timing, request scope, forge write path, and merge-state sync stay as open questions. The change is not spec-ready until those questions close.

## Decisions

1. **Scanner creates one change record per stale pin.** A scanner runs once per day per namespace and compares fixed-version pins from unit settings with registry releases. It picks the newest stable release above the pinned version. On a stale pin it creates one change record per unit, pin, and new version, with old version, new version, and registry release ID. A stored unique key on unit stable ID, pin, and new version makes a duplicate open impossible.
2. **Writer opens one review request per unit.** A pull request writer groups change records by code location and opens one review request per unit through the Forge port. It writes the pin change as one commit on a new branch. It never writes git directly. It records the namespace service principal as actor, the forge credential reference, and the reason in the audit entry.
3. **Writer survives partial failure without orphan duplicates.** The writer opens the forge request first with an idempotency key stored on the change record, then stores the forge reference. If the plan start fails after the forge open, the writer keeps the forge reference and retries only the plan start. A retry never opens a second forge request for the same key.
4. **Scanner polls forge state to close requests.** The scanner polls the forge state of each open request on each scheduled run and closes the change record when the forge request merges or closes, or when the pin changes by hand to the new version. Polling reuses the Forge port read path and needs no new webhook.

## Structure

Unexported type upgradePlan with constructor newUpgradePlan that checks namespace ID, unit stable ID, old version, and new version. Only the constructor makes a value.

## Risks

- The scanner opens duplicate pull requests for the same pin.
- A plan result never returns to the pull request.
- The close poll misses a merge and leaves a request open.
