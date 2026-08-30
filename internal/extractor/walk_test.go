package extractor

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The scheduler's whole job is to stay alive while work is still producing
// work, and to stop the moment it isn't. These tests drive it with synthetic
// jobs so a scheduling bug can't hide behind a slow archive.

func TestSchedulerDrainsRecursiveWork(t *testing.T) {
	for _, workers := range []int{1, 2, 8} {
		t.Run(fmt.Sprint("workers=", workers), func(t *testing.T) {
			e := New(Options{Workers: workers})

			const depth, fanout = 5, 3
			var seen atomic.Int64
			e.process = func(_ context.Context, j job) {
				seen.Add(1)
				if j.depth >= depth {
					return
				}
				for i := range fanout {
					e.submit(job{path: fmt.Sprintf("%s/%d", j.path, i), depth: j.depth + 1})
				}
			}

			e.submit(job{path: "root"})

			done := make(chan struct{})
			go func() { e.work(context.Background()); close(done) }()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("scheduler did not finish: workers are waiting on work that will never come")
			}

			want := int64(0)
			for i, n := 0, int64(1); i <= depth; i, n = i+1, n*fanout {
				want += n
			}
			if got := seen.Load(); got != want {
				t.Errorf("handled %d jobs, want %d", got, want)
			}
		})
	}
}

func TestSchedulerStopsWithEmptyQueue(t *testing.T) {
	e := New(Options{Workers: 4})
	done := make(chan struct{})
	go func() { e.work(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("work() blocked on an empty queue instead of returning")
	}
}

func TestSchedulerHonoursCancellation(t *testing.T) {
	e := New(Options{Workers: 4})
	ctx, cancel := context.WithCancel(context.Background())

	var handled atomic.Int64
	e.process = func(_ context.Context, j job) {
		if handled.Add(1) == 1 {
			cancel()
		}
		e.submit(job{path: j.path + "x", depth: j.depth + 1})
	}
	e.submit(job{path: "seed"})

	done := make(chan struct{})
	go func() { e.work(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not stop the scheduler")
	}
	if handled.Load() > 4 {
		t.Errorf("kept working after cancellation: %d jobs", handled.Load())
	}
}

func TestSchedulerSurvivesFailingJobs(t *testing.T) {
	e := New(Options{Workers: 4})

	var handled atomic.Int64
	e.process = func(_ context.Context, j job) {
		handled.Add(1)
		if j.depth == 0 {
			for i := range 10 {
				e.submit(job{path: fmt.Sprint(i), depth: 1})
			}
		}
		if j.depth == 1 {
			e.fail(j.path, fmt.Errorf("boom"))
		}
	}
	e.submit(job{path: "root"})
	e.work(context.Background())

	if handled.Load() != 11 {
		t.Errorf("handled %d jobs, want 11: one failure must not cancel its siblings", handled.Load())
	}
	if len(e.failures) != 10 {
		t.Errorf("recorded %d failures, want 10", len(e.failures))
	}
}

func TestBudgetAccounting(t *testing.T) {
	b := &budget{maxFiles: 3, maxBytes: 100}

	for i := range 3 {
		if err := b.addFile(); err != nil {
			t.Fatalf("file %d rejected early: %v", i, err)
		}
	}
	if err := b.addFile(); err == nil {
		t.Error("expected the file limit to trip")
	} else if !overBudget(err) {
		t.Errorf("%v should be recognised as a budget error", err)
	}

	if err := b.addBytes(99); err != nil {
		t.Fatalf("bytes rejected early: %v", err)
	}
	if err := b.addBytes(2); err == nil || !overBudget(err) {
		t.Errorf("expected the byte limit to trip, got %v", err)
	}
}

func TestBudgetIsRaceFree(t *testing.T) {
	b := &budget{maxFiles: 1 << 20, maxBytes: 1 << 30}
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				_ = b.addFile()
				_ = b.addBytes(10)
			}
		}()
	}
	wg.Wait()

	if got := b.files.Load(); got != 1600 {
		t.Errorf("files = %d, want 1600", got)
	}
	if got := b.bytes.Load(); got != 16000 {
		t.Errorf("bytes = %d, want 16000", got)
	}
}

func TestExtractorIsSingleUse(t *testing.T) {
	e := New(Options{Workers: 1})
	if _, err := e.Run(context.Background(), []string{"/nonexistent.zip"}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if _, err := e.Run(context.Background(), []string{"/nonexistent.zip"}); err == nil {
		t.Error("a second Run should be reported, not silently do nothing")
	}
}
