# Git host account links API

A Git host account link maps an account of a Git host, for example a GitHub
account, to a principal of the installation. Only an account that a person
linked can run a comment command of a review request. The command runs as that
principal, with the actions of that principal. This page describes the routes
that make, list and delete the links.

The paths of the API start with `/api/dustpan/v1`.

## What is available now

The server does not yet receive the signed events of a Git host. So no Git host
account can prove itself, and the server does not mount the routes that start,
show and accept a link request. Until that work is done, no link can be made,
and the list is empty. These routes work now:

- `GET /me/linked-accounts`
- `DELETE /me/linked-accounts/{kind}/{host}/{account}`
- `DELETE /principals/{principal}/linked-accounts/{kind}/{host}/{account}`

The sections that describe the other three routes say how they will work. They
are not available yet.

## How a link will be made

A link needs proof from two sides. Neither side alone makes a link.

1. The person signs in to the console and starts a link request. The server
   answers with a one-time token. It shows the token one time only, and it
   keeps only the SHA-256 of the token.
2. The Git host account proves itself. The account posts the token in a
   comment, and the Git host sends a signed event to the server. The server
   takes the ID of the account from the signed event. A person never types
   the ID of the account.
3. The person shows the request in the console. The page names the account
   that proved itself, the login of the account, and the principal that the
   account is linked to now, if any.
4. The person accepts. The link names the account of the proof and the
   principal that started the request.

A request is good for 5 minutes from its start. It is good for one accept. A
person can have 10 requests at one time that are not accepted and not expired.

## Paths

| Method and path | Action | Answer |
| --- | --- | --- |
| `POST /me/linked-account-requests` (not available yet) | `caller:manage-git-host-links` | 201 with the token |
| `POST /me/linked-account-requests/show` (not available yet) | `caller:manage-git-host-links` | The state of the request |
| `POST /me/linked-accounts` (not available yet) | `caller:manage-git-host-links` | 201 with the link |
| `GET /me/linked-accounts` | `caller:read` | The links of the person |
| `DELETE /me/linked-accounts/{kind}/{host}/{account}` | `caller:manage-git-host-links` | 204 |
| `DELETE /principals/{principal}/linked-accounts/{kind}/{host}/{account}` | `server:manage-principals` | 204 |

Every person that signs in holds `caller:manage-git-host-links` with no grant.
The four routes that change or show a request or a link accept a console
session only. An access token gets 403 with the code
`git-host.link-person-required`, except on the accept, which answers
`mfa.fresh-step-required` because it needs a recent sign-in. This holds for
the token of an agent too. A break-glass account cannot make a link.

The token is in the body of the request and never in the URL, so no access log
holds it.

## Start a request (not available yet)

`POST /me/linked-account-requests` takes no body.

```json
{
  "token": "Mf3k...",
  "createdAt": "2026-10-30T10:00:00Z",
  "expiresAt": "2026-10-30T10:05:00Z"
}
```

Post the token from the Git host account that you want to link. The answer
shows the token one time. Start a new request when you lose it.

## Show a request (not available yet)

`POST /me/linked-account-requests/show` takes `{"token": "..."}`.

```json
{
  "createdAt": "2026-10-30T10:00:00Z",
  "expiresAt": "2026-10-30T10:05:00Z",
  "proved": true,
  "account": { "kind": "github", "host": "github.com", "id": "583231", "login": "octocat" },
  "currentLink": {
    "principal": "prn-8f2c",
    "displayName": "Ana",
    "linkedAt": "2026-10-01T09:00:00Z"
  }
}
```

`account` is absent until a Git host account proved itself. `currentLink` is
absent when that account has no link. Check the account before you accept:
each comment command of that account will run as you.

A token that no request of yours holds gets 404 with the code
`git-host.link-request-not-found`. The answer is the same for a token of
another person and for a token that does not exist.

## Accept a request (not available yet)

`POST /me/linked-accounts` takes the token and, when the account has a link,
the principal that the page showed.

```json
{ "token": "Mf3k...", "previousPrincipal": "prn-8f2c" }
```

The body has no account field. The server refuses a body with a field that it
does not know.

```json
{
  "link": {
    "account": { "kind": "github", "host": "github.com", "id": "583231", "login": "octocat" },
    "principal": "prn-5a10",
    "linkedAt": "2026-10-30T10:02:00Z"
  },
  "replacedPrincipal": "prn-8f2c"
}
```

`replacedPrincipal` is absent when the accept replaced no link.

An accept needs a recent sign-in: a console session whose sign-in is older
than the token freshness window answers 401 with the code
`mfa.fresh-step-required`, so sign in again and accept.

A link never replaces another link silently. When the account has a link and
`previousPrincipal` is missing or names another principal, the answer is 409
with the code `git-host.link-conflict`, and nothing changes. Show the request
again and look at the principal that holds the link. Accept with that
principal only when you want to take the account over. The token stays good
after this refusal.

| Status and code | Cause |
| --- | --- |
| 409 `git-host.link-not-proved` | No Git host account proved itself for the request yet |
| 409 `git-host.link-request-used` | The request was accepted already |
| 410 `git-host.link-request-expired` | The request is older than 5 minutes |
| 409 `git-host.link-conflict` | The account has another link than the one you saw |
| 429 `git-host.link-limit` | You have 10 requests that are not accepted and not expired |

## List and delete

`GET /me/linked-accounts` answers `{"links": [...]}` with the links of the
person, ordered by kind, host and account ID.

`DELETE /me/linked-accounts/{kind}/{host}/{account}` deletes a link of the
person. A link of another principal, and an account with no link, both answer
404 with the code `git-host.link-not-found`. A deleted link takes effect at
once: the next comment command of the account is refused.

An administrator that holds `server:manage-principals` can delete the link of
another principal with `DELETE /principals/{principal}/linked-accounts/...`.

## Exact IDs

The server compares the kind, the host and the account ID byte for byte. Two
IDs that differ only in case are two accounts. The host is a name in lowercase
letters, with a port when the port is not the default. A path with another
spelling gets 400 with the code `git-host.link-account-invalid` or 404.

The login is text for you to read. The server never compares it. When the
account changes its login, the link stays, because the link holds the ID.

## What the server records

A link that is made, a link that replaces another one, and a link that is
deleted each write an audit entry with the action
`caller:manage-git-host-links`, or `server:manage-principals` for an
administrator. The entry names the kind, the host, the ID and the login of the
account and the principal of the link. It never holds a token.

A link that replaces another one writes a second entry, in the same
transaction. Its object is the principal that lost the link, so that principal
finds the loss in its own entries. The entry records that the principal is to be
told. The server sends no notice yet, because the notification type does not
exist, so the entry is the only record of the loss.

A refused change writes an entry with the outcome `failure`, or `denied` for a
caller that is not a person in a console session. A refused accept writes
`failure` also for an access token: the fresh check refuses it.

A link goes away when its principal is deleted. A link of a principal that is
disabled does not work. The server also leaves links and requests out of a
restore for a caller that does not hold `server:restore-access`, as it does for
the principals.
