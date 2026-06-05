package conventions

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/luuuc/sense/internal/model"
)

func detectFrameworkIdioms(symbols []symbolRow, edges []edgeRow, symbolByID map[int64]symbolRow, filePathByID map[int64]string) []Convention {
	var out []Convention
	out = append(out, detectRailsConcerns(symbols, edges, symbolByID, filePathByID)...)
	out = append(out, detectRailsCallbacks(symbols, edges, symbolByID, filePathByID)...)
	out = append(out, detectScopes(symbols, edges, symbolByID, filePathByID)...)
	out = append(out, detectGoInterfaces(symbols, edges, symbolByID, filePathByID)...)
	out = append(out, detectReactHooks(symbols, edges, symbolByID, filePathByID)...)
	out = append(out, detectGoTypeAliases(symbols, edges, symbolByID, filePathByID)...)
	out = append(out, detectGoMiddleware(symbols, edges, symbolByID, filePathByID)...)
	return out
}

func detectRailsConcerns(symbols []symbolRow, edges []edgeRow, symbolByID map[int64]symbolRow, filePathByID map[int64]string) []Convention {
	type concernGroup struct {
		module    symbolRow
		includers []Example
	}
	concerns := map[int64]*concernGroup{}
	for _, e := range edges {
		if e.kind != "includes" {
			continue
		}
		tgt, ok := symbolByID[e.targetID]
		if !ok || tgt.kind != "module" {
			continue
		}
		fp := filePathByID[tgt.fileID]
		if !strings.Contains(fp, "concerns") && !strings.Contains(fp, "concern") {
			continue
		}
		src, ok := symbolByID[e.sourceID]
		if !ok {
			continue
		}
		g, exists := concerns[e.targetID]
		if !exists {
			g = &concernGroup{module: tgt}
			concerns[e.targetID] = g
		}
		g.includers = append(g.includers, Example{Name: src.name, Path: filePathByID[src.fileID]})
	}
	var concernExamples []Example
	for _, g := range concerns {
		if len(g.includers) < minInstances {
			continue
		}
		concernExamples = append(concernExamples, Example{
			Name: g.module.name,
			Path: filePathByID[g.module.fileID],
		})
	}
	var out []Convention
	if len(concernExamples) >= 1 {
		sortExamples(concernExamples)
		totalModules := countByKind(symbols, "module")
		for _, g := range concerns {
			if len(g.includers) < minInstances {
				continue
			}
			sortExamples(g.includers)
			out = append(out, Convention{
				Category:    CategoryFramework,
				Description: fmt.Sprintf("Concern pattern: %s is mixed into %d classes (%s) for shared behavior", g.module.name, len(g.includers), topNames(g.includers)),
				Instances:   len(g.includers),
				Total:       totalModules,
				Strength:    safeStrength(len(g.includers), totalModules),
				Examples:    g.includers,
			})
		}
	}
	return out
}

func detectRailsCallbacks(symbols []symbolRow, _ []edgeRow, _ map[int64]symbolRow, filePathByID map[int64]string) []Convention {
	classByID := map[int64]symbolRow{}
	for _, s := range symbols {
		if s.kind == "class" {
			classByID[s.id] = s
		}
	}
	type classCallbacks struct {
		cls       symbolRow
		callbacks map[string]bool
	}
	byClass := map[int64]*classCallbacks{}
	for _, s := range symbols {
		if s.kind != "method" || !model.RailsCallbackNames[s.name] {
			continue
		}
		if s.parentID == nil {
			continue
		}
		cls, ok := classByID[*s.parentID]
		if !ok {
			continue
		}
		cc, exists := byClass[cls.id]
		if !exists {
			cc = &classCallbacks{cls: cls, callbacks: map[string]bool{}}
			byClass[cls.id] = cc
		}
		cc.callbacks[s.name] = true
	}
	var examples []Example
	for _, cc := range byClass {
		examples = append(examples, Example{
			Name:      cc.cls.name,
			Path:      filePathByID[cc.cls.fileID],
			EdgeCount: len(cc.callbacks),
		})
	}
	if len(examples) < minInstances {
		return nil
	}
	sort.Slice(examples, func(i, j int) bool {
		return lessExample(examples[i], examples[j])
	})
	totalClasses := countByKind(symbols, "class")
	return []Convention{{
		Category:    CategoryFramework,
		Description: fmt.Sprintf("Callback patterns: %d classes use Rails lifecycle callbacks (%s)", len(examples), topNames(examples)),
		Instances:   len(examples),
		Total:       totalClasses,
		Strength:    safeStrength(len(examples), totalClasses),
		Examples:    examples,
	}}
}

