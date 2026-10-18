# 209. How each tool reads a profile

Status: accepted · 2026-10-18 · part of ADR 200 · supersedes a sentence of ADR 12 in part

## Context

The project owner decided on 18 October 2026 (owner):

- OpenTofu reads profiles through an OpenDustpan provider, built with
  providerkit (ADRs 170 to 179). Its normal data source returns every
  value, with secrets marked sensitive. State is sealed at rest (ADR 41
  decision 10, item keys ADR 151). An ephemeral resource ships as an
  opt-in. A profile entry can be marked "never store", and the normal data
  source then refuses it. The docs must warn that with another state
  backend the value is in that state in plain text, and that the ephemeral
  resource or client-side state encryption avoids this. The provider
  authenticates with the credentials that the CLI already has for our host.
- The Pulumi CLI reads profiles through the Pulumi Cloud-compatible API
  where the CLI supports it: the `environment:` imports of the stack config
  and `pulumi env`. A check with the real CLI decides what it calls. Each
  tool keeps its own words (ADR 13).
- Parity with ESC includes `esc run`: a command that runs any command with
  the values of a profile.

ADR 12 says: "OpenDustpan ships no client plugins or wrappers." The
provider of OpenTofu is a plugin, and the run command is a wrapper. The
owner decided both.

The research note gives what the Pulumi CLI calls, from its source:

- For the `environment:` list of `Pulumi.<stack>.yaml`, the CLI sends
  `POST /api/esc/environments/{org}/yaml/open?duration=2h0m0s` with the
  body `{"imports": [...]}`, then `GET
  /api/esc/environments/{org}/yaml/open/{id}`. The answer holds
  `properties` with `pulumiConfig`, `environmentVariables` and `files`,
  each value with a `secret` mark. `pulumi config` with no `--open` sends
  `POST .../yaml/check`.
- The CLI encrypts each secret of `pulumiConfig` with the secrets
  provider of the stack, and sends it as ciphertext in the update. It sets
  `environmentVariables` in its own process, and writes each of `files` to
  a temporary file that it does not delete.
- `pulumi env` uses `/api/esc/environments/{org}/{project}/{env}` with
  `open`, `open/{id}`, a `GET` of the YAML with the headers `ETag` and
  `Pulumi-ESC-Revision`, a `PATCH` with the YAML, and more.
- No capability gates these calls. The CLI sends them to any backend URL
  over HTTPS that is not the file backend.

## Decision

1. **Two client programs (owner).** This ADR supersedes the sentence of the
   decision of ADR 12, "OpenDustpan ships no client plugins or wrappers", for
   two programs only: the OpenDustpan provider of OpenTofu (decision 2)
   and the run command (decision 8). Each one uses the REST API of
   profiles (ADR 17), so no feature exists only in a client. The rest of
   ADR 12 stays: each tool still connects through the protocol that its
   CLI speaks.
2. **The OpenDustpan provider.** It is a provider of the provider SDK (ADR
   178), so one build serves OpenTofu, and the same binary serves the
   Pulumi CLI as `pulumi-resource-opendustpan` (ADR 176). It has one data
   source and one ephemeral resource, both named `opendustpan_profile`:

   ```hcl
   data "opendustpan_profile" "prod" {
     profile = "payments/aws-prod@stable"
   }

   provider "aws" {
     region = data.opendustpan_profile.prod.values["region"]
   }

   resource "aws_db_instance" "main" {
     password = data.opendustpan_profile.prod.secrets["db.password"]
   }
   ```

   - Inputs: `profile` (required, with an optional `@tag` or `@number`),
     `namespace` (optional, see decision 4) and `paths` (optional, to open
     less).
   - Outputs: `values`, a map from the path of each plain leaf to its
     string form, and `secrets`, a sensitive map from the path of each
     secret leaf to its string form. A path is the dotted path of the leaf,
     for example `db.password`, with list positions as `[0]`. `json` is the
     whole tree as JSON, sensitive when it holds a secret. `revision` is the
     revision that the open read.
   - The data source refuses a profile that holds a leaf marked never store
     on the asked paths, with an error that names the path and says to use
     the ephemeral resource. A plain value never becomes sensitive by
     mistake, and a secret never becomes plain.
