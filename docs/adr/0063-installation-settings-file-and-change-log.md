# 63. Installation settings: precedence, a YAML file, and a record of each change

Status: accepted · 2026-10-14

## Context

[RFC 68](../rfc/0068-installation-settings.md) collects the questions about
installation settings. Today every setting is a flag with an environment
variable, declared once in `internal/cli/settings.go`. There is no
configuration file.

The project owner answered on 2026-10-14:

- About the file format: "Well JSON is a strict subset of YAML. We can
  support YAML instead but document a JSON shape with comments if you want."
- About the dependency `go.yaml.in/yaml/v3`, with the strict decoder below:
  "That sounds good."
- About logs and audit: "log the configuration on startup, WARN for any
  overrides (keys that are specified in multiple levels, even if they are
  duplicates with the same value), and emit an application log and an audit
  log if keys are changed in the running application (via the console or
  via a mounted config reload). We should obviously not log sensitive field
  values, but for others I think we should."

Reports say that its maintainers archived `gopkg.in/yaml.v3`.
`go.yaml.in/yaml/v3` is its
compatible fork under the YAML organization, and Forgejo moved to it in
August 2025.

Accepted by the project lead under the owner's delegation of 2026-10-13.
The owner approved the dependency and the logging rules personally.

## Decision

1. **Precedence: option A of RFC 68.** Flag, then environment variable,
   then the file, then the database, then the built-in default. A value from
   a flag, an environment variable or the file locks the setting. The API
   refuses to change a locked setting.
2. **One YAML file.** `--config` (`DUSTPAN_CONFIG`) names it. The server
   decodes it with `go.yaml.in/yaml/v3`, pinned to a release at least two
   weeks old. The documentation shows each example in the JSON shape with
   `#` comments. That text is valid YAML.
3. **Strict decoding.** The server refuses a file that:
   - is larger than 64 KiB,
   - has an anchor or an alias,
   - has a duplicate key,
   - has a key that no setting declares,
   - has a value of the wrong type. A boolean is only `true` or `false`. A
     string setting keeps `no` as a string.

   At start, a refused file stops the start. At a reload, the server keeps
   the old configuration (RFC 68, "Validation"). The error names the key and
   the rule, never the value.
4. **Secrets never come from a flag.** A secret comes from an environment
   variable or from a file that a `*_FILE` key names (ADR 23 decision 1).
5. **Only the file reloads.** SIGHUP reloads it, and a poll of its content
   hash (10 seconds) catches a change that no one signals (RFC 68 option D).
   A flag and an environment variable change only at a restart.
6. **Each declaration says whether a setting is sensitive.** A secret, a
   credential and the database URL are sensitive (ADR 32 decision 7, ADR 60
   decision 16). A log line or an audit entry shows a sensitive setting only
   as `set` or `not set`, with its source. It never shows the value or a part
   of it, and never a hash of it.
7. **A log of the configuration at start.** After it reads every layer, the
   server writes one INFO line for each declared setting, with the key, the
   effective value and the layer it came from (`flag`, `env`, `file`,
   `database` or `default`). Rule 6 applies.
8. **A warning for each override.** When a key has a value in more than one
   layer, the server writes one WARN line. The line names the key, the layer
   that wins and each layer that lost. It writes the line also when the
   values are equal. The values follow rule 6. The server writes these lines
   at start and after each reload that it applies. The default is not a
   layer for this rule.
9. **A record of each change while the server runs.** A change of an
   effective value through the API (the console uses the same API, ADR 17)
   or through a reload of the file writes:
   - one INFO application log line for each changed key.
   - one audit entry of the kind `change` in the scope `@server` (ADR 60
     decision 7). The entry holds the changed keys, and for each key that is
     not sensitive, the old and the new value (RFC 68 "Audit", option B).

   An API change names the principal as the subject. A file reload names the
   instance ID as the subject and `config-file` as the channel. The person
   who edited the file is outside the server. The audit log of the platform
   that holds the file (for example Kubernetes) records that person.
10. **A change while the server is off.** Each instance stores the
    effective static layer that it last applied (sensitive values only as
    `set` or `not set`). At start, when the new static layer differs, the
    server writes the audit entry of decision 9 with the channel `startup`.
    Without this, an edit of the file or of an environment variable between
    two runs leaves no audit entry.
11. **Fail closed.** A change through the API commits with its audit entry
    in one transaction, as every other audited change does. When the audit
    log is on and the server cannot write the entry for a file reload, the
    server keeps the old configuration and the reload fails.

## Not decided here

RFC 68 keeps these open: the directory form of the file (RFC 68 option C),
the settings API shape and its actions, which settings are live, the
behavior across instances, and the settings that a restore keeps.

## Consequences

- `go.yaml.in/yaml/v3` becomes a direct dependency. `make licenses` and
  `make trivy` check it.
- Each declaration in `internal/cli/settings.go` gets a field that marks it
  sensitive. A test fails when a setting that holds a secret, a credential
  or a URL with user information has no such mark.
- The audit entry gets an optional field for the changed keys and their
  values. It is optional in the hash encoding (ADR 60 decision 5).
- A canary test sets each sensitive setting to a known value in every layer
  and checks that no log line and no audit entry holds it.
