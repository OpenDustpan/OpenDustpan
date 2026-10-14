# Research: built-in roles in infrastructure-as-code backends

Question: Which built-in roles, and which permissions in each, do other
infrastructure-as-code backends and automation products offer? The project
owner asked to copy the role sets of these products, and to prefer a set
that is too wide over a set that is too narrow.

Date: 2026-10-13

This note gives facts. It does not choose a role set. An RFC holds the
proposal, and the project owner decides. All facts come from each vendor's
own documentation, read on the access date. Vendors change these pages
often. Section 10 lists what we could not verify.

## 1. Summary

| Product | Built-in roles | Scopes | Custom roles |
| --- | --- | --- | --- |
| Pulumi Cloud | Organization: Admin, Member, Billing Manager. Stack: Read, Write, Admin. Environment: Read, Open, Write, Admin | Organization, team, stack, environment, cloud account | Yes, from scopes and permission sets |
| HCP Terraform / Terraform Enterprise | Owners team. Project: Read, Write, Maintain, Admin. Workspace: Read, Plan, Write, Admin | Organization, project, workspace | Yes, custom project and workspace permissions |
| Spacelift | Space Reader, Space Writer, Space Admin, Worker pool controller | Space (root space is account-wide) | Yes, from a list of actions |
| env0 | Organization: User, Admin. Project: Viewer, Planner, Deployer, Admin | Organization, project, environment | Yes, from a list of permissions |
| Scalr | System roles (one named `user` found) | Account, environment, workspace | Yes, from `resource:action` permissions |
| OTF | Site admin, owners team. Workspace: Read, Plan, Write, Admin | Site, organization, workspace | No |
| Terrateam | No roles. Capabilities per GitHub user, team or repository role | Repository, directory (tag query) | Rules in a config file |
| Atlantis | No roles. GitHub team allowlist per command | Server, repository | No |

- Four products have a "plan but no apply" role: HCP Terraform (Plan),
  env0 (Project Planner), OTF (Plan) and Terrateam (the `plan` capability
  without `apply`). Pulumi's Stack Read "Allows for running previews".
- Two products have a separate billing role: Pulumi (Billing Manager) and
  HCP Terraform (billing is an owners-only permission).
- Two products put state outputs on their own permission: HCP Terraform
  ("Read outputs only") and Scalr (`state-versions:read` is separate from
  `workspaces:read`).
- No product documents a separate auditor role. Audit log read belongs to
  the top admin role in Pulumi and env0.
- Only OTF documents an installation-level operator, the site admin.

## 2. Pulumi Cloud

Pulumi Cloud has three built-in organization roles. Admins create custom
roles. A custom role "combine[s] entity access rules (direct, global, or
tag-based) with an organization access level". Pulumi builds each role from
scopes such as `stack:read`, grouped in permission sets.

### 2.1. Organization roles

| Role | Scope | Permissions |
| --- | --- | --- |
| Admin | Organization | "Full access to all organization resources and settings." "Can manage members, roles, and organization-wide configurations." Holds by default all scopes in the org-settings reference, for example `audit_logs:read`, `audit_logs:export`, `org_token:create`, `team:create_token`, `role:create`, `org_member:set_admin`, `saml:update`, `scim:update`, `policy_pack:create`, `organization_webhook:create`, `organization:billing` |
| Member | Organization | "Basic access to view organization resources and participate in stack operations." "Cannot modify organization settings." Default scopes include `team:list`, `team:read`, `org_member:read`, `organization:read_usage`, `saml:read`, `policy_groups:read`. Stack access comes from an organization setting: None, Read or Write. Environment access: None, Read, Open or Write. Cloud account access: None, Read, Write or Admin. Toggles let members create stacks, accounts and teams |
| Billing Manager | Organization | "Access to view and manage billing information." "Cannot modify other organization settings or resources." Scopes: `organization:billing`, `organization:read_usage`, `deployments:read_usage`, `org_member:read`, `team:list`, `saml:read` |
| Custom role | Organization and entities | Entity access rules plus an organization access level. Pro allows up to 25 custom roles. Enterprise has no limit. Only an organization admin can create one |

