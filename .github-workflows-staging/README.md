# `.github-workflows-staging/`

Agents cannot push to `.github/workflows/`, so workflow changes land here first.
An agent writes the full NEW or changed workflow file to this directory in its
change. The owner moves each file into `.github/workflows/` later and pushes,
which is tracked in `openspec/changes/person-steps/tasks.md` ("Move staged
workflows into place").

Rules:

- One file per workflow, same name as its target (for example `ci.yml`).
- Each staged file is complete and runnable as written.
- No tool reads this directory. CI ignores it. Only `.github/workflows/` runs.
- Delete the staged file in the same pull request that moves it into place.
