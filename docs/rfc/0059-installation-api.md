---
rfc: 59
title: Shape of the installation API served by api/server
status: draft
created: 2026-10-11
---

# RFC 59. Shape of the installation API served by api/server

Status: draft.

## Problem

ADR 17 says that OpenDustpan's own objects get an OpenDustpan API with its
own route prefix, and that its shape is a separate RFC. ADR 28 says that a
tool-neutral facade waits for that RFC.

No such RFC came first. ADR 30 still created the tool-neutral HTTP adapter
`api/server`, mounted at `/api/server/`, with the object `@server` for the
installation and the action `server:backup` on three backup routes. ADR 30
also made `opendustpan restore` an offline command and the only exception to
ADR 17. ADR 32 then added the metrics route `GET /api/server/metrics` and
the action `server:observe` to the same package.

So the adapter exists ahead of its shape RFC. The shape questions below are
still open, and the owner still has to decide them. This RFC records the
conflict and asks the questions. It decides nothing.

## What exists today

- The package `internal/api/server`, mounted at `/api/server/`, with its own
  route table and its own `NewHandler` (ADR 30).
- The backup routes `POST /api/server/backups`, `GET /api/server/backups`
  and `GET /api/server/backups/{id}`, with the action `server:backup` on the
  object `@server` (ADR 30).
- The metrics route `GET /api/server/metrics`, with the action
  `server:observe` on the object `@server` (ADR 32).
- The offline command `opendustpan restore`, the only exception to ADR 17
  (ADR 30).
- The route test of ADR 17 covers every route. The public list names no
  `api/server` route.

## Open questions

- What is the route prefix of the installation API, and does `api/server`
  keep it.
- What are its objects and its action names, and does `@server`,
  `server:backup` and `server:observe` keep their form.
- What are its JSON and error shapes. Do they follow the Pulumi
  Cloud-compatible API (camelCase, `{"code", "message"}`), the TFE-compatible
  API (JSON:API), or a third shape.
- How is the API versioned as new installation features (backups, metrics,
  upgrade checks, audit log) join it.
- Does the offline `restore` stay an exception to ADR 17, or does restore get
  a REST API.
- How does the console use this API (ADR 8), and which routes does the
  published endpoint list name.

## Outcome

None yet.
