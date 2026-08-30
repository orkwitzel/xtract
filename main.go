// Command xtract recursively extracts archives and the archives inside them.
package main

import (
	"os"

	"github.com/orkwitzel/xtract/cmd"
)

func main() { os.Exit(cmd.Execute()) }
