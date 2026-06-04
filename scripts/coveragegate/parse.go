package main

import (
	"bufio"
	"io"
	"sort"
	"strconv"
	"strings"
)

// modulePrefix is stripped from the absolute import paths the coverage profile
// and `go tool cover -func` emit, so the gate's config can use short
// module-relative paths (internal/scan/scan.go).
const modulePrefix = "github.com/luuuc/sense/"

// ParseLineCoverage reads a Go coverage profile and returns module-relative
// file -> statement (line) coverage percent.
//
// A coverage profile produced with -coverpkg=./... lists each block once per
// test binary, so the same (file, range) appears many times — covered in one
// run, not in another. Blocks are merged by (file, range): a block counts as
// covered if ANY run hit it. Without this merge the duplicates inflate the
// denominator and every file reads near 0%.
func ParseLineCoverage(r io.Reader) (map[string]float64, error) {
	type block struct {
		stmts   int
		covered bool
	}
	blocks := map[string]*block{} // "file:range" -> merged block

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		// Format: <file>:<sL>.<sC>,<eL>.<eC> <numStmts> <count>
		// <file> may contain ':' on no platform we support, but split on the
		// last space-separated triplet to be safe.
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		key := fields[0] // file:range — already unique per block location
		stmts, err1 := strconv.Atoi(fields[1])
		count, err2 := strconv.Atoi(fields[2])
		if err1 != nil || err2 != nil {
			continue
		}
		b := blocks[key]
		if b == nil {
			b = &block{stmts: stmts}
			blocks[key] = b
		}
		if count > 0 {
			b.covered = true
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	total := map[string]int{}
	cov := map[string]int{}
	for key, b := range blocks {
		file := fileOfKey(key)
		total[file] += b.stmts
		if b.covered {
			cov[file] += b.stmts
		}
	}
	out := make(map[string]float64, len(total))
	for file, t := range total {
		if t == 0 {
			out[file] = 100.0
			continue
		}
		out[file] = 100.0 * float64(cov[file]) / float64(t)
	}
	return out, nil
}

// ParseFuncCoverage reads `go tool cover -func` output and returns module-
// relative file -> function coverage percent, defined as the share of the
// file's functions that are exercised at all (percent > 0). A wholly-untested
// function drags this below the floor even when the file's covered functions are
// statement-heavy enough to clear the line metric — the distinct "function"
// signal the cycle's "line AND function" floor asks for.
func ParseFuncCoverage(r io.Reader) (map[string]float64, error) {
	total := map[string]int{}
	hit := map[string]int{}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "total:") {
			continue
		}
		// Format: <file>:<line>:\t<func>\t<pct>%
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pctField := fields[len(fields)-1]
		if !strings.HasSuffix(pctField, "%") {
			continue
		}
		pct, err := strconv.ParseFloat(strings.TrimSuffix(pctField, "%"), 64)
		if err != nil {
			continue
		}
		file := fileOfKey(fields[0]) // fields[0] is file:line:
		total[file]++
		if pct > 0 {
			hit[file]++
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	out := make(map[string]float64, len(total))
	for file, t := range total {
		if t == 0 {
			out[file] = 100.0
			continue
		}
		out[file] = 100.0 * float64(hit[file]) / float64(t)
	}
	return out, nil
}

// fileOfKey strips the module prefix and any trailing ":<range>"/":<line>:" from
// a profile or func-report key, returning a module-relative file path.
func fileOfKey(key string) string {
	if i := strings.IndexByte(key, ':'); i >= 0 {
		key = key[:i]
	}
	return strings.TrimPrefix(key, modulePrefix)
}

func dirOf(file string) string {
	if i := strings.LastIndexByte(file, '/'); i >= 0 {
		return file[:i]
	}
	return file
}

func isTestFile(file string) bool {
	return strings.HasSuffix(file, "_test.go")
}

func sortViolations(v []Violation) {
	sort.Slice(v, func(i, j int) bool {
		if v[i].File != v[j].File {
			return v[i].File < v[j].File
		}
		return v[i].Metric < v[j].Metric
	})
}
