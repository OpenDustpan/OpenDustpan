# Send messages to a Slack channel

This guide shows how to connect a Slack channel to a namespace with an
incoming webhook, how to send a test message, and what the message looks like.

A Slack destination is a notification destination of the kind `chat` with the
platform `slack`. The API names it a `sink`. See
`docs/guides/notification-destinations.md` for the other kinds, and
`docs/reference/notification-destinations-api.md` for every field and error.

**This version sends one message to a Slack destination: the test message.**
The server does not yet send notifications of events to a destination by
itself.

## Before you start

You need:

- The action `namespace:manage-notifications` on the namespace. The built-in
  roles `admin`, `account-administrator` and `site-administrator` hold it.
- An incoming webhook URL from a Slack app that you control. Slack shows it
  in the settings of the app, under "Incoming Webhooks". It looks like
  `https://hooks.slack.com/services/T000/B000/XXXX`.
- A server that can open an `https` connection to the host of the URL.

The URL holds the token of the webhook. Treat the whole URL as a secret.

## Connect the channel

`POST /api/dustpan/v1/namespaces/<namespace>/sinks` makes the destination.

```sh
curl -X POST -H "Authorization: token $DUSTPAN_TOKEN" \
  -d '{"kind":"chat","platform":"slack","name":"Ops channel","url":"https://hooks.slack.com/services/T000/B000/XXXX"}' \
  https://dustpan.example.com/api/dustpan/v1/namespaces/acme/sinks
```

The answer is 201. It shows the ID of the destination, the platform, the name
and the host of the URL, and `"urlSet": true`. It never shows the URL. The
server seals the URL with the data key of the namespace. No route answers it
again, and no log line, audit entry or error text holds it. To change the
URL, send a `PUT` with a new `url`.

The server accepts these hosts for a Slack destination:

| Host | Service |
| --- | --- |
| `hooks.slack.com` | Slack |
| `hooks.slack-gov.com` | GovSlack |

Any other host answers 400 `sink.platform-host` when you make or change the
destination. The message gives the host rule. A host with a port, a URL with user information, and a URL that
is not `https` fail too.

## Send a test message

`POST /api/dustpan/v1/namespaces/<namespace>/sinks/<id>/test` sends one fixed
message to the channel.

```sh
curl -X POST -H "Authorization: token $DUSTPAN_TOKEN" \
  https://dustpan.example.com/api/dustpan/v1/namespaces/acme/sinks/snk-.../test
```

The answer is 200 and shows the result:

| `class` | `delivered` | Meaning |
| --- | --- | --- |
| (none) | `true` | Slack answered `ok`. The message is in the channel. |
| `refused` | `false` | The server does not send to this address, or Slack answered with a redirect. |
| `permanent` | `false` | Slack refused the message, for example because you deleted the webhook or the token is wrong. Make a new webhook and change the `url`. |
| `temporary` | `false` | The message did not arrive: a timeout, a network error, or a 5xx status. Try again later. |

The answer never holds the URL or the text that Slack answered. To find the
reason for a refusal, check the settings of your Slack app.

The server limits the tests: one test for each destination in 10 seconds and
10 tests for each namespace in 1 minute.

## What the message looks like

The message has these parts:

1. The product name of the installation, in small text.
2. The title with an icon and a word for the severity: `Info`, `Warning` or
   `Critical`.
3. The summary.
4. The fields: the namespace, the object, the person who caused the event, and
   the attributes of the notification.
5. One line with a button `Open` for each link of the notification.

The test message has the title "Test message". Its summary says that no event
happened. A phone shows the severity and the title as the text of the
notification.

The message has no button that acts, for example "Approve". A Slack webhook
cannot prove that it belongs to a Slack app of your installation, so the
server sends link buttons only.

## Names with markup

A name of a stack, a workspace or a namespace comes from a person. The server
encodes each text before it sends it to Slack. A name such as `<!channel>` or
`<@U123ABC>` shows as text and does not mention anyone. Characters that
start bold, italic, strike-through or code text in Slack show as plain text.
The server cuts a text that is too long for Slack and ends it with `…`.

The fallback text of a Slack message includes zero-width spaces. The server
puts a zero-width space after `@`, `#`, a dot and a colon. It also puts one
after `*`, `~` and `` ` ``, and after an `_` that is not inside a word. Copying a name from the fallback text, for example `app.prod`, can
copy those zero-width spaces. The copied value can then differ from the name
that the person typed.

## Limits

- The server makes one attempt for each message. It does not follow a redirect.
- The server waits at most 10 seconds for Slack.
- Slack does not let you delete or edit a message that a webhook posted. Delete
  it in Slack.
- A message is at most 64 KiB.
