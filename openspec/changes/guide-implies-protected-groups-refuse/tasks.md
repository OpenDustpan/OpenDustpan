## 1. Guide fix

- [ ] 1.1 Edit the guide near lines 243-245 to state that the flag is shown, that the sync does not yet refuse changes to a protected group, and that the accepted rule is not enforced yet. Fix the sentence about groups that are not protected. Check: rg -n 'protected' docs/guides/principals-and-groups.md shows the corrected paragraph and no sentence that implies a refusal.
- [ ] 1.2 Find every other section that mentions group sync or identity provider membership. Edit each one that implies a refusal. List each edit in the pull request description. Check: rg -n -i 'identity provider|group claim|scim|sync' docs/guides/principals-and-groups.md lists each hit, and the description lists the edits.
- [ ] 1.3 Compare the new wording with the sync code and the SQLite and PostgreSQL group_claims adapters. Confirm that the sync applies no protected-group rule. Check: rg -n -i 'protected' pkg/store/internal/groupclaims pkg/store/sqlite pkg/store/postgres shows no check of the flag in the sync, and the description records the result.
- [ ] 1.4 Comment on issue #2688: this change lands first, and each pull request that enforces the rule, in part or in full, must rewrite the guide sentence and update the catalog of code rules. Check: The comment on issue #2688 exists and the description links it.
- [ ] 1.5 Run docs lint on the edited guide. Check: make docs-lint passes with zero findings.

## 2. Definition of done

- [ ] 2.1 Run the full check. Check: make check passes with zero findings.
