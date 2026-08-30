# Contributing

`AGENTS.md` is about how the code works and what not to break. This file is
about how a change gets from a branch to a release — the commit format, and
what CI does with it.

It applies to humans and agents equally. An agent working in this repository
should read it before writing a commit message, because the message is what
decides the version.

## Before you push

```bash
gofmt -l .                    # silence is the pass
go vet ./... && go test -race ./...
.github/check-commits.sh      # your commit subjects, checked the way CI checks them
.github/next-version.sh       # the version your branch would release
```

CI runs exactly these, on Linux, macOS and Windows, and cross-compiles all six
release targets. Nothing merges until they pass.

## Commit messages

Every commit follows [Conventional Commits](https://www.conventionalcommits.org):

```
type(optional scope): what the change does, in the imperative

Optional body. Wrap it at 72 columns and use it to say why, not what —
the diff already says what.

BREAKING CHANGE: what a user has to do differently now.
Co-Authored-By: someone <someone@example.com>
```

Rules a machine checks, so there is no arguing with it:

- the type is one of the eleven below, lowercase;
- a scope is optional and goes in parentheses — use the package or file the
  change lives in: `extractor`, `detect`, `walk`, `zip`, `ui`, `cmd`, `ci`;
- there is a space after the colon and a description after that;
- the subject is at most 72 characters and does not end in a full stop.

Examples from the shape this repository is in:

```
feat(detect): identify lzip archives by content
fix(walk): stop deleting an archive that had a rejected entry
perf(zip): split entry extraction across goroutines
docs: explain why submit must never block
refactor(naming): fold reserveDir into baseName
feat(cmd)!: rename --no-tui to --plain
```

### The types, and what each one releases

| Type | Meaning | Version |
|---|---|---|
| `feat` | new behaviour a user can see | **minor** — 1.4.2 → 1.5.0 |
| `fix` | a bug fixed | **patch** — 1.4.2 → 1.4.3 |
| `perf` | same behaviour, faster | **patch** |
| `revert` | undoing an earlier commit | **patch** |
| `docs` | documentation only | none |
| `test` | tests and fixtures | none |
| `refactor` | shape of the code, not its behaviour | none |
| `style` | formatting, no code change | none |
| `build` | go.mod, build tooling | none |
| `ci` | the files in `.github/` | none |
| `chore` | anything else with no user-visible effect | none |

A push whose commits are all in the "none" rows releases nothing, and that is
the intended outcome, not a failure.

### Breaking changes

Put a `!` before the colon, and explain the migration in a `BREAKING CHANGE:`
footer:

```
feat(cmd)!: rename --no-tui to --plain

BREAKING CHANGE: --no-tui is gone. Use --plain, which does the same thing
under a name that says what it does.
```

Either the `!` or the footer is enough to make it breaking; use both, because
the `!` is what a reader scanning `git log` sees and the footer is what tells
them what to do. The footer text is copied into the release notes verbatim.

**Below v1.0.0 a breaking change moves the minor, not the major** — nothing is
promised until the interface is meant to hold. Tag `v1.0.0` by hand on the day
it is.

### Reverts

```
revert: feat(detect): identify lzip archives by content

Refs: a1b2c3d
```

### Trailers

`Co-Authored-By:`, `Refs:`, `Signed-off-by:` and friends go at the bottom,
after a blank line. They are ignored by the version logic and by the release
notes, so an agent adding its co-author trailer changes nothing.

## Pull requests

A squash merge uses **the pull request title** as the commit message on `main`
and throws the individual subjects away; a merge commit keeps them. Either is
fine here — the `commit messages` job checks both, so `main` ends up with
messages the release can read whichever you pick. Give the title the same care
as the commits: under a squash it is the one that decides the version.

That job also prints what merging would release, so you can see the version
before it exists.

To take the choice away and squash everything:

```bash
gh api -X PATCH repos/orkwitzel/xtract \
  -F allow_merge_commit=false -F allow_rebase_merge=false
```

### Required checks

Merging should be blocked until these pass. Once, from a machine with `gh`:

```bash
gh api -X PUT repos/orkwitzel/xtract/branches/main/protection --input - <<'JSON'
{
  "required_status_checks": {
    "strict": true,
    "contexts": [
      "test (ubuntu-latest)",
      "test (macos-latest)",
      "test (windows-latest)",
      "format and modules",
      "build every release target",
      "commit messages"
    ]
  },
  "enforce_admins": false,
  "required_pull_request_reviews": null,
  "restrictions": null
}
JSON
```

Without this the workflows still run and still go red — they just do not stop
anyone. The release workflow re-runs the test gate itself for that reason.

## How a release happens

Merging to `main` starts `.github/workflows/release.yml`, which:

1. asks `.github/next-version.sh` what the commits since the last tag add up
   to, and stops right there if the answer is nothing;
2. runs `go vet` and `go test -race` again — CI runs on the same push, but
   nothing makes the release wait for it, and a tag is permanent;
3. cross-compiles six binaries with the version baked in via `-ldflags -X`,
   packages each with the README and the licence, and writes `checksums.txt`;

   | | amd64 | arm64 |
   |---|---|---|
   | Linux | `xtract_1.4.0_linux_amd64.tar.gz` | `xtract_1.4.0_linux_arm64.tar.gz` |
   | macOS | `xtract_1.4.0_darwin_amd64.tar.gz` | `xtract_1.4.0_darwin_arm64.tar.gz` |
   | Windows | `xtract_1.4.0_windows_amd64.zip` | `xtract_1.4.0_windows_arm64.zip` |

4. runs `xtract --version` from the built artefact, because a typo in the
   `-X` path is otherwise silent and ships six binaries that call themselves
   `dev`;
5. tags the commit and publishes the release with notes grouped into breaking
   changes, features, fixes and performance — `.github/release-notes.sh`
   writes them, and you can run it yourself to see them first.

There is no `CHANGELOG.md`: the release page is the changelog, and a generated
file in the tree would only be a second copy to disagree with it.

### Releasing by hand

**Actions → Release → Run workflow**, optionally with a version, when you want
to force one out — a `v1.0.0` promotion, or a release the commit types missed.
The version must still be `vMAJOR.MINOR.PATCH` and must not already exist.

### When a release goes wrong

The tag is created last, so a failure before it leaves nothing to clean up:
fix the problem and push again. If the tag exists but the release is wrong,
delete both and re-run:

```bash
gh release delete v1.4.0 --yes --cleanup-tag
```

## The moving parts

The shell is kept out of the workflow YAML so you can run it on a laptop and
get the same answer CI does.

| | |
|---|---|
| `.github/workflows/ci.yml` | tests on Linux, macOS and Windows; gofmt; `go mod tidy`; all six cross-compiles |
| `.github/workflows/commits.yml` | the commit and pull request title check |
| `.github/workflows/release.yml` | version, gate, six binaries, tagged release |
| `.github/check-commits.sh` | the format check |
| `.github/next-version.sh` | commits → the next version |
| `.github/release-notes.sh` | commits → the release body |
