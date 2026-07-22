package main

import (
	"os"

	"github.com/thiasap/host-mcp/internal/app"
)

func main() { os.Exit(app.Main(os.Args[1:])) }