The org-settings permission sets are Read Only, Standard and Organization
Settings Billing. Read Only holds read scopes such as `org_member:read`,
`team:read`, `policy_pack:read` and `resources:search`. Standard adds, for
example, `environment:create`, `project:encrypt`, `project:decrypt` and
`services:create`.

### 2.2. Stack permission sets

Each set includes the set above it in the table. A stack's creator gets
Stack Admin on that stack.

| Role | Scope | Permissions |
| --- | --- | --- |
| Stack Read | Stack | "Basic read-only access to stacks. Allows for running previews." `stack:read`, `stack:export`, `stack:encrypt`, `stack:decrypt`, `stack_deployment:read`, `stack_deployment_settings:read`, `stack_access:read`, `stack_schedule:read` |
| Stack Write | Stack | "Ability to update stack configurations and run stack updates." Adds `stack:write`, `stack:import`, `stack:cancel_update`, `stack_deployment:create`, `stack_deployment_settings:write`, `stack_deployment_settings:encrypt`, `stack_deployment_cache:read`, `stack_tags:update`, `stack_schedule:*` (create, update, pause, resume, delete), `stack_webhook:*` (create, read, update, delete) |
| Stack Admin | Stack | "Grants full control over stack operations." Adds `stack:delete`, `stack:rename`, `stack:transfer`, `stack_access:update` |

The organization-wide Write setting for members allows `pulumi up` and
`pulumi destroy`, but not `pulumi stack rm`. A separate toggle controls if
stack admins can delete stacks.

### 2.3. Environment and cloud account permission sets

| Role | Scope | Permissions |
| --- | --- | --- |
| Environment Read | Environment | `environment:read`, `environment:rotate_history`, `environment_version:read`, `environment_schedule:read`, `environment_tag:read` |
| Environment Open | Environment | Adds `environment:open`, `environment:clone`, `environment_version:open` (read secret values) |
| Environment Write | Environment | Adds `environment:write`, `environment:rotate`, `environment_version:*`, `environment_tag:*`, `environment_schedule:*`, `environment_webhook:*` |
| Environment Admin | Environment | Adds `environment:delete` |
| Account Read, Write, Admin | Cloud account (Insights) | Read: `insights_account:read` and scan read. Write: update and scan. Admin: adds `insights_account:delete`, `insights_account_access:update` |

### 2.4. Teams

Teams get stack, environment and account permission sets. A team admin can
manage the team's direct entity grants. A change to a team's role needs
`role:update` and `team:update`. Team tokens use `team:create_token`,
`team:list_tokens` and `team:delete_token` (Admin by default).

## 3. HCP Terraform and Terraform Enterprise

Permissions go to teams. They add up across organization, project and
workspace, and "the most permissive" level applies.

### 3.1. Organization

| Role or permission | Scope | Permissions |
| --- | --- | --- |
| Owners team | Organization | "Every organization has an Owners team whose members have the maximum available permissions within the organization." Only owners can "Control organization-wide settings", "Control billing and subscriptions" (HCP Terraform only), manage IP allowlists and "Permanently delete the organization" |
| View all projects | Organization | "Can view all project names in the organization." |
| Manage all projects | Organization | "Can create, edit, and delete projects, and manage team access to all projects." |
| View all workspaces | Organization | "Can view information about all workspaces." |
| Manage all workspaces | Organization | "Admin permissions on all workspaces and can create workspaces." |
| Manage membership | Organization | "Invite, remove, and add users to the team" |
| Manage teams | Organization | "Create, update, delete teams and generate tokens" |
| Manage organization access | Organization | "Update team organization access settings" |
| Include secret teams | Organization | "Access and modify secret teams" |
| Allow member token management | Organization | "Control team token management for team members" |
| Manage policies | Organization | "Create, edit, read, list and delete Sentinel policies" |
| Manage policy overrides | Organization | Override failed soft-mandatory policy checks in all workspaces. "Delegate policy overrides" lets project and workspace managers grant this |
| Manage run tasks | Organization | "Create, edit, and delete run tasks" |
| Manage version control settings | Organization | "Manage VCS providers and SSH keys" |
| Manage agent pools | Organization | "Create, edit, and delete agent pools" |
| Manage modules, providers, Stack component configurations | Organization | "Publish and delete modules in the private registry" and the same for providers and component configurations. Public registry: manage public modules and providers |

The audit trails API needs "an organization token or an audit trail
token". User tokens and team tokens cannot read it.

