# 247. Works with Backstage: an entity tab that shows the units of a component

Status: accepted · 2026-10-19. Extends ADR 13 (labels and consoleUrl on the inventory entry) and ADR 210 decision 3 (the label and reference filters of the unit list). Accepts part of RFC 51. Opens RFC 84.

## Context

On 2026-10-17 the project owner decided to build Backstage support now,
in the words "build Backstage support now". The same decision asks for
spikes of Jira and ServiceNow, only to learn the shape of their interfaces.
This ADR does not cover those spikes. Decision 1 is an owner decision. The
product manager decided decisions 2 to 13 under the owner's decision rule.
RFC 84 holds the questions that only the owner can answer.

[RFC 51](../rfc/0051-change-management-integrations.md) lists "a Backstage
plugin that reads the inventory" as a first idea. ADR 17 names developer
portals as clients of the REST API.

Backstage is an open-source developer portal. Its Software Catalog holds
entities. A `Component` is a piece of software, for example a service. A
`Resource` is infrastructure that a component needs, for example a
database. An entity carries annotations: keys and values in
`metadata.annotations`. A plugin adds a tab or a card to the page of an
entity. A plugin has a frontend part, which runs in the browser, and can
have a backend part, which runs in the Backstage backend.

The product manager decides a question when Google, AWS, PCI DSS, SOC 2 or
ISO 27001 would answer it the same way, when no persona is upset or an
escape hatch exists, and when the answer is not stupid on a step back
(ADR 210 gives the rule). Here the benchmarks are the Backstage plugins of
other infrastructure products.

The facts on `main`:

- The inventory (ADR 13) lists the units of a namespace. Each entry has
  the tool label, the unit reference, the unit ID, the tool's own name,
  the last change time, the status, the resource count, the unit lock and
  the collection. The entry has no labels.
- The context read API (ADR 161) answers one document for one unit. Its
  section `health` holds the status, the unit lock and the outcome of the
  newest job. Its section `changes` holds the newest 10 history items and
  jobs, with their actors. Each section names its action. The call needs
  `unit:read`.
- The code serves these routes under `/api/inventory/` and
  `/api/context/`. ADR 210 moves them under `/api/dustpan/v1/` before the
  first tag. ADR 210 also gives `GET /api/dustpan/v1/namespaces/-/units`
  (a list across namespaces) and a get of one unit by unit ID through
  `namespaces/-`.
- A label (ADR 223 decision 11) is a key and a value on a unit. The Pulumi
  CLI sends stack tags as labels. No list route filters by label.
- A service token belongs to a principal that is not a person (ADR 53). A
  trust rule (ADR 54) issues a short-lived token to a workload, for one
  namespace only. ADR 54 decision 7 recommends workload identity for
  machines first.
- The console has a page for each Pulumi stack and each OpenTofu
  workspace. Its paths use the tool's own names, not the unit ID.
- The console is in `OpenDustpan/console` with the npm package
  `@opendustpan/console` (ADR 8).

## Decision

1. **Build Backstage support now (owner, 2026-10-17).** OpenDustpan ships
   a Backstage plugin. The docs say "works with Backstage". They never use
   the Backstage name or logo as the name or the logo of OpenDustpan or of
   the plugin.

2. **The first slice is one entity tab (product manager).** The plugin
   adds the tab "Infrastructure" to the page of a `Component` or a
   `Resource` entity. The tab shows the units that the entity names
   (decision 3). For each unit, the tab shows:

   - the tool label and the tool's own word for the unit ("stack" for
     Pulumi, "workspace" for OpenTofu), and the tool's own name
   - the namespace and the collection
   - the status and the time of the last change
   - the outcome and the time of the newest job, when the unit has one
   - the unit lock, when the unit has one: the holder, the time and the
     tool's own word for the operation
   - the newest 5 changes, with their actors and times
   - a link to the page of the unit in the console.

   A card with the same data in a short form is the second export of the
   frontend package, for the overview tab of an entity. The plugin reads
   only. It never changes a unit, never unlocks a unit, never starts a job
   and never shows a state, an output or a secret.

   Benchmark: the Argo CD plugin for Backstage shows an overview card and
   a history of the applications of an entity, and starts no sync. The
   GitHub Actions plugin shows the newest workflow runs of an entity. The
   PagerDuty plugin shows the on-call and the open incidents of an entity.
   Each plugin starts with a read-only view of the entity page.

