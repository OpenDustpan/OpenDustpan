Owner: move `ci-draft/workflows/auto-regen.yml` to `.github/workflows/auto-regen.yml` and remove `ci-draft/` in one commit on this branch before merge; `scripts/auto-regen.sh` stays where it is.
Create a GitHub App on this repository with Contents, Pull requests and Workflows set to read and write, then add the repository secrets `REGEN_APP_CLIENT_ID` (its client ID) and `REGEN_APP_PRIVATE_KEY` (a private key).
Optional: create the label `auto-regen` to opt in a pull request that does not have auto-merge on.