### 3.2. Project roles

Each role includes the role below it.

| Role | Scope | Permissions |
| --- | --- | --- |
| Read | Project | View project details, runs, variables, full state and public outputs |
| Write | Project | Adds queue plans, apply runs, edit variables, read and write state, download Sentinel mocks, lock and unlock workspaces |
| Maintain | Project | Adds create workspaces and delete workspaces. Also creates Stacks. "Full control of existing infrastructure" without project settings and team management |
| Admin | Project | Adds edit and delete project, move workspaces between projects, manage team access, view team assignments, manage and view variable sets, manage workspace run tasks |
| Custom | Project | "You can create a set of custom permissions using any of the permissions listed in the Project roles and permissions table." "Stacks do not support custom group permissions." |

### 3.3. Workspace roles

| Role | Scope | Permissions |
| --- | --- | --- |
| Read | Workspace | Read runs, read variables, read outputs only, read state |
| Plan | Workspace | Adds plan runs. No apply |
| Write | Workspace | Adds apply runs, read and write variables, read and write state, download Sentinel mocks, lock/unlock workspace |
| Admin | Workspace | Adds manage workspace run tasks, read and write workspace settings, set workspace permissions for visible teams, delete workspace |
| Custom | Workspace | Any workspace permission except the three admin-only ones |

Custom workspace permissions, with their levels:

| Permission | Levels |
| --- | --- |
| Runs | Read ("View information about workspace runs"), Plan ("Queue Terraform plans in the workspace"), Apply ("Approve and apply Terraform plans in the workspace") |
| Variables | No access, Read, Read and write |
| State versions | No access, Read outputs only ("Access public outputs from workspace state"), Read ("Read complete state files from the workspace"), Read and write ("Create new state versions in the workspace") |
| Download Sentinel mocks | On or off |
| Lock/unlock workspace | "Manually lock the workspace to prevent runs." |
| Manage workspace Run Tasks | "Associate or dissociate run tasks with the workspace." |
| Allow policy overrides | On or off. Needs "Delegate policy override" on the team |

## 4. Spacelift

Spacelift assigns roles per space. The root space is account-wide.

| Role | Scope | Permissions |
| --- | --- | --- |
| Space Reader | Space | "Basic read permissions." View stacks, contexts, policies and runs. Comment on runs. Same as the legacy Reader role |
| Space Writer | Space | Adds trigger stack runs, run tasks and change stack environment variables. Same as the legacy Writer role |
| Space Admin | Space | Adds create and change stacks, contexts, policies and Intent projects. Manage space settings and role bindings in the space. Read-only view of roles, users, API keys and IdP group mappings. Same as the legacy Admin role |
| Root Space Admin | Account | Space Admin in the root space. Adds account-wide work such as SSO setup, VCS configuration and role creation |
| Worker pool controller | Space | Space: Read. Worker pool: Create, Update, Delete |
| Custom role | Space | Any set of actions from the list below. "The **Read** (Space) action is required to view any subjects within a space." |

Custom role actions, by group (from the RBAC reference):

| Group | Actions |
| --- | --- |
| Space | Admin, Read, Write, Share module |
| Run | Trigger, Confirm, Discard, Cancel, Stop, Kill, Retry, Review, Comment, Prioritize, Promote, Propose from local workspace, Propose with overrides, Replan targeted, Trigger with custom runtime config. Blocking variants of cancel, kill, retry, stop |
| Stack | Create, Update, Delete, Manage, Enable, Disable, Lock, Unlock, Force unlock, Lock and unlock stack workspace, Read stack state, Download stack state, Import managed state, Rollback managed state, Add and delete stack config, Re-slug, Set current commit, Sync commit, Set star, Upload local workspace |
| Task | Create task |
| Context | Create, Update, Delete |
| Worker pool | Create, Update, Delete, Drain worker, Cycle, Reset |
| Module and Terraform provider | Create, publish, delete, enable, disable, versions, visibility |
| Integrations and attachments | Attach and detach context, policy, AWS, Azure integration. Create and delete GCP integration |
| Stack role binding | Create, Update, Delete |
| Webhook, scheduling | Webhook create, update, delete. Scheduled delete, task and run: create, update, delete |
| Others | Intent, drift detection, template, repo, AI integration actions |

