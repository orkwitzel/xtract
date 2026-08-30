// Package extractor recursively expands archives.
//
// Point it at one or more archives and it extracts them, inspects everything
// it wrote, extracts any archives it finds in there too, and keeps going until
// nothing archive-shaped is left. Nested archives are deleted once they have
// been expanded; the ones you named are left alone.
//
// Only New, Run and their option and summary types are exported. Scheduling,
// format detection and file writing are internal to the package. Progress
// during a run is published to internal/reporter, which is what lets a UI
// watch without this package knowing a UI exists.
package extractor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
)

// ErrStopped is returned when a run gave up early because MaxFiles or
// MaxBytes was reached. The archives extracted up to that point are still on
// disk, and the Summary describes them.
var ErrStopped = errors.New("stopped early: extraction limit reached")

// DefaultMaxDepth bounds how deep the recursion may go. It is what stops a
// zip quine, an archive that contains itself, from running forever.
const DefaultMaxDepth = 10

// Options configures a run. The zero value is usable: workers default to the
// CPU count, depth to DefaultMaxDepth, and the size caps to unlimited.
type Options struct {
	Workers  int    // archives in flight, and entries in flight within them
	MaxDepth int    // recursion limit
	MaxFiles int64  // total entries across the run; 0 for unlimited
	MaxBytes int64  // total uncompressed bytes across the run; 0 for unlimited
	Keep     bool   // keep nested archives instead of deleting them
	All      bool   // treat .docx/.jar/.apk and friends as archives too
	Out      string // where the named archives go; empty means beside each one
}

// Failure is one archive that did not come out cleanly.
type Failure struct {
	Path string
	Err  error
}

func (f Failure) Error() string { return f.Path + ": " + f.Err.Error() }

// Summary is the outcome of a run.
type Summary struct {
	Archives int64
	Files    int64
	Bytes    int64
	Failures []Failure
}

// Extractor runs one recursive extraction. Create it with New.
type Extractor struct {
	opts Options
	sem  chan struct{} // caps entry-level goroutines across all archives
	bud  budget

	mu       sync.Mutex
	cond     *sync.Cond
	queue    []job
	active   int
	stopped  bool
	failures []Failure

	// process is what a worker does with a job. It is always handle in real
	// use; the indirection lets the scheduler be tested on its own.
	process func(context.Context, job)

	archives atomic.Int64
	files    atomic.Int64
	bytes    atomic.Int64
	halted   atomic.Bool
	used     atomic.Bool
}

// New returns an Extractor with opts applied and defaults filled in.
func New(opts Options) *Extractor {
	if opts.Workers <= 0 {
		opts.Workers = runtime.NumCPU()
	}
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = DefaultMaxDepth
	}
	e := &Extractor{
		opts: opts,
		sem:  make(chan struct{}, opts.Workers),
	}
	e.process = e.handle
	e.bud.maxFiles = max(opts.MaxFiles, 0)
	e.bud.maxBytes = max(opts.MaxBytes, 0)
	e.cond = sync.NewCond(&e.mu)
	return e
}

// Run expands the given archives and everything nested inside them, returning
// once the whole tree is done. A failing archive is recorded and the rest of
// the run continues; the returned error is non-nil only when the context was
// cancelled or a limit stopped the run early.
//
// An Extractor runs once. Calling Run twice is a mistake rather than a way to
// queue more work, so it is reported instead of quietly doing nothing.
func (e *Extractor) Run(ctx context.Context, archives []string) (Summary, error) {
	if len(archives) == 0 {
		return Summary{}, errors.New("no archives given")
	}
	if !e.used.CompareAndSwap(false, true) {
		return Summary{}, errors.New("this Extractor has already run; create a new one")
	}

	for _, p := range archives {
		abs, err := filepath.Abs(p)
		if err != nil {
			e.fail(p, err)
			continue
		}
		st, err := os.Stat(abs)
		if err != nil {
			e.fail(p, err)
			continue
		}
		if st.IsDir() {
			e.fail(p, errors.New("is a directory, not an archive"))
			continue
		}
		j := job{path: abs, container: filepath.Dir(abs), root: true}
		if e.opts.Out != "" {
			j.container = e.opts.Out
			if len(archives) == 1 {
				j.forceDir = e.opts.Out
			}
			if err := os.MkdirAll(j.container, 0o755); err != nil {
				e.fail(p, err)
				continue
			}
		}
		e.submit(j)
	}

	e.work(ctx)

	e.mu.Lock()
	sum := Summary{
		Archives: e.archives.Load(),
		Files:    e.files.Load(),
		Bytes:    e.bytes.Load(),
		Failures: e.failures,
	}
	e.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return sum, err
	}
	if e.halted.Load() {
		return sum, ErrStopped
	}
	return sum, nil
}
