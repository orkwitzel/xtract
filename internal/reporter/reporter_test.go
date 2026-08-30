package reporter

import (
	"sync"
	"testing"
)

func TestCountersRoundTrip(t *testing.T) {
	Reset()
	AddTotal(3)
	AddExtracted(1)

	got := Get()
	if got.Total != 3 || got.Extracted != 1 {
		t.Errorf("Get() = %+v, want {Total:3 Extracted:1}", got)
	}
	if got.Remaining() != 2 {
		t.Errorf("Remaining() = %d, want 2", got.Remaining())
	}
	if got.Ratio() < 0.33 || got.Ratio() > 0.34 {
		t.Errorf("Ratio() = %v, want about 1/3", got.Ratio())
	}
}

func TestZeroValueIsSafe(t *testing.T) {
	Reset()
	s := Get()
	if s.Ratio() != 0 {
		t.Errorf("Ratio() with nothing counted = %v, want 0", s.Ratio())
	}
	if s.Remaining() != 0 {
		t.Errorf("Remaining() with nothing counted = %d, want 0", s.Remaining())
	}
}

func TestRatioIsClamped(t *testing.T) {
	Reset()
	AddTotal(2)
	AddExtracted(5) // shouldn't happen, but must not render a bar past full
	if r := Get().Ratio(); r != 1 {
		t.Errorf("Ratio() = %v, want it clamped to 1", r)
	}
	if rem := Get().Remaining(); rem != 0 {
		t.Errorf("Remaining() = %d, want it clamped to 0", rem)
	}
}

func TestConcurrentUpdates(t *testing.T) {
	Reset()
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				AddTotal(1)
				AddExtracted(1)
			}
		}()
	}
	// Reading while writers run must never tear or panic.
	for range 100 {
		_ = Get()
	}
	wg.Wait()

	if got := Get(); got.Total != 1600 || got.Extracted != 1600 {
		t.Errorf("Get() = %+v, want both counters at 1600", got)
	}
}
