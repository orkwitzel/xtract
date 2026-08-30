package extractor

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/orkwitzel/xtract/internal/reporter"
)

// nested builds root.zip ⊃ a.tar.gz ⊃ b.zip ⊃ c.txt and returns its path.
func nested(t *testing.T) (dir, archive string) {
	t.Helper()
	dir = t.TempDir()
	inner := zipBytes(t, file("c.txt", "deepest"))
	middle := tarGzBytes(t, ent{name: "b.zip", data: inner})
	root := zipBytes(t, file("notes.txt", "hello"), ent{name: "a.tar.gz", data: middle})
	return dir, write(t, filepath.Join(dir, "root.zip"), root)
}

func run(t *testing.T, opts Options, archives ...string) Summary {
	t.Helper()
	reporter.Reset()
	sum, err := New(opts).Run(context.Background(), archives)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return sum
}

func TestRecursiveExtraction(t *testing.T) {
	dir, archive := nested(t)
	sum := run(t, Options{Workers: 4}, archive)

	got := tree(t, filepath.Join(dir, "root"))
	want := []string{
		"a/",
		"a/b/",
		"a/b/c.txt",
		"notes.txt",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("output tree:\n got %q\nwant %q", got, want)
	}
	if c := readFile(t, filepath.Join(dir, "root", "a", "b", "c.txt")); c != "deepest" {
		t.Errorf("deepest file = %q, want %q", c, "deepest")
	}
	if !exists(archive) {
		t.Error("the archive named on the command line was deleted; it must be kept")
	}
	if sum.Archives != 3 {
		t.Errorf("Archives = %d, want 3", sum.Archives)
	}
	if len(sum.Failures) != 0 {
		t.Errorf("unexpected failures: %v", sum.Failures)
	}
}

func TestNestedArchivesAreDeleted(t *testing.T) {
	dir, archive := nested(t)
	run(t, Options{Workers: 4}, archive)

	for _, p := range []string{"root/a.tar.gz", "root/a/b.zip"} {
		if exists(filepath.Join(dir, p)) {
			t.Errorf("%s should have been deleted after extraction", p)
		}
	}
}

func TestKeepPreservesNestedArchives(t *testing.T) {
	dir, archive := nested(t)
	run(t, Options{Workers: 4, Keep: true}, archive)

	for _, p := range []string{"root/a.tar.gz", "root/a/b.zip"} {
		if !exists(filepath.Join(dir, p)) {
			t.Errorf("%s should have been kept with Keep set", p)
		}
	}
}

func TestMaxDepthStops(t *testing.T) {
	dir, archive := nested(t)
	run(t, Options{Workers: 4, MaxDepth: 1}, archive)

	if !exists(filepath.Join(dir, "root", "a", "b.zip")) {
		t.Error("b.zip should still be there: depth 2 is past the limit")
	}
	if exists(filepath.Join(dir, "root", "a", "b")) {
		t.Error("b.zip was extracted despite MaxDepth=1")
	}
}

func TestSingleArchiveWithOut(t *testing.T) {
	dir, archive := nested(t)
	out := filepath.Join(dir, "chosen")
	run(t, Options{Workers: 2, Out: out}, archive)

	if !exists(filepath.Join(out, "notes.txt")) {
		t.Errorf("expected extraction straight into -o dir, got %q", tree(t, out))
	}
}

func TestConcurrencyDoesNotChangeOutput(t *testing.T) {
	var trees [][]string
	for _, workers := range []int{1, 8} {
		dir, archive := nested(t)
		run(t, Options{Workers: workers}, archive)
		trees = append(trees, tree(t, filepath.Join(dir, "root")))
	}
	if !reflect.DeepEqual(trees[0], trees[1]) {
		t.Errorf("output depends on worker count:\n j1 %q\n j8 %q", trees[0], trees[1])
	}
}

