---
rfc: 61
title: A docs site and a landing page
status: discussing
created: 2026-10-11
---

# RFC 61. A docs site and a landing page

Status: discussing.

## Problem

All user documentation is Markdown in this repository: the README, four
guides in `docs/guides/`, a configuration reference and the TFE-compatible
API notes. GitHub renders it. There is no search, no navigation across
pages, no page per CLI, and no page that a search engine or a reader can
find before they find the repository.

The README must do the work of a landing page, a quickstart and a docs
index at the same time. Each new guide makes it longer.

Readers judge a pre-alpha project by its first page. The product direction
records two reader reactions that matter here. Readers close pages with
emoji, stock words and AI-made art. Readers trust a real screenshot and a
real command more than a claim. Plain, exact pages are rare, so they stand
out.

This RFC asks four questions:

1. Which tool builds the docs site?
2. Where do the pages and the site live?
3. How are the pages sorted?
4. What rules hold the landing page?

A new repository and new dependencies need the approval of the project
owner (`CLAUDE.md`). The owner approved the tool and the repository. The
section "Approvals" lists what is still open.

## Options

### The site tool

**Astro Starlight.** A static site generator for docs, built on Astro. It
ships full-text search (Pagefind, which runs in the browser with no
service), dark mode, and a schema for the front matter of each page that the
build checks. Cloudflare moved more than 4,000 pages of its developer docs
to it. It needs Node.js and npm packages at build time. The output is static
HTML with no server.

**Docusaurus.** A static site generator from Meta, on React. It has docs
versions built in and a large user base. Search needs Algolia, a hosted
service, or a community plugin.

**MkDocs with Material.** A Python static site generator. Its maintainers
moved Material for MkDocs to maintenance mode in November 2025. A new site
should not start on it.

**Do nothing.** Keep Markdown on GitHub. It costs nothing and needs no
dependency. Readers get no search, no landing page, and no page per tool.
The README keeps growing.

### Where the pages live

**All in this repository.** Pages, theme and landing page sit next to the
code. One pull request changes an endpoint and its docs. The Node.js
dependencies of the site enter this repository, and its gates (`make trivy`,
the license scan) must cover them.

**All in a new repository.** Pages, theme and landing page sit in
`OpenDustpan/website`. The code repository stays Go only. A docs change and
its endpoint change land in two pull requests in two repositories, and they
can drift apart.

**Hybrid.** OpenTofu uses this layout. The pages stay in this repository as
plain Markdown, under `website/docs/` or under `docs/`. A new repository,
`OpenDustpan/website`, holds the site tool, the theme and the landing page.
It pulls this repository as a Git submodule at build time. A docs change
ships in the same pull request as its endpoint, and `make docs-lint` checks
it. This repository gets no Node.js dependency.

## Proposal

### Tool and layout

Use Astro Starlight in the hybrid layout. Keep the pages in `docs/` of this
repository, so that every link that exists today keeps working. The site
shows them under `/docs/`. Docusaurus is the second choice,
if Starlight turns out to need a feature that it does not have.

The website repository has its own gates: a pinned Node.js version, a
lockfile, a license scan, a link check, and a build of the site on each pull
request. A broken link fails the build.

### One site: the landing page and the docs

`opendustpan.org` holds the landing page and the docs. One website
repository builds both in one Astro build, and one deploy publishes both.

| Path | Content | Made with |
| --- | --- | --- |
| `/` | The landing page: what OpenDustpan is, the tools it works with, the quickstart, links to the docs and to the code repository | A custom Astro page, `src/pages/index.astro` |
| `/docs/...` | The docs, with the sections below | Starlight, from the Markdown pages of this repository |
| `/opendustpan/...` | The `go-import` answers for the Go module path | Static pages that the build makes (see "The Go module path") |

Starlight can make a landing page itself: a page with `template: splash`
and a `hero` block has no sidebar and shows a title, a tagline, an image and
buttons. That is enough for a docs home page. It is not enough for the
landing page, because the page keeps the Starlight header and layout, and
the hero has one fixed shape. The landing page rules below need a terminal
block, a console screenshot and a status line in a layout of our own.

So the landing page is a custom Astro page in `src/pages/`. Astro and
Starlight build it in the same run, with no extra dependency. The docs home
page at `/docs/` uses the Starlight splash template.

The landing page and the docs share one look. Both take the logo, the colors
and the voice from the brand book in the `design/` directory of the console
repository. The site never uses the name, logo or colors of another project
as ours. It names other tools only to say that OpenDustpan works with them.

### Information architecture

Sort the pages by Diataxis: each page has one type and one reader. A
public file in the website repository records the sections, the type of
each, and its reader, as Temporal does.

