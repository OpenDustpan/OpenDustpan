# 139. Runner dependencies and the runner module

Status: proposed · 2026-10-18 · decides the choices that ADR 136 decision 9 and ADR 137 decision 4 park

## Context

[ADR 136](0136-the-kubernetes-coordinator.md) decision 9 parks the choice
of how the Kubernetes coordinator speaks to the Kubernetes API: with
`client-go`, or with a small client of our own.
[ADR 137](0137-the-machine-coordinator.md) decision 4 parks the choice of
how the machine coordinator gets and unpacks an OCI image. The code of
the two coordinators does not land before the project owner decides.

The project owner gave this direction on 18 October 2026:

- The two dependencies are for the runner only. They must not enter the
  server binary or the module list of the server.
- The owner expects `client-go` and go-containerregistry, and accepts the
  options that a spike shows to be better.
- Go code that we build together for the same version stays in one
  repository. So the boundary is a Go module, not a new repository.

[ADR 134](0134-the-launcher-contract.md) keeps the launcher on the
standard library. This ADR does not change that: the launcher never links
either dependency.

A spike on 18 October 2026 measured each option with small programs that
are not in the repository. All sizes are of static binaries built with
`CGO_ENABLED=0 go build -trimpath -ldflags='-s -w'` for `linux/amd64` with
Go 1.27.2, unless a row says otherwise. Module counts are the `dep` lines
of `go version -m` (modules linked into the binary) or the `require`
lines of `go.mod` (modules that `make trivy` scans).

### The Kubernetes client

Each program does the calls of ADR 136: create a Secret, create a Job,
watch the pods of a label selector, stream a pod log, delete a pod, and
create a TokenReview. Projected service account tokens are part of the
pod template, so they need no API call.

| Option | Binary | Added | Linked modules | Licenses (Trivy) |
| --- | --- | --- | --- | --- |
| Standard library HTTP and JSON only (baseline) | 6.74 MB | 0 | 0 | none |
| A client of our own on the standard library (340 lines) | 6.67 MB | 0 | 0 | none |
| `k8s.io/api` types and a transport of our own | 9.69 MB | 2.95 MB | 18 | not scanned |
| `client-go` v0.37.1, typed core, batch and authentication clients only | 26.58 MB | 19.84 MB | 46 | not scanned |
| `client-go` v0.37.1, full `kubernetes.Clientset` | 26.84 MB | 20.10 MB | 46 | 30 Apache-2.0, 13 BSD-3-Clause, 6 MIT, 1 ISC |

- The typed clients save only 0.26 MB: the package `kubernetes/scheme`
  registers the types of every API group, and each typed client imports
  it.
- The license gate of `make trivy` (`scripts/trivy-license-gate.py`)
  passes for `client-go`: 46 requirements, each with a license result,
  none restricted.
- `govulncheck` v1.8.0 finds 5 vulnerabilities that `client-go` v0.37.1
  reaches in `golang.org/x/net` v0.57.0 (GO-2026-6603, GO-2026-6610,
  GO-2026-6611, GO-2026-6612, GO-2026-6617). `golang.org/x/net` v0.60.0
  fixes them, and with it `govulncheck` finds none. Trivy also reports
  CVE-2026-56851 in `golang.org/x/text` v0.40.0, which v0.41.0 fixes. The
  server module already requires `golang.org/x/text` v0.42.0.
- The Go vulnerability database lists 2 entries for `client-go`
  (GO-2021-0064, GO-2021-0065), 2 for `k8s.io/apimachinery`
  (GO-2022-0965, GO-2024-2748), and 1 for `github.com/emicklei/go-restful/v3`
  (GO-2022-0619). None affects v0.37.1.
- API stability: `client-go` is a `v0.x` module. Its Go API can change in
  each minor version, and Kubernetes releases about 3 minor versions each
  year. Its README marks a cluster of another minor version as "+-": it
  works for the APIs that both versions have. The coordinator uses only
  `v1` APIs (`core/v1`, `batch/v1`, `authentication.k8s.io/v1`), which
  Kubernetes keeps across versions. The validation matrix of ADR 136
  decision 10 is the proof for each release.
- Tests: `client-go` gives `kubernetes/fake`, a clientset in memory. It
  does not act as an API server: it does not send watch bookmarks, does
  not return `410 Gone` for an old resource version, and does not review
  tokens. A client of our own tests against an `httptest` server, which
  must copy those behaviors. Both need the k3s run of ADR 136 decision 10
  for the real behavior.
