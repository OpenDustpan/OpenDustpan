## Context

A developer tries to run the first setup from the docs page alone and get it right the first time. Documentation as a product (RFC 57) waits on its research, so the scope is not set yet.

This change is a coarse placeholder for RFC 57. It changes no server code, no route, and no schema now.

## Decisions

1. **No code now.** The scope, the page set, and the toolchain stay open until the research ends. Single-feature docs pages keep shipping with their own changes until then.
2. **The repository guides stay the source of truth.** Until the refinement ships, install and first-run guidance lives in `docs/guides/`. No second source of install truth appears beside them.
3. **Each command on a docs page runs as written.** The docs check (`make docs-lint`) and the docs-route check keep pages honest: a page that names a step the server does not support is wrong, and the check finds it.
4. **Refine when the research ends.** The later refinement names the page set, the ownership, and the review path for documentation as a product.

## Structure

No new structure now. The bug class is a docs page that drifts from the server; the existing docs checks plus one guide directory are the structure that removes it. No second docs source and no new tooling appear before the research decides.

No custom analyzer and no new tool. A docs site toolchain needs the owner.

## Risks

- A developer follows a stale page and the first setup fails. The docs check runs in CI, so a stale page fails the build instead of the developer.
- Research decides a shape that existing guides cannot carry. The refinement moves guides instead of copying them, so no page exists twice.