## 5. env0

| Role | Scope | Permissions |
| --- | --- | --- |
| Organization User | Organization | View organization variables, templates and modules, and the private module and provider registries |
| Organization Admin | Organization | "Full administrative access to the organization." Adds manage organization settings and variables, templates, modules, providers, custom roles, dashboards, audit logs, billing, move environments, credentials, VCS connections. Holds all project and environment permissions |
| Project Viewer | Project | Read project settings, templates, variables and environments. Read Terraform state. View drift causes |
| Project Planner | Project | Adds run plans to create, redeploy or destroy. "These runs require approval" |
| Project Deployer | Project | Adds run applies without approval, edit environment settings, write Terraform state, abort deployments |
| Project Admin | Project | Adds edit project settings and variables, manage project templates, archive environments, lock and unlock environments, override max TTL, cross-project environments, force-release workspace locks, create projects, assign environment roles, VCS environments, import environments, credentials and VCS connections |
| Custom role | Organization, project or environment | Any set of the permissions below |

Custom role permissions, by group:

| Group | Permissions |
| --- | --- |
| Organization | View Organization, Edit Organization Settings, Create & Edit Templates, Create & Edit Modules, Create & Edit Providers, View Modules, View Providers, Create & Edit Custom Roles, View Dashboard, Edit Dashboard, View Audit Logs, Manage Billing Information, Move Environments, Manage Credentials, Manage VCS Connections, Create Cross-Project Environment Triggers |
| Project | View Project, Edit Project Settings, Manage Project Templates, Create Project, Import Environments |
| Environment | View Environment, Edit Environment Settings, Edit Environment VCS Settings, View Drift Cause, Archive Environment, Lock/Unlock Environment, Override Max TTL, Override project's max-environments policy, Assign roles on environment |
| Deployment | Run Plans, Run Applies, Abort Deployments, Run Tasks, Create VCS Environment |
| Remote backend | Read State, Write State, Force Unlock Workspace, Edit Allow Remote Apply |

## 6. Scalr

Scalr has system roles and custom roles. A system role is "A set of
permissions that have been pre-created for common use cases." Scalr
maintains system roles, and users cannot change them. An access policy binds
a user, team or service account to roles at the account, environment or
workspace scope.

| Role | Scope | Permissions |
| --- | --- | --- |
| `user` (system) | Account, environment or workspace | Named in the docs as the role that gives a team full access to one environment. The docs do not list its permissions |
| Other system roles | Not documented in public pages we could read | A 2021 Scalr post names five built-in roles in the first RBAC phase but not their names |
| Custom role | Account, environment or workspace | Permissions in `resource:action` form, with wildcards such as `*:read` and `workspaces:*` |

Permission names that appear in Scalr docs: `accounts:read`,
`accounts:impersonate-users`, `environments:read`, `workspaces:read`,
`workspaces:create`, `plans:read-json-output`, `state-versions:read`,
`runs:apply`, `runs:cancel`, `variable:*`, `modules:*`, `roles:read`,
`teams:read`, `vcs-providers:read`, `service-accounts:create`,
`service-accounts:read`, `service-accounts:update`,
`service-accounts:delete`, `ownership:bypass`. The API endpoint "List
Permissions" returns the full list.

## 7. OTF, Terrateam and Atlantis

### 7.1. OTF

OTF says its model follows Terraform Cloud and Terraform Enterprise.

| Role | Scope | Permissions |
| --- | --- | --- |
| Site admin | Installation (cluster) | "supreme privileges across an OTF cluster." Log in as `site-admin` with a site token, or promote users with `--site-admins` |
| Owners team | Organization | All organization permissions, which cannot be removed. Only owners create teams, manage membership and change organization permissions. The team cannot be deleted and needs one member |
| Manage Workspaces | Organization | "Allows members to create and administrate all workspaces within the organization." |
| Manage VCS Settings | Organization | Manage the VCS providers of the organization |
| Manage Registry | Organization | Publish and delete modules |
| Read, Plan, Write, Admin | Workspace | Based on the TFC and TFE fixed permission sets. The OTF page does not list them |
| User with no team | Installation | Can only create organizations. This can be turned off |

### 7.2. Terrateam