- Code that we own: with `client-go`, the adapter of `runner.Cluster`. With
  a client of our own, the 340 lines of the spike are a transport only.
  It does not retry, does not limit the request rate, and has no
  kubeconfig for a coordinator out of a cluster. Its own types hold only
  the fields that it reads. The pod template of a team holds the whole
  `PodSpec`, and the template rules of ADR 136 read many of its fields. A
  partial type drops the fields that it does not know, so the client must
  either copy the full `PodSpec` type or edit untyped JSON. The spike
  estimates 1,500 lines or more with tests for a complete client. This is
  an estimate, not a measurement.

### The OCI image library

Each program pulls an image by digest and writes the flattened file
system as a tar stream.

| Option | Binary | Added | Linked modules | `go.mod` requirements | Licenses (Trivy) |
| --- | --- | --- | --- | --- | --- |
| Standard library HTTP and JSON only (baseline) | 6.74 MB | 0 | 0 | 0 | none |
| go-containerregistry v0.22.1 (`remote`, `mutate.Extract`, `authn.DefaultKeychain`) | 7.88 MB | 1.13 MB | 9 | 10 | 6 Apache-2.0, 3 MIT, 3 BSD-3-Clause |
| `go.podman.io/image/v5` v5.41.2 (the former `containers/image`), copy to an OCI layout | 24.01 MB | 17.26 MB | 78 | 82 | 43 Apache-2.0, 23 BSD-3-Clause, 19 MIT, 2 MPL-2.0, 2 Unlicense, 1 BSD-2-Clause |

- go-containerregistry links `github.com/docker/cli` (to read a Docker
  config file and run credential helpers), `docker-credential-helpers`,
  `klauspost/compress` (zstd layers), `opencontainers/go-digest`,
  `opencontainers/image-spec`, `sirupsen/logrus` (through
  `docker/cli`), `golang.org/x/sync` and `golang.org/x/sys`. The
  package `authn` imports `docker/cli` itself, so a keychain of our own
  saves only 0.13 MB and no module.
- `govulncheck` and Trivy find no vulnerability in go-containerregistry
  v0.22.1 and its modules. The Go vulnerability database has no entry for
  go-containerregistry. It lists 2 for `docker/cli` (GO-2024-2912,
  GO-2026-4610), 1 for `klauspost/compress` (GO-2026-5841) and 1 for
  `sirupsen/logrus` (GO-2025-4188). None affects the linked versions.
- `go.podman.io/image/v5` links OpenTelemetry (`otelhttp`), gRPC,
  sigstore and `mattn/go-sqlite3`. It needs build tags to build with no C
  library, and it does not unpack layers: that needs `containers/storage`
  or `umoci` in addition.
- Digest pinning: `name.NewDigest` parses only a reference with a digest.
  `remote` checks the digest of each manifest against the reference
  (`fetcher.go`) and the digest and size of each blob as it reads
  (`verify.ReadCloser`).
- Whiteouts and paths: `mutate.Extract` reads the layers from the top,
  applies `.wh.` files and `.wh..wh..opq` directories, returns an error
  for a path that leaves the root, and drops a relative symbolic or hard
  link whose target leaves the root. It keeps a link with an absolute
  target.
- Authentication: `authn.DefaultKeychain` reads a Docker config file. Its
  `credHelpers` and `credsStore` run `docker-credential-<name>` programs,
  for example `docker-credential-ecr-login` or `docker-credential-gcr`.
  The keychains of go-containerregistry for Google and for Kubernetes
  pull secrets link cloud SDKs, and the spike did not use them.
- Rootless: go-containerregistry is HTTP and tar code. It needs no daemon
  and no privilege.
- Tests: the package `pkg/registry` of the same module is a registry in
  memory that runs in an `httptest` server.
- A registry client of our own needs the token challenge of the registry
  protocol, image indexes and the choice of a platform, the Docker and
  OCI media types, redirects to blob storage, gzip and zstd layers,
  digest checks and whiteouts. The spike estimates 1,500 to 2,500 lines
  with tests. This is an estimate, not a measurement.
- A container runtime of the host, such as `containerd`, adds a daemon
  that runs as root to each machine host. The `process` sandbox of
  ADR 137 must also run on a laptop with no such daemon.

The spike also unpacked public images for `linux/amd64` with
`mutate.Extract` on 18 October 2026, to set the unpack limits. "Entries"
counts each file, directory and link of the flattened file system.

| Image | Compressed | Unpacked | Entries | Largest file |
| --- | --- | --- | --- | --- |
| `gcr.io/distroless/static-debian12:latest` | 0.72 MB | 2.11 MB | 1,436 | 0.22 MB |
| `docker.io/library/python:3.13-slim` | 43.06 MB | 114.76 MB | 5,638 | 6.52 MB |
| `ghcr.io/opentofu/opentofu:1.10` | 39.56 MB | 115.83 MB | 1,316 | 88.44 MB (`tofu`) |
| `docker.io/hashicorp/terraform:latest` | 51.11 MB | 148.49 MB | 1,213 | 120.57 MB (`terraform`) |
| `docker.io/pulumi/pulumi-nodejs:latest` | 306.10 MB | 746.97 MB | 13,289 | 150.28 MB (`node`) |
| `docker.io/pulumi/pulumi:latest` (all languages) | 3.38 GB | 10.53 GB | 331,804 | 150.28 MB (`node`) |

