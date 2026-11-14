#!/usr/bin/env bash
# The steps of the auto-regen workflows (.github/workflows/auto-regen*.yml).
# The find job selects pull requests that the merge queue removed because main
# moved and the pull request conflicts only in generated files. A resolve run
# on the pull request branch merges main, takes the side of main for each
# conflicted generated file, runs the generators again and writes a patch. A
# push run on main puts the patch on the pull request branch.
#
# Subcommands:
#   find OUT_FILE
#       Write a JSON list of candidate pull requests to OUT_FILE. Reads
#       GITHUB_REPOSITORY, EVENT_PR and INPUT_PR. Needs gh and jq
#       with a read token. Runs no pull request code.
#   dispatch PRS_JSON MAIN_SHA
#       Dispatch auto-regen-resolve.yml on the branch of each pull request in
#       PRS_JSON, with MAIN_SHA. Reads GITHUB_REPOSITORY. Needs gh with a
#       token that can dispatch workflows. A failed dispatch logs one line and
#       continues with the next pull request.
#   resolve MAIN_SHA OUT_DIR
#       Run in a checkout of the pull request head. Merge MAIN_SHA, check the
#       conflicts, run the generators and write OUT_DIR/result.env and
#       OUT_DIR/regen.patch. This step runs the code of the pull request.
#       Give it no secret and no write token.
#   match-run BRANCH HEAD_SHA OUT_FILE
#       Find the one open pull request from BRANCH at HEAD_SHA to main. Write
#       number, branch and head to OUT_FILE as key=value lines. On a mismatch,
#       log one line and write nothing.
#   apply MAIN_SHA HEAD_SHA PATCH
#       Run in a checkout of the pull request branch. Check the patch, merge
#       MAIN_SHA again, apply the patch to the generated paths and commit.
#       Runs no pull request code: only git.
#   other-conflicts MAIN_SHA HEAD_SHA
#       Print the files that are not generated and that conflict when
#       MAIN_SHA merges into HEAD_SHA. Runs no pull request code: only git.
#   check-patch PATCH
#       Fail when PATCH touches a path that is not generated.
#   paths
#       Print the generated paths.
#
# The script never rebases, resets, stashes, cleans or force-pushes.
set -euo pipefail

# The files that the generators write. A directory ends with a slash. An entry
# with a star matches one file name in that directory. Keep this list the same
# as the list of make regen, plus the hash files of the store baselines.
GENERATED=(
	internal/api/apidesc/openapi.json
	clients/typescript/
	internal/clientgen/golang/testdata/golden/
	internal/clientgen/typescript/testdata/golden/
	pkg/store/postgres/internal/db/
	pkg/store/sqlite/internal/db/
	pkg/preferences/zones_gen.go
	internal/pulumijournal/entry_gen.go
	internal/pulumijournal/internal/gen/testdata/journal.go
	docs/adr/INDEX.md
	.repowise/decisions.yaml
	docs/reference/configuration.md
	'pkg/store/postgres/migrations/*.sql.sha256'
	'pkg/store/sqlite/migrations/*.sql.sha256'
)

# The subject of each commit that this workflow makes. The loop guard reads it.
COMMIT_SUBJECT="Merge main; regenerate generated files"

# The loop guard: skip a pull request when its head is a commit of this
# workflow that is younger than this number of minutes.
LOOP_GUARD_MINUTES=30

log() {
	printf 'auto-regen: %s\n' "$*" >&2
}

die() {
	log "$*"
	exit 1
}

# is_generated PATH: succeed when PATH is a generated path.
is_generated() {
	local path="$1" entry dir
	# Defense in depth: the patch check refuses these names too; this check
	# stays so that a caller that skips the patch check cannot match them.
	case "$path" in
	"" | /* | *..* | *$'\n'* | *\\*) return 1 ;;
	esac
	for entry in "${GENERATED[@]}"; do
		case "$entry" in
		*/)
			[[ "$path" == "$entry"* ]] && return 0
			;;
		*'*'*)
			dir="${entry%/*}/"
			if [[ "$path" == "$dir"* ]]; then
				local base="${path#"$dir"}"
				# One file name only: no slash after the directory.
				# The pattern must stay unquoted to match as a glob.
				# shellcheck disable=SC2053
				[[ "$base" != */* && "$base" == ${entry##*/} ]] && return 0
			fi
			;;
		*)
			[[ "$path" == "$entry" ]] && return 0
			;;
		esac
	done
	return 1
}

