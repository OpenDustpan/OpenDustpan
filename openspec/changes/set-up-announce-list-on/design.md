## Context

Manual setup of a hosted announce-only list on opendustpan.org with docs links and a release checklist step. No server code and no routes.

## Decisions

1. **Hosted announce-only list with plain links.** Use a hosted announce-only list with tracking off and double opt-in. Link it by a plain link from the docs guide. Name it in the release checklist for each release and security notice. The owner holds the service admin account. Subscriber addresses stay private and are removed within 7 days of a request. SPF, DKIM and DMARC pass before the first send.

## Structure

No code structure. The only record is the subscriber list held by the hosted service.

## Risks

- Bounces and abuse of the public form go to the service dashboard and the abuse contact before the first send.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | Docs and hosted service setup only; no server change |  |  |
