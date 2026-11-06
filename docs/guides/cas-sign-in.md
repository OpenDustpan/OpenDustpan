# Console sign-in with a CAS server

This guide shows how the server checks a console sign-in through a CAS
server. The server speaks CAS protocol 3.0. It checks the identity of the
person at the CAS server. Then the sign-in continues as for each other
sign-in provider: see [Console sign-in](console-sign-in.md).

The server has no setting and no API route for a CAS provider yet. This
guide gives the rules that the CAS method applies, so that you can prepare
the CAS server.

## What you need

- The public URL of the server, with HTTPS. Set it with `--public-url`
  (`DUSTPAN_PUBLIC_URL`).
- The base URL of the CAS server, with HTTPS, for example
  `https://cas.example.com/cas`. The server refuses a URL with `http://`,
  a query, a fragment or user information.
- A service entry at the CAS server for the service URL of the server.

## The service URL

The server makes the service URL from its public URL and the provider ID:

```text
<public URL>/api/server/sign-in/<provider ID>/callback?state=<state>
```

The state is a random value of each sign-in. It binds the ticket to the
browser that started the sign-in. Register a service pattern that allows
this URL with any value of `state`. For example, in a service registry of
the CAS server, use a pattern such as
`^https://dustpan\.example/api/server/sign-in/corp/callback\?state=.*$`.

The server never reads the service URL from a request. It builds the URL
again when it checks the ticket. A ticket that the CAS server issued for
another service URL fails the check.

## The sign-in flow

1. The server sends the browser to `<CAS URL>/login?service=<service URL>`.
2. The person signs in at the CAS server. The CAS server sends the browser
   back to the service URL with a `ticket` parameter.
3. The server checks the ticket. It sends one request to
   `<CAS URL>/p3/serviceValidate` with the service URL and the ticket.
4. The CAS server answers with the user and the attributes of the person.

The server never sends `pgtUrl`. It asks for no proxy-granting ticket.

## The ticket

The server accepts a ticket only when all of these are true:

- It starts with `ST-`.
- It has at most 256 characters.
- It holds only the letters `A-Z` and `a-z`, the digits `0-9`, and `-`,
  `.` and `_`.

The server refuses a proxy ticket (`PT-`). It also refuses an answer that
holds a proxy-granting ticket or a list of proxies.

The server uses each ticket one time. It records the ticket before it sends
the ticket to the CAS server. A second use of the same ticket fails, also
when the CAS server would accept it again. The record of a used ticket
stays for 1 hour.

## The subject, the names and the groups

| Value | Source |
| --- | --- |
| Subject | The attribute that you name, or the CAS user when you name none |
| User name | The attribute that you name, or the CAS user when you name none |
| Name | The attribute that you name, or none |
| Email | The attribute that you name, or none |
| Groups | The attribute that you name, or none (groups off) |

Name an attribute that does not change for the subject, for example an
employee number. The subject identifies the principal. A new subject makes
a new principal.

An attribute name has 1 to 64 characters: a letter, then letters, digits,
`_`, `.` and `-`. An attribute for the subject, a name or the email must
hold one value at most. Two values fail the sign-in.

With groups on:

- An answer with no groups attribute fails the sign-in. A missing value
  never removes memberships.
- One empty value is an empty list.
- More than 1000 groups, or a group of more than 256 bytes, fails the
  sign-in.

## A fresh sign-in

For a fresh authentication, the server sends `renew=true` to the login
endpoint and to the validation endpoint. The CAS server then accepts only a
ticket of a new login.

The time of the authentication comes from the attribute
`authenticationDate` when the answer holds it. A fresh sign-in with no such
attribute uses the time of the check. A sign-in that is not fresh, with no
such attribute, has no known time of the authentication.

A fresh sign-in fails when:

- The `authenticationDate` is more than 1 minute before the start of the
  sign-in.
- The attribute `isFromNewLogin` is present and is not `true`.

Each sign-in fails when the `authenticationDate` is more than 1 minute in
the future.

## Limits of the answer

| Limit | Value |
| --- | --- |
| Size of the answer | 64 KiB |
| Depth of the XML elements | 64 |
| Time of the check | 10 seconds |
| Redirects | none |

The server refuses an answer with a document type declaration (DTD), an
entity, or a processing instruction other than the XML declaration. It also
refuses an answer with an element after its root element, and a user or an
attribute value that holds an element. The answer must be UTF-8.

## Outbound connections

The request to the CAS server goes through the egress rules of the server.
The server checks the resolved address when the connection opens. A CAS
URL whose name resolves to a private, loopback, link-local or cloud
metadata address fails, unless an operator allowed that private range.
The server follows no redirect of the CAS server.

## Errors

| What happened | Result |
| --- | --- |
| A ticket outside the rules above | The sign-in input is not valid |
| A second use of a ticket | The server used the sign-in value before |
| The CAS server refuses the ticket | The sign-in was refused |
| The answer fails a check | The identity provider answer failed a check |
| The CAS server does not answer in time, or answers `INTERNAL_ERROR` | The identity provider is not available |
