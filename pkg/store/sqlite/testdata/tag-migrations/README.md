# Tag migration copies

This directory holds one subdirectory per released tag, named for the tag,
for example `v0.1.0`. Each subdirectory holds the copies of
`pkg/store/sqlite/migrations/*.sql` as they were at that tag.

At tag time, copy the migration set of the tag here before the tag goes out:

```sh
tag=v0.1.0
mkdir -p pkg/store/sqlite/testdata/tag-migrations/"$tag"
git archive "$tag" pkg/store/sqlite/migrations | tar -x -C /tmp/tag-migrations
cp /tmp/tag-migrations/pkg/store/sqlite/migrations/*.sql \
  pkg/store/sqlite/testdata/tag-migrations/"$tag"/
```

Then record the SHA-256 of each copied file in `frozen_test.go` in the same
change. The schema upgrade test applies the copies of each tag and then the
current migrations, and compares the result with a fresh install.