3. **An annotation maps an entity to units (product manager).** The
   entity names its units. The server and the plugin never merge the
   models of two tools (ADR 13). Two forms exist:

   ```yaml
   metadata:
     annotations:
       # A list of unit references, separated by commas.
       opendustpan.org/units: pulumi:acme/web/prod, opentofu:acme/network
   ```

   ```yaml
   metadata:
     annotations:
       # One namespace and a label selector.
       opendustpan.org/namespace: acme
       opendustpan.org/unit-labels: app=web,team=payments
   ```

   - `opendustpan.org/units` holds up to 20 unit references. A reference
     uses the lowercase tool code of the glossary. The plugin shows a unit
     that it cannot find, or cannot read, as one row "not found", with the
     reference. It does not tell the two cases apart, because the list
     answers only the units that the principal can read.
   - `opendustpan.org/unit-labels` holds one or more `key=value` pairs.
     A unit matches when it has each pair. The plugin shows up to 50
     units, and says how many more units match.
   - An entity that has both forms shows the units of both, each unit
     one time.
   - A unit reference changes when a person renames a stack or a
     workspace. The "not found" row then shows the reference and the
     annotation to change. A team that renames units uses labels.
   - A person who holds `unit:edit-labels` on a unit, or `unit:write` in the
     namespace (a create sets the first labels, ADR 223 decision 11),
     can show it on each
     entity that selects those labels. For a strict mapping, use
     `opendustpan.org/units`.
   - The annotation keys use the domain `opendustpan.org`.

   Benchmark: the Argo CD plugin reads `argocd/app-name` or
   `argocd/app-selector`. The Backstage Kubernetes plugin reads
   `backstage.io/kubernetes-id` or `backstage.io/kubernetes-label-selector`.
   The PagerDuty plugin reads `pagerduty.com/service-id`. Each gives a
   name form and a selector form, under the domain of its project.

4. **A backend plugin holds the credential (product manager).** The
   plugin has three npm packages: a frontend plugin, a backend plugin and
   a common package with the shared types. The frontend calls only the
   backend plugin. The backend plugin calls the REST API of OpenDustpan.
   The access token never reaches the browser.

   - The backend plugin serves only the read routes of decision 7. It
     never forwards another path, another method or a query value that it
     does not know.
   - The frontend sends only the entity reference. The backend plugin gets
     the entity from the Software Catalog with the credentials of the
     Backstage user, so the catalog read permission of that user applies,
     and reads the annotations there. It never takes a unit reference, a
     namespace or a label from the request.
   - The backend plugin maps each answer to the types of the common package
     and drops each other field. The browser gets only the fields of
     decision 2.
   - The backend plugin accepts only user credentials (httpAuth with
     `allow: ['user']`) and refuses a request with none.
   - The plugin does not use the generic proxy of Backstage. The proxy
     forwards each path under its prefix with the token, so a browser
     could call each route that the principal can call.
   - The backend plugin checks the Backstage permission
     `opendustpan.unit.read` before it answers. It is a resource permission
     on the resource type `catalog-entity`, checked for the entity of the
     request. The permission framework of Backstage is off by default, and
     then Backstage allows each request. So an operator who turns it on can
     show the tab to some Backstage users only. The guide says this and
     gives a sample policy.

   Benchmark: the backend of the Backstage Kubernetes plugin holds the
   cluster credentials and serves narrowed reads to its frontend. The
   Backstage docs advise a backend plugin over the proxy when a secret
   must stay on the server.

5. **One read-only principal for each Backstage installation and each
   namespace (product manager).** The backend plugin calls the REST API as one service
   principal, not as the Backstage user.

   - The operator makes the service principal and grants it a role with
     `namespace:read` and `unit:read` only, on each namespace or each
     collection that Backstage shows. The role holds no secret action, no
     write action and no audit action. The principal can be a principal
     of one namespace or of the installation (ADR 215).
   - The credential is a service token (ADR 53) in the configuration of
     Backstage, as a secret that Backstage reads from the environment.
     Where Backstage runs on a platform with an OIDC issuer, for example
     Kubernetes, the backend plugin can use workload identity in its
     place: it exchanges the platform token for a short-lived token
     through a trust rule of each namespace (ADR 54). The docs show
     workload identity first, as ADR 54 decision 7 asks. With workload
     identity or in OpenDustpan Cloud, the backend plugin holds one
     principal for each namespace. G6 tests both kinds, `service` and
     `workload`.
   - A required backend setting `opendustpan.namespaces` lists the
     namespaces that the plugin may read. It has no wildcard default. A
     reference outside the list shows as "not found". For
     `opendustpan.org/namespace` outside the list, the tab shows one row
     "not found" with the namespace, and the backend plugin makes no call.
   - Each Backstage user who can see the tab sees the data that the
     principal can read. This includes the subject of a lock holder and
     the actors of the changes. The setting `opendustpan.showActors`
     (default true) hides the actors of the changes and the subject of the
     lock holder. The user guide says so, and names
     three limits: the grants of the principal, the Backstage permission of
     decision 4 and the access limits of ADR 223 (a Cedar forbid on the
     Backstage principal).
   - Each request of the backend plugin carries the header
     `User-Agent: opendustpan-backstage/<version>`, so the request logs of
     OpenDustpan name the client. The subject of the principal names it in
     the audit log.
   - A token of the Backstage user, through an exchange of its identity,
     is not in this slice. RFC 84 keeps it open.

   Benchmark: the Argo CD, PagerDuty and SonarQube plugins for Backstage
   each read with one service token in the Backstage configuration. The
   HCP Terraform community plugins read with one organization or team
   token. AWS and Google Cloud recommend workload identity over a static
   key for a service that runs on their platform.

