# Tag migration copies

This directory holds one subdirectory per released tag, named for the tag,
for example `v0.1.0`. Each subdirectory holds the copies of
`pkg/store/postgres/migrations/*.sql` as they were at that tag.

At tag time, copy the migration set of the tag here before the tag goes out:

```sh
tag=v0.1.0
mkdir -p pkg/store/postgres/testdata/tag-migrations/"$tag"
git archive "$tag" pkg/store/postgres/migrations | tar -x -C /tmp/tag-migrations
cp /tmp/tag-migrations/pkg/store/postgres/migrations/*.sql \
  pkg/store/postgres/testdata/tag-migrations/"$tag"/
```

Copy the `.sql.sha256` hash file of each migration too, or run
`scripts/copy-tag-migrations.sh "$tag"`, which copies both. `frozen_test.go`
checks each copy against its hash file. These copies come after the tag.
The commit that the release tags already lists each migration file in
`internal/releaserecord/shipped.txt`, which turns the checks on.
`TestTagCopiesHaveTheirLines` fails when a tag directory here has no lines
in that file. The schema upgrade test applies the copies of each tag and
then the current migrations, and compares the result with a fresh install.
