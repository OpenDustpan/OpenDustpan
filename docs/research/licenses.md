# Research: licenses of comparable projects and our dependencies

Question (issue #29): Which licenses do comparable self-hosted
infrastructure-as-code backends and their tools use, what did each say about
why, and what do the licenses of our dependencies allow or forbid? What does
each candidate license mean for an SDK that others embed (ADR 2)?

Date: 2026-10-10

This note gives facts. It does not choose a license. The owner decides in the
ticket "Choose the license". It is not legal advice.

## Context

- The repository has no `LICENSE` file. The README says "Not yet chosen."
  The Console `package.json` says `"license": "UNLICENSED"`.
- ADR 2: the root package `dustpan` is a Go library. Other companies may import
  it and write their own `main`. Ports are public API. This is what makes the
  license matter beyond the binary.
- ADR 12: a tool is taken on only if "its license lets the e2e suite run its
  CLI". OpenTofu (MPL-2.0) and the Pulumi CLI (Apache-2.0) both pass. We run
  their binaries and do not link their code.

## 1. Comparable projects

Method: each LICENSE file was read at the default branch on 2026-10-10. A
"stated reason" is only what the project said in a primary source. Where no
statement was found, the table says so.

| Project | License | Since | Stated reason | Source |
| --- | --- | --- | --- | --- |
| Terraform (HashiCorp) | MPL-2.0, then BSL 1.1 | MPL-2.0 from 2014; BSL 1.1 from 2023-08-10 | "to ensure continued investment in its community and to continue providing open, freely available products". The post says HashiCorp APIs, SDKs and almost all other libraries stay MPL-2.0. | https://www.hashicorp.com/en/blog/hashicorp-adopts-business-source-license |
| OpenTofu | MPL-2.0 (copyright "The OpenTofu Authors" and HashiCorp, Inc.) | Fork announced 2023-08-25, under the Linux Foundation | Manifesto: return Terraform "to a fully open-source license" because BSL "is not open source". Announcement: a "well-known and widely-accepted license that companies can trust, that won't suddenly change in the future". | https://opentofu.org/manifesto/ , https://opentofu.org/blog/opentofu-announces-fork-of-terraform/ , https://raw.githubusercontent.com/opentofu/opentofu/main/LICENSE |
| Pulumi CLI and SDKs | Apache-2.0 | Not checked | No statement found. The 1.0 post only says the SDK, CLI and libraries are "open source and free to use". | https://raw.githubusercontent.com/pulumi/pulumi/master/LICENSE , https://www.pulumi.com/blog/pulumi-1-0/ |
| OTF (leg100/otf) | MPL-2.0 | Not checked | No statement found. | https://raw.githubusercontent.com/leg100/otf/master/LICENSE |
| Terrakube | Apache-2.0 (copyright "Alfredo España") | Not checked | No reason found. The README says HashiCorp confirmed Terrakube is compatible with the Terraform BSL license. | https://raw.githubusercontent.com/AzBuilder/terrakube/main/LICENSE , https://raw.githubusercontent.com/AzBuilder/terrakube/main/README.md |
| OpenTaco (formerly Digger) | MIT (copyright 2025 Digger) | Rename on 2025-11-07 | No reason found in the README or LICENSE. A web search summary (not a primary source, not verified) says the project moved from Apache-2.0 to MIT and rewrote MPL-licensed code it had borrowed from OTF. Verify in the repository history before relying on it. | https://github.com/diggerhq/opentaco , https://raw.githubusercontent.com/diggerhq/digger/develop/LICENSE , https://raw.githubusercontent.com/diggerhq/digger/develop/README.md |
| Atlantis | Apache-2.0 | Not checked | No statement found. | https://raw.githubusercontent.com/runatlantis/atlantis/main/LICENSE |
| Spacelift, open parts (`spacectl`) | MIT (copyright Spacelift, 2021) | Not checked | No statement found. Spacelift's platform is not open source; this note did not read its terms. | https://raw.githubusercontent.com/spacelift-io/spacectl/main/LICENSE |

Observations:

- Every comparable project is permissive (Apache-2.0, MIT) or file-level
  copyleft (MPL-2.0). None uses AGPL-3.0 or a source-available license today.
- The one source-available move in this field is HashiCorp's BSL 1.1. It caused
  the OpenTofu fork, and the fork's stated aim was a license that "won't
  suddenly change". A relicense after adoption has a visible cost.
- The two projects closest to OpenDustpan in role (OTF, Terrakube) use MPL-2.0
  and Apache-2.0.
- Gaps: "since" dates for most rows, and the reasons for Pulumi, OTF,
  Terrakube, Atlantis and Spacelift, were not found in primary sources.

## 2. Dependencies

Source: the `LICENSE` file of each module in the Go module cache, and the
`license` field of each package in the Console `package-lock.json` (no
`node_modules` is installed). Not checked: npm license texts themselves.

### Go (`go.mod`, 16 modules, all compiled into the binary)

| Module | License | Constraint |
| --- | --- | --- |
| github.com/jackc/pgx/v5 v5.11.0 | MIT | Keep copyright and permission notice. |
| github.com/jackc/pgpassfile v1.0.0 | MIT | Same. |
| github.com/jackc/pgservicefile v0.0.0-20240606... | MIT | Same. |
| github.com/jackc/puddle/v2 v2.2.2 | MIT | Same. |
| modernc.org/sqlite v1.60.1 | BSD-3-Clause. The translated code is SQLite, which is in the public domain (`LICENSE-SQLITE`). | Keep notice. Its `LICENSE-3RD-PARTY.md` lists each component that it links into a program; all of them are MIT, BSD-3-Clause or public domain. The `vec` package (sqlite-vec, MIT) is not linked into our binary. |
| modernc.org/libc v1.77.1 | BSD-3-Clause. Its `LICENSE-3RD-PARTY.md` adds Go (BSD-3-Clause), musl libc, go-netdb and NixOS/nixpkgs (MIT). | Keep the notices. |
| modernc.org/memory v1.12.1 | BSD-3-Clause, plus `LICENSE-GO` and `LICENSE-MMAP-GO` (BSD-3-Clause) | Keep the notices. |
| modernc.org/mathutil v1.7.1 | BSD-3-Clause | Keep notice. |
| github.com/dustin/go-humanize v1.0.1 | MIT | Keep notice. |
| github.com/google/uuid v1.6.0 | BSD-3-Clause | Keep notice. |
| github.com/mattn/go-isatty v0.0.24 | MIT | Keep notice. |
| github.com/ncruces/go-strftime v1.0.0 | MIT | Keep notice. |
| github.com/remyoudompheng/bigfft v0.0.0-20230129... | BSD-3-Clause | Keep notice. |
| golang.org/x/sync v0.23.0 | BSD-3-Clause, plus a Go `PATENTS` file | Keep notice. The PATENTS grant ends if you sue Google over the software for patent infringement. |
| golang.org/x/sys v0.48.0 | Same | Same. |
| golang.org/x/text v0.42.0 | Same | Same. |

Result: no copyleft and no non-OSI license in `go.mod`. Every module allows
use in a closed-source product, in an Apache, MIT, MPL, AGPL or source-available
OpenDustpan, with notice preservation as the only condition. Tools that run in
`make check` (golangci-lint, sqlc) are not in `go.mod` and were not checked.

### Console (`package.json`, 2 dependencies, 15 devDependencies)

| Package | License | Constraint |
| --- | --- | --- |
| react 19.3.0, react-dom 19.3.0 | MIT | Shipped in the built SPA. Keep notice. |
| @eslint/js, eslint, eslint-config-prettier, eslint-plugin-jsx-a11y, eslint-plugin-react-hooks, eslint-plugin-unicorn, globals, prettier, @types/react, @types/react-dom, @vitejs/plugin-react, typescript-eslint, vite, vitest | MIT | Build and lint only. Not shipped, apart from code that vite bundles. |
| typescript 6.0.3 | Apache-2.0 | Build only. |

Transitive packages in `package-lock.json` (335 besides the root): MIT 276,
Apache-2.0 19, ISC 13, MPL-2.0 13, BSD-2-Clause 8, BSD-3-Clause 2, and one each
of BlueOak-1.0.0 (minimatch), Python-2.0 (argparse), CC-BY-4.0 (caniuse-lite)
and CC0-1.0 (language-subtag-registry). All are dev-only in the lock file.

Flags:

- MPL-2.0 (13): `lightningcss` (and its 11 platform binaries) and `axe-core`.
  They are dev-only (vite and the a11y lint). MPL-2.0 is file-level copyleft
  and applies to distribution of those files. We do not distribute them, so it
  constrains nothing now. Check again if vite starts to inline any of that code
  in the shipped bundle.
- CC-BY-4.0 `caniuse-lite` is data, not code. It needs attribution if
  redistributed. It is dev-only.
- No GPL, LGPL, AGPL or source-available license was found.
- The Console itself says `UNLICENSED`. It needs its own license decision
  (ADR 8 keeps it in a separate repository).

## 3. What each candidate means

Sources for license terms: Apache-2.0 text https://www.apache.org/licenses/LICENSE-2.0 ;
MPL FAQ https://www.mozilla.org/en-US/MPL/2.0/FAQ/ ; AGPL rationale
https://www.gnu.org/licenses/why-affero-gpl.html ; GPL FAQ
https://www.gnu.org/licenses/gpl-faq.html ; BSL 1.1 https://mariadb.com/bsl11/ ;
Elastic License 2.0 https://www.elastic.co/licensing/elastic-license ; FSL
https://fsl.software/ . "Embed" means: a company imports package `dustpan`
into its own Go program.

| | Apache-2.0 | MPL-2.0 | AGPL-3.0 | Source-available (BSL 1.1, ELv2, FSL) |
| --- | --- | --- | --- | --- |
| OSI open source | Yes | Yes | Yes | No. HashiCorp's BSL change was called "non-open source" by OpenTofu. |
| (a) Embed the SDK in a closed product | Allowed. Keep license, notices and change marks (section 4). | Allowed. Changes to our files stay MPL and must be shared on distribution. The company's own new files stay closed (FAQ Q11). | The GPL FAQ says linking makes the whole combination covered. A closed product that imports the SDK would need AGPL too, or a commercial license from us. | Depends on terms. BSL denies production use unless an Additional Use Grant allows it. ELv2 and FSL restrict competing or managed-service use. Embedding in a competing product is likely barred. |
| (b) Hosted service built on it | Allowed. | Allowed. Server-side use is not "distribution" (FAQ Q17). The company shares changes only if it ships binaries. | Allowed, but users of the service must be able to get the source of the modified version ("your server must also allow them to download the source code"). | ELv2 forbids providing the software "as a hosted or managed service". FSL forbids "undermin[ing] its producer". BSL depends on the grant. |
| (c) Contributors | Express patent grant, ends if the licensee sues over the Work (section 3). Inbound = outbound by default (section 5), so a DCO is enough. A CLA is needed only to relicense later. | Patent grant in section 2. Same inbound = outbound. A CLA is needed to relicense. | GPLv3-family patent terms (not read for this note). A CLA is common when the owner wants to sell a commercial license. | A CLA (or copyright assignment) is normally needed, because the owner must keep the right to relicense or to convert. |
| (d) With our dependencies | Compatible with all MIT, MIT-0, BSD-3 and Apache deps above. | Compatible with all deps above. | Compatible: permissive deps may go into an AGPL work. The reverse is not true: the AGPL work cannot go into a permissive product. Apache-2.0 into GPLv3 was not verified here. | Compatible: permissive deps impose only notice duties. |

Points for the SDK that apply to all rows:

- The copyleft question depends on how the importer links. Go links
  statically, so "uses the SDK" means "combined work" for the GPL family.
- Custom adapters (ADR 2) are separate packages that import our ports. Under
  MPL-2.0 they stay the author's. Under AGPL-3.0 they probably fall under the
  AGPL when linked into one binary.
- ADR 12 lets us run tools' CLIs in e2e. None of the four candidates blocks
  that: we run OpenTofu (MPL-2.0) and Pulumi (Apache-2.0) as separate
  processes.
- Changing license later is hard once outside contributions exist without a
  CLA. HashiCorp and the OpenTofu fork show the cost of a change after
  adoption.
- A trademark policy is separate from any of these licenses. Apache-2.0 grants
  no trademark rights (section 6).

## What the facts suggest

The field is permissive or MPL. Dependencies set no limit: they accept every
candidate. So the choice depends on goals, not on constraints. If the goal is
wide embedding of the SDK, Apache-2.0 or MPL-2.0 fit best, and Apache-2.0 adds
an express patent grant. MPL-2.0 keeps changes to our files open without
reaching an importer's own code. AGPL-3.0 and source-available licenses mostly
help block hosted competitors, and they limit embedding, which ADR 2 invites.
Decide the CLA or DCO policy together with the license.

## Open items

- Find the first-release dates and any stated reasons for Pulumi, OTF,
  Terrakube, Atlantis and Spacelift.
- Check the OpenTaco license history in its repository.
- Check dev tools outside `go.mod` (golangci-lint, sqlc) if they are
  redistributed.