6. **No catalog entity provider in the first slice (product manager).**
   The plugin does not import units into the Software Catalog as
   entities. A team names its units on its own entities (decision 3). A
   later slice can add an entity provider that makes one `Resource` per
   unit, with the tool's own word as `spec.type` (`pulumi-stack`,
   `opentofu-workspace`) and the unit ID in its name. That slice needs its
   own ADR.

   Benchmark: the Argo CD, GitHub Actions and PagerDuty plugins start with
   an entity tab and add no entities. The AWS and Azure resource plugins of
   the community add entities, and their catalogs then grow by one entity
   per resource.

7. **The API that the plugin reads (product manager).** The plugin uses
   only the paths of ADR 210. It reads:

   | Read | Route | Action |
   | --- | --- | --- |
   | units of a namespace, by label | `GET /api/dustpan/v1/namespaces/{namespace}/units?label=...` | `namespace:read`, then `unit:read` on each unit |
   | units by reference | `GET /api/dustpan/v1/namespaces/{namespace}/units?reference=...`, one call for each namespace of the references, with the token of that namespace | `namespace:read`, then `unit:read` on each unit |
   | one unit with its health and changes | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/context?include=identity,health,changes` | `unit:read` |
   | the console link | the field `consoleUrl` of a unit entry | none more |

   The plugin never reads the sections `outputs`, `readme`, `context` or
   `audit`. A later slice can show owners and docs from the section
   `context`.

8. **Server changes that ship before the plugin (product manager).** The
   plugin needs these changes on the server. Each change is a change of the
   tool-neutral API. Each lands with its action, its route test and its
   user documentation, as each other change does.

   | Gap | Change |
   | --- | --- |
   | G1 | The move of the inventory and context routes to `/api/dustpan/v1/` (ADR 210 decision 3) ships first. The plugin calls no old path. |
   | G2 | The inventory entry holds the labels of the unit, as `labels`, a map of strings. The tool package publishes them to the projection in the same transaction as each label change (ADR 13). |
   | G3 | The unit list takes the query value `label=key=value`, URL-encoded, at most 10 pairs. The key and the value follow the label grammar of ADR 223. A unit matches when it has each pair. The list pages as ADR 210 decision 10 says. |
   | G4 | The unit list takes the query value `reference=<unit reference>`, up to 20 times. Each reference names the namespace of the path. A reference in another namespace answers 400. The list answers the units that the caller can read. |
   | G5 | The inventory entry holds `consoleUrl`: the URL of the page of the unit in the console. The server makes it from `--public-url`. The field is empty when the installation has no public URL. The link is `{public-url}/units/{unit_id}`. The console keeps this path as a contract. It opens the page of the stack or the workspace, so a rename does not break a link. A change of `OpenDustpan/console` ships the path before the plugin. |
   | G6 | A principal with the role of decision 5 can read the list and the context. A test shows it for a service token and for a token of a trust rule. The test covers both kinds, `service` and `workload`. It also shows that such a principal gets 403 on each write route of the inventory and of the context API. |

   Benchmark: Google AIP-160 gives a list a filter in its query. The
   Kubernetes API lists objects by a label selector.

9. **Where the code lives, and its versions (product manager, with an
   owner question).** The plugin is TypeScript and npm, as the console is,
   not Go. It does not go into `opendustpan/opendustpan`, because
   `make check` there is a Go and docs gate.

   - The product manager proposes a new repository,
     `OpenDustpan/backstage-plugins`, with a workspace of three packages
     under the npm scope `@opendustpan`:
     `@opendustpan/backstage-plugin-opendustpan`,
     `@opendustpan/backstage-plugin-opendustpan-backend` and
     `@opendustpan/backstage-plugin-opendustpan-common`. The names follow
     the community form for Backstage plugins. Only the owner can make the
     repository. RFC 84 asks the owner.
   - The packages have one version, in semantic versioning, separate from
     the version of the server. The notes of each release name the
     server versions that its CI ran against and the Backstage release that
     it builds on. Before the server reaches 1.0, the notes name the
     server tags, because the tool-neutral API
     can break (ADR 73).
   - A release publishes to npm from CI, with npm provenance, from a tag.
     No person publishes from a laptop.
   - The license is Apache-2.0, as the server's is.
   - The plugin sends no telemetry (ADR 74).

   Benchmark: the Backstage community plugins repository releases each
   plugin as its own npm package under a scope, in semantic versioning,
   and bumps each workspace to the newest Backstage release each month.
   npm provenance links a package to the CI run that built it.

10. **How CI tests the plugin (product manager).**

    - The frontend tests use the Backstage test utilities
      (`@backstage/test-utils`). They render the tab and the card against
      a fake client of the backend plugin. They cover each row kind: a
      unit, a locked unit, a unit with no job, a "not found" row, an empty
      list, and more units than the limit.
    - The backend tests use `@backstage/backend-test-utils` against a
      fake OpenDustpan API. They check that the backend plugin forwards
      only the routes of decision 7, that it refuses another path, that a
      request that holds a unit reference or a namespace gets 400, that
      no answer to the browser holds the token, and that no answer to the
      browser holds a field outside those types, and that a second user whom the policy
      refuses gets 403 when the cache holds the answer for the first user.
    - One end-to-end test starts a real server with `opendustpan dev`
      (ADR 237). It makes a Pulumi stack with the real Pulumi CLI and an
      OpenTofu workspace with the real OpenTofu CLI, and gives each a
      label. Then it starts a Backstage test backend with the backend
      plugin and an entity with each annotation form, and checks the
      answers. CI runs it against the newest server tag and against
      `main` of the server.
    - The server changes of decision 8 have their own tests in this
      repository, as each other change has.

    Benchmark: the Backstage community plugins test with these two
    packages. The e2e suite of this project drives the real CLI of each
    tool (ADR 12).

11. **Words (product manager).** The tab and the docs say "unit" only
    where they mean a stack and a workspace together, as the console's
    cross-tool views do. A row says "stack" or "workspace". The docs say
    "works with Backstage", and give the Backstage versions that CI
    ran the plugin against. They make no other claim about Backstage.

12. **Docs (product manager).** The first release has a guide page
    (install the packages, make the principal, add the annotation) and a
    reference page (the annotations, the configuration keys and the
    Backstage permission). The pages live with the plugin. The user
    documentation of the server links to them (ADR 214).

13. **Cache and limits (product manager).** The backend plugin caches each
    answer for 60 seconds, keyed by the route, the query and the principal.
    The cache holds only answers of OpenDustpan. On each request, also on a
    cache hit, the backend plugin reads the entity from the Software Catalog
    as the Backstage user and checks `opendustpan.unit.read` for that entity
    before it reads the cache. It never caches the entity or the decision of
    the permission check.
    It makes at most 4 calls to OpenDustpan at a time. The card reads only
    the unit list (status, lock, last change). Only the tab reads the
    context of each unit.

    Benchmark: the Argo CD and Kubernetes plugins for Backstage poll their
    backends with a cache.

## Not decided here

[RFC 84](../rfc/0084-backstage-plugin-open-questions.md) asks the owner:

- the repository of the plugin, and whether to give it to the Backstage
  community plugins repository later
- whether a later slice reads as the Backstage user, so that each user
  sees only the units that the user can read.

## Consequences

- The server changes G1 to G6 of decision 8 come first. Each is a small
  change of the tool-neutral API, and G1 is already decided by ADR 210.
- The console gets a path by unit ID (G5). That change is in
  `OpenDustpan/console`.
- The plugin work cannot start until the owner makes the repository, or
  chooses another place (RFC 84).
- One read-only principal shows its data to each Backstage user who can
  see the tab. An operator who wants less sets a Backstage permission
  policy, an access limit, or narrows the grants of the principal.
- An entity owner in Backstage can show any unit of the listed namespaces
  on the page of the entity.
- A principal with `unit:write` in a listed namespace can make a unit that
  matches the labels of any entity.
- A rename of a unit breaks a reference in an annotation. The label form
  does not break.
- `GLOSSARY.md` gets no new term. The plugin uses the words of the
  inventory and of ADR 13.