cmd_paths() {
	printf '%s\n' "${GENERATED[@]}"
}

# check_patch PATCH: fail when the patch touches a path that is not generated,
# renames or copies a file, or writes a symbolic link or a submodule.
cmd_check_patch() {
	local patch="$1" line a b path bad=0
	[ -f "$patch" ] || die "no patch file: $patch"
	while IFS= read -r line; do
		case "$line" in
		'diff --git "'* | 'rename '* | 'copy '* | 'similarity index'*)
			log "refuse: the patch has a quoted name, a rename or a copy: $line"
			bad=1
			;;
		'diff --git '*)
			a="${line#diff --git a/}"
			a="${a%% b/*}"
			b="${line##* b/}"
			for path in "$a" "$b"; do
				if ! is_generated "$path"; then
					log "refuse: the patch changes a path that is not generated: $path"
					bad=1
				fi
			done
			;;
		*' mode 120000' | *' mode 160000' | 'index '*' 120000' | 'index '*' 160000')
			log "refuse: the patch writes a symbolic link or a submodule: $line"
			bad=1
			;;
		esac
	done <"$patch"
	# Defense in depth: git reads the paths of the patch on its own too, so a
	# header that the loop above reads wrong cannot hide a path.
	git apply --numstat "$patch" >/dev/null || die "git apply cannot read the patch"
	while IFS= read -r -d '' line; do
		path="${line#*$'\t'*$'\t'}"
		if ! is_generated "$path"; then
			log "refuse: git apply sees a path that is not generated: $path"
			bad=1
		fi
	done < <(git apply --numstat -z "$patch")
	[ "$bad" -eq 0 ] || die "the patch is refused"
	log "the patch changes only generated paths"
}

# conflicts: print the paths that the merge left unmerged.
conflicts() {
	git diff --name-only --diff-filter=U
}

# merge_main MAIN_SHA: merge MAIN_SHA into HEAD with no commit. Succeed also
# when the merge stops on conflicts.
merge_main() {
	local main_sha="$1"
	git cat-file -e "$main_sha^{commit}" || die "main commit $main_sha is not in this clone"
	if git -c user.name=auto-regen -c user.email=auto-regen@invalid \
		merge --no-ff --no-commit --no-edit "$main_sha"; then
		log "the merge with main has no conflict"
	else
		log "the merge with main stopped on conflicts"
	fi
}

# take_main_side: for each conflicted path (all generated), take the version
# of main, or the deletion when main deleted it.
take_main_side() {
	local main_sha="$1" path
	while IFS= read -r path; do
		[ -n "$path" ] || continue
		if git cat-file -e "$main_sha:$path" 2>/dev/null; then
			log "take the side of main: $path"
			git checkout "$main_sha" -- "$path"
		else
			log "main deleted $path; take the deletion"
			git rm -q -f -- "$path"
		fi
	done < <(conflicts)
}

# refuse_other_conflicts: fail with status 3 when a conflicted path is not
# generated.
refuse_other_conflicts() {
	local path others=()
	while IFS= read -r path; do
		[ -n "$path" ] || continue
		is_generated "$path" || others+=("$path")
	done < <(conflicts)
	if [ "${#others[@]}" -gt 0 ]; then
		log "conflicts in files that are not generated; a person must resolve them:"
		printf '  %s\n' "${others[@]}" >&2
		return 3
	fi
}

