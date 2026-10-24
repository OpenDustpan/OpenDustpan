# Preferences API

The preferences of a person are the settings of the console that follow the
person to every device: the theme, the language, the formats and the
time zone. This page describes the two routes that read and change them. The
console uses these routes. A script can use them too.

The paths of the API start with `/api/dustpan/v1`.

## Paths

| Method and path | Action | Answer |
| --- | --- | --- |
| `GET /me/preferences` | `caller:read` | The full document |
| `PATCH /me/preferences` | `caller:manage-preferences` | The full document after the change |

Every person that signs in holds both actions with no grant. The routes work
on the person of the request. A request that acts as a person, for example the
run of an agent, reads and changes the preferences of that person. A caller
that is not a person gets 403. This includes a service principal, a job token
and the bootstrap admin token.

## The document

```json
{
  "theme": "terminal",
  "colorMode": "system",
  "terminalSchemes": { "light": "solarized-light", "dark": "tokyo-night" },
  "shortcutHints": "theme",
  "singleKeyShortcuts": "theme",
  "shortcutHintStyle": "brackets",
  "language": "ar",
  "formattingLocale": "ar-EG",
  "hourCycle": null,
  "dateStyle": "locale",
  "firstDayOfWeek": "sat",
  "timeZone": "Asia/Riyadh",
  "digits": null,
  "resolvedLanguage": "ar",
  "updatedAt": "2026-10-18T09:30:00.000Z"
}
```

A field that the person never set has its default. A person that never changed
a preference has no stored row, and `updatedAt` is `null`.

| Field | Values | Default |
| --- | --- | --- |
| `theme` | `default`, `terminal` | `default` |
| `colorMode` | `system`, `light`, `dark` | `system` |
| `terminalSchemes.light`, `terminalSchemes.dark` | A scheme ID: 1 to 64 characters of `a-z`, `0-9` and `-` | `opendustpan` |
| `shortcutHints`, `singleKeyShortcuts` | `theme`, `on`, `off` | `theme` |
| `shortcutHintStyle` | `brackets`, `angles`, `inverse` | `brackets` |
| `language` | A BCP 47 tag of 2 to 35 characters, or `null` | `null`: the browser decides |
| `formattingLocale` | A BCP 47 tag of 2 to 35 characters with no `-u-` or `-x-` extension, or `null` | `null`: the browser decides |
| `hourCycle` | `h12`, `h23` or `null` | `null`: the formatting locale decides |
| `dateStyle` | `locale`, `iso8601`, `dmy`, `mdy` | `locale` |
| `firstDayOfWeek` | `mon`, `tue`, `wed`, `thu`, `fri`, `sat`, `sun` or `null` | `null`: the formatting locale decides |
| `timeZone` | An IANA time zone name of 1 to 64 characters, or `null` | `null`: the browser decides |
| `digits` | `latn`, `arab` or `null` | `null`: the formatting locale decides |
| `resolvedLanguage` | A BCP 47 tag of 2 to 35 characters, or `null` | `null` |
| `updatedAt` | The time of the last change, as an RFC 3339 string in UTC with milliseconds, or `null` | `null` |

The server checks the shape of a language tag and of a scheme ID. The console
has the list of languages and of schemes, so a new one needs no server
release. The server checks a time zone against its time zone database.
`resolvedLanguage` is the language that the console chose. The console sets
it, and the server stores it without a decision of its own. For the rules of
`language`, `formattingLocale` and `digits`, see
[Languages and digits](languages-and-digits.md).

## Change preferences

`PATCH` takes a JSON merge patch. Send the header
`Content-Type: application/merge-patch+json`.

- A field that the body leaves out keeps its value.
- `null` sets a field back to its default.
- `terminalSchemes` is an object with the members `light` and `dark`. A patch
  of one member keeps the other. `"terminalSchemes": null` sets both back to
  the default.
- `updatedAt` cannot change. A body that has it answers 400.
- A body that is not one JSON object answers 400. So does a body that names
  one field two times.
- A body over 4096 bytes answers 413.
- Any other `Content-Type` answers 415.

A patch that changes no value answers 200 with the full document. It does not
change `updatedAt`, and it writes no audit entry.

## Versions

`GET` and `PATCH` answer with the `ETag` header. It holds the version of the
document in quotes. The version changes with each change of a preference. A
person that never changed a preference has the version `"0"`.

`PATCH` can take the `If-Match` header with the `ETag` of an earlier read.
When the version is not the current one, the server answers 412 and changes
nothing. It writes no audit entry. Use this to not overwrite a change that
another device made after your read. A request with no `If-Match` header
always applies. `If-Match: *` also always applies. A weak tag, for example
`W/"5"`, never matches, so it answers 412. An empty `If-Match` header, two
`If-Match` headers and any other value that is not one version in quotes
answer 400.

```sh
curl -i "$SERVER/api/dustpan/v1/me/preferences" -H "Authorization: Bearer $TOKEN"
# ETag: "1760779800000"

curl -X PATCH "$SERVER/api/dustpan/v1/me/preferences" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/merge-patch+json" \
  -H 'If-Match: "1760779800000"' \
  -d '{"theme": "terminal"}'
```

```sh
curl -X PATCH "$SERVER/api/dustpan/v1/me/preferences" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/merge-patch+json" \
  -d '{"language": "ar", "digits": null}'
```

## Errors

An error has the fields `code` (the HTTP status), `message` and `errorCode`.
A client selects its text from `errorCode`. An error of a field has
`errorArgs.field`, which names the field.

| Status | `errorCode` | When |
| --- | --- | --- |
| 400 | `preferences.body-invalid` | The body is not one JSON object, or it names a field two times |
| 400 | `preferences.field-unknown` | The body has a field that the document does not have |
| 400 | `preferences.field-read-only` | The body has `updatedAt` |
| 400 | `preferences.field-invalid` | A value is not allowed. `errorArgs.field` names the field. |
| 400 | `preferences.if-match-invalid` | `If-Match` is empty, is sent twice, or is not one version in quotes or `*` |
| 401 | | No valid token |
| 403 | `preferences.person-required` | The caller is not a person |
| 403 | `access.denied` | The caller lacks the action |
| 403 | `access.ip-not-allowed` | The client address is outside the IP allowlist of the namespace |
| 413 | `preferences.body-too-large` | The body is over 4096 bytes |
| 412 | `preferences.precondition-failed` | `If-Match` holds a version that is not the current one, or a weak tag |
| 415 | `preferences.media-type-unsupported` | The content type is not `application/merge-patch+json` |
| 501 | `preferences.unavailable` | The store of the server keeps no preferences |
| 503 | | The server is in maintenance mode, for a `PATCH` |

An error never holds a value or a field name from the request.

## Audit and backup

A change writes one audit entry with the action `preferences-changed` in the
installation scope. The entry has the names of the changed fields, for example
`language` and `terminalSchemes.dark`, and the credential of the request. It
never has the values.

A `PATCH` that the server refuses changes nothing and writes one entry with the
same action and the same object as a change: the person. The outcome is
`denied` for a caller that is not a person, and `failure` for any other
refusal. The entry names no value and no field of the request. A `PATCH` that
answers 412 writes no entry. A refused `GET` writes no entry.

The server does not serve a stored value that fails its check, for example a
time zone that the time zone database of the server does not know any more. It
leaves the field out of the document, so the field has its default, and writes
the name of the field to the log. The person can still read and change the
other preferences. A `PATCH` that sets the field to `null` or to a good value
works.

A backup holds the preferences of each person, and a restore brings them back
with the person. A restore checks each stored value with the same rules as the
route. The preferences of a person go away when the person goes away.
