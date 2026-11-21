## Context

A developer tries to install the server and run a first stack or workspace from the docs page alone. The docs site and landing page have no accepted scope until documentation as a product (RFC 57) is decided.

This change holds RFC 61 as waiting on RFC 57. It changes no server code, no route, and no schema now.

## Decisions

1. **No site now.** Scope, tooling, and hosting ship only after RFC 57 is accepted (from 57-documentation-product). No half-site appears beside the repository guides.
2. **The repository guides stay the install path.** Until the refined change ships the site, `ls docs/guides` lists the install and first-run guidance. The landing page points nowhere until it exists.
3. **No server behavior changes.** The site is a presentation of existing guides and API answers, not a new endpoint or a new console view.

## Structure

No new structure now. The bug class is a site that shows stale content beside fresh guides; one guide directory with no published copy is the structure that removes it. When RFC 57 is accepted, the site builds from the same guides, so no page exists in two forms.

No custom analyzer and no new tool. Site tooling and hosting need the owner.

## Risks

- A developer looks for a site and finds none. The docs page says to use the repository guides until the site ships.
- A site generator pins a copy of a guide at build time. The build reads the guides in place, so a fix to a guide reaches the site at once.
