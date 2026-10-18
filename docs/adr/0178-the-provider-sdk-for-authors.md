# 178. The provider SDK for authors

Status: accepted · 2026-10-18 · part of ADR 170

## Context

The project owner decided on 17 October 2026 that a Go SDK lets a team write
a small provider for its own tool in a few hundred lines, with the same
conventions as a declared provider (owner).

A declaration covers an API or a tool that maps one operation to one call.
Some tools need logic: two calls for one create, a wait until an object is
ready, a value that the tool prints as text. The SDK is for those.

The types are in `providerkit/sdk`.

## Decision

1. **One struct for each type.** The author writes a Go struct for the
   provider configuration and for each resource and data source. A tag
   maps a field to an attribute:

   ```go
   type Override struct {
   	ID        *string `provider:"id,computed"`
   	Schedule  string  `provider:"schedule,required,replace"`
   	Person    string  `provider:"person,required"`
   	Token     *string `provider:"token,optional,sensitive,writeonly"`
   	CreatedAt *string `provider:"created_at,computed,stable"`
   }
   ```

   The tag is `name,option,...`. The options are one mode (`required`,
   `optional`, `computed`, or `optional,computed`) and any of `sensitive`,
   `writeonly`, `replace` and `stable` (`sdk.ParseTag`). `stable` has the
   meaning of the declaration flag (ADR 171 decision 7).
2. **The schema comes from the struct.** `sdk.SchemaOf` maps `string` to
   string, the integer and float types to number, `bool` to bool, a slice to
   a list, and a map with string keys to a map. The SDK checks every struct
   when the provider starts, and a bad struct stops the start with
   `ErrInvalidModel`.
   - **Null is nil (PM, for the project owner).** The field of a required
     attribute is not a pointer. The field of an optional, computed or
     optional computed attribute is a pointer, and nil means null.
     `SchemaOf` refuses any other field. The zero value of a type, for
     example `""` or `0`, can be a value that the user sets, so it cannot
     also mean "not set". The element of a slice or a map is not a
     pointer.
   - **Conversion.** `sdk.Decode` and `sdk.Encode` convert between the
     values of the core and the struct. A number becomes an integer field
     only when it is a whole number in the range of the field. Any other
     number, for example 1.5 or 300 for an `int8`, is `ErrInvalidConfig`,
     because a conversion would lose a part of it. A number becomes a float
     field rounded to the nearest float. A number of the core has a fixed
     precision of 512 bits (`schema.NumberPrecision`).
3. **One function for each operation.** `sdk.Resource[C, M]` has `Create`,
   `Read`, `Update` and `Delete`, each on the model struct `M`.
   `sdk.DataSource[C, M]` has `Read`. A nil `Update` means that each field
   that the user sets forces a replace, as in a declaration.
4. **The same conventions as a declared provider.**
   - `Read` returns `lifecycle.ErrNotFound`, wrapped, for an object that is
     gone. An import calls `Read` with only the ID.
   - `Delete` returns nil for an object that is gone.
   - The core planner decides every change (ADR 172). The author writes no
     diff.
   - Each sensitive configuration field gets the file form (ADR 175), from
     the same function as a declared provider (`sdk.ConfigSchemaOf`, which
     calls `lifecycle.WithFileForms`).
5. **Commands through the same runner.** `sdk.Call` gives each operation the
   configuration and a `cliexec.Runner`. An SDK provider starts its tool by
   the rules of the CLI engine: an argument list, no shell, a private
   environment, and process groups (ADR 174).
   - The runner does not check arguments or variables for secrets. The
     declaration rules of ADR 171 decision 9 do not reach Go code, so the
     author keeps a secret out of arguments and variables.
   - `Call.SecretFile` writes a secret to a new file of mode 0600 in the
     private directory of the session, and returns the path. The operation
     gives the path to its tool, for example as `--token-file=<path>`. The
     session deletes the directory when it closes (ADR 175 decision 9). The
     other way is the standard input of the command.
6. **Serving.** The SDK turns a `sdk.Provider[C]` into a
   `lifecycle.Provider`, and serves it through both protocol adapters (ADR
   176). The `main` function of the author calls one serve function, which
   returns an error. The `main` function exits, because only a `main`
   package may exit.
7. **Building.** `dustpan-provider package` packs an SDK provider as it packs
   a declared one (ADR 177).
8. **Versions of the SDK.** The SDK has the version of the `providerkit`
   module. Until `providerkit/v1.0.0`, a minor version can break the SDK. The
   release notes name each break, and the migration.

## Consequences

- An author handles no protocol of OpenTofu or Pulumi. A provider of three
  resource types over one tool is a few hundred lines: the structs, the
  operations, and a `main` function.
- The conformance suite (ADR 179) runs against an SDK provider in the same
  way as against a declared one.
- A provider that grows past the SDK, for example with nested blocks,
  needs a later version of the SDK. It cannot fall back to the plugin
  libraries of a tool without leaving `providerkit`.

## Open questions

- Nested objects. The SDK maps a struct field only to a primitive, a list or
  a map in version 1, like the declaration format.
- Helpers for common tasks: run a command and decode its JSON output, wait
  until an object is ready, and map exit codes to errors. They come with the
  CLI engine, so that both share one implementation.
