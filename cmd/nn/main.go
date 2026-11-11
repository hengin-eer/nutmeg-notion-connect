// Command nn reads the NUTMEG Notion workspace (read-only) in a compact form for agents.
package main

import (
	"os"

	"github.com/hengin-eer/nutmeg-notion-connect/internal/cli"
)

// version is set at release time with -ldflags "-X main.version=...".
var version = "dev"

func main() { os.Exit(cli.Main(os.Args[1:], version)) }
