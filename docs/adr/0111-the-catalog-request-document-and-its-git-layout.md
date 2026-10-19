# 111. The Catalog request document, its schema version and its git layout

Status: accepted · 2026-10-17. Extended by ADR 213 decisions 7 and 8

## Context

ADR 110 decides that a Catalog request is a declarative JSON document with
one home: a file in git, or OpenDustpan alone. The owner decided that a
request is never tool code, and that YAML is a concern of the client. The
console form shows the same request as `request.json`, with the API call
and the git path.

## Decision

1. **The document.** A request document is one JSON object. This is the
   document of schema version 1:

   ```json
   {
     "schemaVersion": 1,
     "namespace": "payments",
     "name": "orders-db",
     "blueprint": { "name": "postgres", "version": 3 },
     "ensure": "present",
     "inputs": {
       "size": "small",
       "network": "net-0a1b2c",
       "subnetGroup": "orders-private"
     },
     "home": {
       "kind": "git",
       "repository": "payments/infra",
       "ref": "main",
       "delivery": "review-request"
     }
   }
   ```

   `catalog.Document` holds it, with these fields:

   | Field | Rule |
   | --- | --- |
   | `schemaVersion` | Required. The integer 1. |
   | `namespace` | Required. The namespace of the request and of its unit. |
   | `name` | Required. The name of the request: 1 to 63 bytes of lowercase letters, digits and `-`, with a letter first. It is unique for each blueprint in the namespace. |
   | `blueprint.name` | Required. A blueprint of the namespace (ADR 112). |
   | `blueprint.version` | Required. A published version, an integer of 1 or more. |
   | `ensure` | `present` (the default) or `absent`. |
   | `inputs` | An object from input name to answer. An input that the document leaves out takes its default. |
   | `home` | Required at the first submit. Ignored after: the first submit fixes the home (ADR 110 decision 5). |
   | `home.kind` | `git` or `opendustpan`. |
   | `home.connection` | Optional, for `git`. Empty takes the forge connection of the namespace. |
   | `home.repository`, `home.ref` | Required for `git`. |
   | `home.delivery` | Optional, for `git`: `review-request` (the default) or `direct-push`. |

2. **No other field.** The parser refuses an unknown field, a duplicate key,
   a value of the wrong JSON type and a document over 64 KiB. So a typing
   error fails at the first submit, not at the apply. `catalog.ParseDocument`
   holds the rule, and a table-driven test covers it.
3. **An answer is plain JSON.** An answer is a string, a number, a boolean
   or a list of strings. It is never an object, never null and never a
   secret. A request file in git is readable by each person who can read the
   repository, so a secret has no place in it. A secret value of a unit
   comes from the profile of the blueprint (ADR 112 decision 8).
4. **The schema version.** `schemaVersion` names the shape of the document.
   The server reads each version that it knows, and refuses a higher version
   with `catalog.ErrSchemaVersion`, which names the highest version that the
   server reads. A new field that a server can ignore with no change of
   meaning does not need a new version. Each other change does. Until 1.0,
   a version that no tag shipped can change (ADR 73).
5. **The reason is not in the document.** The document is the desired state.
   Why a person asks for a change belongs to the submit: the `reason` of the
   submit body (ADR 117 decision 5), the commit message in git, and the audit
   entry. So two submits of one document with two reasons give one desired
   state.
6. **Equal documents are equal states.** `catalog.Document.Canonical` gives
   the canonical bytes of a document: defaults filled in, `home` left out
   after the first submit, and the JSON in the canonical form of RFC 8785,
   the JSON Canonicalization Scheme (`catalog.CanonicalJSON`). So the keys
   of each object are in the order of their UTF-16 code units, there is no
   white space, each number has the shortest form that reads back as the
   same IEEE 754 double (`20`, `20.0` and `2e1` give `20`), and each string
   has only the escapes of the scheme (`"\u00e9"` and `"é"` give `"é"`). A
   submit whose canonical bytes equal those of the last applied revision
   makes no job. The API answers with that revision and the status
   `unchanged`. The equality filters of choices (ADR 113 decision 4) use the
   same form.
7. **The git layout.** In a repository, the request of namespace `<ns>`,
   blueprint `<bp>` and name `<name>` is the file

   ```text
   catalog/<ns>/<bp>/<name>/request.json
   ```

   - The directory `catalog/<ns>/<bp>/<name>` is the code location of the
     unit for the write-back job and for the team's CI. The render writes
     its output below it, in `.render/`, which the repository ignores.
   - The path and the document must agree. A document at a path whose
     namespace, blueprint or name differs fails with
     `catalog.ErrPathMismatch`. `catalog.GitPath` gives the path, and
     `catalog.ParseGitPath` reads it back. A table-driven test covers both.
   - The root `catalog` stays fixed in version 1. A setting for another root of
     each repository is an open question.
   - The namespace is part of the path, so one repository can hold the
     requests of more than one namespace.
8. **The file in git is the document as the API takes it.** The write-back
   job commits the bytes that `catalog.Document.Encode` gives: the keys in
   the order of the table of decision 1, two spaces of indent, and a final
   new line. So the console can show the same bytes as `request.json`, and a
   diff in a review request shows only the keys that changed.
9. **Clients and YAML.** A client can let a person write YAML. The client
   converts it to JSON before it calls the API. The server reads JSON only,
   and the request file in git is JSON only. So the server needs no YAML
   library.
10. **The three views of the console.** The console form is one client of
    the API. For each request, it shows three equal views of the one
    document: the form, `request.json`, and the API call with the git path of
    decision 7. Each view gives the same document, so a person can copy any
    of them and get the same result.

## Consequences

- A person can write a request file by hand, commit it, and get the same
  checks at the render as a submit through the API gives.
- A request in git has one file. The team reviews the change of the file in
  its forge, as for any other change.
- The document has no free text for the reason, so the history of the
  reasons is the history of the commits and of the audit log.
- A YAML editor in the console converts on the client. The console must show
  the JSON that it sends.

## Open questions

- Can a repository set another root than `catalog`? The render and the
  review-request check must then read a setting of the repository.
- Can one file hold the requests of many units, for example one file for
  each team (RFC 14)? Version 1 has one file for each request.
- Does schema version 2 need a list answer of numbers or of objects? Version
  1 allows lists of strings only.
