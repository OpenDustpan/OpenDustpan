# Import state into OpenDustpan

An import moves the state of a unit from a source into OpenDustpan. A source is the backend that holds the state before the import. A unit is one deployable instance with its own state. A Pulumi stack is a unit. An OpenTofu workspace is a unit.

This guide covers Pulumi sources and OpenTofu sources. It follows the steps that the e2e suite ran. Each command below matches a step in that suite.

## What the guide moves and what it leaves

The guide moves state, history where the tool exports it, and secrets. It leaves stack config, variables, teams, policies, schedules, triggers, and run history in the source. It leaves each of them unchanged. Move each of them by hand where the team needs it.

Stack config lives in `Pulumi.<stack>.yaml`, not on the server. The config part below writes new ciphertext on the destination. It leaves the source stack file in place.

An OpenTofu workspace keeps its variables outside its state. Move each of them by hand where the team needs it.

## Safety rules

- Stop each pipeline that writes to the source before the import. A Pulumi service backend has no lock. Stopped pipelines are the only guard. An OpenTofu `http` backend locks during the move. Local state has no lock.
- Run a read-only dry run first. List stacks, show history, and export state. The dry run makes no write request.
- Check that the source has no new writer before the switch. Take stack history before the import. Take it again after the import. The two reports must match.
- Resume a failed import. Run the export again for each version. Import only the versions that the destination still lacks.
- Never delete in the source during the import. The source stays the record until the team points each stack at OpenDustpan.

## History

Choose N, the count of versions to replay. The guide suggests 10. The file backend exports the latest state only. It has no history to replay.

The original kind, time, and user of each replayed version drop away. Each replayed version records kind `import` with the time of the import. Each replayed version stores one full checkpoint. A long history uses more storage for that reason.

For OpenTofu, choose N for the replay of state versions through the TFE-compatible API. The guide suggests 10. A move from an OpenTofu backend moves the current state only. The backend has no history to replay. Each replayed OpenTofu version keeps its serial, lineage, and MD5. Its time is the time of the replay.

## Import from a Pulumi service backend

Use two saved logins, one for the source and one for OpenDustpan. Pass the access token only at the first login. Later logins reuse the saved login with no token.

```sh
export PULUMI_ACCESS_TOKEN=<source-token>
pulumi login <source-url>
export PULUMI_ACCESS_TOKEN=<destination-token>
pulumi login <destination-url>
```

Log in to the source. The steps below build the example used here. Skip them where the source stack already exists.

```sh
pulumi login <source-url>
pulumi stack init <org>/<project>/<stack>
pulumi stack init <org>/<project>/other
pulumi stack select <org>/<project>/<stack>
printf '%s' 's3cr3t-1' | pulumi config set --secret apiKey
pulumi up --yes --non-interactive
```

### Dry run

The dry run changes nothing. It lists stacks, shows history, and exports state.

```sh
pulumi login <source-url>
pulumi stack ls --json
pulumi stack select <org>/<project>/<stack>
pulumi stack history --json
pulumi stack export
```

Save the export and the history. Compare them with the source again after the import.

### Replay the last N versions

Stay logged in to the source. Export each version, oldest first. Hold each export in a shell variable. The inner command is the step. The variable keeps the export in memory. No file holds plain text.

```sh
pulumi login <source-url>
EXPORT1=$(pulumi stack export --version 1)
EXPORT2=$(pulumi stack export --version 2)
EXPORT3=$(pulumi stack export --version 3)
```

Log in to the destination. Init the same stack names there. Set the same secret config value. Where the program reads a second stack, init it on the destination too.

```sh
pulumi login <destination-url>
pulumi stack init <org>/<project>/<stack>
pulumi stack init <org>/<project>/other
pulumi stack select <org>/<project>/<stack>
printf '%s' 's3cr3t-1' | pulumi config set --secret apiKey
```

Import each held export on standard input, oldest first. The CLI decrypts through the source login. It encrypts through OpenDustpan. No plain text file exists.

```sh
printf '%s' "$EXPORT1" | pulumi stack import
printf '%s' "$EXPORT2" | pulumi stack import
printf '%s' "$EXPORT3" | pulumi stack import
```

Repeat the pattern for all N versions. The destination history then holds N entries of kind `import`.

### Check the destination

The secret output decrypts on the destination. The history holds one `import` entry per replayed version.

```sh
pulumi stack output --show-secrets
pulumi stack export
pulumi stack history --json
```

The destination export matches the latest source export apart from two parts. The backend URL names the new server. Each ciphertext differs because the destination encrypts secrets with its own key.

The moved state is current. An update changes nothing.

