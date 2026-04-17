package main

import (
	"fmt"
	"os"

	"github.com/luuuc/sense/internal/version"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println(version.Version)
		return
	}
	fmt.Fprintln(os.Stderr, "sense: not yet implemented (see .doc/pitches/ for the build plan)")
	os.Exit(1)
}
