package extractor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestBaseName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"foo.zip", "foo"},
		{"foo.tar.gz", "foo"},
		{"foo.tgz", "foo"},
		{"foo.tar.zst", "foo"},
		{"/a/b/archive.7z", "archive"},
		{"report.docx", "report"},
		{"noext", "noext"},
		{"weird.name.rar", "weird.name"},
		{"UPPER.ZIP", "UPPER"},
		{".zip", "extracted"},
	}
	for _, c := range cases {
		if got := baseName(c.in); got != c.want {
			t.Errorf("baseName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDecompressedName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"foo.gz", "foo"},
		{"foo.zip.gz", "foo.zip"},
		{"foo.tgz", "foo.tar"},
		{"data.xz", "data"},
		{"plain", "plain"},
	}
	for _, c := range cases {
		if got := decompressedName(c.in); got != c.want {
			t.Errorf("decompressedName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestReserveDirIsUniquePerCaller(t *testing.T) {
	parent := t.TempDir()
	const n = 32

	var wg sync.WaitGroup
	got := make([]string, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			dir, err := reserveDir(parent, "out")
			if err != nil {
				t.Errorf("reserveDir: %v", err)
				return
			}
			got[i] = dir
		}()
	}
	wg.Wait()

	seen := map[string]bool{}
	for _, d := range got {
		if seen[d] {
			t.Fatalf("two callers were given the same directory: %s", d)
		}
		seen[d] = true
	}
	if len(seen) != n {
		t.Errorf("got %d distinct directories, want %d", len(seen), n)
	}
}

func TestReserveDirSkipsExistingFile(t *testing.T) {
	parent := t.TempDir()
	if err := os.WriteFile(filepath.Join(parent, "data"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir, err := reserveDir(parent, "data")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dir) != "data (1)" {
		t.Errorf("reserveDir = %q, want the name to sidestep the existing file", dir)
	}
}

func TestSafeName(t *testing.T) {
	bad := []string{"../escape", "a/../../b", "..", "../"}
	for _, in := range bad {
		if _, ok := safeName(in); ok {
			t.Errorf("safeName(%q) accepted an unsafe path", in)
		}
	}
	// Names for the destination directory itself. "tar -cf x.tar ." leads with
	// one of these, so they have to be a no-op rather than a rejection.
	for _, in := range []string{".", "./", "", "/"} {
		got, ok := safeName(in)
		if !ok || got != "" {
			t.Errorf("safeName(%q) = %q,%v; want \"\",true", in, got, ok)
		}
	}
	good := map[string]string{
		"a/b.txt":    filepath.FromSlash("a/b.txt"),
		"./a/b.txt":  filepath.FromSlash("a/b.txt"),
		"a/../b.txt": "b.txt",
		// Leading separators are stripped rather than refused, which is what
		// tar and unzip do: the entry lands inside the destination.
		"/absolute":  "absolute",
		"/a/b/c.txt": filepath.FromSlash("a/b/c.txt"),
	}
	for in, want := range good {
		got, ok := safeName(in)
		if !ok || got != want {
			t.Errorf("safeName(%q) = %q,%v; want %q,true", in, got, ok, want)
		}
	}
}

func TestSafeLinkTarget(t *testing.T) {
	cases := []struct {
		name, target string
		want         bool
	}{
		{"link", "sibling.txt", true},
		{"sub/link", "../top.txt", true},
		{"sub/link", "../../out.txt", false},
		{"link", "/etc/passwd", false},
		{"link", "../../../../etc/passwd", false},
		{"link", "", false},
	}
	for _, c := range cases {
		if got := safeLinkTarget(filepath.FromSlash(c.name), c.target); got != c.want {
			t.Errorf("safeLinkTarget(%q, %q) = %v, want %v", c.name, c.target, got, c.want)
		}
	}
}

func TestDetect(t *testing.T) {
	ctx := context.Background()

	zip := zipBytes(t, file("a.txt", "hello"))
	if f := detect(ctx, "thing.bin", zip); !f.isArchive || f.name != "zip" {
		t.Errorf("zip bytes detected as %+v", f)
	}
	// The name is what marks a container, but only once the bytes agree.
	if f := detect(ctx, "thing.jar", zip); !f.isArchive || !f.container {
		t.Errorf("jar detected as %+v, want an archive flagged as a container", f)
	}
	if f := detect(ctx, "notes.txt", []byte(strings.Repeat("plain text\n", 100))); f.isArchive {
		t.Errorf("plain text detected as %+v", f)
	}
	// A lying extension must not be enough on its own.
	if f := detect(ctx, "lies.zip", []byte("this is not a zip at all")); f.isArchive {
		t.Errorf("text named .zip detected as %+v", f)
	}
	if f := detect(ctx, "empty", nil); f.isArchive {
		t.Errorf("empty input detected as %+v", f)
	}
	if f := detect(ctx, "a.tar.gz", tarGzBytes(t, file("x", "y"))); !f.isArchive {
		t.Errorf("tar.gz detected as %+v", f)
	}
}

func TestHeadWriterStopsAtLimit(t *testing.T) {
	h := &headWriter{}
	for range 10 {
		if _, err := h.Write(make([]byte, 512)); err != nil {
			t.Fatal(err)
		}
	}
	if len(h.head) != headSize {
		t.Errorf("captured %d bytes, want %d", len(h.head), headSize)
	}
}

func TestContinuationVolumesAreNotArchives(t *testing.T) {
	continuation := []string{
		"movie.part02.rar", "movie.part10.rar", "MOVIE.PART03.RAR",
		"data.r00", "data.r01", "data.r15",
	}
	for _, name := range continuation {
		if !isContinuationVolume(name) {
			t.Errorf("%q should be treated as a continuation volume", name)
		}
	}
	first := []string{
		"movie.part01.rar", "movie.part1.rar", "plain.rar",
		"notes.txt", "archive.7z.001", "report.r", "thing.rar",
	}
	for _, name := range first {
		if isContinuationVolume(name) {
			t.Errorf("%q should not be treated as a continuation volume", name)
		}
	}
}
