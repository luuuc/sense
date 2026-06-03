package scan

import (
	"fmt"
	"slices"
	"strings"

	"github.com/luuuc/sense/internal/index"
	"github.com/luuuc/sense/internal/model"
)

const confidenceNamingConvention = 0.6

var roleSuffixes = []string{
	"Service", "Controller", "Serializer", "Validator",
	"Decorator", "Job", "Worker", "Presenter",
	"Contract", "Representer", "Mailer",
}

var excludedSuffixes = map[string]bool{
	"Gateway":   true,
	"Provider":  true,
	"Client":    true,
	"Adapter":   true,
	"Error":     true,
	"Exception": true,
}

type namingCandidate struct {
	sym      model.Symbol
	prefixes []string
}

// namingConventionEdges creates low-confidence calls edges between
// classes whose names follow Rails/Django naming conventions and their
// associated model classes. For example, WorkPackagesController → WorkPackage
// and WorkPackages::CreateService → WorkPackage.
//
// Only runs for projects with Ruby or Python files — Go and TypeScript
// have explicit references that tree-sitter already captures.
func (h *harness) namingConventionEdges() error {
	dynamicFiles, err := h.dynamicFileIDs()
	if err != nil {
		return err
	}
	if len(dynamicFiles) == 0 {
		return nil
	}

	syms, err := h.idx.Query(h.ctx, index.Filter{})
	if err != nil {
		return fmt.Errorf("naming: query symbols: %w", err)
	}

	classesByName, candidates := collectNamingCandidates(syms, dynamicFiles)
	if len(candidates) == 0 {
		return nil
	}

	written, err := h.writeNamingEdges(candidates, classesByName)
	if err != nil {
		return err
	}
	h.edges += written
	return nil
}

// dynamicFileIDs returns the set of file ids whose language uses naming-based
// dispatch (Ruby and Python). Go and TypeScript have explicit references
// tree-sitter already captures, so they are excluded.
func (h *harness) dynamicFileIDs() (map[int64]bool, error) {
	rubyFiles, err := h.idx.FileIDsByLanguage(h.ctx, "ruby")
	if err != nil {
		return nil, fmt.Errorf("naming: query ruby files: %w", err)
	}
	pythonFiles, err := h.idx.FileIDsByLanguage(h.ctx, "python")
	if err != nil {
		return nil, fmt.Errorf("naming: query python files: %w", err)
	}
	dynamicFiles := make(map[int64]bool, len(rubyFiles)+len(pythonFiles))
	for id := range rubyFiles {
		dynamicFiles[id] = true
	}
	for id := range pythonFiles {
		dynamicFiles[id] = true
	}
	return dynamicFiles, nil
}

// collectNamingCandidates indexes the dynamic-language classes/modules by bare
// name and collects those whose name matches a role suffix (the WorkPackages-
// Controller / CreateService forms) as edge candidates.
func collectNamingCandidates(syms []model.Symbol, dynamicFiles map[int64]bool) (map[string][]model.Symbol, []namingCandidate) {
	classesByName := map[string][]model.Symbol{}
	var candidates []namingCandidate
	for _, s := range syms {
		if !dynamicFiles[s.FileID] {
			continue
		}
		if s.Kind != model.KindClass && s.Kind != model.KindModule {
			continue
		}
		bare, _ := splitQualified(s.Qualified)
		classesByName[bare] = append(classesByName[bare], s)
		if prefixes, ok := modelPrefixes(s.Qualified); ok {
			candidates = append(candidates, namingCandidate{sym: s, prefixes: prefixes})
		}
	}
	return classesByName, candidates
}

