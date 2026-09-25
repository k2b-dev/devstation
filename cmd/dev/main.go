package main

import (
	"fmt"
	"github.com/k2b-dev/devstation/internal/devstation"
	"os"
)

var version = "dev"

func main() {
	if err := devstation.Run(os.Args[1:], version, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "dev:", err)
		os.Exit(1)
	}
}
