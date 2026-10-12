# Security policy

## Report a vulnerability

<!-- Placeholder: private vulnerability reporting must be on in the
repository settings before these steps work. The project owner turns it on. -->

Report a vulnerability in private, through GitHub:

1. Open the **Security** tab of this repository on GitHub.
2. Select **Report a vulnerability**.
3. Say what is wrong, the version or commit, and the steps that show the
   problem.

Do not open a public issue, a discussion or a pull request for a
vulnerability.

## What happens next

1. The project owner reads the report and gives a first response in the
   private advisory within 14 days.
2. The project owner and you agree on whether it is a vulnerability, and on
   its severity.
3. The fix goes into a release. The advisory names the fixed version.
4. The project owner publishes the advisory in the advisory list of this
   repository on GitHub when the fixed release exists. The project owner asks
   GitHub for a CVE ID through the advisory for each fixed vulnerability.
   The advisory credits you, unless you ask it not to.

## Supported versions

OpenDustpan is pre-alpha. Before 1.0, only the latest release gets security
fixes. No release exists yet, so fixes go to the `main` branch only.

## Scope

In scope: the server in this repository, its APIs, the container image that
its `Dockerfile` builds, and the release archives.

Out of scope: the web console, which lives in
[OpenDustpan/console](https://github.com/OpenDustpan/console). Report a
console problem in that repository. A problem in the Pulumi CLI or in
OpenTofu goes to that project.

## What the server protects

The README section "What the server stores" says which data the server
encrypts and which data it keeps in plain text. OpenTofu state is sealed
with the key-encryption key in the blob store and in backups. The project
already documents this behavior, so a report of it alone is not a
vulnerability.
