## Context

A developer who automates namespaces, units, and jobs from .NET and Java services wants typed clients that match the stable REST API instead of handwritten HTTP. The roadmap places C# and Java clients after a stable REST API, with Go first and then Python and TypeScript. The API is not stable yet, so no client work starts now.

This change records the deferred work and its gates. It changes no server code, no route, and no schema.

## Decisions

1. **No code now.** The change adds no client, no generator output, and no docs page change. Raw HTTP against the tool-neutral REST API stays the path until the gates pass.
2. **Generate from the API description when the gates pass.** When the REST API is stable and the Go, Python, and TypeScript clients have landed, extend the existing client generator to emit C# and Java from the same API description. One generator and one release pipeline cover each language, so no second source of truth and no new tool.
3. **Future clients follow the masked-read and fenced-write rules.** Reads use the masked state shape and the separate reveal call. Writes carry the fence. These rules are decided already and need no new decision at the gate.

## Structure

No new structure now. The bug class is a second source of truth for client shapes; the single API description plus the single generator is the structure that removes it. When the gate passes, the new generator templates are unexported template sets of the existing generator package, built only through its existing constructor.

No custom analyzer and no new tool. A new generator dependency needs the owner.

## Risks

- .NET and Java users keep using raw HTTP until the clients ship. The docs page for automation options says so.
- A toolchain choice made before the gate would add a dependency too early. The choice stays open until the gates pass.
