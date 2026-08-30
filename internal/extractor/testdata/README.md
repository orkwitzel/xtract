# Test fixtures

`test.part01.rar` / `test.part02.rar` are a two-volume rar set. Nothing in Go
can *write* a rar archive, so unlike every other fixture in this package these
cannot be generated at test time and have to be checked in.

They are copied verbatim from the testdata of `github.com/mholt/archives`
(MIT licensed), the library this package reads rar with. Together they hold a
single `test.txt` of 8895 bytes.
