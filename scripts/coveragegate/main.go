package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
)

func main() {
	profile := flag.String("profile", "coverage.txt", "path to the Go coverage profile")
	flag.Parse()
	os.Exit(run(*profile))
}

// run returns the process exit code: 0 pass, 1 violations, 2 setup error.
func run(profile string) int {
	lineCov, err := lineCoverage(profile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "coveragegate: %v\n", err)
		return 2
	}
	funcCov, err := funcCoverage(profile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "coveragegate: func coverage: %v\n", err)
		return 2
	}

	violations := Check(lineCov, funcCov)

	fmt.Printf("per-file coverage gate: floor %.0f%% (line AND function) over the whole production tree, %d excluded dirs, %d straggler exceptions\n",
		floor, len(excludedDirs), len(stragglerExceptions))
	if len(violations) == 0 {
		fmt.Println("PASS: every gated file meets the floor")
		return 0
	}
	fmt.Printf("FAIL: %d gated file(s) below the %.0f%% floor:\n", len(violations), floor)
	for _, v := range violations {
		fmt.Printf("  %-8s %5.1f%%  %s\n", v.Metric, v.Got, v.File)
	}
	fmt.Println("Cover the gap, or — only if the residual is genuinely unreachable — add a justified entry to stragglerExceptions. Do not lower the floor.")
	return 1
}

// lineCoverage reads and parses the profile's per-file statement coverage.
func lineCoverage(profile string) (map[string]float64, error) {
	f, err := os.Open(profile)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read-only handle; close error is immaterial
	return ParseLineCoverage(f)
}

// funcCoverage runs `go tool cover -func` over the profile and parses its output.
// Kept out of ParseFuncCoverage so that function is a pure reader the test feeds
// synthetic input.
func funcCoverage(profile string) (map[string]float64, error) {
	cmd := exec.Command("go", "tool", "cover", "-func="+profile)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return ParseFuncCoverage(&out)
}