func TestBareCompressedFileUnwraps(t *testing.T) {
	dir := t.TempDir()
	// payload.gz holds a zip, so it must unwrap twice: gz -> zip -> file.
	inner := zipBytes(t, file("prize.txt", "found me"))
	archive := write(t, filepath.Join(dir, "payload.zip.gz"), gzipBytes(t, inner))

	run(t, Options{Workers: 2}, archive)

	want := filepath.Join(dir, "payload", "prize.txt")
	if !exists(want) {
		t.Errorf("expected %s, got tree %q", want, tree(t, dir))
	}
}

func TestContainersAreLeftAlone(t *testing.T) {
	dir := t.TempDir()
	doc := zipBytes(t, file("word/document.xml", "<xml/>"))
	archive := write(t, filepath.Join(dir, "bundle.zip"), zipBytes(t, ent{name: "report.docx", data: doc}))

	run(t, Options{Workers: 2}, archive)

	if !exists(filepath.Join(dir, "bundle", "report.docx")) {
		t.Error("report.docx should have been left as a file")
	}
	if exists(filepath.Join(dir, "bundle", "report")) {
		t.Error("report.docx was exploded without --all")
	}
}

func TestAllExpandsContainers(t *testing.T) {
	dir := t.TempDir()
	doc := zipBytes(t, file("word/document.xml", "<xml/>"))
	archive := write(t, filepath.Join(dir, "bundle.zip"), zipBytes(t, ent{name: "report.docx", data: doc}))

	run(t, Options{Workers: 2, All: true}, archive)

	if !exists(filepath.Join(dir, "bundle", "report", "word", "document.xml")) {
		t.Errorf("--all should have expanded the .docx, got %q", tree(t, dir))
	}
}

func TestPathTraversalIsRejected(t *testing.T) {
	dir := t.TempDir()
	archive := write(t, filepath.Join(dir, "evil.zip"), zipBytes(t,
		file("../../escaped.txt", "pwned"),
		file("fine.txt", "ok"),
	))

	reporter.Reset()
	sum, err := New(Options{Workers: 2}).Run(context.Background(), []string{archive})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if exists(filepath.Join(filepath.Dir(dir), "escaped.txt")) {
		t.Fatal("an entry escaped the destination directory")
	}
	if !exists(filepath.Join(dir, "evil", "fine.txt")) {
		t.Error("the safe entry should still have been extracted")
	}
	if len(sum.Failures) != 1 || !strings.Contains(sum.Failures[0].Err.Error(), "unsafe") {
		t.Errorf("expected one unsafe-entry failure, got %v", sum.Failures)
	}
}