```sh
pulumi up --yes --non-interactive
```

The report holds the word `unchanged`.

### Resume a failed import

Run the export again for each source version. Compare each export with the latest destination export, apart from the backend URL and the ciphertexts. Import only the versions that the destination still lacks. A run after a full import moves nothing. The destination export and history stay identical.

### Leave the source unchanged

Log in to the source again. Export the latest state. Show the history. Both must match the snapshot from before the import.

```sh
pulumi login <source-url>
pulumi stack export
pulumi stack history --json
```

No write request reaches the source past the dry run. Decrypt requests are reads. The CLI decrypts through the source at import time. It encrypts through the destination.

Log out of the source. The destination needs no source login afterwards. The secret output and the config still decrypt through the destination alone.

```sh
pulumi logout <source-url>
pulumi login <destination-url>
pulumi stack output --show-secrets
pulumi config get apiKey
```

## Move stack config secrets

Stack config is not on the server. Move it apart from state. Set the value on the source where it is missing. Read all values as JSON. Set each value on the destination with the value on standard input, never as an argument.

```sh
pulumi login <source-url>
pulumi stack init <org>/<project>/<stack>
printf '%s' "$VALUE" | pulumi config set --secret <key>
pulumi config --show-secrets --json
```

Use a fresh project directory on the destination with the same `Pulumi.yaml` and no stack file yet. Init the same stack name there. Set each value from standard input.

```sh
pulumi login <destination-url>
pulumi stack init <org>/<project>/<stack>
printf '%s' "$VALUE" | pulumi config set --secret <key>
pulumi config get <key>
```

The value on the destination decrypts to the same text. The stack file on the destination holds a `secure:` value. No plain text file stays on disk.

## Import from a Pulumi file backend

The file backend exports the latest state only. The destination history holds one `import` update. The stack keeps its passphrase secrets provider. The step below shows `change-secrets-provider` as an option.

Log in to the file backend. The backend URL points at the source directory. The step passes no access token. The passphrase unlocks the provider.

```sh
export PULUMI_BACKEND_URL=file://<source-dir>
export PULUMI_ACCESS_TOKEN=
export PULUMI_CONFIG_PASSPHRASE=<passphrase>
pulumi login file://<source-dir>
pulumi stack init <stack> --secrets-provider passphrase
pulumi up --yes --non-interactive --stack <stack>
pulumi stack history --json --stack <stack>
```

Log in to the destination. Init the same stack with the passphrase provider. Keep the passphrase set, so the imported stack still unlocks.

```sh
unset PULUMI_BACKEND_URL
export PULUMI_ACCESS_TOKEN=<destination-token>
pulumi login <destination-url>
export PULUMI_CONFIG_PASSPHRASE=<passphrase>
pulumi stack init <org>/<project>/<stack> --secrets-provider passphrase
```

Pipe the export of the file stack into the import on standard input. No file holds the export. The export becomes the first state of the stack on OpenDustpan.

```sh
PULUMI_BACKEND_URL=file://<source-dir> PULUMI_CONFIG_PASSPHRASE=<passphrase> pulumi stack export --stack <stack> | PULUMI_CONFIG_PASSPHRASE=<passphrase> pulumi stack import --stack <org>/<project>/<stack>
```

Check the destination. The export holds the moved state with the passphrase provider. The history holds one `import` update. An update changes nothing.

```sh
pulumi stack export --stack <org>/<project>/<stack>
pulumi stack history --json --stack <org>/<project>/<stack>
pulumi up --yes --non-interactive --stack <org>/<project>/<stack>
```

As an option, change the secrets provider to the service provider. An update after the change changes nothing either.

```sh
pulumi stack change-secrets-provider default --stack <org>/<project>/<stack>
pulumi up --yes --non-interactive --stack <org>/<project>/<stack>
```

The source directory stays byte-identical after the import. The import never deletes in the source.

## Import from an OpenTofu backend

Move each workspace in its own run. One source workspace fills one destination workspace. The e2e suite moved three sources this way. It moved local state. It moved an `http` backend. It moved client-side encrypted state.

A project with no backend block keeps local state. A project with an `http` backend block keeps state on that backend.

```hcl
terraform {
  backend "http" {
    address        = "<backend-url>"
    lock_address   = "<backend-url>"
    unlock_address = "<backend-url>"
  }
}
```

State that Terraform wrote with a version newer than 1.5 may not load in OpenTofu. This is a limit of the tools.

### Dry run

The dry run changes nothing. In the source project, pull the current state.

```sh
tofu state pull -no-color
```

Save the pulled state. Compare its resources and outputs with the destination after the move.

### Move each workspace

