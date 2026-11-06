# Proxy rules

A proxy rule is one allow line of the job proxy. The job proxy runs next to
a job and stands between the job and the network. It allows only the
requests that a proxy rule allows. It refuses all other requests.

A proxy rule set is the list of proxy rules of one scope. This page gives the
format of a proxy rule set, its variables, its limits and the refusals.

The routes that save and read a proxy rule set are coming later. Until then,
no unit holds a proxy rule set, and no job starts a job proxy.

## Format

A proxy rule set is one JSON object with the field `rules`. Each item of
`rules` is one proxy rule.

```json
{
  "rules": [
    {
      "id": "grafana-folders",
      "operations": ["plan", "apply"],
      "host": "grafana.example.com",
      "mode": "inspect",
      "methods": ["GET", "POST"],
      "paths": ["/api/folders/{{ unit.labels.team }}/**", "/api/search"],
      "credential": "grafana-token"
    },
    {
      "id": "registry",
      "operations": ["plan"],
      "host": "registry.example.com",
      "port": 8443,
      "mode": "tunnel"
    }
  ]
}
```

| Field | Required | Value |
| --- | --- | --- |
| `id` | Yes | 1 to 64 letters, digits or `-`. Each ID is unique in the proxy rule sets of a unit. |
| `operations` | Yes | One or more of `plan`, `apply`, `destroy` and `refresh`, each one time. |
| `host` | Yes | An exact DNS name in lowercase, with 2 or more labels and no dot at the end. |
| `port` | No | 1 to 65535. The default is 443. |
| `mode` | Yes | `inspect`, `tunnel` or `endpoint`. |
| `methods` | For `inspect` and `endpoint` | One or more of `GET`, `HEAD`, `POST`, `PUT`, `PATCH`, `DELETE` and `OPTIONS`, each one time. |
| `paths` | For `inspect` and `endpoint` | 1 to 64 path patterns, each one time. |
| `credential` | No | The name of an export of a profile of the unit. |
| `clientCertificate` | No | The name of an export of a profile of the unit. |
| `serverTrust` | No | `system`, or the name of a CA export of a profile. The default is `system`. |

The API shows the effective value of `port` and `serverTrust`, also when the
document does not give them.

### Modes

| Mode | What the job proxy checks |
| --- | --- |
| `inspect` | The job proxy ends the TLS connection of the job. It checks the method and the path of each request. It can add a credential. |
| `tunnel` | The job proxy checks the host and the port only. It cannot see the requests. A `tunnel` proxy rule has no `methods`, `paths`, `credential` or `clientCertificate`, and its `serverTrust` is `system`. |
| `endpoint` | The job proxy serves the host on a local port of the job. It checks the method and the path of each request. |

One host and port have one mode in all the proxy rule sets of a unit.

### Path patterns

A path pattern starts with `/`. Each segment between two `/` is one of these:

| Segment | Matches |
| --- | --- |
| A literal, for example `folders` | A segment with the same bytes. The case must be the same. |
| `*` | Exactly one segment. |
| `**` | One or more segments. It must be the last segment. `/api/**` does not match `/api`. |
| `{{ variable }}` | The value of the variable. The template must be the full segment. |

`/` alone matches the root path only. A literal segment obeys the rules of a
canonical path, below. A segment cannot mix `*` with other characters.

## Variables

The job start fills the templates of the path patterns one time. The rendered
proxy rules then stay the same for each phase of the job. The job spec holds
the SHA-256 of the rendered proxy rules.

| Variable | Value |
| --- | --- |
| `unit.tool` | The tool code of the unit, for example `opentofu`. |
| `unit.namespace` | The namespace of the unit. |
| `unit.name` | The name of the unit. |
| `unit.labels.<key>` | The value of the label `<key>` of the unit. A key with `/` cannot be in a template. |
| `operation` | The operation of the job, for example `plan`. |
| `inputs.<name>` | The string answer `<name>` of the Catalog request of the job. |
| `profile.<name>.<key>` | The value `<key>` of the profile `<name>`. The job start refuses a secret value. |

The job start fails when a value:

- is not set,
- is empty,
- is a secret value,
- contains `*`,
- is not exactly one canonical path segment. For example, the job start
  refuses `team-a/x`, `..` and `a%2Fb`.

The error names the proxy rule and the variable. It never shows the value.

## Canonical paths

The job proxy refuses a path that is not canonical. It does not change a path
to make it canonical. So each allowed path has one spelling, and the job proxy
sends the path that it checked.

A canonical path:

- starts with `/` and has at most 2048 bytes,
- is valid UTF-8, also after the percent-decoding of its bytes,
- has no empty segment. So it has no `//` and no `/` at the end,
- has no `.` or `..` segment, also when encoded,
- has no `?`, `#`, `;`, `\`, space, NUL or control character,
- encodes no `/` (`%2F`), `\` (`%5C`), NUL (`%00`), `%` (`%25`) or control
  character,
- percent-encodes only a byte that must be encoded, with uppercase hex
  digits. For example, the job proxy refuses `%61` for `a` and `%c3` for
  `%C3`,
- has no character that looks like `/`, `\` or `.`, also when encoded. An
  example is the fullwidth solidus U+FF0F (`%EF%BC%8F`). A server can change
  such a character to `/`, `\` or `.` after the check of the job proxy.

The path of a request is the part before the query. The job proxy removes
`?` and the query before the check. No proxy rule checks the query, so a
proxy rule that allows a path allows each query on that path.

The job proxy compares hosts and paths byte for byte. `API.example.com` and
`api.example.com.` do not match the host `api.example.com`. The path `/API`
does not match a proxy rule for `/api`.

## Joined proxy rule sets

A namespace, a blueprint version and a unit can each hold a proxy rule set.
The job of a unit uses all of the proxy rule sets of its scopes together. A
proxy rule only allows. So the joined set allows each request that one proxy
rule allows, and no other request.

The join fails when two proxy rule sets use one ID, or give one host and port
two different modes.

## Limits

| Limit | Value |
| --- | --- |
| Size of a proxy rule set document | 64 KiB |
| Proxy rules in one proxy rule set | 256 |
| Length of an ID | 64 bytes |
| Path patterns in one proxy rule | 64 |
| Length of a path pattern | 2048 bytes |
| Length of a host | 253 bytes |

The limit of 256 proxy rules applies to each scope. A joined set can hold more.

## Refusals

The server refuses a proxy rule set document when:

- it is larger than 64 KiB or has more than 256 proxy rules,
- it is not one JSON object with the field `rules`,
- it has an unknown field, or a known field in another case, for example
  `Host`,
- one object has the same key two times,
- a proxy rule breaks a rule of the table in "Format",
- a host is an IP address. A private address in a range that the runner pool
  allows is the one exception,
- a path pattern is not canonical, or `**` is not the last segment,
- a template names an unknown variable, or fills only part of a segment.

The error names the number of the proxy rule, its ID and the field. It does
not show the value of the field.

## Decisions on a request

The job proxy checks each request against the rendered proxy rules. The first
proxy rule that allows the request decides. A denied request gets one fixed
reason:

| Reason | Meaning |
| --- | --- |
| `no-rule-host` | No proxy rule names the host and the port for the operation of the job. |
| `no-rule-matched` | A proxy rule names the host, but no proxy rule allows the method and the path. |
| `path-not-canonical` | The path is not canonical. |
| `credential-over-http` | The proxy rule that allows the request adds a credential, but the request uses plain HTTP. |

A `tunnel` proxy rule allows each connection to its host and port.
