#!/usr/bin/env bash
#
# Prints the version the commits since the last tag add up to — v1.4.0, say —
# and prints nothing at all when none of them is releasable. The release
# workflow reads "nothing" as "no release today", which is how a branch of
# docs and refactors lands on main without cutting a version nobody wanted.
#
#   .github/next-version.sh     # what would the next tag be?
#
# The rules it implements are written down in CONTRIBUTING.md:
#
#   feat!:  or a BREAKING CHANGE: footer  -> major   (minor while below v1.0.0)
#   feat:                                 -> minor
#   fix:  perf:  revert:                  -> patch
#   anything else                         -> no release
#
# It needs the tags and the history behind them, which is why CI checks out
# with fetch-depth: 0.
set -euo pipefail

cd "$(dirname "$0")/.."

# Regexes live in variables because [[ =~ ]] wants the parentheses unquoted.
breaking_subject='^[a-z]+(\([^)]*\))?!:'
breaking_footer=$'(^|\n)BREAKING[ -]CHANGE:'
feature='^feat(\([^)]*\))?:'
patchable='^(fix|perf|revert)(\([^)]*\))?:'

last=$(git describe --tags --abbrev=0 --match 'v[0-9]*' 2>/dev/null || true)
range="${last:+$last..}HEAD"

IFS=. read -r major minor patch <<<"${last#v}"
major=${major:-0}
minor=${minor:-0}
patch=${patch:-0}
patch=${patch%%[!0-9]*}   # tolerate a v1.2.3-rc1 style tag in the history

# Merge commits are excluded: their subjects are written by GitHub rather than
# by whoever wrote the change, so they would never match the format. The
# records are split with -z rather than a %x00 in the format, because git
# still puts a newline after each entry and it lands at the front of the next
# subject.
bump=none
while IFS= read -r -d '' message; do
	subject=${message%%$'\n'*}

	if [[ $subject =~ $breaking_subject ]] || [[ $message =~ $breaking_footer ]]; then
		bump=major
		break   # nothing outranks this, so stop reading
	elif [[ $subject =~ $feature ]]; then
		bump=minor
	elif [[ $subject =~ $patchable ]]; then
		if [ "$bump" = none ]; then
			bump=patch
		fi
	fi
done < <(git log --no-merges -z --format='%B' "$range")

case $bump in
major)
	# Below v1.0.0 nothing has been promised yet, so a breaking change moves
	# the minor. Tag v1.0.0 by hand the day the interface is meant to hold.
	if [ "$major" -eq 0 ]; then
		minor=$((minor + 1))
		patch=0
	else
		major=$((major + 1))
		minor=0
		patch=0
	fi
	;;
minor)
	minor=$((minor + 1))
	patch=0
	;;
patch)
	patch=$((patch + 1))
	;;
*)
	exit 0
	;;
esac

echo "v$major.$minor.$patch"
