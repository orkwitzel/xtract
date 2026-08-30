// Package ui shows what the extractor is doing.
//
// It reads the two counters in internal/reporter and nothing else, so it has
// no idea what an archive is or how one gets extracted. Everything the program
// prints to the terminal is printed from here: while the TUI is running it
// owns the screen, and a stray write from a worker would tear the frame.
package ui

import (
	"context"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"

	"github.com/orkwitzel/xtract/internal/reporter"
)

// Result is the final tally, in terms this package can print without knowing
// anything about archives.
type Result struct {
	Archives int64
	Files    int64
	Bytes    int64
	Elapsed  time.Duration
	Failures []string
}

// IsTTY reports whether stdout is a terminal, and so whether the TUI is worth
// starting at all.
//
// This asks the terminal driver rather than looking at the file mode: /dev/null
// is a character device too, and redirecting to it should not be mistaken for
// having a screen to draw on.
func IsTTY() bool { return term.IsTerminal(os.Stdout.Fd()) }

// Run displays the TUI while work runs, and returns work's error.
//
// bubbletea wants the main goroutine for input handling, so the extraction
// runs beside it and signals completion with a message. A stop request —
// ctrl+c, or a signal cancelling ctx — cancels the work but leaves the program
// up until the work actually returns, so nothing is killed mid-write.
func Run(ctx context.Context, cancel context.CancelFunc, work func() error) error {
	p := tea.NewProgram(newModel(cancel), tea.WithoutSignalHandler())

	errc := make(chan error, 1)
	go func() {
		errc <- work()
		p.Send(doneMsg{})
	}()

	// A signal reaches us as a cancelled context; forward it to the model so
	// the display can say what is happening.
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		select {
		case <-ctx.Done():
			p.Send(interruptMsg{})
		case <-stopped:
		}
	}()

	if _, err := p.Run(); err != nil {
		// The terminal turned out not to be usable after all. The work is
		// already running, so let it finish rather than losing it.
		fmt.Fprintln(os.Stderr, "xtract: falling back to plain output:", err)
		return <-errc
	}
	return <-errc
}

// Plain runs work without taking over the terminal, for pipes, logs and
// --no-tui. With verbose set it prints a progress line every second.
func Plain(ctx context.Context, verbose bool, work func() error) error {
	if !verbose {
		return work()
	}

	done := make(chan struct{})
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				s := reporter.Get()
				fmt.Fprintf(os.Stderr, "extracting: %d/%d archives\n", s.Extracted, s.Total)
			}
		}
	}()

	err := work()
	close(done)
	return err
}

// Summary prints the final tally. Failures go to stderr so a caller piping
// stdout gets only the one-line result.
func Summary(r Result) {
	fmt.Printf("%d archive%s · %d file%s · %s · %s\n",
		r.Archives, plural(r.Archives), r.Files, plural(r.Files), humanBytes(r.Bytes), round(r.Elapsed))

	if len(r.Failures) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "\n%d failed:\n", len(r.Failures))
	for _, f := range r.Failures {
		fmt.Fprintf(os.Stderr, "  %s\n", f)
	}
}

func plural(n int64) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// humanBytes formats a byte count the way people read them.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for size := n / unit; size >= unit && exp < 4; size /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTP"[exp])
}
