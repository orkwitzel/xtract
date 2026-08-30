package extractor

import (
	"bytes"
	"compress/zlib"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The zip, tar and gzip paths are covered with fixtures built in-process. 7z
// and rar cannot be written from Go, so they are covered here: 7z by shelling
// out to the system tool when it exists, rar by a checked-in volume set.

func TestSevenZip(t *testing.T) {
	sevenZip, err := exec.LookPath("7z")
	if err != nil {
		t.Skip("7z not installed")
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	write(t, filepath.Join(src, "inner.txt"), []byte("seven zip payload"))

	archive := filepath.Join(dir, "bundle.7z")
	cmd := exec.Command(sevenZip, "a", "-bso0", "-bsp0", archive, filepath.Join(src, "inner.txt"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the 7z fixture: %v\n%s", err, out)
	}

	sum := run(t, Options{Workers: 4}, archive)
	if len(sum.Failures) != 0 {
		t.Fatalf("failures: %v", sum.Failures)
	}
	if got := readFile(t, filepath.Join(dir, "bundle", "inner.txt")); got != "seven zip payload" {
		t.Errorf("content = %q", got)
	}
}

func TestSevenZipNestedInsideAZip(t *testing.T) {
	sevenZip, err := exec.LookPath("7z")
	if err != nil {
		t.Skip("7z not installed")
	}

	dir := t.TempDir()
	staging := t.TempDir()
	write(t, filepath.Join(staging, "deep.txt"), []byte("buried"))
	inner := filepath.Join(staging, "inner.7z")
	if out, err := exec.Command(sevenZip, "a", "-bso0", "-bsp0", inner, filepath.Join(staging, "deep.txt")).CombinedOutput(); err != nil {
		t.Fatalf("building the 7z fixture: %v\n%s", err, out)
	}
	payload, err := os.ReadFile(inner)
	if err != nil {
		t.Fatal(err)
	}

	archive := write(t, filepath.Join(dir, "outer.zip"), zipBytes(t, ent{name: "inner.7z", data: payload}))
	run(t, Options{Workers: 4}, archive)

	if got := readFile(t, filepath.Join(dir, "outer", "inner", "deep.txt")); got != "buried" {
		t.Errorf("content = %q, want the 7z inside the zip to have been expanded", got)
	}
	if exists(filepath.Join(dir, "outer", "inner.7z")) {
		t.Error("the nested 7z should have been deleted")
	}
}

func TestMultiVolumeRar(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"test.part01.rar", "test.part02.rar"} {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Skipf("rar fixture missing: %v", err)
		}
		write(t, filepath.Join(dir, name), data)
	}

	sum := run(t, Options{Workers: 4}, filepath.Join(dir, "test.part01.rar"))
	if len(sum.Failures) != 0 {
		t.Fatalf("failures: %v", sum.Failures)
	}

	// The payload spans both volumes, so a short read means only the first
	// one was followed.
	out := filepath.Join(dir, "test.part01", "test.txt")
	info, err := os.Stat(out)
	if err != nil {
		t.Fatalf("stat %s: %v", out, err)
	}
	if info.Size() != 8895 {
		t.Errorf("extracted %d bytes, want 8895: the second volume was not followed", info.Size())
	}
}

func TestSplitRarInsideAnArchiveIsExtractedOnce(t *testing.T) {
	var parts []ent
	for _, name := range []string{"test.part01.rar", "test.part02.rar"} {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Skipf("rar fixture missing: %v", err)
		}
		parts = append(parts, ent{name: name, data: data})
	}

	dir := t.TempDir()
	archive := write(t, filepath.Join(dir, "volumes.zip"), zipBytes(t, parts...))
	sum := run(t, Options{Workers: 4}, archive)

	if len(sum.Failures) != 0 {
		t.Fatalf("failures: %v", sum.Failures)
	}
	// Only the first volume is an archive; the second is pulled in by the
	// decoder and must not be extracted, or deleted, on its own.
	if sum.Archives != 2 {
		t.Errorf("extracted %d archives, want 2 (the zip and the rar set)", sum.Archives)
	}
	if got := tree(t, filepath.Join(dir, "volumes")); len(got) != 2 {
		t.Errorf("tree = %q, want just the extracted directory and its file", got)
	}
}

func TestVolumeCleanupLeavesUnrelatedFilesAlone(t *testing.T) {
	dir := t.TempDir()
	// Names that share a prefix with the archive but are not its volumes.
	bystanders := []string{"foobar.r01", "foo.txt", "foobar.rar"}
	for _, name := range bystanders {
		write(t, filepath.Join(dir, name), []byte("keep me"))
	}
	volumes := []string{"foo.r00", "foo.r01"}
	for _, name := range volumes {
		write(t, filepath.Join(dir, name), []byte("volume"))
	}
	write(t, filepath.Join(dir, "foo.rar"), []byte("archive"))

	removeVolumes(filepath.Join(dir, "foo.rar"))

	for _, name := range bystanders {
		if !exists(filepath.Join(dir, name)) {
			t.Errorf("%s was deleted, but it is not a volume of foo.rar", name)
		}
	}
	for _, name := range volumes {
		if exists(filepath.Join(dir, name)) {
			t.Errorf("%s should have been cleaned up with foo.rar", name)
		}
	}
}

// Zlib's whole signature is two bytes out of a table of 32, so random binary
// data matches it often enough to matter: about one file in 66,000 survives
// identification and gets "extracted" into garbage, failing the run. It is
// believed only when the name agrees.
func TestZlibNeedsItsExtension(t *testing.T) {
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write([]byte(strings.Repeat("compress me ", 100))); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	head := buf.Bytes()
	if len(head) > headSize {
		head = head[:headSize]
	}

	if fm := detect(context.Background(), "blob.bin", head); fm.isArchive {
		t.Errorf("a nameless zlib stream was taken for a %s archive; random data hits this", fm.name)
	}
	if fm := detect(context.Background(), "blob.zz", head); !fm.isArchive {
		t.Error("blob.zz should still be recognised as zlib")
	}
}
