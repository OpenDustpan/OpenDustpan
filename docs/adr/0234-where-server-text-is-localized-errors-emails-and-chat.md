# 234. Where the server localizes its text: errors, emails and chat messages

Status: accepted · 2026-10-18. Extends ADR 145 decisions 1 and 24 and ADR 181 decision 8. Supersedes in part ADR 182 decision 5. Answers an open consequence of ADR 181

## Context

ADR 181 translates the console. ADR 182 gives each API error a code and
arguments, so that the console shows the error in the language of the
person, and keeps the server text in English. ADR 181 leaves email and chat
messages in English, and says that a later decision can use the `language`
of the preferences for them. ADR 145 sends notices to people by email and
by chat. A notice has a type, attributes and an English summary of at most
500 bytes.

The project owner asked where the server localizes the text that reaches
people: error messages, notification emails and chat messages. The project
manager decided it under the owner's decision rule. Each such decision has
a "Benchmark:" line. On 18 October 2026 the owner answered the questions of
RFC 78 that this ADR asked: the Go dependency, the form of dates, the
language of a person who never set one, and the product name in chat
messages. Those decisions have the label "(owner)".

Facts:

- The console sits between the server and a person for an API error. No
  client of the project sits between the server and an inbox or a chat
  channel. Only the server can choose the words of an email.
- The CLIs of the tools show the server's error text as it is. Operators
  search for that text in logs and in issues.
- The preferences of a person hold `language`, `formattingLocale`,
  `hourCycle`, `dateStyle`, `firstDayOfWeek` and `timeZone` (ADR 181
  decision 8), and `digits` (ADR 231 decision 5). `language` is `null` when the person never set it. The
  console then uses the browser, which the server does not know.

## Decision

### Text that stays in English

1. **API errors: a code and arguments, the client renders.** ADR 182 stays.
   The server never translates an error, and ignores `Accept-Language`. The
   console shows the message of the code in the language of the person.
   ADR 182 decision 5 stays for errors. Its sentence "The server never
   translates" now applies to errors, logs and the text of decision 2
   only.

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
5. **The language of a notice (owner).**

   | Recipient | The language, in this order |
   | --- | --- |
   | A person (ADR 145 decision 24) | `language` in the preferences of the person, then `resolvedLanguage`, then the installation setting `localization.defaultLanguage` |
   | A subscription to a channel or an address | the new field `language` of the subscription, then `localization.defaultLanguage` |

   `resolvedLanguage` is a new read-only field of the preferences. After
   sign-in, the console writes to it the language that it matched from the
   browser (ADR 181 decision 2). A person never sets it, and their own
   `language` always wins over it. So a person who never set a language
   gets emails in the language of their console.

   `localization.defaultLanguage` is a BCP 47 tag of 2 to 35 characters. Its
   default is `en`. An administrator sets it through the settings resource
   of ADR 212 decision 7. The server matches a tag to its message files as
   ADR 231 decision 4 does: on the language subtag after canonicalization,
   with `golang.org/x/text/language`. A tag with no match gives `en`. The
   server reads the preferences again before each attempt, as ADR 145
   decision 15 does for contacts.
6. **The formats of a notice (owner).** The server formats numbers with
   the formatting locale and the `digits` override of the person, through
   `golang.org/x/text`. With no formatting locale, it uses the language of
   the notice. A date and a time have one fixed form in every language,
   for example `2026-10-18 15:33 UTC`, in the time zone and the digits of
   the person. With no time zone, it uses UTC. Each time names its time
   zone, because a person often reads a notice on another device. A
   subscription has no formatting locale. It uses the language of the
   notice and UTC. A date form for each locale in notices stays an open
   question of RFC 78, because it needs the date data of CLDR.
7. **Text that a person wrote stays as written.** A title, a comment or a
   summary that a person or a tool wrote is never translated. An email puts
   it in an element with `dir="auto"`. A chat message and a subject wrap it
   in the Unicode isolates U+2068 and U+2069 (ADR 231 decision 9). An
   identifier stays as it is, as in ADR 182 decision 4. The agent's
   answers in decision 10 also stay as the agent wrote them.
8. **The same status rules as the console.** Server message files follow
   ADR 232 and ADR 233. A notice in a language with the status
   `machine-translated` ends with the note of ADR 232 decision 4, in that
   language and in English, with a report link if the installation shows
   it. The link of a notice holds the language and the notice type, never
   a value of the notice, and has the rules of ADR 232 decision 5. A high-stakes notice that is not
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
    of a run gives the model the language, the term lists of ADR 233 and the
    rule that machine-made values stay as they are. Tool names, tool output
    and the descriptions of tools stay in English. An answer of Dusty has no
    translation status and no machine-translation note, because it is not
    text of a message file.

### Where the code lives

11. **One package for server messages (owner).** A package in `internal/`
    holds the server message files and their formatter. The formatter is a
    small subset of ICU MessageFormat: plain text, arguments, `plural`,
    `select` and the named formats of the wrapper. It takes the plural
    rules from `golang.org/x/text/feature/plural`. `golang.org/x/text` is a
    direct dependency, with the approval of the owner, for language
    matching, plural rules and number formatting. A test checks the
    formatter against the results of FormatJS for the same messages.
12. **Who calls the formatter.** The delivery operation of `pkg/notify`
    reads the language and the format fields of the recipient through a new
    method of `notify.Contacts`. The conformance tests of that method go in
    `notifytest` before an adapter implements it. The email and chat
    adapters of `notify.Sender` call the formatter. `pkg/notify` and the
    other domain packages never import it.

## Consequences

- ADR 181 said that email and chat stay in English. This ADR replaces that
  consequence, once the formatter of decision 11 lands.
- ADR 182 decision 5 says that the server never translates. Decision 1
  limits that sentence to errors, logs and machine-facing text.
- The server reads `language`, `resolvedLanguage` and the format fields of
  the preferences. The document of ADR 181 decision 8 gets one field from
  this ADR, the read-only `resolvedLanguage` (decision 5). ADR 231
  decision 5 adds `digits`, and the server reads it too.
- `golang.org/x/text` moves from an indirect to a direct dependency in
  `go.mod` with the formatter.
- The settings resource gets `localization.defaultLanguage`. A subscription
  of ADR 145 gets the field `language`, a BCP 47 tag or `null`.
- Each notice type gets its server messages in `en` first. The other
  languages follow with machine translation (ADR 232 decision 8).
- The console renders audit entries from their type and fields. It never
  shows a translated audit entry from the server.