Terrateam has no roles. A rule list per capability in `.terrateam/config.yml`
says who can do it. A rule is `*`, `user:<name>`, `team:<GitHub team>` or
`role:<repository role>`. Repository roles are "read, triage, write,
maintain, and admin".

| Capability | Meaning |
| --- | --- |
| `plan` | Run a plan |
| `apply` | Run an apply after apply requirements pass |
| `apply_autoapprove` | "Execute a plan and apply in the same execution, skipping approvals" |
| `apply_force` | "Run an apply even if the change has not passed apply requirements" |
| `superapproval`, `apply_with_superapproval` | A list of users whose approvals count as super approvals, and who may apply after one |
| `unlock` | "Ruleset for which users can trigger an unlock operation on a pull request. Default is ['*']." |
| `terrateam_config_update` | Who can run an operation on a pull request that changes the Terrateam config. Default `['*']` |

The docs show a pattern where "anyone can plan, but only members of the SRE
team can apply".

### 7.3. Atlantis

Atlantis has no users or roles. Access comes from the VCS host and server
flags.

| Control | Meaning |
| --- | --- |
| Apply requirements | `approved`: "at least one person other than the author". `mergeable`: the pull request can merge. After that, "anyone that can comment on the pull request can run the actual `atlantis apply` command" |
| `--gh-team-allowlist` | "Comma-separated list of GitHub teams and permission pairs." "By default, any team can plan and apply." |
| `--allow-commands` | Defaults to `version,plan,apply,unlock,approve_policies,cancel` |
| `--repo-allowlist` | The repositories that Atlantis accepts webhooks from |

## 8. Concepts across products

Y means the product has a role or permission for the concept. "-" means we
found none. "Admin" means only the top admin or owner role holds it.

| Concept | Pulumi | HCP Terraform | Spacelift | env0 | Scalr | OTF | Terrateam | Atlantis |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| Read unit metadata, runs, config | Y (`stack:read`) | Y (Read runs) | Y (Space Reader) | Y (View Environment) | Y (`workspaces:read`) | Y (Read) | - | - |
| Read full state | Y (`stack:export`) | Y (Read state) | Y (Read stack state) | Y (Read State) | Y (`state-versions:read`) | Y | - | - |
| Read outputs only | - | Y (Read outputs only) | - | - | - | - | - | - |
| Plan or preview, no apply | Y (Stack Read runs previews) | Y (Plan role) | Y (Propose run) | Y (Project Planner) | - | Y (Plan) | Y (`plan`) | Y (team allowlist) |
| Apply | Y (Stack Write) | Y (Apply runs) | Y (Trigger and confirm run) | Y (Run Applies) | Y (`runs:apply`) | Y (Write) | Y (`apply`) | Y (team allowlist) |
| Apply without approval or bypass checks | - | - | - | Y (Deployer) | - | - | Y (`apply_autoapprove`, `apply_force`) | - |
| Cancel a run | Y (`stack:cancel_update`) | - | Y (Cancel, Stop, Kill run) | Y (Abort Deployments) | Y (`runs:cancel`) | - | - | Y (`cancel`) |
| Write state directly (import) | Y (`stack:import`) | Y (Read and write state) | Y (Import managed state) | Y (Write State) | - | Y | - | - |
| Manage variables or config | Y (`stack:write`) | Y (Read and write variables) | Y (Space Writer) | Y (Edit Environment Settings) | Y (`variable:*`) | Y | - | - |
| Read secrets or decrypt | Y (`stack:decrypt`, `environment:open`) | - | - | - | - | - | - | - |
| Manage locks | - (no lock permission found) | Y (Lock/unlock workspace) | Y (Lock, Unlock, Force unlock stack) | Y (Lock/Unlock Environment, Force Unlock Workspace) | - | Y | Y (`unlock`) | Y (`unlock`) |
| Create and delete units | Y (`stack:delete`, member toggle) | Y (Maintain, Admin) | Y (Space Admin) | Y (Project Admin) | Y (`workspaces:create`) | Y (Manage Workspaces) | - | - |
| Rename, move or transfer units | Y (`stack:rename`, `stack:transfer`) | Y (Move workspaces) | Y (Re-slug stack) | Y (Move Environments) | - | - | - | - |
| Manage access to a unit | Y (`stack_access:update`) | Y (Set workspace permissions) | Y (Stack role binding) | Y (Assign roles on environment) | - | Y | - | - |
| Manage teams and members | Y (Admin, `team:*`, `org_member:*`) | Y (Manage membership, Manage teams) | Y (root admin) | Y (Admin) | Y (`teams:read` and others) | Y (owners only) | - | - |
| Manage tokens | Y (`org_token:*`, `team:create_token`) | Y (Manage teams, Allow member token management) | - | - | Y (`service-accounts:*`) | - | - | - |
| Manage custom roles | Y (`role:*`) | - | Y (root admin) | Y (Create & Edit Custom Roles) | Y (`roles:read` and others) | - | - | - |
| Manage policies | Y (`policy_pack:*`) | Y (Manage policies, policy overrides) | Y (Space Admin) | - | - | - | - | Y (`approve_policies`) |
| Manage VCS connections | - | Y (Manage version control settings) | Y (root admin) | Y (Manage VCS Connections) | Y (`vcs-providers:read`) | Y (Manage VCS Settings) | - | - |
| Manage runners or agent pools | - | Y (Manage agent pools) | Y (Worker pool controller) | - | - | - | - | - |
| Manage module or provider registry | - | Y (Manage modules, Manage providers) | Y (Module and provider actions) | Y (Create & Edit Modules, Providers) | Y (`modules:*`) | Y (Manage Registry) | - | - |
| Manage webhooks | Y (`stack_webhook:*`, `organization_webhook:*`) | - | Y (Stack webhook) | - | - | - | - | - |
| Manage SSO, SAML or SCIM | Y (`saml:update`, `scim:*`) | Admin | Y (root admin) | - | - | - | - | - |
| Billing | Y (Billing Manager) | Admin | - | Y (Manage Billing Information) | - | - | - | - |
| Audit log read | Admin (`audit_logs:read`) | Admin (organization or audit trail token) | - | Y (View Audit Logs) | - | - | - | - |
| Admin of one scope | Y (Stack Admin, Environment Admin) | Y (Project Admin, Workspace Admin) | Y (Space Admin) | Y (Project Admin) | Y (access policy per scope) | Y (Admin) | - | - |
| Owner of the organization | Y (Admin) | Y (Owners team) | Y (root Space Admin) | Y (Organization Admin) | - | Y (Owners team) | - | - |
| Installation operator | - | - | - | - | - | Y (site admin) | - | - |
| Custom roles | Y | Y | Y | Y | Y | - | - | - |

