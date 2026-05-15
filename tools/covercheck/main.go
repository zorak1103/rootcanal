package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func main() {
	min := flag.Float64("min", 0, "minimum coverage percentage required")
	flag.Parse()

	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: covercheck -min=<pct> <coverage.out>")
		os.Exit(2)
	}

	out, err := exec.Command("go", "tool", "cover", "-func="+flag.Arg(0)).Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "go tool cover: %v\n", err)
		os.Exit(2)
	}

	pct, err := parseTotalPct(string(out))
	if err != nil {
		fmt.Fprintf(os.Stderr, "covercheck: %v\n", err)
		os.Exit(2)
	}

	fmt.Printf("Coverage: %.1f%%  (min %.1f%%)\n", pct, *min)
	if pct < *min {
		fmt.Fprintf(os.Stderr, "FAIL: coverage %.1f%% is below %.1f%% minimum\n", pct, *min)
		os.Exit(1)
	}
}

func parseTotalPct(output string) (float64, error) {
	for _, line := range strings.Split(output, "\n") {
		if !strings.HasPrefix(line, "total:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		raw := strings.TrimSuffix(fields[len(fields)-1], "%")
		return strconv.ParseFloat(raw, 64)
	}
	return 0, fmt.Errorf("no 'total:' line found in go tool cover output")
}
