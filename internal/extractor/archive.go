package extractor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mholt/archives"
)

// tarballExts fold the archive and its compression into one extension.
var tarballExts = map[string]string{
	".tgz": ".tar", ".tbz": ".tar", ".tbz2": ".tar", ".txz": ".tar", ".tzst": ".tar",
}

// extract expands one archive. It knows nothing about recursion: give it a
// job and it produces files, reporting which of them are archives themselves.
//
// The archive is always a real file on disk, never a stream, because zip and
// 7z extraction need io.ReaderAt and io.Seeker. That constraint is why nested
// archives are written out before being expanded rather than piped in memory,
// and it has the pleasant side effect of keeping memory flat no matter how big
// the archive is.
func (e *Extractor) extract(ctx context.Context, j job) (result, error) {
	f, err := os.Open(j.path)
	if err != nil {
		return result{}, err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return result{}, err
	}

	format, _, err := archives.Identify(ctx, filepath.Base(j.path), f)
	if err != nil {
		if errors.Is(err, archives.NoMatch) {
			return result{}, errors.New("not a recognised archive")
		}
		return result{}, fmt.Errorf("identifying format: %w", err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return result{}, err
	}

	// Anything that unwraps to a single file goes straight into the directory
	// the archive was found in. Wrapping one file in a directory named after
	// it only adds a level nobody asked for.
	if dec, solo := soloDecompressor(format); solo {
		return e.decompress(ctx, dec, f, j.container, j.path)
	}

	dest, err := e.destDir(j)
	if err != nil {
		return result{}, err
	}
	root, err := os.OpenRoot(dest)
	if err != nil {
		return result{}, err
	}
	defer root.Close()

	switch fm := format.(type) {
	case archives.Extractor:
		// A rar split across volumes can only be followed if the decoder
		// knows the file name, so it can derive part02 from part01.
		if r, ok := fm.(archives.Rar); ok {
			r.Name = j.path
			fm = r
		}
		if _, isZip := format.(archives.Zip); isZip && e.opts.Workers > 1 && st.Size() > 0 {
			res, err := e.extractZip(ctx, f, st.Size(), root, dest)
			if err == nil || overBudget(err) {
				return res, err
			}
			// A zip the stdlib reader chokes on may still open through the
			// generic path, so fall back rather than give up on it.
			if _, seekErr := f.Seek(0, io.SeekStart); seekErr != nil {
				return res, err
			}
		}
		return e.extractStream(ctx, fm, f, root, dest)
	}

	return result{}, fmt.Errorf("unsupported format %s", format.Extension())
}

// destDir is the directory a multi-file archive extracts into: a fresh one
// named after the archive, unless the caller demanded a specific path.
func (e *Extractor) destDir(j job) (string, error) {
	if j.forceDir != "" {
		return j.forceDir, os.MkdirAll(j.forceDir, 0o755)
	}
	return reserveDir(j.container, baseName(j.path))
}

// soloDecompressor reports whether a format unwraps to exactly one file, and
// returns the decompressor to do it with.
//
// That covers a plain foo.gz, and also a zip or 7z inside compression: those
// cannot be read in one pass, because the inner reader needs random access
// that a decompression stream cannot provide. Unwrapping the outer layer to
// disk lets the recursion pick the archive up on the next pass.
func soloDecompressor(f archives.Format) (archives.Decompressor, bool) {
	if ca, ok := f.(archives.CompressedArchive); ok {
		if needsRandomAccess(ca.Extraction) {
			return ca.Compression, true
		}
		return nil, false
	}
	if _, ok := f.(archives.Extractor); ok {
		return nil, false
	}
	d, ok := f.(archives.Decompressor)
	return d, ok
}

// needsRandomAccess reports whether a format can only be read from something
// that implements io.ReaderAt and io.Seeker.
func needsRandomAccess(f archives.Extraction) bool {
	switch f.(type) {
	case archives.Zip, archives.SevenZip:
		return true
	}
	return false
}

// extractStream walks an archive entry by entry. Tar and rar are sequential by
// nature; 7z stays here too, because its solid blocks mean parallel entry
// reads would decompress the same block over and over.
func (e *Extractor) extractStream(ctx context.Context, fm archives.Extractor, f *os.File, root *os.Root, dest string) (result, error) {
	var res result
	err := fm.Extract(ctx, f, func(ctx context.Context, info archives.FileInfo) error {
		return e.entry(ctx, info, root, dest, &res)
	})
	return res, err
}

// decompress unwraps a single compressed file into dir: foo.txt.gz becomes
// foo.txt beside it. The output is inspected like any other file, so a
// foo.zip.gz keeps unwrapping on the next pass.
func (e *Extractor) decompress(ctx context.Context, fm archives.Decompressor, f *os.File, dir, src string) (result, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return result{}, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return result{}, err
	}
	defer root.Close()

	rc, err := fm.OpenReader(f)
	if err != nil {
		return result{}, err
	}
	defer rc.Close()

	if err := e.bud.addFile(); err != nil {
		return result{}, err
	}

	// The archive lives in this directory too, so take a name nothing else
	// has claimed rather than overwriting a neighbour.
	dst, name, err := createUnique(root, decompressedName(src))
	if err != nil {
		return result{}, err
	}

	head := &headWriter{}
	buf := bufPool.Get().(*[]byte)
	n, err := io.CopyBuffer(io.MultiWriter(dst, head, charge{&e.bud}), rc, *buf)
	bufPool.Put(buf)

	if closeErr := dst.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return result{}, err
	}

	res := result{files: 1, bytes: n}
	if ft := detect(ctx, name, head.head); ft.isArchive {
		res.outputs = append(res.outputs, output{path: filepath.Join(dir, name), fm: ft})
	}
	return res, nil
}

// createUnique makes a file called name, or "name (1)", "name (2)", ... if
// that is taken, numbering it the same way reserveDir numbers directories.
// O_EXCL is what makes it safe for two workers to race here.
func createUnique(root *os.Root, name string) (*os.File, string, error) {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 0; i < 1000; i++ {
		candidate := suffixed(stem, ext, i)
		f, err := root.OpenFile(candidate, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
		if err == nil {
			return f, candidate, nil
		}
		if !os.IsExist(err) {
			return nil, "", err
		}
	}
	return nil, "", fmt.Errorf("no free name for %q", name)
}

// decompressedName is what a single compressed file unwraps to.
func decompressedName(src string) string {
	b := filepath.Base(src)
	ext := strings.ToLower(filepath.Ext(b))
	if repl, ok := tarballExts[ext]; ok {
		return strings.TrimSuffix(b, filepath.Ext(b)) + repl
	}
	if compressionExts[ext] {
		if trimmed := strings.TrimSuffix(b, filepath.Ext(b)); trimmed != "" {
			return trimmed
		}
	}
	return b
}
