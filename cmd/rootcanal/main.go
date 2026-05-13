package main

import (
	"fmt"
	"os"

	"gitlab.com/zorak1103/rootcanal/internal/version"
)

func main() {
	fmt.Fprintf(os.Stderr, "rootcanal %s (%s, built %s)\n", version.Version, version.Commit, version.Date)
	fmt.Fprintln(os.Stderr, "not yet implemented — see upcoming milestones")
	os.Exit(1)
}
