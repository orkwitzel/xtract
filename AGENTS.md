# AGENTS.md

Notes for anyone — human or agent — working on xtract.

xtract extracts an archive, then extracts every archive it finds inside, at any
depth and in any mix of formats. Nested archives are deleted once expanded; the
ones named on the command line are kept. See `README.md` for what it does from
the outside; this file is about how to change it without breaking it.

## Commands

```bash
go build -o xtract .            # binary
go vet ./... && go test -race ./...   # the gate — always with -race
go test ./internal/extractor -run TestRecursive -v

./testdata/make.sh              # (re)build the fixed sample archive
./testdata/random.sh -d 5       # a random tree, as deep and wide as you want
./xtract -v testdata/sample.zip # try it; -v gives plain output, no TUI
```

`testdata/make.sh` needs `zip tar gzip bzip2 xz zstd 7z` on PATH. The archive it
writes is five levels deep and mixes zip, tar.gz, 7z, tar.zst, tar.bz2 and
tar.xz, and includes the awkward cases on purpose: a zip named `.txt`, an
archive with no extension, a lone `.gz`, and a `.docx` that must be left alone.
A clean run is `exit=0`, no failures, and no archives left under
`testdata/sample/`.

`testdata/random.sh` is the other half of this: it builds a tree of a given
depth and breadth where every archive picks its own format, so it covers
combinations the fixed sample never will. `--seed` prints on every run and
replays the same shape and names, which is how you get a failing tree back.
Both scripts have already turned up real bugs — see below.

## Layout

```
main.go              os.Exit(cmd.Execute())
cmd/root.go          cobra flags -> extractor.Options, lifecycle, exit codes
internal/
  extractor/         all the work
    extractor.go     the entire public API: Options, Summary, Failure, New, Run
    walk.go          worker pool, recursion, deletion policy
    archive.go       expand one archive into one directory
    detect.go        identify formats from content; the container table
    write.go         write one entry safely (os.Root)
    zip.go           parallel per-entry extraction for zip
    naming.go        baseName, reserveDir
    result.go        result/budget bookkeeping
  reporter/          two counters, nothing else
  ui/                bubbletea TUI + plain fallback + the summary line
```

Dependencies point one way: `cmd` -> {`extractor`, `ui`} -> `reporter`.
`reporter` imports nothing internal, which is what keeps `extractor` and `ui`
unaware of each other.

## Invariants

These are the things that look harmless to change and are not.

**Only `internal/ui` writes to stdout.** bubbletea owns the terminal; a stray
`fmt.Println` from a worker corrupts the frame. Status goes to the reporter.

**Every write goes through `*os.Root`.** Not `filepath.Join` plus a prefix
check. `os.Root` refuses to leave the destination in the kernel, which is the
only version of this that is actually safe. Symlink *targets* get a lexical
check too (`safeLinkTarget`), because `os.Root` will happily create a link to
`/etc/passwd` even though it won't write through one.

**`extractor` exports `New`, `Run` and their types. Nothing else.** If a caller
seems to need more, the work belongs inside the package. This structure was
arrived at deliberately after flatter and deeper versions were both tried.

**`reporter` is two counters.** Total discovered and total finished. Adding
per-archive state, phases or task handles to it has been considered and
rejected; that is the whole conversation between the work and the display.

**`submit` must never block.** The queue is a slice under a mutex and a
condition variable, not a channel, because workers enqueue the archives they
discover. A bounded channel deadlocks the moment a worker blocks on a queue
only workers can drain. Quiescence is `len(queue) == 0 && active == 0`.

**Entry goroutines never wait on a job.** Workers wait on entry goroutines,
entry goroutines wait only on the entry semaphore, and that semaphore is
released solely by goroutines with nothing left to wait for. No cycle, no
deadlock. Keep it that way.

**Archives are always real files on disk.** Zip, rar and 7z extraction needs
`io.ReaderAt` + `io.Seeker`, which a decompression stream cannot give. That is
why jobs carry paths rather than readers, and why `foo.zip.gz` is unwrapped to
disk first and picked up by the recursion on the next pass.

## Things that have already bitten

- **`tar -cf x.tar .` writes `./` as its first entry.** It names the
  destination itself. `safeName` returns `("", true)` for it — an empty name
  with ok true means "no-op", not "escape". Rejecting it fails every such
  tarball and, because archives with rejected entries are kept, leaves them
  undeleted too.
- **Rar continuation volumes carry a full rar header**, so they look like
  archives in their own right. `isContinuationVolume` keeps them out of the
  queue; `removeVolumes` deletes them along with the volume that consumed them.
  That prefix match needs the separator — without it, extracting `foo.rar`
  sweeps up an unrelated `foobar.r01`.
- **`/dev/null` is a character device**, so TTY detection uses
  `term.IsTerminal`, never `os.ModeCharDevice`. `ui.Run` also falls back to
  plain output if bubbletea fails to start.
- **Detection ignores the filename** (`archives.Identify(ctx, "", …)`) because
  nested files routinely have no extension, and a misleading one is worse than
  none. The name is consulted only to spot container formats.
- **Container formats** (`.docx`, `.jar`, `.apk`, …) are zips that nobody wants
  exploded. They live in `containerExts` in `detect.go` and are skipped unless
  `--all`.
- **Zlib's signature is two bytes from a table of 32**, so one random buffer in
  1880 matches it and about one binary file in 66,000 survives identification
  and gets "extracted" into garbage. `weakMagic` in `detect.go` requires the
  name to agree for formats like that. Anything else added there should be
  measured, not guessed.

## Tests

White-box, in `package extractor`, so unexported code is testable without
widening the API. Zip/tar/gzip fixtures are built in-process by
`fixture_test.go` — no binary blobs. 7z fixtures are generated with the system
`7z` and skipped when it is missing. Nothing in Go can write a rar, so
`internal/extractor/testdata/` holds a checked-in two-volume set (MIT, from
mholt/archives; provenance is in the README there).

Run with `-race`. The queue, the parallel zip path and the reporter globals are
where races hide, and they will not show up otherwise.

## Style

Comments explain *why*, not what — the surrounding code is written that way and
new code should match. Prefer cutting a layer over adding one. Errors from a
single archive are recorded as `Failure` and the run continues; only context
cancellation or a budget limit stops everything.
