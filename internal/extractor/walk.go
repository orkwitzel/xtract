package extractor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/orkwitzel/xtract/internal/reporter"
)

// job is one archive waiting to be expanded.
//
// It carries the directory the output belongs in rather than the output
// directory itself, because which of the two we need is not known until the
// archive has been identified: a tar makes a directory of its own, while a
// lone foo.txt.gz should just become foo.txt right where it was found.
type job struct {
	path      string
	container string // directory the result goes into
	forceDir  string // extract into exactly this directory, if set
	depth     int
	root      bool // named on the command line, so never deleted
}

// submit queues a job. It must never block: it is called from inside workers,
// which is exactly why the queue is an unbounded slice rather than a channel.
// A buffered channel would deadlock the moment a worker tried to enqueue into
// a full queue that only workers can drain.
func (e *Extractor) submit(j job) {
	e.mu.Lock()
	e.queue = append(e.queue, j)
	e.mu.Unlock()
	e.cond.Signal()
	reporter.AddTotal(1)
}

// next blocks until there is a job, or until the run is finished. The run is
// finished when the queue is empty and no worker is running, because only a
// running worker can produce more work.
func (e *Extractor) next() (job, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for len(e.queue) == 0 {
		if e.active == 0 || e.stopped {
			e.stopped = true
			e.cond.Broadcast()
			return job{}, false
		}
		e.cond.Wait()
	}
	j := e.queue[0]
	e.queue = e.queue[1:]
	e.active++
	return j, true
}

func (e *Extractor) finish() {
	e.mu.Lock()
	e.active--
	if e.active == 0 && len(e.queue) == 0 {
		e.stopped = true
	}
	e.cond.Broadcast()
	e.mu.Unlock()
}

// work runs the pool until quiescence.
//
// Deadlock invariant: the goroutines that extract zip entries never wait on a
// job. Workers wait on entry goroutines, entry goroutines wait only on the
// entry semaphore, and that semaphore is released solely by entry goroutines
// that have nothing left to wait for. The dependency graph has no cycle.
func (e *Extractor) work(ctx context.Context) {
	var wg sync.WaitGroup
	for i := 0; i < e.opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				j, ok := e.next()
				if !ok {
					return
				}
				if ctx.Err() == nil && !e.halted.Load() {
					e.process(ctx, j)
				}
				e.finish()
			}
		}()
	}
	wg.Wait()
}

// handle expands one archive, queues whatever archives came out of it, and
// deletes it if it was nested.
func (e *Extractor) handle(ctx context.Context, j job) {
	e.archives.Add(1)
	defer reporter.AddExtracted(1)

	res, err := e.extract(ctx, j)
	e.files.Add(res.files)
	e.bytes.Add(res.bytes)
	if err != nil {
		e.fail(j.path, err)
		if overBudget(err) {
			// The disk is the shared resource here; once the cap is hit,
			// every remaining job would just fail the same way.
			e.halted.Store(true)
		}
		return
	}

	clean := len(res.rejected) == 0
	if !clean {
		err := fmt.Errorf("rejected %d unsafe entr%s: %s",
			len(res.rejected), plural(len(res.rejected), "y", "ies"), strings.Join(trim(res.rejected, 3), ", "))
		e.fail(j.path, err)
	}

	e.queueChildren(j, res)

	// Deleting right after this archive's own extraction (rather than after
	// its whole subtree) frees space as the tree unwinds. An archive that had
	// entries rejected is kept, so there is something left to inspect.
	if !j.root && !e.opts.Keep && clean {
		_ = os.Remove(j.path)
		removeVolumes(j.path)
	}
}

func (e *Extractor) queueChildren(j job, res result) {
	if j.depth+1 > e.opts.MaxDepth {
		return
	}
	for _, out := range res.outputs {
		if out.fm.container && !e.opts.All {
			continue // a .docx or .jar is a file to most people, not an archive
		}
		e.submit(job{
			path:      out.path,
			container: filepath.Dir(out.path),
			depth:     j.depth + 1,
		})
	}
}

func (e *Extractor) fail(path string, err error) {
	e.mu.Lock()
	e.failures = append(e.failures, Failure{Path: path, Err: err})
	e.mu.Unlock()
}

// removeVolumes deletes the continuation volumes of a split archive that has
// just been extracted.
//
// The decoder pulls part02 onwards in by itself, so they were consumed by the
// same extraction and belong with the first volume. Without this, the one case
// where split archives are common — a rar set inside a download — leaves half
// its parts lying around.
func removeVolumes(first string) {
	dir := filepath.Dir(first)
	base := strings.ToLower(filepath.Base(first))

	var stem string
	switch {
	case rarPartRe.MatchString(base):
		stem = base[:rarPartRe.FindStringIndex(base)[0]]
	case strings.HasSuffix(base, ".rar"):
		stem = strings.TrimSuffix(base, ".rar")
	default:
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	// The separator matters: without it, extracting foo.rar would sweep up an
	// unrelated foobar.r01 sitting next to it.
	prefix := stem + "."
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		if isContinuationVolume(name[len(stem):]) {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func trim(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return append(s[:n:n], "...")
}
