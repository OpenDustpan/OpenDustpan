# 233. Where server text is localized: errors, emails and chat messages

Status: accepted · 2026-10-18. Extends ADR 145 decisions 1 and 24, ADR 181 decision 8 and ADR 182 decision 5. Answers an open consequence of ADR 181

## Context

ADR 181 translates the console. ADR 182 gives each API error a code and
arguments, so that the console shows the error in the language of the
person, and keeps the server text in English. ADR 181 leaves email and chat
messages in English, and says that a later decision can use the `language`
of the preferences for them. ADR 145 sends notices to people by email and
by chat. A notice has a type, attributes and an English summary of at most
500 bytes.

The project owner asked where server text that reaches people is localized:
error messages, notification emails and chat messages. The project manager
decided it under the owner's decision rule. Each decision has a
"Benchmark:" line.

Facts:

- The console sits between the server and a person for an API error. No
  client of the project sits between the server and an inbox or a chat
  channel. Only the server can choose the words of an email.
- The CLIs of the tools show the server's error text as it is. Operators
  search for that text in logs and in issues.
- The preferences of a person hold `language`, `formattingLocale`,
  `hourCycle`, `dateStyle`, `firstDayOfWeek` and `timeZone` (ADR 181
  decision 8). `language` is `null` when the person never set it. The
  console then uses the browser, which the server does not know.

## Decision

### Text that stays in English

1. **API errors: a code and arguments, the client renders.** ADR 182 stays.
   The server never translates an error, and ignores `Accept-Language`. The
   console shows the message of the code in the language of the person.

   Benchmark: Google APIs (AIP-193) answer an English message for the
   developer, with an `ErrorInfo` reason and metadata. Text for an end user
   in another language is a separate, optional detail. AWS APIs answer an
   error code and an English message.
2. **Machine-facing text stays in English.** Logs, CLI output, the text of
   the tool-compatible APIs, audit entries and the body of a signed webhook
   (ADR 145) stay in English or in their machine shape. A client that shows
   an audit entry renders it from its type and its fields.

### Text that the server localizes

3. **Emails and chat notices: the server renders them in the language of
   the recipient.** The sender of each notice renders the subject and the
   body from server messages, in the language of decision 5. The domain
   packages keep their part of ADR 145: a type, attributes and an English
   summary. They never hold text in another language.

   Benchmark: GitLab sends each notification email in the preferred
   language of its recipient.
4. **One message for each type and channel.** Each notice type has a server
   message for the email subject, the email body and the chat text. Its
   arguments come from the fields and the attributes of the notification.
   A type with no message in a language uses the English message. A type
   with no server message at all uses the English summary, as today. The
   summary stays in each notice as the English fallback and in each webhook
   body.
5. **The language of a notice.**

   | Recipient | The language | With no value |
   | --- | --- | --- |
   | A person (ADR 145 decision 24) | `language` in the preferences of the person | the installation setting `localization.defaultLanguage` |
   | A subscription to a channel or an address | the new field `language` of the subscription | `localization.defaultLanguage` |

   `localization.defaultLanguage` is a BCP 47 tag of 2 to 35 characters. Its
   default is `en`. An administrator sets it through the settings resource
   of ADR 212 decision 7. A language that has no server message file gives
   `en`. The server reads the preferences again before each attempt, as
   ADR 145 decision 15 does for contacts.
6. **The formats of a notice.** The server formats numbers, dates and
   times with the formatting locale, the overrides and the time zone of the
   person. With no formatting locale, it uses the language of the notice.
   With no time zone, it uses UTC. Each time in a notice names its time
   zone, for example `2026-10-18 15:33 UTC`, because a notice is often read
   on another device. A subscription has no formatting locale. It uses the
   language of the notice and UTC.
7. **Text that a person wrote stays as written.** A title, a comment or a
   summary that a person or a tool wrote is never translated. An email puts
   it in an element with `dir="auto"`. A chat message and a subject wrap it
   in the Unicode isolates U+2068 and U+2069 (ADR 230 decision 6). An
   identifier stays as it is, as in ADR 182 decision 4.
8. **The same status rules as the console.** Server message files follow
   ADR 231 and ADR 232. A notice in a language with the status
   `machine-translated` ends with the note of ADR 231 decision 4, in that
   language and in English, with the link of ADR 231 decision 5 if the
   installation shows it. A high-stakes notice that is not
   `native-reviewed` goes in English as a whole, with `lang="en"`. High-stakes
   notices are approval requests, security notices, secret and token
   expiry, and failed jobs.
9. **The shape of an email.** An email has a plain-text part and an HTML
   part. Both carry the same text. The HTML root has `lang` and `dir`. The
   email has a `Content-Language` header. No part puts meaning in an image
   or in color alone.

   Benchmark: GitLab sends each notification email with a plain-text part
   and an HTML part.

### The agent

10. **Dusty answers in the language of the person.** Dusty writes its
    answers in the language of the person who asks, from the preferences.
    With no `language`, it uses the language of the question. The prompt
    of a run gives the model the language, the term lists of ADR 232 and the
    rule that machine-made values stay as they are. Tool names, tool output
    and the descriptions of tools stay in English.

### Where the code lives

11. **One package for server messages.** A package in `internal/` holds the
    server message files and their formatter. The senders of email and chat
    call it. The domain packages do not import it. The Go library for ICU
    MessageFormat is an open question of RFC 78. Until a decision, notices
    stay in English.

## Consequences

- ADR 181 said that email and chat stay in English. This ADR replaces that
  consequence, once the formatter of decision 11 lands.
- The server reads `language` and the format fields of the preferences. The
  document of ADR 181 decision 8 does not change.
- The settings resource gets `localization.defaultLanguage`. A subscription
  of ADR 145 gets the field `language`, a BCP 47 tag or `null`.
- Each notice type gets its server messages in `en` first. The other
  languages follow with machine translation (ADR 231 decision 8).
- The console renders audit entries from their type and fields. It never
  shows a translated audit entry from the server.
