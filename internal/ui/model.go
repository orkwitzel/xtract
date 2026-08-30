package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/orkwitzel/xtract/internal/reporter"
)

// refreshRate is how often the display re-reads the counters. Fast enough to
// feel live, slow enough to cost nothing next to the extraction itself.
const refreshRate = 100 * time.Millisecond

type tickMsg time.Time

// doneMsg says the extraction returned. It is the only thing that ends the
// program: a stop request sets the display to "stopping" and waits for this,
// so workers finish the file they are writing instead of being cut off.
type doneMsg struct{}

// interruptMsg is a stop request arriving from a signal rather than a keypress.
type interruptMsg struct{}

var (
	titleStyle  = lipgloss.NewStyle().Bold(true)
	dimStyle    = lipgloss.NewStyle().Faint(true)
	numberStyle = lipgloss.NewStyle().Bold(true)
	stopStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
)

type model struct {
	spin  spinner.Model
	bar   progress.Model
	stats reporter.Stats

	start    time.Time
	elapsed  time.Duration
	cancel   context.CancelFunc
	stopping bool
	done     bool
	width    int
}

func newModel(cancel context.CancelFunc) model {
	s := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	return model{
		spin:   s,
		bar:    progress.New(progress.WithDefaultGradient(), progress.WithWidth(40)),
		start:  time.Now(),
		cancel: cancel,
		width:  80,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, tick())
}

func tick() tea.Cmd {
	return tea.Tick(refreshRate, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		m.stats = reporter.Get()
		m.elapsed = time.Since(m.start)
		return m, tick()

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case doneMsg:
		m.stats = reporter.Get()
		m.elapsed = time.Since(m.start)
		m.done = true
		return m, tea.Quit

	case interruptMsg:
		return m.stop(), nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			return m.stop(), nil
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.bar.Width = min(msg.Width-6, 60)
	}
	return m, nil
}

// stop asks the extractor to wind down but keeps the program running until
// doneMsg arrives, so a half-written file is never left behind.
func (m model) stop() model {
	if !m.stopping {
		m.stopping = true
		if m.cancel != nil {
			m.cancel()
		}
	}
	return m
}

func (m model) View() string {
	var b strings.Builder

	head := m.spin.View()
	if m.done {
		head = "✓"
	}
	b.WriteString(fmt.Sprintf("\n %s %s\n\n", head, titleStyle.Render("xtract")))
	b.WriteString(" " + m.bar.ViewAs(m.stats.Ratio()) + "\n\n")

	b.WriteString(fmt.Sprintf(" %s of %s archives",
		numberStyle.Render(fmt.Sprint(m.stats.Extracted)),
		numberStyle.Render(fmt.Sprint(m.stats.Total))))
	if r := m.stats.Remaining(); r > 0 {
		b.WriteString(dimStyle.Render(fmt.Sprintf(" · %d to go", r)))
	}
	b.WriteString(dimStyle.Render(" · "+round(m.elapsed)) + "\n\n")

	switch {
	case m.done:
		b.WriteString(dimStyle.Render(" done") + "\n")
	case m.stopping:
		b.WriteString(stopStyle.Render(" stopping — letting workers finish the file they're on") + "\n")
	default:
		b.WriteString(dimStyle.Render(" ctrl+c to stop") + "\n")
	}
	return b.String()
}

// round trims a duration to something readable at a glance.
func round(d time.Duration) string {
	switch {
	case d < time.Second:
		return d.Round(time.Millisecond).String()
	case d < time.Minute:
		return d.Round(100 * time.Millisecond).String()
	default:
		return d.Round(time.Second).String()
	}
}
