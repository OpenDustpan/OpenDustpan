# Quickstart

This guide shows two ways to run OpenDustpan:

- [One process with SQLite on loopback](#one-process-with-sqlite), for a first try on one machine.
- [A compose stack with PostgreSQL and S3-compatible storage](#compose-stack-with-postgresql-and-s3-compatible-storage), to try PostgreSQL and S3-compatible storage. For production, use an S3 service that you operate or buy. See [s3-blobs.md](s3-blobs.md).

Then it shows how to connect the [Pulumi CLI](#connect-the-pulumi-cli) and [OpenTofu](#connect-opentofu).

No release exists yet, so you build the server from source. The server is pre-alpha. Do not keep production state on it.

## Before you start

You need these tools:

- Go 1.27.4 or later, to build the server.
- OpenSSL, to make secrets and a test certificate.
- The Pulumi CLI, OpenTofu, or both.
- Docker with the Compose plugin, for the compose stack only.

## One process with SQLite

SQLite keeps all data in one file. The server listens on loopback, so only your machine can reach it.

Build the server and make the secrets. Keep the secrets outside the data directory.

```sh
go build -o bin/opendustpan ./cmd/opendustpan
export DUSTPAN_ADMIN_TOKEN=$(openssl rand -hex 32)
export DUSTPAN_KEY_ENCRYPTION_KEY=$(openssl rand -base64 32)
```

`serve` has no default key and no default database. It stops if you set none. The command below sets the database.

The administrator signs in with the admin token. The key-encryption key protects secret values and OpenTofu state. A lost key loses every secret and every OpenTofu state, and nobody can recover them. Save both values in a password manager.

OpenTofu refuses plain HTTP, so make a test certificate and start the server with it:

```sh
mkdir -p data
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 30 \
  -subj /CN=localhost -addext subjectAltName=DNS:localhost,IP:127.0.0.1 \
  -keyout data/key.pem -out data/cert.pem
./bin/opendustpan serve \
  --listen 127.0.0.1:8080 \
  --database sqlite:data/dustpan.db \
  --tls-cert data/cert.pem --tls-key data/key.pem
```

The server keeps its data in `data/dustpan.db` and in `data/blobs`. Leave it running. In a second shell, export the same `DUSTPAN_ADMIN_TOKEN` and make a namespace:

```sh
./bin/opendustpan namespace create --server https://localhost:8080 --ca-file data/cert.pem acme
```

Use at least 3 characters for a namespace name. The Pulumi CLI refuses a shorter owner name with `error: invalid stack owner`. The server accepts 1 to 100 characters.

Go on to [Connect the Pulumi CLI](#connect-the-pulumi-cli). The certificate file is `data/cert.pem`.

## Compose stack with PostgreSQL and S3-compatible storage

The file `deploy/compose/compose.yaml` runs three services:

| Service | Image | License |
| --- | --- | --- |
| `opendustpan` | Built from this repository | Apache-2.0 |
| `postgres` | PostgreSQL 18.6 | PostgreSQL License |
| `rustfs` | RustFS 1.0.3 | Apache-2.0 |

RustFS is the S3-compatible store. The stack uses RustFS because its license is Apache-2.0. The server needs an S3 API with conditional writes, and RustFS has them. The stack turns off the RustFS console and does not publish its port.

Every image has a version and a digest. The data of PostgreSQL and of RustFS is in the named volumes `postgres-data` and `rustfs-data`. The server waits until PostgreSQL and the bucket are ready. The stack opens the port on `127.0.0.1` only.

Build the server image and make the secrets:

```sh
./scripts/build-image-local.sh
cd deploy/compose
./init.sh
```

The script `init.sh` writes `.env` with random secrets, mode 0600, and `tls/` with a test certificate. The compose file holds no secret. A lost `OPENDUSTPAN_KEY_ENCRYPTION_KEY` loses every secret and every OpenTofu state, so save a copy of `.env` in a password manager.

Start the stack and wait for every healthcheck:

```sh
docker compose up --detach --wait
docker compose ps
```

Make a namespace. The container already has the admin token:

```sh
docker compose exec opendustpan /opendustpan namespace create \
  --server https://localhost:8080 --ca-file /tls/cert.pem acme
```

The server is at `https://localhost:8080`. The certificate file is `deploy/compose/tls/cert.pem`. To use another host port, set `OPENDUSTPAN_PORT` in `.env`.

To stop the stack, run `docker compose down`. To stop it and delete the data, run `docker compose down --volumes`.

The test certificate expires in 30 days. Before you open the port to other hosts, put a reverse proxy with a real certificate in front of the server.

## Connect the Pulumi CLI

Give the Pulumi CLI the admin token and the certificate. Use the paths and the token of the setup that you chose. Then log in and make a stack:

```sh
export PULUMI_ACCESS_TOKEN=$DUSTPAN_ADMIN_TOKEN
export SSL_CERT_FILE=<path to cert.pem>
pulumi login https://localhost:8080
mkdir demo && cd demo
printf 'name: demo\nruntime: yaml\n' > Pulumi.yaml
pulumi stack init acme/demo/dev
```

For the compose stack, read the token from `deploy/compose/.env`:

```sh
export DUSTPAN_ADMIN_TOKEN=$(sed -n 's/^OPENDUSTPAN_ADMIN_TOKEN=//p' deploy/compose/.env)
```

`SSL_CERT_FILE` works on Linux. On other systems, use a certificate that the system trusts. Replace `<path to cert.pem>` with the full path of the certificate file.

The stack name has the form `<namespace>/<project>/<stack>`. Run `pulumi up` to make the first update.

## Connect OpenTofu

In a directory of its own, make a `main.tf` with a `cloud` block. It names the server, the namespace and the workspace:

```hcl
terraform {
  cloud {
    hostname     = "localhost:8080"
    organization = "acme"
    workspaces {
      name = "web"
    }
  }
}
```

Give OpenTofu the token and the certificate, then run it:

```sh
printf 'credentials "localhost:8080" {\n  token = "%s"\n}\n' "$DUSTPAN_ADMIN_TOKEN" > dev.tfrc
export TF_CLI_CONFIG_FILE=$PWD/dev.tfrc SSL_CERT_FILE=<path to cert.pem>
tofu init
tofu apply
```

`SSL_CERT_FILE` works on Linux. On other systems, use a certificate that the system trusts.

OpenTofu makes the workspace `web` in the namespace `acme` at the first `tofu init`.

## Test the compose stack

`make compose-smoke` runs this guide against the compose stack. It builds the image, runs `init.sh`, starts the stack, and makes a namespace. Then it runs `pulumi login`, `pulumi stack init`, `tofu init` and `tofu apply` with the real CLIs. Then it restarts the stack and checks that `pulumi stack ls` still lists the stack and that `tofu output greeting` still shows `hello`. At the end it deletes the stack and its volumes. It needs Docker, the Compose plugin and OpenSSL.

## Next steps

- [S3-compatible object storage](s3-blobs.md) explains the S3 settings.
- [Kubernetes](kubernetes.md) shows the Helm chart.
- [Backup, restore and upgrade](backup-restore-upgrade.md) shows how to protect the data.