| Section | Type | Content |
| --- | --- | --- |
| Get started | Tutorial | One quickstart per CLI: Pulumi CLI, OpenTofu. Each ends at a working `up` or `apply`. |
| Migrate in | How-to | Import state from each other backend: Pulumi Cloud, Pulumi DIY backends, HCP Terraform and TFE, OpenTofu backends. |
| Migrate out | How-to | Move each unit from OpenDustpan to each other backend, with its history. The same weight as Migrate in. |
| How-to | How-to | Back up and restore, upgrade, S3 storage, TLS behind a proxy. |
| Operate | How-to | Observe the server, probes, metrics, maintenance mode, size limits. |
| Reference | Reference | Configuration, the installation API from its OpenAPI document, the tested CLI versions, error messages. |
| Explanation | Explanation | Architecture, the glossary. The security model: sign-in, roles, encryption at rest, where keys live, no telemetry. |
| Compliance | Reference | For operators: which features support controls of DORA, NIS2, PCI DSS and ISO 27001. Examples: the audit log, backup and restore tests, change records, MFA through the identity provider, export. Each item says "shipped" or "planned". The page says "This is not legal advice" and claims no compliance and no certification. |
| Compare | Explanation | OpenDustpan next to other backends, with "When not to use OpenDustpan". |

Each page carries a label for the tool it covers: Pulumi CLI, OpenTofu, or
both.

A migration guide, in or out, ships only with an e2e step that runs it.
Until then, the page says what the suite tests and what it does not.

One page under Migrate out lists the migration guides that other vendors
publish for a move from OpenDustpan, when a vendor asks to be listed. The
page says that a listing is not an endorsement.

Each page has "Edit this page", which opens the file in this repository.

The site publishes `llms.txt`, and each page is also available as plain
Markdown at the same path with `.md`.

### URL policy

Never break a doc URL. A page that moves gets a redirect from its old path.
The link check in the website build covers the redirects.

### Versions

No versioned docs before 1.0. The site shows the docs of `main` and says
so on each page. The tested CLI versions appear on the reference page.

### Landing page rules

- A headline that says what OpenDustpan is, in the words of the README.
- The tools it works with, and the CLI versions that the e2e suite tests.
- The quickstart of the README, and links to the docs and to the code
  repository. The repository URL is set in one place in the site
  configuration, because the repository may move.
- A real terminal block: commands and output from the quickstart, copied from
  a real run.
- A real screenshot of the web console.
- The status line of the README, word for word.
- The license and "self-hosted" up front.
- A link to "Leaving OpenDustpan": the way out is part of the offer.
- No AI-generated art, no gradients, no stock icons.
- No customer logos, no testimonials and no numbers until they are real.
- The visual system of the console: its colors and type, no shadows.
- No emoji, and the banned words of `make docs-lint`.

### The Go module path

The Go module path of the server is `opendustpan.org/opendustpan`. The site
at `opendustpan.org` must keep that import path working. A change to the
site that breaks it breaks `go get` for every user of the SDK.

What the go command needs, from `go help importpath` (Go 1.27.2) and
<https://go.dev/ref/mod#vcs-find>:

- HTTPS. The go command asks for `https://opendustpan.org/opendustpan?go-get=1`.
- A `<meta name="go-import" content="opendustpan.org/opendustpan git <repository URL>">`
  tag early in `<head>`, before any script or style.
- The go command follows redirects and ignores the status code, so a 404
  page that carries the tag also works.

In module mode, only the module path itself must carry the tag. A read of
the Go 1.27.2 source (`cmd/go/internal/modfetch/repo.go`) shows that the go
command skips a package path that does not answer and falls back to the
module path. The Go documentation does not promise this, so the site serves
the tag on every path under `/opendustpan/`.

The repository URL in the tag is set in one place, because the repository
may move. While the repository is private, users also need
`GOPRIVATE=opendustpan.org/*` and Git credentials.

The website build checks the tag before each deploy:

```sh
for p in opendustpan opendustpan/pkg/blob opendustpan/internal/api/tfe; do
  curl -sSL "https://opendustpan.org/$p?go-get=1" | grep -o '<meta name="go-import"[^>]*>'
done
GOPROXY=direct GOPRIVATE='opendustpan.org/*' go list -x -m -json opendustpan.org/opendustpan@latest
```

The check passes when each loop prints the tag. With credentials, it also
passes when `go list` prints `"Path": "opendustpan.org/opendustpan"` and a
version. Without credentials, `go list -x` must show the request to
`opendustpan.org` and then the `git` command for the right repository.

### Host

This comparison uses the official docs and price pages in the sources
below, read on 2026-10-11. "Unconfirmed" marks what that reading could not
check. The site is small: under 10 GB of traffic a month.

