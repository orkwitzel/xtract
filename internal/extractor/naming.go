package extractor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// compressionExts are outer wrappers, stripped first: foo.tar.gz -> foo.tar.
var compressionExts = map[string]bool{
	".gz": true, ".bz2": true, ".xz": true, ".zst": true, ".zstd": true,
	".lz4": true, ".br": true, ".sz": true, ".lz": true, ".z": true,
	".zz": true, ".mz": true, ".bz": true,
}

// archiveExts are stripped second. The .tgz family bundles both layers into
// one extension, so they live here.
var archiveExts = map[string]bool{
	".tar": true, ".zip": true, ".zipx": true, ".rar": true, ".7z": true,
	".tgz": true, ".tbz": true, ".tbz2": true, ".txz": true, ".tzst": true,
	".cbz": true, ".cbr": true, ".cb7": true,
}

// baseName is the directory name an archive should extract into:
// foo.tar.gz -> foo, bar.zip -> bar, data -> data.
func baseName(p string) string {
	b := filepath.Base(p)
	stripped := false
	if ext := filepath.Ext(b); compressionExts[strings.ToLower(ext)] {
		b, stripped = strings.TrimSuffix(b, ext), true
	}
	if ext := filepath.Ext(b); archiveExts[strings.ToLower(ext)] {
		b, stripped = strings.TrimSuffix(b, ext), true
	}
	// Anything else we were asked to extract still deserves a directory named
	// after it rather than after the file: report.docx -> report. Without this
	// the directory would collide with the archive sitting next to it.
	if !stripped {
		if ext := filepath.Ext(b); ext != "" && ext != b {
			b = strings.TrimSuffix(b, ext)
		}
	}
	if b == "" || b == "." || b == string(filepath.Separator) {
		return "extracted"
	}
	return b
}

// suffixed is the nth candidate name for stem+ext: the plain name for n == 0,
// then "stem (1).ext", "stem (2).ext" and so on. It is the convention browsers
// and file managers use for the same problem, so the numbering reads as a
// duplicate rather than as part of the name.
func suffixed(stem, ext string, n int) string {
	if n == 0 {
		return stem + ext
	}
	return fmt.Sprintf("%s (%d)%s", stem, n, ext)
}

// reserveDir creates a fresh directory under parent, named base, or
// "base (1)", "base (2)", ... if that is taken. os.Mkdir is atomic, which is
// what makes this safe when several workers are naming directories in the same
// parent at the same time: whoever loses the race simply moves on to the next
// candidate.
func reserveDir(parent, base string) (string, error) {
	for i := 0; i < 1000; i++ {
		dir := filepath.Join(parent, suffixed(base, "", i))
		err := os.Mkdir(dir, 0o755)
		if err == nil {
			return dir, nil
		}
		if !os.IsExist(err) {
			return "", err
		}
	}
	return "", fmt.Errorf("no free directory name for %q in %s", base, parent)
}