3. **Never store, and the ephemeral resource (owner).** `ephemeral
   "opendustpan_profile"` has the same inputs and outputs. OpenTofu keeps
   an ephemeral value out of the plan and the state. It is the only way for
   OpenTofu to read a value marked never store. It is opt-in: a team writes
   `ephemeral` in place of `data` where it wants it.
   - The provider kit has no ephemeral resource yet (ADRs 172 and 176). This
     ADR asks for it: a kind of its own in `lifecycle`, served by
     `protocol/tofu` with the ephemeral calls of the provider protocol, and
     cases in `lifecycletest`. The Pulumi adapter has no ephemeral resource,
     so the Pulumi build of the provider does not serve it.
   - `make e2e` checks which OpenTofu versions have ephemeral resources
     before the provider ships it. The docs name the versions.
   - The Pulumi CLI reads a value marked never store through the
     `environment:` path (decision 5), where it gets no copy in state except
     the ciphertext of the stack config. The docs say so.
4. **The provider authenticates as OpenTofu does (owner).** The provider
   needs the host and a token. In order, it uses:
   1. the attributes `host` and `token` of the provider block (`token` is
      sensitive and has the file form `token_file`, ADR 175),
   2. the host from `TF_CLOUD_HOSTNAME`, which also sets the `cloud` block
      of OpenTofu, and the namespace from `TF_CLOUD_ORGANIZATION`,
   3. the token from `TF_TOKEN_<host>`, then the `credentials` blocks of the
      CLI configuration, then `credentials.tfrc.json`, then the credentials
      helper that the CLI configuration names.

   So `tofu login` on a workstation is enough, and on a runner the
   credentials helper of the launcher answers the job token (ADR 134
   decision 7). With a job token, the provider opens only the profiles that
   the job fixed (ADR 203 decision 4). A profile that the unit does not
   attach fails with an error that names the attachment to make.
5. **The Pulumi CLI, through the Pulumi Cloud-compatible API (owner).** The
   server serves the paths that the CLI sends, under the route prefix of
   that API:

   | Method and path | Use | Answer |
   | --- | --- | --- |
   | `POST /api/esc/environments/{org}/yaml/open` | `environment:` of a stack, `pulumi config --open` | `{"id": "<pos-...>"}`, or `400` with `diagnostics` |
   | `GET /api/esc/environments/{org}/yaml/open/{id}` | the values of that open | `properties` with `pulumiConfig`, `environmentVariables` and `files` |
   | `POST /api/esc/environments/{org}/yaml/check` | `pulumi config` with no `--open` | the same shape with no provider call |
   | `POST /api/esc/environments/{org}/{project}/{env}/open`, `.../versions/{v}/open` | `pulumi env open`, `pulumi env run` | `{"id": ...}` |
   | `GET /api/esc/environments/{org}/{project}/{env}/open/{id}` | the values of that open | as above |
   | `GET /api/esc/environments`, `GET /api/esc/environments/{org}` | `pulumi env ls` | the profiles that the caller can read |
   | `POST /api/esc/environments/{org}`, `GET`, `PATCH /api/esc/environments/{org}/{project}/{env}` | `pulumi env init`, `get`, `edit` | the document in our format, with `ETag` and `Pulumi-ESC-Revision` |

   - `{org}` is the namespace. `{project}/{env}` is a profile name of two
     segments, folder and name. A profile name of one segment is the
     project `default`. `@tag` and `@number` in an import name a revision.
   - A body of `yaml/open` and `yaml/check` holds `imports` only. A body
     with any other key, for example `values` or an `fn::` function, fails
     with `400` and a diagnostic that says that OpenDustpan reads profiles
     in their own format, and names the command `pulumi env edit`. The
     server never evaluates ESC syntax (owner: no ESC language
     compatibility). A `PATCH` with ESC syntax fails the same way.
   - The answer maps `exports.pulumi` to `pulumiConfig`, `exports.variables`
     to `environmentVariables` and `exports.files` to `files`, each value
     with `secret` from its mark. `exprs` and `schema` are empty objects.
   - **With a job token**, `environmentVariables` and `files` are empty.
     The launcher already wrote the files on its memory-backed volume (ADR
     203 decision 6), and the CLI would set the variables in its process
     and write the files to a temporary directory on disk. Only
     `pulumiConfig` reaches the CLI, which encrypts its secrets with the
     secrets provider of the stack.
   - The update metadata of the CLI names each import with its version
     (`stack.environments`). The server stores it with the update, so the
     history of a stack shows the profiles and revisions that each update
     read.
   - `make e2e` drives each path with the real CLI before it ships, for
     each supported version, as each endpoint of this API needs (CLAUDE.md).
