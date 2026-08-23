package main

import (
	"os"

	"github.com/baron-shared-brain/baron/internal/app"
	"github.com/baron-shared-brain/baron/internal/cli"
)

var version = "dev"

func main() {
	application := app.New()
	os.Exit(cli.Run(os.Args[1:], application.CLIOptions(os.Stdout, os.Stderr)))
}