| | Cloudflare Workers static assets | GitHub Pages | Netlify Free | S3 and CloudFront free tier |
| --- | --- | --- | --- | --- |
| Cost | $0 | $0 | $0, with a hard cap of 300 credits a month | $0 |
| Private repository, free plan | Yes, deploy from GitHub Actions with `wrangler` | No. The free plan serves Pages from public repositories only | Organization repositories need Pro. Deploy from Actions: unconfirmed | Yes, deploy from GitHub Actions |
| Apex domain with HTTPS | Yes. The DNS zone of `opendustpan.org` must move to Cloudflare | Yes, with A and AAAA records | Yes | Yes, with Route 53. Detail unconfirmed |
| Deploy flow | Actions: `astro build`, then `wrangler deploy` | Actions: `astro build`, `upload-pages-artifact`, `deploy-pages` | Git integration or `netlify deploy` | Actions: `astro build`, `aws s3 sync`, cache invalidation |
| Preview per pull request | Yes, with Workers Builds | None built in (unconfirmed) | Yes | None built in |
| `go-import` on every path | One rule in `_redirects`: `/opendustpan/* /go-import.html 200` | No rewrites: a static page per package path, and the tag in `404.html` | One rewrite rule | An edge function or the error page (unconfirmed) |
| Limits that matter | 100,000 Worker requests a day. Requests for static files are free | 1 GB site, 100 GB a month, no use for an online business | Each production deploy costs 15 credits, and at 0 credits all sites stop | 1 million requests and 100 GB a month |

All four build in GitHub Actions. The GitHub Free plan includes 2,000
Actions minutes a month for private repositories, on runners that GitHub
hosts. At about 2 minutes a build, a deploy on each merge to `main` and each
pull request fits. A deploy on every push may not.

CI of the project runs on runners that the project owner hosts. The
repository variable `CI_RUNS_ON` names them. The workflows of the website
repository, the Cloudflare deploy among them, take `runs-on` from the same
variable, with the same expression as `.github/workflows/ci.yml` of this
repository:

```yaml
runs-on: ${{ fromJSON(vars.CI_RUNS_ON || '"ubuntu-latest"') }}
```

`ubuntu-latest` is only the fallback for a repository with no variable. No
workflow names a runner in any other way. Jobs on the owner's runners use
no GitHub minutes.

Decision of the project owner: Cloudflare Workers static assets, deployed
with `wrangler` from GitHub Actions. It costs nothing, works from a private
repository today, and serves the `go-import` tag on every path with one
rule. The DNS zone of `opendustpan.org` is already on Cloudflare, so the
site needs no DNS move. The website build also writes
`public/opendustpan/index.html`, so the module path is a real file.

Rejected:

- GitHub Pages. The free plan serves no Pages from a private repository. It
  has no rewrites, so each package path needs its own page, and it has no
  previews.
- Netlify Free. Each production deploy costs 15 credits, against a hard cap
  of 300 credits a month: 20 deploys.
- S3 and CloudFront. It works, but the `go-import` rule needs an edge
  function, and the site would add a second cloud account.

Sources:

- GitHub plans: <https://docs.github.com/en/get-started/learning-about-github/githubs-plans>
- GitHub Pages limits and 404 pages: <https://docs.github.com/en/pages/getting-started-with-github-pages/github-pages-limits>,
  <https://docs.github.com/en/pages/getting-started-with-github-pages/creating-a-custom-404-page-for-your-github-pages-site>
- GitHub Pages custom domains: <https://docs.github.com/en/pages/configuring-a-custom-domain-for-your-github-pages-site/managing-a-custom-domain-for-your-github-pages-site>
- Cloudflare Workers static assets: <https://developers.cloudflare.com/workers/static-assets/>,
  <https://developers.cloudflare.com/workers/static-assets/billing-and-limitations/>,
  <https://developers.cloudflare.com/workers/platform/pricing/>
- Cloudflare redirects and custom domains: <https://developers.cloudflare.com/pages/configuration/redirects/>,
  <https://developers.cloudflare.com/workers/configuration/routing/custom-domains/>
- Netlify pricing and credits: <https://www.netlify.com/pricing/>,
  <https://docs.netlify.com/manage/accounts-and-billing/billing/billing-for-credit-based-plans/how-credits-work/>
- CloudFront pricing: <https://aws.amazon.com/cloudfront/pricing/>
- Go remote import paths: <https://go.dev/ref/mod#vcs-find>

### Approvals

On 2026-10-11 the project owner approved Astro Starlight as the tool, in a
separate website repository. That approval covers:

1. A new website repository.
2. New dependencies in that repository: Astro, Starlight and their npm
   dependencies, and Node.js as a build tool.

The owner also decided:

3. The domain: the site lives at `opendustpan.org`.
4. The host: Cloudflare Workers static assets (see "Host").
5. The owner of the website repository: the project owner, unless the owner
   names another person.

Still open: the rest of this proposal, which is the layout of the pages, the
sections, the URL policy and the landing page rules.

## Open questions

None. The owner answered every question. See "Owner direction (2026-10-12)".

## Owner direction (2026-10-12)

The project owner answered questions in this RFC on 2026-10-12. Words in
quotation marks are the words of the owner. A question that the owner sent to
research or to a spike stays in "Open questions", with a mark. This section
decides nothing. An ADR records a decision (ADR 15).

- Question "Do the pages stay in `docs/` or move to `website/docs/`?": "Yes."
- Question "Do ADRs and RFCs appear on the site, or stay on GitHub only?":
  "GitHub only for now."
- Question "Does the terminal recording on the landing page come from a committed
  VHS script that CI runs?": "Sure."

## Outcome

Empty until the RFC is accepted or dropped.