### The module boundary

The spike added a stub `dustpan-runner` command that imports
`pkg/runner`, `client-go` and go-containerregistry, in three ways, on a
copy of `main` at `89138bd`.

| Measure | A: same `go.mod`, the linker leaves it out | B: same `go.mod`, a build tag | C: nested module `cmd/dustpan-runner/go.mod` |
| --- | --- | --- | --- |
| Requirements in the server `go.mod` | 21 to 70 (49 lines added) | as A: `go mod tidy` keeps the requirements of tagged files | 21, no change |
| Server binary, stripped | 24,588,448 bytes, no change | no change | 24,588,448 bytes, no change |
| Modules linked into the server | no change | no change | no change |
| `govulncheck ./...` from the root | reports the 5 `golang.org/x/net` findings that only the runner reaches | does not scan the tagged files | scans the server only |
| Trivy from the root | scans every requirement of the one `go.mod` | as A | scans both `go.mod` files and reports each one |
| `golangci-lint run ./...` from the root | lints the runner | lints the runner only with the tag | does not lint the runner (0 issues on spike code that has 12 in its own module) |
| A server package imports a runner package | builds | builds with the tag | does not build: no module provides the package |

The runner stub weighs 2.41 MB alone, 26.83 MB with `client-go`, 7.90 MB
with go-containerregistry, and 27.72 MB with both (25.76 MB for
`linux/arm64`, 39.45 MB for `linux/amd64` with no `-s -w`). It links 55
modules. Its `go.mod` has 56 requirements.

With A or B, the shared requirements of the two binaries take the highest
version that either one needs. In the spike no linked module of the
server moved, but a later `client-go` can raise a version that the server
links.

## Decision

1. **`client-go` for the Kubernetes coordinator (owner).** The adapter of
   `runner.Cluster` uses `k8s.io/client-go` v0.37.1 with `k8s.io/api`
   v0.37.1 and `k8s.io/apimachinery` v0.37.1, all released on
   23 September 2026. It uses the typed clients and watches. It does not
   use informers or the dynamic client. It reads the pod template of the
   team into `corev1.PodTemplateSpec`, so that the template rules of
   ADR 136 see each field and no field gets lost. Tests use
   `kubernetes/fake` for the logic of the adapter and k3s for the real
   API server.
2. **go-containerregistry for the machine coordinator (owner).** The
   machine coordinator uses `github.com/google/go-containerregistry`
   v0.22.1, released on 4 September 2026. It uses the packages `name`,
   `authn`, `v1/remote` and `v1/mutate`, and `pkg/registry` in tests. It
   does not use `crane` itself, the Google keychain or the Kubernetes
   keychain.
   - It pulls each image only by the digest that the runner protocol
     gives (decision 6). It parses the reference with `name.NewDigest`,
     which refuses a tag.
   - `mutate.Extract` makes the flattened tar stream. The coordinator
     writes it to the bundle directory through an `os.Root`, which refuses
     each path and link that leaves the directory, also a link with an
     absolute target. It writes no device node, and it changes the owner
     of a file only when it runs as root. It applies the unpack limits
     of decision 7.
   - Credentials come from a Docker config file in the configuration
     directory of the coordinator, read by `authn.DefaultKeychain`. A
     cloud registry uses the credential helper of the cloud, installed on
     the host and named in `credHelpers`. No cloud SDK links into the
     runner.
3. **Pinned versions and the `golang.org/x/net` floor.** Each version is
   pinned exactly in `go.mod` and is at least two weeks old (ADR 7). The
   runner module requires `golang.org/x/net` v0.60.0 or later, because
   `client-go` v0.37.1 reaches 5 vulnerabilities in v0.57.0.
   The Go team released `golang.org/x/net` v0.60.0 on 8 October 2026,
   so it is two weeks old on 22 October 2026. The runner code lands on
   that day or later. There is no exception to the two-week rule for this
   fix: no feature needs the runner before that day.