## 9. Roles that recur

| Role | Products | Facts |
| --- | --- | --- |
| Viewer or reader | Pulumi (Stack Read, Member with Read), HCP Terraform (Read), Spacelift (Space Reader), env0 (Project Viewer, Organization User), OTF (Read) | Each reads runs and config. Pulumi, HCP Terraform and env0 also give full state. Pulumi Stack Read also decrypts secrets and runs previews |
| Outputs-only reader | HCP Terraform (custom state permission) | No product has a built-in role for it. HCP Terraform has it as a permission level |
| Planner | HCP Terraform (Plan), env0 (Project Planner), OTF (Plan), Terrateam (`plan` only) | Can start a plan, cannot apply. env0 lets a planner start a run that then needs approval |
| Writer or deployer | Pulumi (Stack Write), HCP Terraform (Write), Spacelift (Space Writer), env0 (Project Deployer), OTF (Write) | Applies, changes variables, writes state. HCP Terraform and env0 also give lock and unlock |
| Maintainer | HCP Terraform (project Maintain) | Writer plus create and delete workspaces, without settings and access. Terrateam and Atlantis can use the GitHub `maintain` repository role in rules |
| Scope admin | Pulumi (Stack Admin), HCP Terraform (project and workspace Admin), Spacelift (Space Admin), env0 (Project Admin), OTF (Admin) | Writer plus delete, settings and access management on one scope |
| Owner or organization admin | Pulumi (Admin), HCP Terraform (Owners team), Spacelift (root Space Admin), env0 (Organization Admin), OTF (Owners team) | Holds all permissions in the organization, including members, teams, roles and settings. HCP Terraform and OTF make the owners team undeletable |
| Billing | Pulumi (Billing Manager), HCP Terraform (owners only), env0 (Organization Admin or custom) | Only Pulumi has a separate built-in billing role |
| Auditor | None | No product documents a built-in auditor role. env0 has the custom permission "View Audit Logs". Pulumi has the scope `audit_logs:read`. HCP Terraform has an audit trail token |
| Runner or agent operator | Spacelift (Worker pool controller) | Manages worker pools only |
| Installation operator | OTF (site admin) | The only product in this list with a documented role above the organization. HCP Terraform and Terrateam on-premise admin roles were not in scope |
| Member baseline | Pulumi (Member), env0 (Organization User), OTF (user with no team) | The role a new user gets. Pulumi and OTF let admins change what it can create |

