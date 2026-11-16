// Command bulkhead is the command-line tool of Bulkhead. Its main command, bulkhead check,
// tests tenant isolation on a cluster (docs/adr/0012-active-tenant-isolation-checker.md).
package main

import (
	"os"

	"github.com/OpeyemiAdeniji/bulkhead/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
