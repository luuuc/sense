package conventions

import (
	"fmt"
	"path"
	"strings"

	"github.com/luuuc/sense/internal/model"
)

// PHP/Laravel convention refinement - the PHP sibling of detectors_ruby.go
// and the Rails idioms in detectors_framework.go: a significance post-pass
// plus one framework detector, kept out of the generic detectors so
// detectors_generic.go stays language-neutral.

// refinePHPSignificance ranks PHP inheritance/composition conventions so a
// project's own architecture outranks the generic Laravel structure the
// caller already knows (extending Model or Controller is the framework
// speaking, not the project). Mirrors refineRubySignificance; runs only on
// conventions whose instances are PHP files.
func refinePHPSignificance(conventions []Convention) {
	for i := range conventions {
		c := &conventions[i]
		if c.Category != CategoryInheritance && c.Category != CategoryComposition {
			continue
		}
		if !hasPHPExample(c.Examples) {
			continue
		}
		c.Significance = phpBaseSignificance(c.KeySymbol)
	}
}

// phpBaseSignificance scores a base/mixin target: a Laravel framework base
// (matched by fully-qualified name or by leaf, since the extractor
// namespace-expands what it can and leaves vendor bases as written) ranks
// lowest; everything else shares one tier. PHP class names are uniformly
// namespaced, so Ruby's middle bare-name tier carries no signal here -
// within the tier, raw prevalence orders.
func phpBaseSignificance(qualified string) float64 {
	if model.LaravelFrameworkBaseClasses[qualified] {
		return 0.0
	}
	if i := strings.LastIndex(qualified, `\`); i >= 0 && model.LaravelFrameworkBaseClasses[qualified[i+1:]] {
		return 0.0
	}
	return 1.0
}

// hasPHPExample reports whether any of a convention's instances is a PHP
// source file, the signal that the convention is PHP's to rank.
func hasPHPExample(examples []Example) bool {
	for _, e := range examples {
		if strings.HasSuffix(e.Path, ".php") {
			return true
		}
	}
	return false
}

// detectPHPTestStyle splits a project's PHP test files into the two
// framework families: PHPUnit-style (a test class per file) and Pest-style
// (function calls at the top level, no class). The discriminator is
// structural - whether the test file declares a class - because both
// families share the *Test.php filename convention the generic testing
// detector already reports.
func detectPHPTestStyle(symbols []symbolRow, _ []edgeRow, _ map[int64]symbolRow, filePathByID map[int64]string) []Convention {
	classful := map[int64]bool{}
	for _, s := range symbols {
		if s.kind == "class" {
			classful[s.fileID] = true
		}
	}
	var unit, pest []Example
	total := 0
	for fid, fp := range filePathByID {
		if !strings.HasSuffix(fp, ".php") || !isTestFile(filePathByID, fid) {
			continue
		}
		total++
		ex := Example{Name: path.Base(fp), Path: fp}
		if classful[fid] {
			unit = append(unit, ex)
		} else {
			pest = append(pest, ex)
		}
	}
	var out []Convention
	out = append(out, phpTestStyleRow(unit, total, "PHPUnit-style test classes")...)
	out = append(out, phpTestStyleRow(pest, total, "Pest-style function tests (no test class)")...)
	return out
}

// phpTestStyleRow renders one family's Testing row when it clears the
// instance floor.
func phpTestStyleRow(ex []Example, total int, label string) []Convention {
	if len(ex) < minInstances {
		return nil
	}
	sortExamples(ex)
	return []Convention{{
		Category:    CategoryTesting,
		Description: fmt.Sprintf("%s: %d of %d PHP test files (%s)", label, len(ex), total, topNames(ex)),
		Instances:   len(ex),
		Total:       total,
		Strength:    float64(len(ex)) / float64(total),
		Examples:    ex,
	}}
}