# write_hashes: write the SHA-256 file of each store migration that has one.
write_hashes() {
	local hash_file sql sum
	for hash_file in pkg/store/postgres/migrations/*.sql.sha256 pkg/store/sqlite/migrations/*.sql.sha256; do
		[ -f "$hash_file" ] || continue
		sql="${hash_file%.sha256}"
		[ -f "$sql" ] || die "no migration for the hash file $hash_file"
		sum="$(sha256sum "$sql")"
		printf '%s\n' "${sum%% *}" >"$hash_file"
		log "wrote $hash_file"
	done
}

# regenerate: run each generator. The API description comes first, because
# the clients read it. This runs the Makefile and the tests of the pull
# request.
regenerate() {
	[ -n "${DUSTPAN_TEST_POSTGRES_URL:-}" ] || die "set DUSTPAN_TEST_POSTGRES_URL: the API description step needs PostgreSQL"
	go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description -count=1
	make generate
	go test ./internal/clientgen/golang -run TestRenderGolden -update -count=1
	go test ./internal/clientgen/typescript -run TestRenderGolden -update -count=1
	make adr-index
	make adr-decisions
	write_hashes
}

# filter_generated: read NUL-separated paths and write the generated ones,
# NUL-separated.
filter_generated() {
	local path
	while IFS= read -r -d '' path; do
		if is_generated "$path"; then
			printf '%s\0' "$path"
		fi
	done
}

# add_generated: stage each changed, deleted or new generated file by name.
add_generated() {
	local path
	while IFS= read -r -d '' path; do
		git add -A -- ":(literal)$path"
	done < <(git ls-files -z --modified --deleted --others --exclude-standard | filter_generated)
}

cmd_resolve() {
	local main_sha="$1" out="$2" head_sha status=0
	mkdir -p "$out"
	head_sha="$(git rev-parse HEAD)"
	write_result() {
		{
			printf 'status=%s\n' "$1"
			printf 'head=%s\n' "$head_sha"
			printf 'main=%s\n' "$main_sha"
		} >"$out/result.env"
	}
	[ -z "$(git status --porcelain)" ] || die "the work tree is not clean"
	merge_main "$main_sha"
	if [ -z "$(conflicts)" ]; then
		log "no conflict with main: nothing to do"
		write_result no-conflict
		return 0
	fi
	refuse_other_conflicts || status=$?
	if [ "$status" -ne 0 ]; then
		write_result other-conflicts
		return 0
	fi
	take_main_side "$main_sha"
	regenerate
	add_generated
	[ -z "$(conflicts)" ] || die "conflicts remain after the generators ran"
	local changed=()
	mapfile -d '' -t changed < <(git diff --cached --name-only -z --no-renames "$head_sha" | filter_generated)
	if [ "${#changed[@]}" -eq 0 ]; then
		die "the merge result has no change to a generated file"
	fi
	if git grep -q -I -E '^(<<<<<<<|>>>>>>>) ' --cached -- "${changed[@]/#/:(literal)}"; then
		die "a conflict marker remains in a generated file"
	fi
	# The patch holds the generated files of the merge result, against the
	# pull request head. The apply step merges again and puts these files on
	# top.
	git diff --cached --binary --full-index --no-renames "$head_sha" -- "${changed[@]/#/:(literal)}" >"$out/regen.patch"
	cmd_check_patch "$out/regen.patch"
	write_result ready
	log "wrote $out/regen.patch ($(wc -l <"$out/regen.patch") lines)"
}

cmd_apply() {
	local main_sha="$1" head_sha="$2" patch="$3"
	[ "$(git rev-parse HEAD)" = "$head_sha" ] || die "the branch moved: HEAD is not $head_sha"
	[ -z "$(git status --porcelain)" ] || die "the work tree is not clean"
	cmd_check_patch "$patch"
	merge_main "$main_sha"
	[ -n "$(conflicts)" ] || die "no conflict with main: nothing to apply"
	# Defense in depth: the resolve step refuses these conflicts too; this
	# check stays because the resolve step runs pull request code.
	refuse_other_conflicts || die "refuse: conflicts in files that are not generated"
	# Put each generated file back to the pull request head, then apply the
	# patch, which goes from the head to the merge result.
	local path
	while IFS= read -r -d '' path; do
		# An unmerged path is listed once for each stage.
		git rm -q -f --cached --ignore-unmatch -- ":(literal)$path"
		rm -f -- "$path"
	done < <(git ls-files -z | filter_generated)
	while IFS= read -r -d '' path; do
		rm -f -- "$path"
	done < <(git ls-files -z --others | filter_generated)
	while IFS= read -r -d '' path; do
		git checkout "$head_sha" -- ":(literal)$path"
	done < <(git ls-tree -r -z --name-only "$head_sha" | filter_generated)
	if [ -s "$patch" ]; then
		git apply --index --binary "$patch"
	fi
	[ -z "$(conflicts)" ] || die "conflicts remain after the patch"
	git -c user.name="${BOT_NAME:?}" -c user.email="${BOT_EMAIL:?}" \
		commit -q -s --no-verify -m "$COMMIT_SUBJECT" \
		-m "Main moved and the merge stopped only on generated files. This commit takes the side of main for those files and runs the generators again on the merged code."
	log "committed $(git rev-parse HEAD) on top of $head_sha and $main_sha"
}

# cmd_other_conflicts MAIN_SHA HEAD_SHA: print the conflicted files that are
# not generated. The merge happens in memory: the work tree does not change.
cmd_other_conflicts() {
	local main_sha="$1" head_sha="$2" path
	git cat-file -e "$main_sha^{commit}" || die "main commit $main_sha is not in this clone"
	git cat-file -e "$head_sha^{commit}" || die "head commit $head_sha is not in this clone"
	# The first line is the tree. The exit status is 1 when there are
	# conflicts.
	while IFS= read -r path; do
		[ -n "$path" ] || continue
		is_generated "$path" || printf '%s\n' "$path"
	done < <(git merge-tree --write-tree --name-only --no-messages "$head_sha" "$main_sha" | tail -n +2 | LC_ALL=C sort -u)
}

# cmd_find OUT_FILE: write the candidate pull requests as a JSON list.
cmd_find() {
	local out="$1" numbers=() n pr reason repo="${GITHUB_REPOSITORY:?}" list='[]'
	local now commit_date subject age tries state
	if [ -n "${INPUT_PR:-}" ]; then
		[[ "$INPUT_PR" =~ ^[0-9]{1,7}$ ]] || die "the pr input is not a number: $INPUT_PR"
		numbers=("$INPUT_PR")
	elif [ -n "${EVENT_PR:-}" ]; then
		[[ "$EVENT_PR" =~ ^[0-9]{1,7}$ ]] || die "the event pull request is not a number: $EVENT_PR"
		numbers=("$EVENT_PR")
	else
		# The list gives no mergeable state, so ask for each pull request
		# alone. To save requests, keep only the ones that can be candidates.
		# The checks below run again on the full pull request.
		mapfile -t numbers < <(gh api --paginate "repos/$repo/pulls?state=open&base=main&per_page=100" \
			--jq ".[] | select(.draft == false and .head.repo.full_name == \"$repo\" and (.auto_merge != null or ([.labels[].name] | index(\"auto-regen\") != null))) | .number")
		log "${#numbers[@]} open pull requests have auto-merge on or the label auto-regen"
	fi
	now="$(date +%s)"
	for n in "${numbers[@]}"; do
		[[ "$n" =~ ^[0-9]{1,7}$ ]] || continue
		# GitHub computes mergeable_state after a change. Ask again while it
		# is unknown.
		for tries in 1 2 3; do
			pr="$(gh api "repos/$repo/pulls/$n")"
			state="$(jq -r '.mergeable_state // "unknown"' <<<"$pr")"
			[ "$state" = unknown ] || break
			log "#$n: mergeable state is unknown (try $tries); ask again in 10 seconds"
			sleep 10
		done
		if [ "$(jq -r .state <<<"$pr")" != open ]; then
			log "#$n: skip: not open"
			continue
		fi
		if [ "$(jq -r .draft <<<"$pr")" != false ]; then
			log "#$n: skip: draft"
			continue
		fi
		if [ "$(jq -r .head.repo.full_name <<<"$pr")" != "$repo" ]; then
			log "#$n: skip: the branch is not in this repository"
			continue
		fi
		if [ "$(jq -r .base.ref <<<"$pr")" != main ]; then
			log "#$n: skip: the base is not main"
			continue
		fi
		local branch
		branch="$(jq -r .head.ref <<<"$pr")"
		case "$branch" in
		main | gh-readonly-queue/*)
			log "#$n: skip: branch $branch is protected from this workflow"
			continue
			;;
		esac
		if [ "$state" != dirty ]; then
			log "#$n: skip: mergeable state is $state, not dirty"
			continue
		fi
		reason=""
		if [ "$(jq -r '.auto_merge != null' <<<"$pr")" = true ]; then
			reason="auto-merge is on"
		elif [ "$(jq -r '[.labels[].name] | index("auto-regen") != null' <<<"$pr")" = true ]; then
			reason="label auto-regen"
		elif [ -n "${INPUT_PR:-}" ]; then
			reason="manual run for this pull request"
		fi
		if [ -z "$reason" ]; then
			log "#$n: skip: not queued, no auto-merge and no auto-regen label"
			continue
		fi
		local head_sha
		head_sha="$(jq -r .head.sha <<<"$pr")"
		subject="$(gh api "repos/$repo/commits/$head_sha" --jq '.commit.message | split("\n")[0]')"
		commit_date="$(gh api "repos/$repo/commits/$head_sha" --jq '.commit.committer.date')"
		age=$(((now - $(date -d "$commit_date" +%s)) / 60))
		if [ "$subject" = "$COMMIT_SUBJECT" ] && [ "$age" -lt "$LOOP_GUARD_MINUTES" ]; then
			log "#$n: skip: loop guard: the head is a commit of this workflow from $age minutes ago"
			continue
		fi
		log "#$n: candidate ($reason): branch $branch, head $head_sha"
		list="$(jq -c --argjson n "$n" --arg b "$branch" --arg h "$head_sha" \
			'. + [{number: $n, branch: $b, head: $h}]' <<<"$list")"
	done
	printf '%s\n' "$list" >"$out"
	log "candidates: $list"
}

# cmd_dispatch PRS_JSON MAIN_SHA: dispatch the resolve workflow on the branch
# of each candidate. A failed dispatch logs one line and does not stop the
# others.
cmd_dispatch() {
	local prs_json="$1" main_sha="$2" repo="${GITHUB_REPOSITORY:?}"
	local number branch head_sha err
	[ -f "$prs_json" ] || die "no pull request list: $prs_json"
	[[ "$main_sha" =~ ^[0-9a-f]{40}$ ]] || die "main is not a full commit SHA: $main_sha"
	while IFS=$'\t' read -r number branch head_sha; do
		[[ "$number" =~ ^[0-9]{1,7}$ ]] || die "a pull request number is not a number: $number"
		[[ "$head_sha" =~ ^[0-9a-f]{40}$ ]] || die "the head of #$number is not a full commit SHA: $head_sha"
		case "$branch" in
		main | gh-readonly-queue/*)
			die "refuse to dispatch on $branch"
			;;
		esac
		if err="$(gh api -X POST "repos/$repo/actions/workflows/auto-regen-resolve.yml/dispatches" \
			-f ref="$branch" \
			-f "inputs[main]=$main_sha" \
			-f "inputs[head]=$head_sha" \
			-f "inputs[pr]=$number" 2>&1 >/dev/null)"; then
			log "#$number: dispatched the resolve run on $branch"
		elif [[ "$err" == *"HTTP 422"* ]]; then
			# GitHub answers 422 when the branch has no copy of the resolve
			# workflow, for example a branch made before that file reached main.
			log "#$number: dispatch refused with HTTP 422: the branch $branch may have no .github/workflows/auto-regen-resolve.yml; merge main into the branch; continue with the next candidate"
		else
			log "#$number: dispatch failed; continue with the next candidate"
		fi
	done < <(jq -r '.[] | [.number, .branch, .head] | @tsv' "$prs_json")
}

# cmd_match_run BRANCH HEAD_SHA OUT_FILE: find the one open pull request from
# BRANCH at HEAD_SHA to main. Write number, branch and head to OUT_FILE. On a
# mismatch, log one line and write nothing.
cmd_match_run() {
	local branch="$1" head_sha="$2" out="$3" repo="${GITHUB_REPOSITORY:?}"
	local owner="${repo%%/*}" prs count number state draft head_repo actual_head actual_branch base
	[[ "$branch" =~ ^[A-Za-z0-9._/-]{1,200}$ ]] || { log "the branch is not valid for this workflow: $branch"; return 0; }
	case "$branch" in
	main | gh-readonly-queue/*)
		log "the branch is protected from this workflow: $branch"
		return 0
		;;
	esac
	case "$branch" in
	*..*)
		log "the branch has a dot dot: $branch"
		return 0
		;;
	esac
	[[ "$head_sha" =~ ^[0-9a-f]{40}$ ]] || { log "the head is not a full commit SHA: $head_sha"; return 0; }
	prs="$(gh api --paginate "repos/$repo/pulls?state=open&base=main&head=$owner:$branch" \
		--jq '.[] | [.number, .state, (.draft | tostring), .head.repo.full_name, .head.sha, .head.ref, .base.ref] | @tsv')"
	[ -n "$prs" ] || { log "no open pull request from $branch to main"; return 0; }
	count="$(printf '%s\n' "$prs" | wc -l)"
	[ "$count" -eq 1 ] || { log "$count open pull requests from $branch to main; expected one"; return 0; }
	IFS=$'\t' read -r number state draft head_repo actual_head actual_branch base <<<"$prs"
	if [ "$state" != open ] || [ "$draft" != false ] || [ "$head_repo" != "$repo" ] ||
		[ "$actual_head" != "$head_sha" ] || [ "$actual_branch" != "$branch" ] || [ "$base" != main ]; then
		log "the pull request from $branch does not match the run"
		return 0
	fi
	[[ "$number" =~ ^[0-9]{1,7}$ ]] || { log "the pull request number is not valid: $number"; return 0; }
	{
		printf 'number=%s\n' "$number"
		printf 'branch=%s\n' "$branch"
		printf 'head=%s\n' "$head_sha"
	} >"$out"
	log "#$number: matched the current head of $branch"
}

main() {
	local sub="${1:-}"
	shift || true
	case "$sub" in
	find) [ "$#" -eq 1 ] || die "usage: auto-regen.sh find OUT_FILE"; cmd_find "$@" ;;
	dispatch) [ "$#" -eq 2 ] || die "usage: auto-regen.sh dispatch PRS_JSON MAIN_SHA"; cmd_dispatch "$@" ;;
	resolve) [ "$#" -eq 2 ] || die "usage: auto-regen.sh resolve MAIN_SHA OUT_DIR"; cmd_resolve "$@" ;;
	match-run) [ "$#" -eq 3 ] || die "usage: auto-regen.sh match-run BRANCH HEAD_SHA OUT_FILE"; cmd_match_run "$@" ;;
	apply) [ "$#" -eq 3 ] || die "usage: auto-regen.sh apply MAIN_SHA HEAD_SHA PATCH"; cmd_apply "$@" ;;
	other-conflicts) [ "$#" -eq 2 ] || die "usage: auto-regen.sh other-conflicts MAIN_SHA HEAD_SHA"; cmd_other_conflicts "$@" ;;
	check-patch) [ "$#" -eq 1 ] || die "usage: auto-regen.sh check-patch PATCH"; cmd_check_patch "$@" ;;
	paths) cmd_paths ;;
	*) die "usage: auto-regen.sh find|dispatch|resolve|match-run|apply|other-conflicts|check-patch|paths ..." ;;
	esac
}

main "$@"
