package main

import (
	"os"

	"github.com/katarem/lray/internal/cli"
)

// version la inyecta el build: -ldflags "-X main.version=v1.2.3"
var version = "dev"

func main() {
	os.Exit(cli.Execute(version))
}
