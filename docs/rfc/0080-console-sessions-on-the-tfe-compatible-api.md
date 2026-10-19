---
rfc: 80
title: Console sessions on the TFE-compatible API, and fresh authentication for force-unlock and the state download
status: draft
created: 2026-10-18
---

# RFC 80. Console sessions on the TFE-compatible API, and fresh authentication for force-unlock and the state download

Status: draft. Not decided. Gap G6 of the console design for the OpenTofu
workspace page (`OpenDustpan/console`,
`docs/design/opentofu-workspace-page.md`, section 3.2).

## Problem

The console design asks for two things once the console signs in with a
session:

1. The TFE-compatible API must accept the console session. Today it reads
   only `Authorization: Bearer <token>` (ADR 25 decision 8).
2. A force-unlock (ADR 239) and a state download (ADR 240) need a fresh
   authentication (ADR 56, ADR 71, ADR 212 decision 3, ADR 227).

The product manager gave the direction (console design, Q3): a fresh
authentication for both, once the console has session sign-in. It is not a
blocker now. ADR 240 decision 7 says that a download is not under the
window now.

Facts from `main`:

- The server has console sessions. `internal/sessionauth` checks the
  cookie `__Host-dustpan_session`. A request with the cookie must come from
  an allowed origin, or the browser must not mark it as cross-site. A
  method that can change state must send the CSRF value of the session in
  `X-Dustpan-CSRF`. Both checks run before the store read.
- The Pulumi Cloud-compatible API, the audit API and the context API take
  the session cookie through one `sessionauth.Checker`. The TFE-compatible
  API and the http backend do not.
- An access token never gives a fresh authentication (ADR 227 decision 6).
  A step with no fresh authentication answers 403 with
  `mfa.fresh-step-required` and names the path of a new sign-in.
- ADR 5 lets a console on another hostname call the API through CORS with
  credentials, and recommends same-site hostnames, so that a session cookie
  works.
- The real OpenTofu CLI sends a bearer token and no cookie. The
  TFE-compatible API is a contract with the CLI. A change must not break
  it (`CLAUDE.md`).

Fresh authentication only works with a session. So item 2 depends on
item 1.

## Options

### A. The session cookie and CSRF on the TFE-compatible API

The TFE-compatible adapter takes the same `sessionauth.Checker` as the
Pulumi adapter. A request with a bearer token works as today. A request
with the cookie and no bearer token authenticates as the session, with the
origin and CSRF checks. Force-unlock and the download read the age of the
authentication of the session, under `freshAuthSeconds`.

- Gives: one sign-in model for every API that the console calls (ADR 5). No
  new route and no new credential. The audit entry names the session. The
  checks are code that exists, with its tests.
- Costs: the TFE-compatible API gets a second credential. The adapter must
  refuse a request that sends both, or pick one by a fixed rule. A route
  that the CLI calls with `GET` gets the cookie with no CSRF check, so the
  origin check is the only cross-site guard on reads. The download is a
  `GET` that answers secret values. It needs the origin check to hold.
- Risk to the CLI contract: low. The CLI sends no cookie. The e2e suite
  shows that each supported version still works.

### B. A console-only token exchange

The console exchanges its session for a short-lived access token with a
claim of the fresh authentication, for example 5 minutes, bound to one unit
and one action. It sends that token as a bearer token to the
TFE-compatible API.

- Gives: the TFE-compatible API keeps one credential. No cookie and no
  CSRF on that API.
- Costs: a new kind of token with a fresh-authentication claim. This
  changes ADR 227 decision 6 ("an access token never gives a fresh
  authentication"). A token in the memory of the console can leak through
  a script on the page, which an HttpOnly cookie cannot. A new route, new
  storage and new audit entries. The route is for the console only, which
  ADR 17 discourages.

### C. Tool-neutral routes for the two actions

The tool-neutral API (ADR 210) gets a force-unlock and a download of a unit
state version. It already takes the session. The console calls these
routes, and the TFE-compatible routes stay for the CLI.

- Gives: the TFE-compatible API does not change. The tool-neutral API
  already has sessions, CSRF and the error codes of ADR 182.
- Costs: two routes for one action, with the same audit entries and the
  same checks. The two must stay equal. ADR 13 asks that cross-tool code
  calls the tool package, so the tool-neutral route calls `pkg/workspace`,
  not the TFE adapter. It is more code than option A.

### D. Do nothing

The console sends an access token. No fresh authentication is possible.

- Gives: no change.
- Costs: the direction of the product manager is not met. A stolen access
  token can force-unlock and download state with no further step.

## Proposal

Option A. It reuses the session check that three APIs already use, it adds
no credential and no route, and it keeps ADR 227 decision 6. The adapter
refuses a request with both a bearer token and the cookie, with 400. A
force-unlock or a download with the session needs a fresh authentication.
The same request with a bearer token keeps working with no step, so the CLI
contract does not change.

Option C stays the fallback if the review of option A finds that the
origin check alone does not protect the download.

## Open questions

1. Does a download with a session need a fresh authentication at each
   download, or one window for each session? ADR 227 gives one window. A
   person who downloads 5 versions in 2 minutes then needs one step.
2. Does the http backend also take the session? The console does not call
   it. The proposal says no.
3. Does a request with a bearer token stay without a step for a person's
   personal token? ADR 212 decision 3 refuses an access token for a change
   that reduces security. A force-unlock and a download are not such a
   change. The proposal keeps them working with a token, because the CLI
   cannot do a step.
4. Is a `GET` with the cookie from a same-site but other-origin page safe
   enough for the download? This decides between options A and C.

## Outcome

Empty until the RFC is accepted or dropped.
