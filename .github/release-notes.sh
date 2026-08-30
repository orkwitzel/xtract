#!/usr/bin/env bash
#
# Prints the markdown that goes in the release body: everything since the last
# tag, grouped by what it does to a user.
#
#   .github/release-notes.sh          # notes for what is on HEAD now
#   .github/release-notes.sh v1.3.0   # same, with the compare link pointing at the new tag
#
# Only feat, fix, perf and breaking changes are listed. Refactors, tests and
# chores are real work but they are not news, and the compare link at the
# bottom has them for anyone who wants the whole log.
set -euo pipefail

cd "$(dirname "$0")/.."

subject_format='^([a-z]+)(\(([^)]*)\))?(!)?: (.*)$'
breaking_footer=$'(^|\n)BREAKING[ -]CHANGE:[ ]*'

new=${1:-HEAD}
last=$(git describe --tags --abbrev=0 --match 'v[0-9]*' 2>/dev/null || true)
range="${last:+$last..}HEAD"

breaking="" features="" fixes="" perf=""

while IFS=$'\x1f' read -r -d '' sha subject body; do
	[[ $subject =~ $subject_format ]] || continue
	type=${BASH_REMATCH[1]}
	scope=${BASH_REMATCH[3]}
	bang=${BASH_REMATCH[4]}
	description=${BASH_REMATCH[5]}

	entry="- ${scope:+**$scope:** }$description ($sha)"$'\n'

	if [ -n "$bang" ] || [[ $body =~ $breaking_footer ]]; then
		breaking+=$entry
		# The footer is where the author explains what to do about it, so it
		# is worth more than the subject line and gets carried over verbatim.
		if [[ $body =~ $breaking_footer ]]; then
			note=${body#*"${BASH_REMATCH[0]}"}
			note=${note%%$'\n\n'*}
			breaking+="  ${note//$'\n'/ }"$'\n'
		fi
		continue
	fi

	case $type in
	feat) features+=$entry ;;
	fix | revert) fixes+=$entry ;;
	perf) perf+=$entry ;;
	esac
done < <(git log --no-merges -z --format='%h%x1f%s%x1f%b' "$range")

section() {
	if [ -n "$2" ]; then
		printf '### %s\n\n%s\n' "$1" "$2"
	fi
}

section "Breaking changes" "$breaking"
section "Features" "$features"
section "Fixes" "$fixes"
section "Performance" "$perf"

if [ -z "$breaking$features$fixes$perf" ]; then
	# The first release is cut by hand out of whatever history exists, which
	# predates the convention and so has nothing to summarise. That is not the
	# same thing as a release that changed nothing.
	if [ -z "$last" ]; then
		printf 'First release.\n\n'
	else
		printf 'Maintenance only — no user-visible changes.\n\n'
	fi
fi

# GITHUB_REPOSITORY is set in Actions; the remote is the fallback for a run
# from a laptop.
repo=${GITHUB_REPOSITORY:-}
if [ -z "$repo" ]; then
	url=$(git remote get-url origin 2>/dev/null || true)
	url=${url%.git}
	repo=${url#*github.com[:/]}
	if [ "$repo" = "$url" ]; then
		repo=""   # not a github remote, so there is nothing to link to
	fi
fi

if [ -n "$repo" ]; then
	base=${GITHUB_SERVER_URL:-https://github.com}
	if [ -n "$last" ]; then
		printf '**Full changelog**: %s/%s/compare/%s...%s\n' "$base" "$repo" "$last" "$new"
	else
		printf '**Full changelog**: %s/%s/commits/%s\n' "$base" "$repo" "$new"
	fi
fi
