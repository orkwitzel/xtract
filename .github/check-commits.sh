#!/usr/bin/env bash
#
# Checks commit subjects against the format in CONTRIBUTING.md. CI runs it on
# every pull request; run it yourself before you push and you will never be
# told about it by a red build:
#
#   .github/check-commits.sh                  # every commit not yet on main
#   .github/check-commits.sh v1.2.0..HEAD     # an explicit range
#   .github/check-commits.sh -m "feat: x"     # one message, as CI checks the PR title
#
# It reads subjects only. The BREAKING CHANGE: footer is free-form by design
# and is not something a check can get wrong for you.
set -euo pipefail

cd "$(dirname "$0")/.."

types='build|chore|ci|docs|feat|fix|perf|refactor|revert|style|test'
format="^($types)(\([^)]+\))?!?: .+"
limit=72

messages=()
case "${1:-}" in
-m | --message)
	messages=("${2?usage: check-commits.sh [range] | -m <message>}")
	;;
-h | --help)
	sed -n '2,10p' "$0" | cut -c3-
	exit 0
	;;
*)
	range=${1:-}
	if [ -z "$range" ]; then
		# A fresh clone in CI has origin/main; a local checkout may only have main.
		base=origin/main
		git rev-parse -q --verify "$base" >/dev/null || base=main
		range="$base..HEAD"
	fi
	# Without this a typo'd range makes git log fail inside the process
	# substitution, where set -e cannot see it, and the check passes with
	# nothing checked.
	if ! git rev-list --count "$range" >/dev/null 2>&1; then
		echo "check-commits.sh: not a range this repository knows about: $range" >&2
		exit 2
	fi

	# Merge commits are GitHub's words, not yours, and are never checked.
	# A read loop rather than mapfile, so the bash macOS ships still runs this.
	while IFS= read -r line; do
		messages+=("$line")
	done < <(git log --no-merges --format=%s "$range")
	;;
esac

if [ "${#messages[@]}" -eq 0 ]; then
	echo "No commits to check."
	exit 0
fi

bad=0
for subject in "${messages[@]}"; do
	problem=""
	if ! [[ $subject =~ $format ]]; then
		problem="does not match type(scope): description"
	elif [ "${#subject}" -gt "$limit" ]; then
		problem="is ${#subject} characters; keep the subject under $limit"
	elif [ "${subject: -1}" = "." ]; then
		problem="ends in a full stop"
	fi

	if [ -n "$problem" ]; then
		bad=$((bad + 1))
		printf '  %s\n      ^ %s\n' "$subject" "$problem" >&2
	fi
done

if [ "$bad" -gt 0 ]; then
	cat >&2 <<-MESSAGE

		$bad commit message(s) above do not follow the convention.

		    type(optional scope): what the change does, in the imperative

		    types:    $(echo "$types" | tr '|' ' ')
		    breaking: put a ! before the colon, or a BREAKING CHANGE: footer
		    version:  feat -> minor, fix/perf/revert -> patch, ! -> major

		The type is what decides the next version, so it is worth getting
		right. CONTRIBUTING.md has the whole story, and \`git commit --amend\`
		or \`git rebase -i\` will fix what is already written.
	MESSAGE
	exit 1
fi

echo "${#messages[@]} commit message(s) look right."
