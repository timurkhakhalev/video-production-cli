package main

import (
	"os"

	"video-production-cli/internal/cli"
)

var version = "dev"

func main() {
	os.Exit(cli.Execute(version))
}
