## Context

The console lives in a separate repository and uses only the tool-neutral REST API. The blueprints change owns the server routes, the input check, the unit write, the grants, and the audit record. This change owns only the console catalog pages and the browser test gate.

## Decisions

1. **Server routes stay in blueprints change, console pages in this change.** This change builds the catalog page and the create form in the console repository and the browser tests that drive them. The console holds no copy of blueprint rules and sends the blueprint stable ID.

## Structure

No new server structure in this change. The server keeps stable IDs for blueprint and unit references so a rename of a blueprint keeps the request valid. The console holds no copy of blueprint rules.

## Risks

- The console repository and the server suite drift when the request shape changes.
- Long input schemas cost time when the form shows inputs that do not apply to the chosen blueprint.