## 10. Limits of this note

- Scalr: the public docs name the system role `user` only. They do not list
  the names or permissions of the other system roles. We did not see the
  Scalr console.
- Pulumi: the stack-permissions page fetch returned the permission set data,
  not a separate None/Read/Write/Admin matrix. We found no Pulumi lock
  permission. Pulumi Cloud may not have user-managed stack locks.
- HCP Terraform: the project page fetch gave the matrix in summary form.
  Read the page before you copy the Read project row, which shows "Read full
  state" for Read.
- Spacelift: the legacy Reader, Writer and Admin pages were not read. We
  rely on the RBAC page that says the new roles are "equivalent" to them.
  Spacelift audit trail access was not found.
- Terrateam: `docs.terrateam.io` now redirects to `stategraph.com/docs`. The
  capability text comes from search result excerpts of the old pages, not a
  full page read.
- OTF: the page does not list what each workspace role holds.
- Terraform Enterprise site admin and Spacelift self-hosted admin roles
  were not researched.

## 11. Sources

All accessed 2026-10-13.

- Pulumi, Roles:
  https://www.pulumi.com/docs/administration/access-identity/rbac/roles/
- Pulumi, Permission sets:
  https://www.pulumi.com/docs/administration/access-identity/rbac/permission-sets/
- Pulumi, Scopes:
  https://www.pulumi.com/docs/administration/access-identity/rbac/scopes/
- Pulumi, Organization settings scopes:
  https://www.pulumi.com/docs/administration/access-identity/rbac/scopes/org-settings/
- Pulumi, Stack permissions:
  https://www.pulumi.com/docs/pulumi-cloud/access-management/stack-permissions
- HashiCorp, Permissions overview:
  https://developer.hashicorp.com/terraform/cloud-docs/users-teams-organizations/permissions
- HashiCorp, Organization permissions:
  https://developer.hashicorp.com/terraform/cloud-docs/users-teams-organizations/permissions/organization
- HashiCorp, Project permissions:
  https://developer.hashicorp.com/terraform/cloud-docs/users-teams-organizations/permissions/project
- HashiCorp, Workspace permissions:
  https://developer.hashicorp.com/terraform/cloud-docs/users-teams-organizations/permissions/workspace
- HashiCorp, Audit trails API:
  https://developer.hashicorp.com/terraform/cloud-docs/api-docs/audit-trails
- Spacelift, RBAC system:
  https://docs.spacelift.io/concepts/authorization/rbac-system
- Spacelift, Space access control:
  https://docs.spacelift.io/concepts/spaces/access-control
- env0, Default roles:
  https://docs.envzero.com/guides/admin-guide/user-role-and-team-management/default-roles
- env0, Custom roles:
  https://docs.envzero.com/guides/admin-guide/user-role-and-team-management/custom-roles
- Scalr, Identity and access management:
  https://docs.scalr.io/docs/identity-and-access-management
- Scalr, List permissions API:
  https://docs.scalr.io/reference/get_permissions
- Scalr, `scalr_role` data source:
  https://docs.scalr.io/docs/provider_datasource_scalr_role
- Scalr, RBAC release post (June 10, 2021):
  https://scalr.com/blog/scalr-rbac-release
- OTF, RBAC:
  https://docs.otf.ninja/rbac/
- Terrateam, Access control reference (now redirects to stategraph.com):
  https://docs.terrateam.io/configuration-reference/access-control
- Terrateam, Access control recipes:
  https://terrateam.io/docs/recipes/access-controls
- Atlantis, Command requirements:
  https://www.runatlantis.io/docs/command-requirements.html
- Atlantis, Server configuration:
  https://www.runatlantis.io/docs/server-configuration.html
