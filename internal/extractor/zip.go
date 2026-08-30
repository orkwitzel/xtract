package extractor

import (
	"archive/zip"
	"context"
	"io"
	"io/fs"
	"os"
	"sync"
	"sync/atomic"

	"github.com/mholt/archives"
)

// parallelZipMin is the entry count below which fanning out costs more in
// coordination than it saves in throughput.
const parallelZipMin = 32

// extractZip expands a zip with several goroutines sharing one reader.
//
// This is the difference between a single 5 GB zip pinning one core and it
// using the whole machine. It is safe because zip.Reader hands every
// File.Open() an independent io.SectionReader over the same *os.File, and
// ReadAt on a file is a pread — no shared offset to race on. os.Root is
// likewise documented as safe for concurrent use.
func (e *Extractor) extractZip(ctx context.Context, f *os.File, size int64, root *os.Root, dest string) (result, error) {
	zr, err := zip.NewReader(f, size)
	if err != nil {
		return result{}, err
	}

	workers := min(e.opts.Workers, len(zr.File))
	if len(zr.File) < parallelZipMin {
		workers = 1 // coordination would cost more than it saves
	}
	var (
		next     atomic.Int64
		mu       sync.Mutex
		res      result
		firstErr error
		wg       sync.WaitGroup
	)

	fail := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
	}
	failed := func() bool {
		mu.Lock()
		defer mu.Unlock()
		return firstErr != nil
	}

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var local result
			for {
				i := int(next.Add(1) - 1)
				if i >= len(zr.File) || ctx.Err() != nil || failed() {
					break
				}

				// One global slot per in-flight entry, so N archives each
				// fanning out N ways don't oversubscribe the machine.
				e.sem <- struct{}{}
				err := e.zipEntry(ctx, zr.File[i], root, dest, &local)
				<-e.sem

				if err != nil {
					fail(err)
					break
				}
			}
			mu.Lock()
			res.merge(local)
			mu.Unlock()
		}()
	}
	wg.Wait()

	return res, firstErr
}

func (e *Extractor) zipEntry(ctx context.Context, f *zip.File, root *os.Root, dest string, res *result) error {
	info, err := zipFileInfo(f)
	if err != nil {
		return err
	}
	return e.entry(ctx, info, root, dest, res)
}

// zipFileInfo adapts a stdlib zip entry to the shape the shared writer wants.
func zipFileInfo(f *zip.File) (archives.FileInfo, error) {
	info := f.FileInfo()
	fi := archives.FileInfo{
		FileInfo:      info,
		Header:        f.FileHeader,
		NameInArchive: f.Name,
		Open: func() (fs.File, error) {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			return openedFile{rc, info}, nil
		},
	}

	// In a zip, a symlink's target is its file content.
	if info.Mode()&fs.ModeSymlink != 0 {
		rc, err := f.Open()
		if err != nil {
			return fi, err
		}
		target, err := io.ReadAll(io.LimitReader(rc, 4096))
		rc.Close()
		if err != nil {
			return fi, err
		}
		fi.LinkTarget = string(target)
	}
	return fi, nil
}

type openedFile struct {
	io.ReadCloser
	info fs.FileInfo
}

func (o openedFile) Stat() (fs.FileInfo, error) { return o.info, nil }
