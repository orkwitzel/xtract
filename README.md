# xtract

Extract an archive, and every archive inside it, in one pass.

A `.zip` holding a `.tar.gz` holding a `.rar` holding a `.7z` comes out fully
unpacked. Each nested archive is expanded into a directory named after it and
then deleted, so what you are left with is files. The archives you name on the
command line are never deleted.

```
$ xtract bundle.zip
151 archives · 3150 files · 79.3 MiB · 133ms
```

## Install

Grab a binary from the [latest release](https://github.com/orkwitzel/xtract/releases/latest)
— Linux, macOS and Windows, on x86-64 and arm64, one static file with no
runtime to install:

```
tar -xzf xtract_1.0.0_linux_amd64.tar.gz xtract && sudo mv xtract /usr/local/bin/
```

Every release ships `checksums.txt` alongside the archives. Or build it
yourself:

```
go install github.com/orkwitzel/xtract@latest      # or: go build -o xtract .
```

## Usage

```
xtract [flags] <archive>...

  -o, --out DIR       where to extract to (default: a directory beside each archive)
  -j, --jobs N        archives to work on at once (default: one per CPU)
      --keep          keep nested archives instead of deleting them once extracted
      --all           also expand .docx, .jar, .apk and other zip-based file formats
      --max-depth N   how deep to recurse (default 10)
      --max-files N   stop after this many extracted files (0 for no limit)
      --max-size N    stop after this many extracted bytes (0 for no limit)
      --no-tui        plain output instead of the progress display
  -v, --verbose       print progress as plain lines
```

Exit codes: `0` clean, `1` one or more archives failed, `2` misuse, `130` interrupted.

## Try it

```
./testdata/make.sh                 # builds testdata/sample.zip
./xtract -v testdata/sample.zip
```

The sample is five levels deep and mixes zip, tar.gz, 7z, tar.zst, tar.bz2 and
tar.xz, with a zip named `.txt`, an archive with no extension, a lone `.gz` and
a `.docx` thrown in to show the naming and detection rules. It needs `zip tar
gzip bzip2 xz zstd 7z` on PATH to build.

For something bigger, `testdata/random.sh` builds a random tree as deep and as
wide as you ask for, picking a different format for every archive in it:

```
./testdata/random.sh -d 5              # five layers
./testdata/random.sh -d 6 -b 3 --weird # 364 archives, misleading names
./testdata/random.sh --seed 1234       # same shape and names as an earlier run
```

## What it handles

zip, tar, rar (including multi-volume sets), 7z, and the gz, bz2, xz, zstd,
lz4, brotli, lzip, snappy and zlib wrappers around them — in any combination,
at any depth.

Archives are identified by their **contents**, not their names. A nested file
with a misleading extension, or no extension at all, is still found and
extracted. A lone compressed file like `access.log.gz` becomes `access.log`
right where it was found, rather than getting a pointless directory of its own.

When the name an archive wants is already taken, the next one is numbered the
way a browser numbers downloads — `bundle`, then `bundle (1)`, `bundle (2)` —
so re-running over the same archive never overwrites an earlier result. An
explicit `--out` is used exactly as given and never renumbered.

Formats that merely happen to be zips — `.docx`, `.xlsx`, `.jar`, `.apk`,
`.epub`, `.whl` and friends — are treated as ordinary files, because almost
nobody wants a Word document exploded into XML. `--all` turns that off.

## Speed

Work is parallel at two levels. Nested archives are expanded concurrently, and
a single large zip is split across goroutines as well, so one big archive uses
the whole machine rather than one core. On a 12-core laptop:

| workload | `-j1` | `-j12` |
|---|---|---|
| 150 nested zips, 3150 files | 194 ms | 42 ms |
| one flat zip, 3000 entries | 385 ms | 60 ms |

## Safety

Extracting an untrusted archive is a way to get files written where you did not
ask for them, so:

- Every write goes through [`os.Root`](https://pkg.go.dev/os#Root), which
  refuses to leave the destination directory at the syscall level rather than
  by inspecting strings.
- Entries whose names climb out (`../../etc/passwd`) are rejected, the rest of
  the archive is still extracted, and the run exits non-zero.
- Symlinks pointing outside the destination are refused rather than written.
- Recursion is bounded by `--max-depth`, so an archive that contains itself
  terminates. `--max-files` and `--max-size` bound the output; bytes are
  charged as they stream, so a zip bomb is stopped mid-copy rather than after
  it has filled the disk.
- An archive is deleted only after it has been extracted without a single
  rejected entry.

## How it is put together

```
cmd/            flags, wiring, exit codes
internal/
  extractor/    all the work: scheduling, format detection, writing files
  reporter/     two counters, written by the extractor, read by the UI
  ui/           the progress display, and the only thing that writes to stdout
```

`extractor` exposes `New` and `Run` and nothing else. `reporter` holds how many
archives are known about and how many are finished — that is the entire
conversation between the work and the display, which is why neither needs to
know the other exists.

Inside `extractor`, `walk.go` schedules, `archive.go` expands one archive,
`detect.go` identifies formats, `write.go` writes entries safely, and `zip.go`
holds the parallel zip path. The scheduler uses a mutex and a condition
variable over an unbounded queue rather than a channel: workers enqueue the
archives they discover, and a bounded channel would deadlock the moment a
worker blocked on a queue only workers can drain.

## Tests

```
go test -race ./...
```

Fixtures for zip, tar and gzip are built in-process, so there are no binary
blobs to trust. 7z fixtures are generated with the system `7z` when it is
installed and skipped when it is not. Nothing in Go can write a rar, so
`internal/extractor/testdata` holds a checked-in two-volume set.

## Contributing

`AGENTS.md` is how the code is put together and what not to break.
`CONTRIBUTING.md` is the commit format — [Conventional
Commits](https://www.conventionalcommits.org), where the type of a commit is
what picks the next version — and how a merge to `main` becomes a tagged
release with binaries for all six platforms.

## License

[MIT](LICENSE) © Or Kwitzel.

Dependencies are MIT, BSD and Apache-2.0, plus `hashicorp/golang-lru` under
MPL-2.0, which is file-level copyleft and unmodified here. The rar fixtures in
`internal/extractor/testdata` come from [mholt/archives](https://github.com/mholt/archives)
under MIT; provenance is recorded alongside them.
