# 171. The provider declaration format and its schema version

Status: accepted · 2026-10-18 · part of ADR 170

## Context

The project owner decided on 17 October 2026 that an HTTP provider comes
from a description, "for example an OpenAPI document plus a small mapping
file", and that one description produces a provider for OpenTofu and for
the Pulumi CLI (owner). A CLI provider comes from a description too.

An OpenAPI document describes paths, methods and body shapes. It does not
say which path creates an object and which one reads it, which field is the
ID, which field forces a new object, or which value is secret. A mapping
must say this. The mapping is also all that a CLI provider has, because a
command-line tool has no OpenAPI document.

The rules are in `providerkit/declaration`. The examples are in
`providerkit/declaration/testdata`.

## Decision

1. **The declaration is the mapping file.** One JSON file declares one
   provider: its name, its configuration, its engine, and each resource
   type and data source type with the requests or commands that implement
   it. The same file format serves the HTTP engine and the CLI engine.
2. **The schema version comes first.** The first key is `"schema": 1`.
   `declaration.Parse` reads the version before anything else. It refuses
   a missing version and a version that it does not read, with
   `ErrSchemaVersion`.
3. **Strict reading.** The parser refuses a key that the version does not
   define, a second JSON value after the first, and a file above 4 MiB.
   It then runs `Document.Validate`. A typing error fails the build, not
   the apply.
4. **When the version changes.** A builder of version N reads every
   version up to N. A new optional key needs a newer builder, and an older
   builder refuses the file. That is the safe failure. The version number
   goes up only when the meaning of an existing key changes.
5. **JSON, not YAML, in version 1.** The standard library reads JSON, so the
   format adds no dependency. YAML is an open question.
6. **One engine for each provider.** A document sets exactly one of `http`
   and `cli`. One provider then has one configuration and one model for
   its credentials. A team with an API and a command-line tool for one
   system makes two providers, or writes one with the SDK.
7. **Attributes.** An attribute has a name in snake_case, a type, a mode,
   and the flags `sensitive`, `writeOnly` and `replace`.
   - Types: `string`, `number`, `bool`, `list(T)` and `map(T)`. Version 1
     has no object type. A nested object is a map of strings or a resource
     of its own.
   - Modes: `required`, `optional`, `computed` and `optionalComputed`.
   - Each resource has an `id` string.
   - A resource without `update` must mark each attribute that the user
     sets as `replace`.
   - Provider configuration attributes are `required` or `optional`.
8. **References, and no expressions.** A string in a request or a command
   can hold `{name}`, `{config.name}` and `{file:config.name}` (ADR 175).
   There are no functions, no conditions and no escape for a brace. The
   declaration is data. Logic goes in an SDK provider.
9. **Public and private places.** A process list, a proxy log or a shell
   history can show a command argument, an environment variable, a URL path
   and a query. A reference in one of these places cannot name a sensitive
   attribute. A sensitive value goes in a request body, on the standard
   input, through authentication, or as a file. `Validate` enforces this.
10. **OpenAPI is input to the build, not to the provider.** The build tool
    (ADR 177) has two steps for an OpenAPI document:
    - `dustpan-provider derive` writes a first declaration from chosen
      operations of the document. The team reviews it, changes it and
      commits it.
    - `dustpan-provider check` compares a declaration with the document:
      each method and path exists, and each pointer of a reply exists in
      the reply schema.

    The built provider never reads the OpenAPI document. A provider of a
    few resources does not carry a document of thousands of operations.
11. **State versions.** Version 1 of the format has no key for the version of
    the stored shape of a resource. A built provider reports version 0 for
    every type.

## Consequences

- Each rule of `Validate` has a row in a table test that changes one thing in
  an example and expects `ErrInvalid`.
- A declaration in version control is a review surface. It holds no
  credential, because a credential field must be a reference to a sensitive
  configuration attribute.
- Teams that keep their mapping in YAML convert it to JSON before a build,
  until the PM decides the YAML question.

## Open questions

- YAML. The root module already uses `go.yaml.in/yaml/v3` (MIT and
  Apache-2.0). Adding it to `providerkit` is a new dependency of that
  module, so the PM decides.
- An object type, and the version key for stored shapes, which an upgrade of
  state needs. Both can come in version 2.
- Reading OpenAPI documents in YAML has the same dependency question. The
  standard library reads OpenAPI in JSON.