// writeNamingEdges resolves each candidate to its model class and writes a
// low-confidence calls edge, in one transaction, skipping self-edges and
// candidates whose target is absent.
func (h *harness) writeNamingEdges(candidates []namingCandidate, classesByName map[string][]model.Symbol) (int, error) {
	var written int
	err := h.idx.InTx(h.ctx, func() error {
		for _, c := range candidates {
			target, found := resolveModelTarget(c.prefixes, classesByName)
			if !found || target.ID == c.sym.ID {
				continue
			}
			_, werr := h.idx.WriteEdge(h.ctx, &model.Edge{
				SourceID:   model.Int64Ptr(c.sym.ID),
				TargetID:   target.ID,
				Kind:       model.EdgeCalls,
				FileID:     c.sym.FileID,
				Confidence: confidenceNamingConvention,
			})
			if werr != nil {
				return fmt.Errorf("write naming edge %s → %s: %w", c.sym.Qualified, target.Qualified, werr)
			}
			written++
		}
		return nil
	})
	return written, err
}

// splitQualified splits a qualified name into its bare name and
// immediate namespace. Returns ("CreateService", "WorkPackages") for
// "WorkPackages::CreateService" and ("UserController", "") for
// "UserController".
func splitQualified(qualified string) (bare, namespace string) {
	for _, sep := range []string{"::", ".", "/"} {
		i := strings.LastIndex(qualified, sep)
		if i < 0 {
			continue
		}
		bare = qualified[i+len(sep):]
		prefix := qualified[:i]
		for _, sep2 := range []string{"::", ".", "/"} {
			if j := strings.LastIndex(prefix, sep2); j >= 0 {
				namespace = prefix[j+len(sep2):]
				return
			}
		}
		namespace = prefix
		return
	}
	return qualified, ""
}

// modelPrefixes returns candidate model names to look up for a class
// that matches a role suffix. Returns prefixes from both the bare name
// and the namespace.
//
// "WorkPackagesController" → ["WorkPackages"]
// "WorkPackages::CreateService" → ["Create", "WorkPackages"]
// "UserError" → nil (excluded suffix)
func modelPrefixes(qualified string) ([]string, bool) {
	bare, ns := splitQualified(qualified)

	for suffix := range excludedSuffixes {
		if strings.HasSuffix(bare, suffix) {
			return nil, false
		}
	}

	var prefixes []string

	for _, suffix := range roleSuffixes {
		if strings.HasSuffix(bare, suffix) && len(bare) > len(suffix) {
			prefixes = append(prefixes, bare[:len(bare)-len(suffix)])
			break
		}
	}

	if ns != "" {
		for _, suffix := range roleSuffixes {
			if strings.HasSuffix(bare, suffix) {
				if !slices.Contains(prefixes, ns) {
					prefixes = append(prefixes, ns)
				}
				break
			}
		}
	}

	if len(prefixes) == 0 {
		return nil, false
	}
	return prefixes, true
}

// resolveModelTarget tries to find a class matching any of the
// candidate prefixes. Singularized forms are tried first so the
// model class (WorkPackage) is preferred over a namespace module
// (WorkPackages) that shares the plural name.
func resolveModelTarget(prefixes []string, classesByName map[string][]model.Symbol) (model.Symbol, bool) {
	for _, prefix := range prefixes {
		singular := singularize(prefix)
		if singular != prefix {
			if ss, ok := classesByName[singular]; ok {
				return ss[0], true
			}
		}
	}
	for _, prefix := range prefixes {
		if ss, ok := classesByName[prefix]; ok {
			return ss[0], true
		}
	}
	return model.Symbol{}, false
}

func singularize(s string) string {
	if s == "" {
		return s
	}
	if strings.HasSuffix(s, "ies") && len(s) > 3 {
		return s[:len(s)-3] + "y"
	}
	if strings.HasSuffix(s, "sses") {
		return s[:len(s)-2]
	}
	if strings.HasSuffix(s, "ses") && len(s) > 3 {
		return s[:len(s)-2]
	}
	if strings.HasSuffix(s, "xes") && len(s) > 3 {
		return s[:len(s)-2]
	}
	if strings.HasSuffix(s, "ss") || strings.HasSuffix(s, "us") || strings.HasSuffix(s, "is") {
		return s
	}
	if strings.HasSuffix(s, "s") && len(s) > 1 {
		return s[:len(s)-1]
	}
	return s
}