func TestSymlinkCannotEscape(t *testing.T) {
	dir := t.TempDir()
	archive := write(t, filepath.Join(dir, "links.zip"), zipBytes(t,
		ent{name: "sneaky", link: "../../../../etc/passwd"},
		file("real.txt", "content"),
	))

	reporter.Reset()
	if _, err := New(Options{Workers: 2}).Run(context.Background(), []string{archive}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Whether the link was written or refused, reading through it must not
	// reach outside the destination.
	if data, err := readThroughLink(filepath.Join(dir, "links", "sneaky")); err == nil {
		t.Errorf("symlink resolved outside the destination: %q", data)
	}
	if !exists(filepath.Join(dir, "links", "real.txt")) {
		t.Error("the ordinary entry should still have been extracted")
	}
}

func TestFileLimitStopsTheRun(t *testing.T) {
	dir := t.TempDir()
	archive := write(t, filepath.Join(dir, "many.zip"), zipBytes(t,
		file("a", "1"), file("b", "2"), file("c", "3"), file("d", "4"),
	))

	reporter.Reset()
	sum, err := New(Options{Workers: 1, MaxFiles: 2}).Run(context.Background(), []string{archive})
	if err == nil {
		t.Fatal("expected the run to stop once the file limit was hit")
	}
	if len(sum.Failures) == 0 {
		t.Error("the limit should be reported as a failure")
	}
}

func TestCancelledContextStops(t *testing.T) {
	_, archive := nested(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	reporter.Reset()
	if _, err := New(Options{Workers: 4}).Run(ctx, []string{archive}); err == nil {
		t.Error("expected a context error")
	}
}

func TestMissingArchiveIsReported(t *testing.T) {
	reporter.Reset()
	sum, err := New(Options{}).Run(context.Background(), []string{filepath.Join(t.TempDir(), "nope.zip")})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(sum.Failures) != 1 {
		t.Errorf("expected one failure, got %v", sum.Failures)
	}
}

func TestReporterCountsEveryArchive(t *testing.T) {
	_, archive := nested(t)
	sum := run(t, Options{Workers: 4}, archive)

	stats := reporter.Get()
	if stats.Total != 3 || stats.Extracted != 3 {
		t.Errorf("reporter = %+v, want 3 discovered and 3 finished", stats)
	}
	if int64(stats.Extracted) != sum.Archives {
		t.Errorf("reporter counted %d archives, summary counted %d", stats.Extracted, sum.Archives)
	}
	if stats.Ratio() != 1 {
		t.Errorf("Ratio() = %v at the end of a clean run, want 1", stats.Ratio())
	}
}

func TestReporterCountsFailuresAsFinished(t *testing.T) {
	dir := t.TempDir()
	broken := write(t, filepath.Join(dir, "broken.zip"), []byte("PK\x03\x04 not really a zip"))
	reporter.Reset()

	if _, err := New(Options{Workers: 2}).Run(context.Background(), []string{broken}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats := reporter.Get(); stats.Extracted != stats.Total {
		t.Errorf("reporter = %+v; a failed archive must still count as finished, or progress never completes", stats)
	}
}

func TestSingleCompressedFileLandsInPlace(t *testing.T) {
	dir := t.TempDir()
	// A lone compressed file is not a container, so wrapping it in a
	// directory named after it would only add a level nobody asked for.
	archive := write(t, filepath.Join(dir, "notes.txt.gz"), gzipBytes(t, []byte("plain text")))

	run(t, Options{Workers: 2}, archive)

	got := tree(t, dir)
	want := []string{"notes.txt", "notes.txt.gz"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tree = %q, want %q", got, want)
	}
	if c := readFile(t, filepath.Join(dir, "notes.txt")); c != "plain text" {
		t.Errorf("content = %q", c)
	}
}

func TestDecompressionDoesNotClobberNeighbours(t *testing.T) {
	dir := t.TempDir()
	inner := tarGzBytes(t,
		ent{name: "notes.txt", data: []byte("original")},
		ent{name: "notes.txt.gz", data: gzipBytes(t, []byte("unwrapped"))},
	)
	archive := write(t, filepath.Join(dir, "pair.tar.gz"), inner)

	run(t, Options{Workers: 2}, archive)

	if c := readFile(t, filepath.Join(dir, "pair", "notes.txt")); c != "original" {
		t.Errorf("the existing file was overwritten: %q", c)
	}
	if c := readFile(t, filepath.Join(dir, "pair", "notes (1).txt")); c != "unwrapped" {
		t.Errorf("decompressed output = %q, want it beside the original", c)
	}
}

func TestReRunDoesNotMergeIntoTheSameDirectory(t *testing.T) {
	dir, archive := nested(t)
	run(t, Options{Workers: 2}, archive)
	run(t, Options{Workers: 2}, archive)

	if !exists(filepath.Join(dir, "root (1)", "notes.txt")) {
		t.Errorf("a second run should get its own directory, got %q", tree(t, dir))
	}
}

// "tar -cf x.tar ." leads with an entry called "./", which names the
// destination directory rather than anything inside it. Treating that as an
// attempt to escape used to fail every such tarball — and, because an archive
// with rejected entries is kept, leave it undeleted as well.
func TestTarRootEntryIsNotAnEscape(t *testing.T) {
	dir := t.TempDir()
	archive := write(t, filepath.Join(dir, "bundle.tar.gz"),
		tarGzBytes(t, dirEnt("./"), file("./notes.txt", "hi")))

	sum := run(t, Options{Workers: 2}, archive)

	if len(sum.Failures) != 0 {
		t.Errorf("unexpected failures: %v", sum.Failures)
	}
	if got := tree(t, filepath.Join(dir, "bundle")); !reflect.DeepEqual(got, []string{"notes.txt"}) {
		t.Errorf("output tree = %q, want [notes.txt]", got)
	}
}
