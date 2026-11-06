# 312. The action of a change of the own password is caller:manage-sign-in

Status: accepted · 2026-11-01. A PM decision under the owner's delegation for the details of ADR 285. The action name follows the grammar of the API description

Changes:

- Changes ADR 285 decision 29: the action of a change of the own credentials is `caller:manage-sign-in`, not `sign-in:manage-own`.

## Context

ADR 285 decision 29 names the action `sign-in:manage-own` for a change of
the own password and the own factors.

The API description refuses an action whose area holds a hyphen. Its grammar
of an action is `^[a-z]+:[a-z]+(-[a-z]+)*$`. So `sign-in:manage-own` cannot be
the action of a route under `/api/dustpan/v1/`, and the server does not
start with it.

The action `caller:manage-preferences` exists. Its object is the caller. Every
authenticated person holds it with no grant.

## Decision

1. **The action is `caller:manage-sign-in`.** A person changes the own
   password, and later the own factors, with the action
   `caller:manage-sign-in` on the caller. Every authenticated person holds it
   with no grant, as `caller:manage-preferences`. No agent tool holds it. The
   rest of ADR 285 decision 29 stays.

## Consequences

- The route `POST /api/dustpan/v1/me/password` declares
  `caller:manage-sign-in`.
- The routes of the own factors use the same action when they land.
