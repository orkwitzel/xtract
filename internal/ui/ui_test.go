package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/orkwitzel/xtract/internal/reporter"
)

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1024 * 1024, "1.0 MiB"},
		{3 * 1024 * 1024 * 1024, "3.0 GiB"},
	}
	for _, c := range cases {
		if got := humanBytes(c.in); got != c.want {
			t.Errorf("humanBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRound(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{1500 * time.Microsecond, "2ms"},
		{2500 * time.Millisecond, "2.5s"},
		{90 * time.Second, "1m30s"},
	}
	for _, c := range cases {
		if got := round(c.in); got != c.want {
			t.Errorf("round(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The model is a pure function of the counters, so the view can be checked
// without a terminal anywhere in sight.
func TestViewReflectsCounters(t *testing.T) {
	reporter.Reset()
	reporter.AddTotal(13)
	reporter.AddExtracted(8)

	m, _ := newModel(nil).Update(tickMsg(time.Now()))
	view := m.View()

	if n := strings.Count(view, "\n"); n > 1 {
		t.Errorf("view should be a single line, got %d newlines:\n%s", n, view)
	}
	for _, want := range []string{"8", "13"} {
		if !strings.Contains(view, want) {
			t.Errorf("view is missing %q:\n%s", want, view)
		}
	}
}

func TestStopRequestWaitsForTheWork(t *testing.T) {
	reporter.Reset()
	cancelled := false
	m, cmd := newModel(func() { cancelled = true }).Update(tea.KeyMsg{Type: tea.KeyCtrlC})

	if !cancelled {
		t.Error("ctrl+c should cancel the run")
	}
	if cmd != nil {
		t.Error("ctrl+c must not quit yet: the workers are still writing files")
	}
	if !strings.Contains(m.View(), "stopping") {
		t.Errorf("view should say it is stopping:\n%s", m.View())
	}
}

func TestDoneQuits(t *testing.T) {
	reporter.Reset()
	reporter.AddTotal(2)
	reporter.AddExtracted(2)

	m, cmd := newModel(nil).Update(doneMsg{})
	if cmd == nil {
		t.Fatal("doneMsg should quit the program")
	}
	if msg := cmd(); msg != tea.Quit() {
		t.Errorf("expected tea.Quit, got %T", msg)
	}
	if !strings.Contains(m.View(), "done") {
		t.Errorf("finished view should say so:\n%s", m.View())
	}
}

func TestStopIsIdempotent(t *testing.T) {
	calls := 0
	m := newModel(func() { calls++ })
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	next, _ = next.Update(interruptMsg{})
	if calls != 1 {
		t.Errorf("cancel called %d times, want 1", calls)
	}
	_ = next
}
