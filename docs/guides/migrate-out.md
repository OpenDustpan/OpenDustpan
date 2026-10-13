# Migrate out of OpenDustpan

This guide shows how to move a Pulumi stack or an OpenTofu workspace from
OpenDustpan to another backend. Your state stays yours. The steps use only
the standard commands of each CLI.

The end-to-end suite tests the commands that read state out of OpenDustpan:
`pulumi stack change-secrets-provider`, `pulumi stack export` and
`tofu state pull`. The steps that write into the other backend are not yet
tested by our e2e suite. Each of them ran once by hand, with Pulumi CLI
3.250.0 and OpenTofu 1.12.7, into a local file backend.

These steps move the latest state of each unit. They do not move the history.

## Pulumi CLI

Do these steps for each stack. The example moves `acme/demo/dev` to a file
backend with the passphrase secrets provider. Use the login URL of your
target instead.

### Move the secrets to a passphrase first

This way never writes a secret in plain text.

1. Log in to OpenDustpan and select the stack.

   ```sh
   pulumi login https://dustpan.example
   pulumi stack select acme/demo/dev
   ```

2. Change the secrets provider of the stack to a passphrase. Tested by our
   e2e suite. The CLI reads the new passphrase from the terminal only, and
   ignores `PULUMI_CONFIG_PASSPHRASE` for it. Give the passphrase twice on
   standard input. With no input, the CLI sets an empty passphrase.

   ```sh
   export PULUMI_CONFIG_PASSPHRASE='<passphrase>'
   printf '%s\n%s\n' "$PULUMI_CONFIG_PASSPHRASE" "$PULUMI_CONFIG_PASSPHRASE" \
     | pulumi stack change-secrets-provider passphrase
   ```

3. Check that the stack decrypts with the passphrase. Tested by our e2e
   suite. Each command must print the value, not an error.

   ```sh
   pulumi config get apiKey
   pulumi stack output key --show-secrets
   ```

4. Export the stack. Tested by our e2e suite. The file holds the secrets
   encrypted with the passphrase.

   ```sh
   pulumi stack export --file dev.json
   ```

5. Log in to the target, create the stack, and import the file. Not yet
   tested by our e2e suite. Check that the login succeeds before the next
   step: a failed login leaves you logged in to OpenDustpan.

   ```sh
   mkdir -p "$HOME/pulumi-state"
   pulumi login "file://$HOME/pulumi-state"
   pulumi stack init organization/demo/dev
   pulumi stack import --file dev.json
   ```

6. Check the result. The secrets read back, and `pulumi preview` shows no
   changes.

   ```sh
   pulumi config get apiKey
   pulumi stack output key --show-secrets
   pulumi preview
   ```

`Pulumi.dev.yaml` keeps working, because it holds the same passphrase salt.
To change to the secrets provider of your target after the move, run
`pulumi stack change-secrets-provider` there.

### Or export the secrets in plain text

Use this way when you cannot change the stack in OpenDustpan.

1. Write down the secret config values, then export the stack with its
   secrets in plain text. The export is tested by our e2e suite.

   ```sh
   pulumi config --show-secrets
   pulumi stack export --show-secrets --file dev.json
   ```

   `dev.json` holds every secret of the stack in plain text. Keep it on an
   encrypted disk and delete it after the import.

2. Move the old stack config file away, log in to the target, create the
   stack with its secrets provider, import, and set each secret config value
   again. Not yet tested by our e2e suite.

   ```sh
   mv Pulumi.dev.yaml Pulumi.dev.yaml.opendustpan
   mkdir -p "$HOME/pulumi-state"
   pulumi login "file://$HOME/pulumi-state"
   pulumi stack init organization/demo/dev --secrets-provider passphrase
   pulumi stack import --file dev.json
   pulumi config set --secret apiKey <value>
   rm dev.json
   pulumi preview
   ```

## OpenTofu

The way out depends on the block that names OpenDustpan.

### With the `remote` backend

Replace the `backend "remote"` block with the block of the target. Then let
OpenTofu copy the state. Not yet tested by our e2e suite.

```hcl
terraform {
  backend "local" {
    path = "web.tfstate"
  }
}
```

```sh
tofu init -migrate-state
tofu plan
```

`tofu plan` must find no differences.

### With the `cloud` block

OpenTofu 1.12.7 cannot migrate state out of a `cloud` block. It stops with
"Migrating state from cloud backend to another backend is not yet
implemented." Copy the state by hand instead.

1. In the directory that uses OpenDustpan, read the state. Tested by our
   e2e suite.

   ```sh
   tofu state pull > web.tfstate
   ```

   `web.tfstate` holds the state in plain text, with every sensitive value.

2. Replace the `cloud` block with the block of the target. Start the new
   backend and write the state into it. Not yet tested by our e2e suite.

   ```sh
   tofu init -reconfigure
   tofu state push web.tfstate
   tofu plan
   ```

   `tofu plan` must find no differences. Then delete `web.tfstate`.

Do these steps once for each workspace.

## After the move

Each unit in OpenDustpan keeps its state and its history after the move.
Keep it until the target works. Then delete a stack with `pulumi stack rm`
while you are logged in to OpenDustpan.

A delete is a soft delete (ADR 47). The stack leaves every list and its name
is free at once, but OpenDustpan keeps its state, its history and its secrets
until the retention period ends.

## Guides from other vendors

Other backends publish their own import guides. Read the guide of your
target for its login, its secrets provider and its limits. This project
lists such a guide here when the vendor asks. A listing is not an
endorsement.
