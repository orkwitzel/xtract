package extractor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/mholt/archives"
)

// output is a file we wrote that turned out to be an archive itself.
type output struct {
	path string // absolute path on disk
	fm   format
}

// result is what one archive produced.
type result struct {
	files    int64
	bytes    int64
	outputs  []output
	rejected []string // entries refused for trying to escape the destination
}

func (r *result) merge(o result) {
	r.files += o.files
	r.bytes += o.bytes
	r.outputs = append(r.outputs, o.outputs...)
	r.rejected = append(r.rejected, o.rejected...)
}

var (
	errFileLimit = errors.New("file limit exceeded")
	errByteLimit = errors.New("size limit exceeded")
)

// budget caps how much a single run may unpack. It is what keeps a zip bomb
// from filling the disk: bytes are charged as they stream, not after the fact.
type budget struct {
	files atomic.Int64
	bytes atomic.Int64

	maxFiles int64
	maxBytes int64
}

func (b *budget) addFile() error {
	if b.maxFiles > 0 && b.files.Add(1) > b.maxFiles {
		return fmt.Errorf("%w (%d)", errFileLimit, b.maxFiles)
	}
	return nil
}

func (b *budget) addBytes(n int64) error {
	if b.maxBytes > 0 && b.bytes.Add(n) > b.maxBytes {
		return fmt.Errorf("%w (%d bytes)", errByteLimit, b.maxBytes)
	}
	return nil
}

// overBudget reports whether a limit has already been hit, so the remaining
// queue can be abandoned instead of failing one archive at a time.
func overBudget(err error) bool {
	return errors.Is(err, errFileLimit) || errors.Is(err, errByteLimit)
}

// entry writes one archive entry and folds it into res. res must belong to the
// calling goroutine: the parallel zip path gives each worker its own and
// merges at the end.
//
// An entry that tries to escape the destination is recorded and skipped rather
// than failing the archive, so one hostile path doesn't cost us the other
// thousand files. The run still exits non-zero because the rejection is
// reported against the archive.
func (e *Extractor) entry(ctx context.Context, info archives.FileInfo, root *os.Root, dest string, res *result) error {
	w, err := writeEntry(root, info, &e.bud)
	if err != nil {
		if errors.Is(err, errUnsafePath) {
			res.rejected = append(res.rejected, info.NameInArchive)
			return nil
		}
		return err
	}
	if !w.reg {
		return nil
	}

	res.files++
	res.bytes += w.bytes

	if fm := detect(ctx, w.name, w.head); fm.isArchive {
		res.outputs = append(res.outputs, output{path: filepath.Join(dest, w.name), fm: fm})
	}
	return nil
}
