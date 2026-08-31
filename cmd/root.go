// Package cmd wires the pieces together: it turns flags into extractor
// options, hands the run to the UI, and translates the outcome into an exit
// code. It is the only place that knows about all three internal packages.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/orkwitzel/xtract/internal/extractor"
	"github.com/orkwitzel/xtract/internal/reporter"
	"github.com/orkwitzel/xtract/internal/ui"
)

// version is overridable at build time:
//
//	go build -ldflags "-X github.com/orkwitzel/xtract/cmd.version=v1.2.3"
var version = "dev"

// errFailed marks a run that finished with at least one broken archive, as
// opposed to one that could not start at all.
var errFailed = errors.New("some archives failed")

// New builds the command. It is a function rather than a package-level
// variable so flags land in a fresh options struct each time, which is what
// makes the command testable.
func New() *cobra.Command {
	var (
		opts    extractor.Options
		noTUI   bool
		verbose bool
	)

	cmd := &cobra.Command{
		Use:   "xtract [flags] <archive>...",
		Short: "Recursively extract archives, including the archives inside them",
		Long: `xtract extracts an archive, then extracts every archive it finds inside,
however deeply they are nested and whatever formats are mixed together —
a zip inside a tar.gz inside a rar all come out in one pass.

Each nested archive is expanded into a directory named after it and then
deleted. The archives you name on the command line are never deleted.

Formats: zip, tar, rar, 7z, and the gz, bz2, xz, zstd, lz4, brotli, lzip
and snappy wrappers around them. Files are identified by content, so an
archive with a misleading name or no extension at all is still found.`,
		Example: `  xtract bundle.zip
  xtract -j 16 --keep big.tar.zst
  xtract -o ./unpacked one.zip two.rar`,
		Args:          cobra.MinimumNArgs(1),
		Version:       version,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// From here on, failures are runtime problems rather than misuse,
			// so stop cobra from printing the whole usage block after them.
			cmd.SilenceUsage = true
			return run(cmd.Context(), opts, args, noTUI, verbose)
		},
	}

	f := cmd.Flags()
	f.StringVarP(&opts.Out, "out", "o", "",
		"where to extract to (default: a directory beside each archive)")
	f.IntVarP(&opts.Workers, "jobs", "j", runtime.NumCPU(),
		"archives to work on at once")
	f.BoolVar(&opts.Keep, "keep", false,
		"keep nested archives instead of deleting them once extracted")
	f.BoolVar(&opts.All, "all", false,
		"also expand .docx, .jar, .apk and other zip-based file formats")
	f.IntVar(&opts.MaxDepth, "max-depth", extractor.DefaultMaxDepth,
		"how deep to recurse")
	f.Int64Var(&opts.MaxFiles, "max-files", 1_000_000,
		"stop after this many extracted files (0 for no limit)")
	f.Int64Var(&opts.MaxBytes, "max-size", 0,
		"stop after this many extracted bytes (0 for no limit)")
	f.BoolVar(&noTUI, "no-tui", false,
		"plain output instead of the progress display")
	f.BoolVar(&verbose, "verbose", false,
		"print progress as plain lines (implies --no-tui)")

	return cmd
}

func run(ctx context.Context, opts extractor.Options, args []string, noTUI, verbose bool) error {
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	reporter.Reset()
	ex := extractor.New(opts)

	start := time.Now()
	var sum extractor.Summary
	work := func() (err error) {
		sum, err = ex.Run(ctx, args)
		return err
	}

	// The TUI needs a terminal to draw on, and gets out of the way when the
	// user asked for plain output.
	var err error
	if noTUI || verbose || !ui.IsTTY() {
		err = ui.Plain(ctx, verbose, work)
	} else {
		err = ui.Run(ctx, cancel, work)
	}

	ui.Summary(ui.Result{
		Archives: sum.Archives,
		Files:    sum.Files,
		Bytes:    sum.Bytes,
		Elapsed:  time.Since(start),
		Failures: failureLines(sum.Failures),
	})

	switch {
	case errors.Is(err, context.Canceled), errors.Is(ctx.Err(), context.Canceled):
		return context.Canceled
	case len(sum.Failures) > 0, errors.Is(err, extractor.ErrStopped):
		// Whatever went wrong is already in the summary above; saying it
		// again as a top-level error would only repeat it.
		return errFailed
	case err != nil:
		return err
	}
	return nil
}

func failureLines(fs []extractor.Failure) []string {
	lines := make([]string, 0, len(fs))
	for _, f := range fs {
		lines = append(lines, f.Error())
	}
	return lines
}

// Execute runs the command and returns the process exit code:
// 0 clean, 1 archives failed, 2 misuse or a run that could not start,
// 130 interrupted.
func Execute() int {
	cmd := New()
	err := cmd.ExecuteContext(context.Background())

	switch {
	case err == nil:
		return 0
	case errors.Is(err, context.Canceled):
		fmt.Fprintln(os.Stderr, "xtract: interrupted")
		return 130
	case errors.Is(err, errFailed):
		return 1
	default:
		fmt.Fprintln(os.Stderr, "xtract:", err)
		return 2
	}
}
