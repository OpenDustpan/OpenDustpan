# Make the first administrator

This guide shows how the operator of a new installation makes the first
person who holds a role. A person who signs in to the console for the first
time has no role. The bootstrap admin token cannot give a role, because each
change of a grant needs a console session with a fresh sign-in. The one-time
code of the first administrator closes this gap. You need no shell on the
server.

## What you need

- The bootstrap admin token, in `$TOKEN` in the commands below.
- The public URL of the server, with HTTPS. In the commands below it is
  `https://dustpan.example`.
- A person who can sign in to the console: a local user with a password
  ([Local users with a password](local-users.md)), or a person of an identity
  provider who signed in once ([Console sign-in](console-sign-in.md)).

## Make the first administrator

1. Make the person. For a local user, make the user and let the person set a
   password with the set-up code
   ([Local users with a password](local-users.md#make-a-local-user)). The
   answer holds the principal ID, `prn-...`. For a person of an identity
   provider, the person signs in once, and you read the principal ID in the
   principal list. The person has no role yet.

2. Read the state. The installation has no first administrator yet:

   ```sh
   curl -H "Authorization: Bearer $TOKEN" \
     https://dustpan.example/api/server/first-administrator
   ```

   ```json
   {"made": false, "offer": null}
   ```

3. Ask for the one-time code, with the principal ID and a reason:

   ```sh
   curl -X POST -H "Authorization: Bearer $TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"principal":"prn-...","reason":"first administrator of the new installation"}' \
     https://dustpan.example/api/server/first-administrator
   ```

   ```json
   {"principal": "prn-...", "code": "<43 characters>", "expiresAt": "2026-11-19T10:10:00Z"}
   ```

   The answer is `201`. It is the only place that holds the code. The answer
   has `Cache-Control: no-store`. The server keeps a digest of the code only,
   and never writes the code to a log or to the audit log. The code works
   once, for this person only, and ends after 10 minutes. A new request
   replaces the code. The reason is 1 to 500 characters of text with no
   control character.

   Give the code to the person on another channel.

4. The person signs in to the console, and sends the code in that console
   session. The sign-in must be fresh, that is, not older than the window of
   the session policy. A local user signs in with the password. The command
   keeps the session cookie in a file:

   ```sh
   curl -c session.txt -X POST -H "Content-Type: application/json" \
     -d '{"userName":"alice","password":"<password>"}' \
     https://dustpan.example/api/dustpan/v1/sign-in/password
   ```

   Read the CSRF value of the session. It is the field `csrfToken`:

   ```sh
   curl -b session.txt https://dustpan.example/api/server/session
   ```

   Send the code:

   ```sh
   curl -b session.txt -X POST \
     -H "Origin: https://dustpan.example" \
     -H "X-Dustpan-Csrf: <csrfToken>" \
     -H "Content-Type: application/json" \
     -d '{"code":"<code>"}' \
     https://dustpan.example/api/server/first-administrator/redeem
   ```

   ```json
   {"principal": "prn-...", "role": "site-administrator", "grant": "grt-...", "madeAt": "2026-11-19T10:04:00Z"}
   ```

   In one transaction, the server makes the `site-administrator` grant on
   the installation, spends the code and stores the marker "first
   administrator made". The person can now do each action that needs a fresh
   sign-in.

5. Read the state again:

   ```sh
   curl -H "Authorization: Bearer $TOKEN" \
     https://dustpan.example/api/server/first-administrator
   ```

   ```json
   {
     "made": true,
     "principal": "prn-...",
     "grant": "grt-...",
     "session": "<session ID>",
     "createdBy": "<subject of the bootstrap administrator>",
     "madeAt": "2026-11-19T10:04:00Z",
     "offer": null
   }
   ```

   The grant is a stored grant. `GET /api/dustpan/v1/me/grants` in the
   session of the person lists it. The audit log of the installation
   (`GET /api/audit/entries?organization=@server`) has one entry for the
   request of the code and one entry for the redeem. No entry holds the code.

## Errors

Each error answer has an error code. The table gives the one command that
helps.

| Answer | Error code | Cause | What to do |
| --- | --- | --- | --- |
| `400` | `first-administrator.code-invalid` | The code is wrong, used, older than 10 minutes, or made for another person. One text covers the four cases. | Ask for a new code with `POST /api/server/first-administrator` (step 3). |
| `403` | `mfa.fresh-step-required` | The sign-in is too old, or the request has an access token. An access token never gives a fresh sign-in. | Sign in again in the console. Use the path in the answer (`reauthenticate`), and send the code in the new session. |
| `403` | `access.denied` | The request to make the code does not use the bootstrap admin token. A personal token, a temporary administrator and a console session are refused. | Send the request with the bootstrap admin token (step 3). |
| `409` | `first-administrator.already-made` | The installation has the marker. | Recover with `opendustpan host create-temp-admin` (see below). |
| `400` | `first-administrator.principal-invalid` | The principal is not a live, enabled person, or it is a break-glass account, a temporary administrator, a service principal or a `dev:` subject. The audit entry of the refusal names the rule. | Send the principal ID of a live person who is not one of these. |
| `400` | `first-administrator.reason-invalid` | The reason is blank, longer than 500 characters, not UTF-8, or has a control or format character. | Send a plain reason of 1 to 500 characters. |
| `409` | `first-administrator.grant-exists` | The person holds the `site-administrator` role already. The server spends no code and stores no marker. | Ask for a code for another person, or let the person make the next grants. |

A wrong code spends one unit of the sign-in failure budget of the source
address. Too many wrong codes answer `429` until the budget refills.

## After the first administrator

- The route works one time. After the marker, a new `POST` answers `409`
  with `first-administrator.already-made`, for each caller.
- The bootstrap admin token stays valid after the redeem. The redeem does
  not end it. Keep it secret. To replace it, restart the
  server with a new value of `DUSTPAN_ADMIN_TOKEN`.
- If the installation loses every administrator after the marker, this route does not help.
  The recovery is `opendustpan host create-temp-admin` on the host of the
  server ([Host recovery](host-recovery.md)).
- A restore of a backup carries the marker with the grants. It never carries
  the code.
- The setting `freshAuthEnabled` does not apply to the redeem. With the
  setting off, the redeem still needs a fresh sign-in.
