# Terragrunt with OpenDustpan

OpenDustpan works with Terragrunt as the backend of OpenTofu. Each
Terragrunt unit talks to its own workspace through the TFE-compatible API,
with its own lock, its own state versions and its own history. There is no
wrapper and no change order on this path.

Profiles and blueprints stay the recommended path for new setups. See
[Why profiles and blueprints stay the recommended path](../explanation/profiles-and-blueprints.md).

## Run Terragrunt locally

You need OpenTofu, Terragrunt, and a running server. The examples use the
server `https://localhost:8080`, the namespace `acme`, and the certificate
file `data/cert.pem` from the quickstart. Set them once:

```sh
export SERVER=https://localhost:8080 TOKEN=$DUSTPAN_ADMIN_TOKEN CACERT=data/cert.pem
```

### 1. Make one workspace per unit

Make each workspace before the run, so no creation race occurs during the
run. The body names the workspace:

```sh
for unit in web api worker; do
  curl -sS --cacert "$CACERT" -X POST -H "Authorization: Bearer $TOKEN" \
    -H 'Content-Type: application/vnd.api+json' \
    -d "{\"data\":{\"type\":\"workspaces\",\"attributes\":{\"name\":\"$unit\"}}}" \
    "$SERVER/api/v2/organizations/acme/workspaces"
done
```

The answer is `201` for each workspace. One workspace per unit directory. Two
unit directories that name one workspace merge their state into one chain.

### 2. Point each unit at its workspace

In each unit directory, the backend names the server and the workspace of
that unit:

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

Give OpenTofu the token and the certificate:

```sh
printf 'credentials "localhost:8080" {\n  token = "%s"\n}\n' "$TOKEN" > dev.tfrc
export TF_CLI_CONFIG_FILE=$PWD/dev.tfrc SSL_CERT_FILE=data/cert.pem
```

### 3. Run the apply

From the live directory, run the apply over all units:

```sh
terragrunt run --all --non-interactive --parallelism 3 -- apply --auto-approve
```

Each unit applies in its own workspace. A held lock blocks another unit
only when both name one workspace.

### 4. Confirm the run

Read the state versions of each workspace. Each workspace holds one new
state version:

```sh
for unit in web api worker; do
  curl -gsS --cacert "$CACERT" -H "Authorization: Bearer $TOKEN" \
    "$SERVER/api/v2/state-versions?filter[organization][name]=acme&filter[workspace][name]=$unit"
done
```

The units list is the cross-unit view of the run:

```sh
curl -sS --cacert "$CACERT" -H "Authorization: Bearer $TOKEN" \
  "$SERVER/api/dustpan/v1/namespaces/acme/units"
```

## Errors

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A second apply on the same workspace fails while the first apply holds the lock. | Wait for the holder to finish, then retry the unit. To release a stale lock after Ctrl-C, run the unlock command once per locked workspace. | `tofu force-unlock <lock-id>` |
| A unit with a wrong backend hostname or workspace name fails to read state. | Fix the backend hostname and workspace name, then reconfigure that unit directory. | `tofu init -reconfigure` |
| One unit fails with a refusal while the other units apply. | Grant the principal read and write on that workspace, then rerun the failed unit only. | `terragrunt apply` |