Point the project at OpenDustpan. Add a `cloud` block with the namespace and the workspace name.

```hcl
terraform {
  cloud {
    hostname     = "<host>"
    organization = "<namespace>"
    workspaces {
      name = "<workspace>"
    }
  }
}
```

Hold the token in a CLI configuration file.

```hcl
credentials "<host>" {
  token = "<token>"
}
```

Point `TF_CLI_CONFIG_FILE` at that file. Run init with input on. Answer `yes` at the copy prompt. Use plain `tofu init`. The flags `-migrate-state` and `-force-copy` are for state backends only. A `cloud` block asks the copy question instead.

```sh
export TF_CLI_CONFIG_FILE=<config-file>
TF_INPUT=1 tofu init -no-color
```

### Check the destination

Pull the moved state. Its resources and outputs match the source. Its serial matches the source. Its lineage is fresh. OpenTofu writes a new lineage into an empty workspace. Record both values.

```sh
tofu state pull -no-color
tofu plan -input=false -no-color
```

The plan report holds `No changes.`

Client-side encryption stays. Keep the same `encryption` block with the same passphrase on the destination. The moved state stays encrypted. Its stored bytes hold the encrypted envelope.

### Leave the source unchanged

The local backup keeps the source state. The `http` backend gets no new write and no delete. Locks do not count. The move locks the source where the source allows it.

A move into a missing organization fails and names the organization. A move with a wrong token fails and names the authentication failure. Each leaves the source unchanged.

## Replay OpenTofu state versions through the TFE-compatible API

Use the replay script when the source holds state behind a `cloud` block. `tofu init -migrate-state` cannot move state out of a `cloud` block. The script reads the last N state versions of the source workspace. It creates them on the destination from the oldest to the newest. Move each workspace in its own run.

Each apply with a new output value writes a new state version. The steps below build the example used here. Skip them where the source workspace already holds versions. Change one output value and apply again for each version. Three applies write three versions.

```sh
tofu init -input=false -no-color
tofu apply -input=false -auto-approve -no-color
```

### Dry run

The dry run changes nothing. It lists what the replay would move. Pass the tokens in the environment. Never pass them as arguments. The script holds them in curl config files. They never show in arguments or output.

```sh
export IMPORT_TFE_SOURCE_TOKEN=<source-token>
export IMPORT_TFE_DEST_TOKEN=<destination-token>
scripts/import-tfe-state.sh \
  --source-url <source-url> \
  --dest-url <destination-url> \
  --source-org <namespace> \
  --source-workspace <workspace> \
  --dest-org <namespace> \
  --dest-workspace <workspace> \
  --count 10 \
  --ca-bundle <bundle.pem> \
  --dry-run
```

`--count` sets how many of the newest source versions the replay moves. The default is 10. `--ca-bundle` names a PEM bundle file for both servers. The dry run prints one line per version with its serial and MD5. It ends with `dry run, changed nothing`.

### Replay

Run the same call without `--dry-run`. The script replays from the oldest to the newest. For each version it locks the destination workspace. It creates the state version with its serial, lineage, and MD5. It uploads the state. The upload finalizes the version. It unlocks the workspace.

The script records the newest source version before it writes. It stops when the source gains a new state version during the run.

It prints one line per version with its serial and MD5. It ends with the count it replayed and the count it skipped.

### Check the destination

The destination holds each replayed version with the same serial, lineage, and MD5 as the source. Point a project with a `cloud` block at the destination workspace. Run a normal apply for the next change. It succeeds on the replayed state.

```sh
tofu init -input=false -no-color
tofu apply -input=false -auto-approve -no-color
tofu state pull -no-color
```

The pulled state shows the new output value. A replayed version carries no outputs document until the next apply writes one. `state pull` still shows the outputs in the state.

### Resume a failed replay

Run the script again with the same call. It skips each version that the destination already holds with the same serial, lineage, and MD5. A second run after a full replay prints `moving 0` and moves nothing.

### Leave the source unchanged

Every request to the source is a GET. The source holds the same versions after the replay.

A replay with a missing organization fails and names it. A replay with a wrong token fails with `401`. Each exits non-zero and changes neither side. The output holds no token.

## Support

The e2e suite drove a Pulumi service backend with OpenDustpan as the source. It drove the pipe, the secrets, the config secrets, and the checks above. It also drove a file backend as the source. It did not test Pulumi Cloud.

The e2e suite drove OpenTofu backends with OpenDustpan as the destination. It drove local state, the `http` backend, and client-side encryption. It drove a TFE-compatible API with OpenDustpan as the source and as the destination. It drove the dry run, the replay, the resume, and the checks above. It did not test HCP Terraform.