func detectScopes(symbols []symbolRow, edges []edgeRow, symbolByID map[int64]symbolRow, filePathByID map[int64]string) []Convention {
	scopeTargets := collectScopeTargets(edges, symbolByID)
	examples := gatherScopeClasses(symbols, scopeTargets, filePathByID)
	if len(examples) < minInstances {
		return nil
	}
	sort.Slice(examples, func(i, j int) bool {
		return lessExample(examples[i], examples[j])
	})
	totalClasses := countByKind(symbols, "class")
	return []Convention{{
		Category:    CategoryFramework,
		Description: fmt.Sprintf("Scope definitions: %d classes define query scopes (%s)", len(examples), topNames(examples)),
		Instances:   len(examples),
		Total:       totalClasses,
		Strength:    safeStrength(len(examples), totalClasses),
		Examples:    examples,
	}}
}

// collectScopeTargets returns the ids of symbols a class calls directly.
// Positive identification: scope symbols have a calls edge from their parent
// class pointing to them (emitted by emitScopeEdge). Regular class methods from
// `def self.x` do not have this edge.
func collectScopeTargets(edges []edgeRow, symbolByID map[int64]symbolRow) map[int64]bool {
	scopeTargets := map[int64]bool{}
	for _, e := range edges {
		if e.kind != "calls" {
			continue
		}
		src, ok := symbolByID[e.sourceID]
		if !ok || src.kind != "class" {
			continue
		}
		scopeTargets[e.targetID] = true
	}
	return scopeTargets
}

// gatherScopeClasses returns one Example per Ruby class that defines at least
// two query scopes, with EdgeCount set to its scope count.
func gatherScopeClasses(symbols []symbolRow, scopeTargets map[int64]bool, filePathByID map[int64]string) []Example {
	classByID := map[int64]symbolRow{}
	for _, s := range symbols {
		if s.kind == "class" {
			classByID[s.id] = s
		}
	}
	type classScopes struct {
		cls    symbolRow
		scopes []Example
	}
	byClass := map[int64]*classScopes{}
	for _, s := range symbols {
		if s.kind != "method" || s.parentID == nil {
			continue
		}
		if !scopeTargets[s.id] {
			continue
		}
		fp := filePathByID[s.fileID]
		if !strings.HasSuffix(fp, ".rb") {
			continue
		}
		if model.RailsCallbackNames[s.name] {
			continue
		}
		cls, ok := classByID[*s.parentID]
		if !ok {
			continue
		}
		cs, exists := byClass[cls.id]
		if !exists {
			cs = &classScopes{cls: cls}
			byClass[cls.id] = cs
		}
		cs.scopes = append(cs.scopes, Example{Name: s.name, Path: fp})
	}
	var examples []Example
	for _, cs := range byClass {
		if len(cs.scopes) < 2 {
			continue
		}
		examples = append(examples, Example{
			Name:      cs.cls.name,
			Path:      filePathByID[cs.cls.fileID],
			EdgeCount: len(cs.scopes),
		})
	}
	return examples
}

func detectReactHooks(symbols []symbolRow, _ []edgeRow, _ map[int64]symbolRow, filePathByID map[int64]string) []Convention {
	var hooks []Example
	for _, s := range symbols {
		if s.kind != "function" {
			continue
		}
		fp := filePathByID[s.fileID]
		ext := path.Ext(fp)
		if ext != ".js" && ext != ".jsx" && ext != ".ts" && ext != ".tsx" {
			continue
		}
		if strings.HasPrefix(s.name, "use") && len(s.name) > 3 && s.name[3] >= 'A' && s.name[3] <= 'Z' {
			hooks = append(hooks, Example{Name: s.name, Path: filePathByID[s.fileID]})
		}
	}
	if len(hooks) < minInstances {
		return nil
	}
	sortExamples(hooks)
	totalFuncs := countByKind(symbols, "function")
	return []Convention{{
		Category:    CategoryFramework,
		Description: fmt.Sprintf("React hook pattern: %s — custom hooks encapsulate stateful logic (%d hooks)", topNames(hooks), len(hooks)),
		Instances:   len(hooks),
		Total:       totalFuncs,
		Strength:    safeStrength(len(hooks), totalFuncs),
		Examples:    hooks,
	}}
}
