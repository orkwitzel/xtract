package extractor

import (
	"archive/tar"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mholt/archives"
)

var errUnsafePath = errors.New("entry path escapes the destination")

// written describes what one archive entry produced on disk.
type written struct {
	name  string // path relative to the destination root
	bytes int64
	head  []byte // leading bytes, for archive detection
	reg   bool   // regular file, so worth inspecting
}

// 256 KiB copy buffers, reused across entries and goroutines.
var bufPool = sync.Pool{New: func() any { b := make([]byte, 256<<10); return &b }}

// safeName turns an in-archive path into one that is safe to join onto the
// destination. os.Root blocks escapes at the syscall level anyway; this
// rejects the obvious cases early so they can be reported per entry.
//
// An empty name with ok true means the entry is the destination directory
// itself: "tar -cf x.tar ." writes exactly that as its first entry, so it is
// an everyday thing to find and a no-op rather than an escape.
//
// Backslashes are treated as separators, because zips written on Windows use
// them. That does mean a file whose name genuinely contains a backslash gets
// split into directories, which is the trade every extractor makes.
func safeName(name string) (string, bool) {
	clean := path.Clean(strings.TrimLeft(strings.ReplaceAll(name, `\`, "/"), "/"))
	if clean == "." || clean == "" {
		return "", true
	}
	local := filepath.FromSlash(clean)
	if !filepath.IsLocal(local) {
		return "", false
	}
	return local, true
}

// safeLinkTarget reports whether a symlink may be created pointing at target.
//
// os.Root stops us from *writing through* a symlink that leaves the tree, but
// it will happily create one, and a link to /etc/passwd sitting in the output
// is a trap for whatever reads the extracted files next. Anything absolute, or
// relative enough to climb out, is refused.
func safeLinkTarget(name, target string) bool {
	if target == "" {
		return false
	}
	t := filepath.FromSlash(target)
	if filepath.IsAbs(t) || strings.HasPrefix(target, "/") {
		return false
	}
	return filepath.IsLocal(filepath.Join(filepath.Dir(name), t))
}

// writeEntry materialises one archive entry beneath root. Every path goes
// through the *os.Root, so a "../../etc/passwd" entry or a symlink pointing
// out of the tree fails in the kernel rather than relying on us to spot it.
func writeEntry(root *os.Root, info archives.FileInfo, bud *budget) (written, error) {
	name, ok := safeName(info.NameInArchive)
	if !ok {
		return written{}, errUnsafePath
	}
	if name == "" {
		return written{}, nil // the destination itself; already exists
	}

	mode := info.Mode()
	switch {
	case info.IsDir():
		if err := root.MkdirAll(name, 0o755); err != nil {
			return written{}, err
		}
		return written{name: name}, nil

	case mode&fs.ModeSymlink != 0:
		if !safeLinkTarget(name, info.LinkTarget) {
			return written{}, errUnsafePath
		}
		if err := mkParent(root, name); err != nil {
			return written{}, err
		}
		_ = root.Remove(name)
		if err := root.Symlink(info.LinkTarget, name); err != nil {
			return written{}, err
		}
		return written{name: name}, nil

	case isHardlink(info):
		if err := mkParent(root, name); err != nil {
			return written{}, err
		}
		target, ok := safeName(info.LinkTarget)
		if !ok || target == "" {
			return written{}, errUnsafePath
		}
		_ = root.Remove(name)
		if err := root.Link(target, name); err != nil {
			return written{}, err
		}
		return written{name: name}, nil

	case !mode.IsRegular():
		// devices, fifos, sockets: nothing useful to extract
		return written{}, nil
	}

	if err := mkParent(root, name); err != nil {
		return written{}, err
	}

	if err := bud.addFile(); err != nil {
		return written{}, err
	}

	perm := mode.Perm()
	if perm == 0 {
		perm = 0o644
	}
	dst, err := root.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return written{}, err
	}

	src, err := info.Open()
	if err != nil {
		dst.Close()
		return written{}, err
	}

	head := &headWriter{}
	buf := bufPool.Get().(*[]byte)
	n, copyErr := io.CopyBuffer(io.MultiWriter(dst, head, charge{bud}), src, *buf)
	bufPool.Put(buf)

	src.Close()
	closeErr := dst.Close()
	if copyErr != nil {
		return written{}, copyErr
	}
	if closeErr != nil {
		return written{}, closeErr
	}

	if mt := info.ModTime(); !mt.IsZero() {
		_ = root.Chtimes(name, mt, mt)
	}
	return written{name: name, bytes: n, head: head.head, reg: true}, nil
}

// mkParent creates the directories leading up to name.
func mkParent(root *os.Root, name string) error {
	dir := filepath.Dir(name)
	if dir == "." || dir == string(filepath.Separator) {
		return nil
	}
	return root.MkdirAll(dir, 0o755)
}

// isHardlink reports whether the entry is a tar hard link, which arrives
// looking like an empty regular file unless the header is consulted.
func isHardlink(info archives.FileInfo) bool {
	hdr, ok := info.Header.(*tar.Header)
	return ok && hdr.Typeflag == tar.TypeLink && hdr.Linkname != ""
}

// charge counts bytes against the run budget as they stream past, so an
// oversized entry is stopped mid-copy rather than after it lands on disk.
type charge struct{ bud *budget }

func (c charge) Write(p []byte) (int, error) {
	if err := c.bud.addBytes(int64(len(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}
