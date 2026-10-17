# Research: how long can names, URNs, addresses and IDs get?

Question: which length limits should the API enforce on the strings that
clients and tools send, so that a limit never refuses real use, and every
stored and audited value stays bounded?

Date: 2026-10-16

This note gives facts and proposals. It decides nothing. A limit becomes
policy only when the project owner accepts it in an ADR.

Conventions used below:

- "Documented" means a limit that a vendor page or a standard states.
- "In code" means a limit in the vendor's own source, cloned at the commit
  listed in the sources.
- "Unknown" means no source in this run gave a value. No number is guessed.
- "Longest seen" means the longest real value found in a public source. A
  value marked "unknown" was not found.

## Summary

| Field | Real maximum seen | Documented limit | OpenDustpan today | Proposed limit |
| --- | --- | --- | --- | --- |
| Pulumi project name | none found | 100 bytes, in CLI code | 100 characters (`pkg/stack/stack.go:39`) | 100 characters |
| Pulumi stack name | none found. The CLI applies the 100 limit to a qualified `org/project/stack` name in one reported case ([Pulumi community thread](https://archive.pulumi.com/t/2847494/any-reason-why-the-stack-names-are-arbitrarily-limited-to-10)) | 100 characters, in CLI code | 100 characters (`pkg/stack/stack.go:39`) | 100 characters |
| Pulumi resource URN | 149 characters, a nested EKS role policy attachment ([Pulumi display test data](https://github.com/pulumi/pulumi/blob/7b4b596ff7b67387859c44f82d196c9365fc3088/pkg/backend/display/testdata/not-truncated/template-body.json)) | none found | none, bounded by body caps only | 2048 bytes |
| Pulumi resource type | 90 characters, `kubernetes:admissionregistration.k8s.io/v1alpha1:ValidatingAdmissionPolicyBindingSpecPatch` ([provider schema](https://github.com/pulumi/pulumi-kubernetes/blob/b97e31330796/provider/cmd/pulumi-resource-kubernetes/schema.json)). A nested type chain in a URN reaches 121 characters | none found | none, bounded by body caps only | 256 bytes |
| Pulumi config key | none found | none found | none, bounded by body caps only | 256 bytes |
| Pulumi output name | none found | none found | none, bounded by body caps only | 256 bytes |
| OpenTofu workspace name | none found. A HashiCorp support article reports that a name above 63 characters breaks Kubernetes label creation in TFE ([HashiCorp](https://support.hashicorp.com/hc/en-us/articles/27007645191699-Issue-with-large-workspace-names-on-a-Terraform-Enterprise-Flexible-Deployment-Options-Kubernetes-deployment)) | none in CLI. URL-safe only | 90 bytes (`pkg/workspace/workspace.go:53`) | 90 bytes, to review |
| OpenTofu resource address | 75 characters, a three-level module path ([OpenTofu `module_test.go`](https://github.com/opentofu/opentofu/blob/105b18bb846f80760ee983c8e49044b2bd30f0a4/internal/addrs/module_test.go)) | none found | none, bounded by body caps only | 1024 bytes, for audit `Object` |
| OpenTofu provider address | 44 characters, `example.com/hashicorp/terraform-provider-bad` ([OpenTofu `provider_test.go`](https://github.com/opentofu/opentofu/blob/105b18bb846f80760ee983c8e49044b2bd30f0a4/internal/addrs/provider_test.go)) | none found | none, bounded by body caps only | 1024 bytes, for audit `Object` |
| OpenTofu output name | none found | none found. HCL identifier rule | none, bounded by body caps only | 256 bytes |
| OIDC `sub` | not measured in this run | 255 ASCII characters (OIDC Core 1.0, section 2) | 255 characters for API subjects (`pkg/principal/principal.go:116`). None for sign-in `IDPSubject` | 255 bytes, on sign-in too |
| OIDC `iss` | not measured in this run | none in the spec | none | 512 bytes |
| SCIM `userName` | not measured in this run | none in RFC 7643 | not checked in this run | 255 bytes |
| SCIM `displayName` (user) | not measured in this run | none in RFC 7643 | 255 bytes (`pkg/principal/principal.go:117`) | 255 bytes (keep) |
| Group name | none found | none in RFC 7643 | 100 bytes (`pkg/principal/principal.go:118`) | 100 bytes (keep) |
| AWS ARN (role ARN field) | 163 characters, an autoscaling launch configuration ARN ([Pulumi display test data](https://github.com/pulumi/pulumi/blob/7b4b596ff7b67387859c44f82d196c9365fc3088/pkg/backend/display/testdata/not-truncated/template-body.json)) | 2048 characters, IAM Role API | none for ARN fields | 2048 bytes |
| Azure resource ID | 285 characters, a Site Recovery replicated item ([azurerm provider docs](https://github.com/hashicorp/terraform-provider-azurerm/blob/bfbabe713676/website/docs/r/site_recovery_replicated_vm.html.markdown)) with placeholder names. Real names are longer | none found | none | 1024 bytes |
| GCP resource name | none found | 1 to 63 characters, Compute Engine | none | 63 characters for DNS-style names |
| Kubernetes object name | none found above 63 characters | 253 (DNS subdomain), 63 (DNS label) | none | 253 bytes |
| Pulumi `User-Agent` | none observed. The fixed format is 21 characters plus version, command and agent | none in code | 512 bytes stored (`pkg/audit/audit.go:97`) | 512 bytes (keep) |
| OpenTofu `User-Agent` | none observed. The client sends `OpenTofu/<version>` ([OpenTofu `client_test.go`](https://github.com/opentofu/opentofu/blob/105b18bb846f80760ee983c8e49044b2bd30f0a4/internal/httpclient/client_test.go)) | none in code | 512 bytes stored (`pkg/audit/audit.go:97`) | 512 bytes (keep) |
| `Authorization` header | about 4 KB for a plain token. Over 18K characters for an Auth0 machine token. About 414 KB for an enriched Okta token ([Google Cloud thread](https://discuss.google.dev/t/apigee-x-request-header-size-limit-exceeds/105592), [Auth0 thread](https://community.auth0.com/t/m2m-applications-being-generated-with-unreasonably-large-access-tokens/141656)) | none in a standard | Go default, 1 MiB (`pkg/dustpan/dustpan.go:1323` sets no limit) | 64 KiB (`MaxHeaderBytes`), to review |

Three rows need a decision from the owner:

1. The OpenTofu workspace name limit (90) is stricter than OpenTofu. The
   OpenTofu CLI accepts any length. No real long name was found.
2. The URN, resource address and output limits are proposals with no
   measured data behind them.
3. The `iss` limit is a proposal. No standard sets one.

## Pulumi

### Project and stack names

The Pulumi CLI tests both names in its own code.

- Project names: `ValidateProjectName` allows at most 100 characters and
  the characters `[A-Za-z0-9_.-]`. Source:
  [`sdk/go/common/tokens/project.go`](https://github.com/pulumi/pulumi/blob/7b4b596ff7b67387859c44f82d196c9365fc3088/sdk/go/common/tokens/project.go).
- Stack names: `ParseStackName` allows at most 100 characters and the same
  character set. Source:
  [`sdk/go/common/tokens/stack_name.go`](https://github.com/pulumi/pulumi/blob/7b4b596ff7b67387859c44f82d196c9365fc3088/sdk/go/common/tokens/stack_name.go).

The CLI does not reject a leading `-` or `.` in a stack name. OpenDustpan
does (`internal/ident/ident.go:15`). This is a difference, listed under
open questions.

Longest seen: unknown. Secondary sources also give 100 for stack names
(for example a [DrDroid note](https://drdroid.io/framework-diagnosis/pulumi-invalidstackname-error-encountered-when-creating-or-managing-a-stack-in-pulumi/)).
That note is not a primary source.

### Stack tags

Tag names are at most 40 characters and tag values at most 256 characters.
Source:
[`pkg/util/validation/stack.go`](https://github.com/pulumi/pulumi/blob/7b4b596ff7b67387859c44f82d196c9365fc3088/pkg/util/validation/stack.go).
OpenDustpan limits a stack to 64 tags (`pkg/stack/stack.go:40`). It does not
check tag length in this run.

### Resource URN, resource type, config key, output name, secret path

The Pulumi SDK defines name tokens with a regular expression per segment.
Source:
[`sdk/go/common/tokens/names.go`](https://github.com/pulumi/pulumi/blob/7b4b596ff7b67387859c44f82d196c9365fc3088/sdk/go/common/tokens/names.go).
The SDK does not set a length for a URN, a resource type, or a config key.
Search for a URN length limit found only provider limits, for example the
53-character Helm name limit in a
[Pulumi community thread](https://archive.pulumi.com/t/2766689/urn-seems-to-be-too-tightly-coupled-to-helm-generated-resour?t=v2).
That thread describes a problem with a provider, not a Pulumi limit.

Longest seen: unknown. Secret paths: unknown.

Proposal: a URN limit of 2048 bytes. Reason: a URN carries a type chain and
a parent chain. No real value was measured. 2048 is the same size as the
longest documented ARN field in this note. A URN joins a type chain and a
name chain, so its length grows with the depth of the resource. The owner
should check this against real stacks.

### Pulumi `User-Agent`

The Pulumi CLI sends this format:

```
pulumi-cli/1 (<version>; <GOOS>[; cmd=<command>][; agent=<agent>])
```

Sources:
[`pkg/backend/httpstate/client/api.go`](https://github.com/pulumi/pulumi/blob/7b4b596ff7b67387859c44f82d196c9365fc3088/pkg/backend/httpstate/client/api.go)
(lines 126 to 136, and the sanitizer above them) and
[`sdk/go/common/workspace/plugins.go`](https://github.com/pulumi/pulumi/blob/7b4b596ff7b67387859c44f82d196c9365fc3088/sdk/go/common/workspace/plugins.go)
(line 1659, a shorter form for plugin downloads). The sanitizer replaces
spaces and parentheses. It does not cap the length of `cmd` or `agent`.

Longest seen: unknown.

OpenDustpan stores at most 512 bytes of the user agent. The HTTP adapters
truncate longer values (`internal/api/server/audit.go:161`,
`internal/api/pulumi/secrets.go:469`).

## OpenTofu

### Workspace names

The OpenTofu CLI does not set a length limit for workspace names. Its only
rule is that the name must not change under `url.PathEscape`. Source:
[`internal/command/workspace_command.go`](https://github.com/opentofu/opentofu/blob/105b18bb846f80760ee983c8e49044b2bd30f0a4/internal/command/workspace_command.go)
(lines 47 to 52). The error text says: "The name must contain only URL safe
characters, and no path separators"
(`internal/command/views/workspace.go:202`).

OpenDustpan sets 90 bytes (`pkg/workspace/workspace.go:53`). This limit is
stricter than the tool. A team that uses a longer name in OpenTofu would
be refused. No real example of a long name was found.

Longest seen: unknown. A third-party blog suggests a 20-character limit as
a team rule. That is a convention, not a limit of the tool.

### Resource addresses, module paths, `for_each` keys, provider addresses

The OpenTofu address code in `internal/addrs` sets no length limit. Source:
[`internal/addrs`](https://github.com/opentofu/opentofu/tree/105b18bb846f80760ee983c8e49044b2bd30f0a4/internal/addrs).
Keys in `for_each` are strings. Longest seen: unknown.

Proposal: audit `Object` and `ValuePath` at 1024 bytes. Reason: the audit
log stores these values in `TEXT` columns with no limit
(`pkg/store/sqlite/migrations/0019_audit_entries.sql`). The request body
caps bound the total size of a state write, not each address.

### Output names

The OpenTofu CLI checks output names with `hclsyntax.ValidIdentifier`
(`internal/configs/named_values.go:567`). The HCL identifier rule sets no
length. Longest seen: unknown.

Proposal: 256 bytes. Reason: a name in a state output key should stay short
enough to show in a list. No real value was measured.

## Identities

### OIDC `sub` and `iss`

OIDC Core 1.0, section 2, says that the `sub` value "MUST NOT exceed 255
ASCII characters in length"
([spec](https://openid.net/specs/openid-connect-core-1_0.html)). This is the
only documented limit found for either claim. The spec sets no length for
`iss`. It says the value is a case-sensitive URL that uses the `https`
scheme.

OpenDustpan limits subjects made through the API to 255 characters with the
pattern in `pkg/principal/principal.go:323`. A sign-in stores the identity
provider's `sub` in `IDPSubject` (`internal/api/server/signin.go:435`). That
value has no limit in this run.

Proposal: `sub` at 255 bytes on sign-in too, to match the spec. `iss` at
512 bytes. Reason: the spec gives no limit for `iss`. A 512-byte limit
covers a long issuer URL and keeps a stored value bounded. No real `iss`
value of that length was measured.

Longest `sub` seen: unknown. A Microsoft Q&A reply gives 255 as the
maximum. This is seen in a search snippet, and the thread was not opened.
The Entra access-token reference says that the `sub` value is immutable.
It must not be reassigned or reused
([Microsoft Learn](https://learn.microsoft.com/en-us/entra/identity-platform/access-token-claims-reference)).

### Token size and the groups claim

Microsoft Entra ID stops emitting the `groups` claim above 150 groups (SAML)
or 200 groups (JWT). Above that limit, it sends an overage claim and the
application queries Graph. This is the only documented token-size effect
found in this run. Source: the same Microsoft Learn page above.

Longest JWT size seen: see the `Authorization` header section.

### SCIM

RFC 7643 sets no maximum length for `userName`, `displayName` (user), or
`displayName` (group). Source:
[RFC 7643, section 4](https://datatracker.ietf.org/doc/html/rfc7643).

Vendor limits found:

- AWS IAM Identity Center documents attributes but no lengths in its
  [SCIM limitations page](https://docs.aws.amazon.com/singlesignon/latest/developerguide/limitations.html).
- A GitHub username is at most 39 characters, from the
  [Ping Identity connector reference](https://docs.pingidentity.com/integrations/github/github_emu_provisioner/pf_gh_emu_supported_attributes_ref.html).
  This is a provider rule, not a SCIM rule.
- In one [1Password community thread](https://www.1password.community/developers-69/azure-scim-failed-to-sync-structure-of-request-was-invalid-21557),
  a user suspected a 71-character group name limit for some Azure-provisioned
  groups. The thread does not verify the limit or name the cause.

OpenDustpan sets a limit of 255 bytes for the user `displayName` field
(`pkg/principal/principal.go:117`) and group names to 100 bytes
(`pkg/principal/principal.go:118`).

Proposal: keep 255 for display names and 100 for group names. Reason: the
two values are above the longest reported group name, and the limits
hold a stored value to a fixed size. Add a limit of 255 bytes to a SCIM
`userName`, which has no limit now.

## Cloud IDs in state and in names

### AWS ARNs

- The IAM Role API gives the `Arn` field a maximum of 2048 characters
  ([AWS](https://docs.aws.amazon.com/IAM/latest/APIReference/API_Role.html)).
  The same page gives the `path` field 512 characters.
- An IAM role name is at most 64 characters
  ([AWS IAM quotas](https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_iam-quotas.html)).
- AWS Cost Explorer `TagResource` takes a resource ARN of up to 2048
  characters.
- Amazon Connect's `AssociateLambdaFunction` takes a function ARN of up to
  140 characters. This is one API field, not a general Lambda ARN limit.

No general ARN limit was found. Longest real ARN seen: unknown.

Proposal: 2048 bytes for an ARN field. Reason: it matches the largest
documented ARN field found.

### Azure resource IDs

No primary Microsoft page found sets a maximum length for a full resource
ID. The
[Azure naming rules page](https://learn.microsoft.com/en-us/azure/azure-resource-manager/management/resource-name-rules)
gives per-type lengths. A Microsoft Q&A answer from 2023 estimates 966
characters by adding the component limits. That estimate is not official.

Longest seen: unknown.

Proposal: 1024 bytes. Reason: it is above the unofficial estimate and under
the 2048 limit used for ARNs. No measured value supports it.

### GCP resource names

Compute Engine says that a resource name must be "1-63 characters in
length" ([Google Cloud](https://docs.cloud.google.com/compute/docs/naming-resources)).
Other GCP services set their own rules.

Project IDs: a third-party source gives 6 to 30 characters. The Compute
Engine page does not state this, so the value is not confirmed here.

Longest seen: unknown.

### Kubernetes names and labels

The Kubernetes names page says:

- DNS subdomain names: at most 253 characters
  ([Kubernetes](https://kubernetes.io/docs/concepts/overview/working-with-objects/names/)).
- DNS label names: at most 63 characters, for resource types that use them.
- Path segment names: no length. They must not be `.` or `..`, and must not
  contain `/` or `%`.

Label and annotation keys and values were not read from the labels page in
this run. The 63-character limit for label values and name segments is from
general knowledge and is not verified here.

Longest seen: unknown.

Proposal: 253 bytes for a Kubernetes object name in an audit `Object`.
Reason: it is the largest name Kubernetes accepts for a DNS subdomain name.

## HTTP

### `Authorization` header

No standard sets a limit for a bearer token. Sizes seen in public sources:

- A plain Okta access token is about 4 KB. An "enriched" token (extra
  claims added) was about 414 KB and was refused with HTTP 413 before it
  reached the API gateway. The gateway cites 60 KB for its own header limit
  and 64 KB for the Google Cloud load balancer
  ([Google Cloud community thread](https://discuss.google.dev/t/apigee-x-request-header-size-limit-exceeds/105592)).
- An Auth0 machine-to-machine token was "over 18K characters" and caused
  nginx `400 Request Header Or Cookie Too Large`
  ([Auth0 community thread](https://community.auth0.com/t/m2m-applications-being-generated-with-unreasonably-large-access-tokens/141656)).
- A Microsoft Q&A reply says that the maximum is 64 KB, with "I believe" and
  no source
  ([Microsoft Q&A](https://learn.microsoft.com/en-us/answers/questions/1356857/azure-ad-authentication-431-error)).
- Calculation, not an observed value: Entra ID sends up to 200 group IDs in
  a JWT. Each ID is a 36-character GUID, so 200 groups need about 7.8 KB of
  JSON. Base64 adds one third. The token is about 10 KB before the other
  claims. This is above the 8 KiB nginx default.

Limits that do exist:

- nginx sets 8 KiB for a request header buffer by default
  ([nginx](https://nginx.org/en/docs/http/ngx_http_core_module.html): `large_client_header_buffers 4 8k`).
  A header line larger than one buffer gets `400`.
- Go sets `DefaultMaxHeaderBytes` to 1 MiB
  ([pkg.go.dev](https://pkg.go.dev/net/http#Server)). OpenDustpan sets only
  `ReadHeaderTimeout` (`pkg/dustpan/dustpan.go:1323`), so the 1 MiB default
  applies.

Correction: the earlier proposal of 8 KiB at the proxy is wrong. An Entra
token with the full 200 groups is about 10 KB by calculation, and Auth0 and
Okta tokens above 18 KB exist. An 8 KiB limit would refuse real sign-ins.

Proposal: set `Server.MaxHeaderBytes` to 64 KiB. Reason: it is above every
token that a gateway still accepts (60 KB at Apigee, 64 KB at the Google
Cloud load balancer), and it is 16 times lower than the Go default. State in
the deployment guide that a proxy in front must allow 64 KiB, and that
nginx needs a raised `large_client_header_buffers`.

### Request bodies

- Namespace create: 8192 bytes (`internal/api/server/namespaces.go:49`).
- Pulumi and TFE adapters: 1 MiB (`internal/api/pulumi/pulumi.go:58`,
  `internal/api/tfe/tfe.go:42`).

## Audit entry fields

Source: `pkg/audit/audit.go`, `validateWho` (line 193) and `validateWhat`
(line 207), and the `Entry` struct (line 148).

| Field | Limit in code | Note |
| --- | --- | --- |
| `Namespace` | 100 (`pkg/namespace/namespace.go:48`) | |
| `Subject` | none. Must be non-empty | `validateWho` |
| `IDPSubject` | none | set on sign-in |
| `TokenID` | none | |
| `Action` | none. Must be non-empty | `validateWhat` |
| `Object` | none. Must be non-empty | `validateWhat` |
| `Version` | none | |
| `ValuePath` | none | |
| `SourceIP` | none | from `clientip.String` |
| `UserAgent` | 512 bytes (`pkg/audit/audit.go:97`, `:213`) | the HTTP adapter truncates |
| `RequestID` | none | |
| `ObjectID` | none | |

The audit `TEXT` columns have no limit (for example
`pkg/store/sqlite/migrations/0019_audit_entries.sql`).

## Proposals in one list

- Namespace, stack, project: keep 100.
- Workspace: keep 90, pending the owner's review of the OpenTofu gap.
- Principal subject: keep 255. Apply it to sign-in `sub` too.
- Issuer (`iss`): 512 bytes.
- URN: 2048 bytes.
- Resource type, config key, output name, OpenTofu output name: 256 bytes.
- Resource address, provider address, audit `Object`, audit `ValuePath`: 1024 bytes.
- ARN field: 2048 bytes.
- Azure resource ID: 1024 bytes.
- Kubernetes object name: 253 bytes.
- User agent: keep 512.
- Header: 64 KiB (`MaxHeaderBytes`). The earlier 8 KiB proposal was wrong.

Measured maxima exist only for URN (149), resource type (90), ARN (163),
Azure ID (285, placeholder names), module path (75), provider address (44)
and the `Authorization` header (about 4 KB to 414 KB). Each of those
proposals is at least 3 times the longest value seen. The header limit is
set from the gateway limits. All other fields have no measured value. The
test fixtures hold short, clean examples, so they set a floor, not a
ceiling. The owner should check the rest against real state files.

User agent: the 512-byte cap is correct on `main`. `MaxUserAgentLen` is
defined at `pkg/audit/audit.go:97` and checked at `:213`. No real user
agent above 100 bytes was found. The Pulumi `cmd` and `agent` parts have no
cap in the CLI, so a cap of 512 stays useful.

## Open questions

1. The OpenTofu CLI accepts workspace names of any length. Should the
   OpenDustpan limit be raised, or should the gap stay and be documented?
2. The Pulumi CLI does not reject a leading `-` or `.` in a stack name.
   OpenDustpan rejects both (`internal/ident/ident.go:15`). Should the two
   match?
3. Pulumi stack tag length is not checked here. Should OpenDustpan check it?
4. Measured examples come from test fixtures and docs, not from production
   state. A sample of real state files would settle the URN, address and
   config key proposals.
5. The Azure resource ID limit and the Kubernetes label value limit need a
   primary source before a value is set.
6. Project IDs for GCP need a primary source.

## Sources

Documents read (9):

- [Kubernetes object names](https://kubernetes.io/docs/concepts/overview/working-with-objects/names/)
- [OIDC Core 1.0](https://openid.net/specs/openid-connect-core-1_0.html)
- [AWS IAM Identity Center SCIM limitations](https://docs.aws.amazon.com/singlesignon/latest/developerguide/limitations.html)
- [Azure naming rules](https://learn.microsoft.com/en-us/azure/azure-resource-manager/management/resource-name-rules)
- [Google Cloud Compute Engine naming](https://docs.cloud.google.com/compute/docs/naming-resources)
- [RFC 7643, SCIM core schema](https://datatracker.ietf.org/doc/html/rfc7643)
- [nginx core module](https://nginx.org/en/docs/http/ngx_http_core_module.html)
- [Microsoft Entra access-token claims](https://learn.microsoft.com/en-us/entra/identity-platform/access-token-claims-reference)
- [Go net/http](https://pkg.go.dev/net/http#Server)

Cloned sources (2):

- [Pulumi CLI](https://github.com/pulumi/pulumi/tree/7b4b596ff7b67387859c44f82d196c9365fc3088), commit `7b4b596`
- [OpenTofu](https://github.com/opentofu/opentofu/tree/105b18bb846f80760ee983c8e49044b2bd30f0a4), commit `105b18b`

Secondary sources (not relied on for a value): the AWS IAM Role API and quotas
pages, the AWS Cost Explorer and Amazon Connect reference pages, the Microsoft
Q&A on resource IDs, the Microsoft Q&A on `sub` length, the Ping Identity
connector reference, the 1Password community thread, the Pulumi community
thread on URN length, and a DrDroid note on Pulumi stack names.

Web searches: 21 (11 earlier, 10 in this pass). Repositories cloned for
fixture search: Pulumi, pulumi-kubernetes, Pulumi examples, OpenTofu and the
azurerm provider.