4. **A nested Go module for the runner (option C).** `cmd/dustpan-runner`
   holds its own `go.mod`, with the module path
   `opendustpan.org/opendustpan/cmd/dustpan-runner`. The runner packages
   are under `cmd/dustpan-runner/internal/`. The module requires
   `opendustpan.org/opendustpan` and replaces it with `../..`, so the
   runner always builds from the same commit as the server. The server
   module never requires the runner module.
   - The release builds `dustpan-runner` from the same tag as the server.
     `go install ...@version` does not work for a module with a
     `replace` directive. The release archives and the image ship the
     runner.
   - The changes to the checks are in the list under "Consequences".
5. **A depguard rule keeps the imports out of the server.** The rule
   `runnerdeps` in `.golangci.yml` applies to every file outside
   `cmd/dustpan-runner/`. It denies `k8s.io`, `sigs.k8s.io`,
   `github.com/google/go-containerregistry` and the runner module. The
   nested module stops a server import at build time, and the rule
   stops it at lint time with the reason. The rule also covers the
   launcher, which stays on the standard library.
6. **Images are digests in the runner protocol.** The runner protocol
   carries the image of a phase only as a reference with a digest
   (`<repository>@sha256:<hex>`), never as a tag. When a unit or a pool
   names a tag, the server resolves the tag to a digest when it plans the
   job, and records the digest in the job spec. Each phase of the job
   uses that digest, also after a takeover. The Kubernetes coordinator
   puts the reference with the digest in the pod, and the machine
   coordinator pulls it. A coordinator refuses a claim whose image has
   no digest.
   - The server resolves a tag with a request for the manifest on the
     standard library (`HEAD`, and the token challenge of the registry
     protocol). It does not link go-containerregistry: the depguard rule
     of decision 5 still applies to the server.
7. **Unpack limits with defaults, for each pool.** The machine coordinator
   counts while it unpacks an image, and stops at the first limit that
   the image passes. Each pool can change each limit. The defaults give
   about 1.5 times the largest image of the table above, and more than
   10 times its largest file:

   | Limit | Default | Largest measured |
   | --- | --- | --- |
   | Total unpacked bytes | 16 GiB (17,179,869,184 bytes) | 10.53 GB |
   | Entries (files, directories and links) | 500,000 | 331,804 |
   | Size of one file | 2 GiB (2,147,483,648 bytes) | 150.28 MB |

   An image that passes a limit fails the phase before its first step.
   The coordinator deletes the files that it unpacked, and the phase
   reports an error that names the image digest, the limit, its value
   and the pool setting that changes it, for example: `Image
   sha256:65801d79252f... has more than 500,000 entries. Set a larger
   unpack.maxEntries on the pool, or use a smaller image.`

## Consequences

- The server binary, its `go.mod`, its `govulncheck` scope and its
  license list do not change.
- The runner image is about 25 MB larger than a runner on the standard
  library. Most of it is `client-go` (about 20 MB). The image of the
  machine coordinator ships the same binary.
- Each minor version of Kubernetes can bring a `client-go` version with
  changes to its Go API. The runner takes it at most once for each
  Kubernetes minor version, and the validation matrix runs before each
  release.
- `docker/cli` and `logrus` enter the runner through go-containerregistry.
  The runner code itself still logs with `log/slog`. depguard checks
  direct imports only.
- The Makefile and CI do more work: each Go check runs once for each
  module.
- A team that uses a tag gets the image of the tag at the time of the
  plan, not at the time of the apply. To use a new image, the team plans
  again.
- The server needs network access to the registries of the images, and
  credentials for a private registry, to resolve a tag. A unit that
  names a digest needs neither.
- The implementation must make these changes:
  1. `cmd/dustpan-runner/go.mod` with the module path
     `opendustpan.org/opendustpan/cmd/dustpan-runner` and
     `replace opendustpan.org/opendustpan => ../..`.
  2. `make lint`, `make lint-journal-build`, `make nilaway`, `make vuln`,
     `make test` and `make licenses` run in both modules. A run from the
     root does not include the nested module.
  3. `make trivy` runs `go mod download` in both modules, and gives both
     `go.mod` files to `scripts/trivy-license-gate.py`.
  4. The `gomoddirectives` settings in `.golangci.yml` allow the one local
     replacement of `opendustpan.org/opendustpan`, and no other.
  5. `.github/dependabot.yml` watches `/cmd/dustpan-runner` as well as
     `/`.
  6. The machine coordinator writes each unpacked file through an
     `os.Root`, because `mutate.Extract` keeps links with absolute
     targets.
  7. The release builds `dustpan-runner` from the same tag as the
     server, and ships the license files of the runner modules with the
     runner archive and image.
- If the image size of the Kubernetes coordinator becomes a problem, the
  third row of the first table (`k8s.io/api` types and a transport of our
  own, 2.95 MB) is the next option to measure.

## Open questions

- Where does the server keep the registry credentials that it uses to
  resolve a tag of a private image, and which action allows a namespace
  to set them?
