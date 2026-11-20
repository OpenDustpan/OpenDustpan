# syntax=docker/dockerfile:1
# Container image for OpenDustpan (ADR 27, decision 5).
#
# Build it with:
#
#   docker build -t opendustpan:dev \
#     --build-arg VERSION="$(git describe --tags --always --dirty)" \
#     --build-arg CONSOLE_URL=<console release tarball URL> \
#     --build-arg CONSOLE_SHA256=<tarball SHA-256> .
#
# scripts/smoke-image.sh builds with a local test tarball instead.

# Go stage: static binary with the version stamp of issue #91.
FROM golang:1.27.4-bookworm AS build
WORKDIR /src
ARG TARGETARCH
ARG VERSION=dev
# Refuse a VERSION outside the allowlist before any other step: the value goes
# into the go build line below. The check refuses and never edits the value, and
# its message does not print the value. Defense in depth: the scripts
# build-image-local.sh and smoke-image.sh check the value too; this check stays
# so that a direct docker build cannot skip them, and it does not rely on them.
RUN case "${VERSION}" in \
    ''|[!0-9A-Za-z]*|*[!0-9A-Za-z._+-]*) bad=1;; \
    *) bad=0;; \
  esac; \
  if [ "${#VERSION}" -gt 64 ]; then bad=1; fi; \
  if [ "${bad}" = 1 ]; then \
    echo "Dockerfile: build argument VERSION must be 1 to 64 characters: letters, digits, dot, plus and dash, and it must start with a letter or a digit; for example --build-arg VERSION=v0.1.0" >&2; \
    exit 1; \
  fi
COPY go.mod go.sum ./
# Optional BuildKit secret for builds behind a TLS-intercepting proxy: pass
# --secret id=extra_ca,src=<PEM bundle file> to trust one more CA bundle.
# A normal build passes no secret and this step changes nothing.
RUN --mount=type=secret,id=extra_ca,required=false sh -c 'if [ -s /run/secrets/extra_ca ]; then cat /run/secrets/extra_ca >> /etc/ssl/certs/ca-certificates.crt; fi; go mod download'
COPY . ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH="${TARGETARCH}" \
  go build -trimpath -ldflags "-X main.version=${VERSION}" \
  -o /out/opendustpan ./cmd/opendustpan
# The license files of the code linked into the binary, for /licenses below.
RUN ./scripts/third-party-licenses.sh linux "${TARGETARCH}" /out/third_party_licenses
# The data directory of the image. The final stage has no shell, so this stage
# makes it. The user nonroot owns it in the final stage, so that opendustpan dev
# can write its data there with no volume.
RUN mkdir -p /out/data

# Console stage: static files from a console release tarball. No Node runs
# here: the tarball is a built release. CONSOLE_URL is https: for a release or
# file:<repo-relative path> for a local test tarball (see
# scripts/smoke-image.sh). The SHA-256 check fails the build on a mismatch.
#
# No console release exists yet in OpenDustpan/console, so the defaults below
# are placeholders for its first tag. A build without explicit CONSOLE_URL and
# CONSOLE_SHA256 stops at the first step of this stage and names the pin. When
# the first console release lands, pin its tag and SHA-256 here.
FROM golang:1.27.4-bookworm AS console
ARG CONSOLE_URL=https://github.com/OpenDustpan/console/releases/download/v0.1.0/console.tar.gz
ARG CONSOLE_SHA256=0000000000000000000000000000000000000000000000000000000000000000
COPY . /src
# Defense in depth: the placeholder test below is the first step of this RUN,
# before the download; the sha256sum check after the download stays and does
# not rely on it.
# The same optional extra-CA secret as in the Go stage: curl trusts the
# bundle before it downloads the console tarball.
RUN --mount=type=secret,id=extra_ca,required=false set -eu; \
  if [ -s /run/secrets/extra_ca ]; then cat /run/secrets/extra_ca >> /etc/ssl/certs/ca-certificates.crt; fi; \
  case "${CONSOLE_SHA256}" in \
    0000000000000000000000000000000000000000000000000000000000000000) \
      echo "Dockerfile: the console pin is the placeholder. Pin the console tag and SHA-256 in the Dockerfile (ARG CONSOLE_URL and ARG CONSOLE_SHA256), or pass --build-arg CONSOLE_URL=<https URL> --build-arg CONSOLE_SHA256=<sha256>" >&2; \
      exit 1;; \
  esac; \
  case "${CONSOLE_URL}" in \
    https://*) curl -fsSL --retry 3 -o /tmp/console.tgz "${CONSOLE_URL}";; \
    file:*) cp "/src/${CONSOLE_URL#file:}" /tmp/console.tgz;; \
    *) echo "CONSOLE_URL must start with https: or file:" >&2; exit 1;; \
  esac; \
  echo "${CONSOLE_SHA256}  /tmp/console.tgz" | sha256sum -c -; \
  mkdir -p /console; \
  tar -xzf /tmp/console.tgz -C /console; \
  test -f /console/index.html

# Base: distroless static nonroot, pinned by digest.
# Digest read 2026-10-11 from gcr.io/distroless/static-debian12:nonroot.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/opendustpan /opendustpan
COPY --from=console /console /console
# Our license and the licenses of the linked dependencies travel with the image.
COPY --from=build /src/LICENSE /licenses/LICENSE
COPY --from=build /out/third_party_licenses /licenses/third_party_licenses
# The image fails closed: the command below is serve, and serve stops without a
# key-encryption key. Set DUSTPAN_KEY_ENCRYPTION_KEY or
# DUSTPAN_KEY_ENCRYPTION_KEY_FILE, or run the trial command:
#   docker run -p 127.0.0.1:8080:8080 <image> dev --listen 0.0.0.0:8080
# opendustpan dev keeps its data in the directory dev of DUSTPAN_DATA_DIR.
COPY --from=build --chown=65532:65532 /out/data /data
ENV DUSTPAN_DATA_DIR=/data
VOLUME /data
# The base image already runs as nonroot. The explicit USER keeps that true if
# the base changes.
USER nonroot:nonroot
# The base image has no shell, curl or wget, so the check runs the binary
# in the exec form. It probes GET /health/ready with the same listen
# address and TLS settings as serve: override the check when --listen
# changes, and set DUSTPAN_TLS_CERT with DUSTPAN_TLS_KEY when the server
# uses TLS. Kubernetes ignores this instruction and uses its own probes.
HEALTHCHECK --interval=30s --timeout=10s --start-period=15s --retries=3 \
  CMD ["/opendustpan", "health"]
ENTRYPOINT ["/opendustpan"]
CMD ["serve", "--database", "sqlite:/data/dustpan.db", "--blob-dir", "/data/blobs", "--listen", ":8080", "--console-dir", "/console"]