6. **Each tool keeps its words.** Profiles, revisions and tags are the words
   of the tool-neutral API and the shared console views. The paths of
   decision 5 keep `environments`, because the CLI sends them. RFC 5 keeps
   open which word the messages of the Pulumi Cloud-compatible API and the
   Pulumi views of the console use.
7. **`pulumi env` commands that write.** `pulumi env set --secret` writes an
   `fn::secret` map into the YAML before it sends it, so the server refuses
   it as decision 5 says. The docs show `pulumi env edit` with `$secret`,
   and the REST API, instead. Which other `pulumi env` commands the server
   serves in its first release is in RFC 5. Each one needs its e2e step
   first.
8. **The run command.** `opendustpan profile run <profile> -- <command>`
   runs any command with a profile, on a workstation or in a CI:
   - It opens the profile with the consumer `run`, so the open has the
     access `open` (ADR 205 decision 4).
   - It writes each of `exports.files` to a new directory of mode 0700,
     on a memory-backed directory when one exists (as ADR 175 decision 9
     does), sets each variable, and starts the command as an argument list
     with no shell. It deletes the directory when the command ends, also on
     a signal.
   - It masks each secret value in the standard output and the standard
     error of the command with `[masked]`. `--interactive` passes the
     terminal through with no mask, for a shell.
   - `opendustpan profile open <profile> --format json|dotenv|shell` prints
     the values, for direnv and other tools.
   - It is a client command of the `opendustpan` binary, as the other
     client commands are: it opens no store (ADR 39), and takes the flag
     `--server` (`DUSTPAN_SERVER`) of those commands. It finds the token as
     the provider does (decision 4), and also reads `DUSTPAN_TOKEN`.
   - On a workstation, `exports.variables` can hold a secret, which the
     command then has in its environment. The docs say that a file is
     safer, and that a runner never does this.
9. **Runners, for both tools.** A job on a runner gets its profiles as ADR
   203 decision 6 says. A unit's code needs no change to read the profiles
   that the unit attaches.
10. **The warning in the docs (owner).** The docs page of the data source
    says, in its first lines: "OpenDustpan seals OpenTofu state at rest.
    With another state backend, the values of this data source are in that
    state in plain text. Use the ephemeral resource, or the state
    encryption of OpenTofu, for secrets." The page of each `pulumi env`
    command that writes secrets names `$secret`.

## Consequences

- An OpenTofu user on a workstation runs `tofu login` and adds a data
  source. A Pulumi user adds `environment: [payments/aws-prod]` to the
  stack config. Both read the same profile, with the words of their tool.
- The provider is the first provider of OpenDustpan itself. RFC 5 asks
  where its code lives.
- The provider kit gets a request for ephemeral resources (decision 3).
- `make e2e` gets steps for the data source, the ephemeral resource, the
  authentication order, each path of decision 5 and the run command.
