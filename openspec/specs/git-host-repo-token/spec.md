# git-host-repo-token Specification

## Purpose
The Git host adapter for GitHub gives a token for one repository. The token reads the contents only and lives at most 1 hour. The adapter refuses a token that grants more than it asked for, and no error holds a token value. A runner or a later feature uses this token to read a repository without the key of the app.

## Requirements

### Requirement: Scoped repo token

The GitHub adapter SHALL answer RepoToken for an app connection with a token for one repository, with the permission to read the contents only, an expiry of at most 1 hour from the time of the call, and Scoped set. A token connection SHALL answer its own token with Scoped not set. The adapter SHALL refuse a token that GitHub makes for more repositories or with more permissions than it asked for, and SHALL list the capabilities repo-token and git-access.

#### Scenario: Scoped token
- **WHEN** the server asks for a repo token of one repository
- **THEN** the token reads that repository only, has Scoped set, and expires within 1 hour

#### Scenario: Longer life from GitHub
- **WHEN** GitHub answers a token that expires in more than 1 hour
- **THEN** the adapter sets the expiry to 1 hour from the call

#### Scenario: Wider grant
- **WHEN** GitHub answers a token for more repositories or more permissions than asked
- **THEN** the adapter returns an error and no token

#### Scenario: Token connection
- **WHEN** the connection is a token connection
- **THEN** RepoToken answers the token of the connection with Scoped not set, and the adapter lists git-access and not repo-token
