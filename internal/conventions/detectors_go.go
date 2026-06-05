package conventions

import (
	"fmt"
	"sort"
	"strings"
)

func detectGoInterfaces(symbols []symbolRow, edges []edgeRow, symbolByID map[int64]symbolRow, filePathByID map[int64]string) []Convention {
	type ifaceGroup struct {
		iface        symbolRow
		implementors []Example
	}
	ifaces := map[int64]*ifaceGroup{}
	for _, e := range edges {
		if e.kind != "inherits" {
			continue
		}
		tgt, ok := symbolByID[e.targetID]
		if !ok || tgt.kind != "interface" {
			continue
		}
		src, ok := symbolByID[e.sourceID]
		if !ok || (src.kind != "struct" && src.kind != "class") {
			continue
		}
		g, exists := ifaces[e.targetID]
		if !exists {
			g = &ifaceGroup{iface: tgt}
			ifaces[e.targetID] = g
		}
		g.implementors = append(g.implementors, Example{Name: src.name, Path: filePathByID[src.fileID]})
	}
	var out []Convention
	for _, g := range ifaces {
		if len(g.implementors) < minInterfaceInstances {
			continue
		}
		sortExamples(g.implementors)
		totalStructs := countByKind(symbols, "struct", "class")
		out = append(out, Convention{
			Category:    CategoryFramework,
			Description: fmt.Sprintf("Interface contract: %s is satisfied by %d types (%s) — polymorphic dispatch point", g.iface.name, len(g.implementors), topNames(g.implementors)),
			Instances:   len(g.implementors),
			Total:       totalStructs,
			Strength:    safeStrength(len(g.implementors), totalStructs),
			Examples:    g.implementors,
			KeySymbol:   g.iface.name,
		})
	}
	return out
}

func detectGoTypeAliases(symbols []symbolRow, _ []edgeRow, _ map[int64]symbolRow, filePathByID map[int64]string) []Convention {
	var goAliases []Example
	for _, s := range symbols {
		if s.kind != "type" {
			continue
		}
		fp := filePathByID[s.fileID]
		if !strings.HasSuffix(fp, ".go") {
			continue
		}
		goAliases = append(goAliases, Example{Name: s.name, Path: fp, Kind: s.kind})
	}
	if len(goAliases) < minInstances {
		return nil
	}
	sortExamples(goAliases)
	totalTypes := countByKind(symbols, "type")
	return []Convention{{
		Category:    CategoryStructure,
		Description: fmt.Sprintf("Type aliases: %s — named domain types (%d aliases)", topNames(goAliases), len(goAliases)),
		Instances:   len(goAliases),
		Total:       totalTypes,
		Strength:    safeStrength(len(goAliases), totalTypes),
		Examples:    goAliases,
	}}
}

var routerMethodNames = map[string]bool{
	"Use": true, "GET": true, "POST": true, "PUT": true, "DELETE": true,
	"PATCH": true, "Handle": true, "HandleFunc": true, "Group": true,
	"Any": true, "HEAD": true, "OPTIONS": true,
}

func detectGoMiddleware(symbols []symbolRow, edges []edgeRow, symbolByID map[int64]symbolRow, filePathByID map[int64]string) []Convention {
	routerSymbolIDs := collectRouterSymbols(symbols, filePathByID)
	if len(routerSymbolIDs) == 0 {
		return nil
	}
	factories := collectMiddlewareFactories(edges, routerSymbolIDs, symbolByID, filePathByID)
	if len(factories) < minInstances {
		return nil
	}
	sort.Slice(factories, func(i, j int) bool {
		return lessExample(factories[i], factories[j])
	})
	totalFuncs := countByKind(symbols, "function")
	return []Convention{{
		Category:    CategoryFramework,
		Description: fmt.Sprintf("Middleware factories: %s return handler functions — composable request pipeline (%d factories)", topNames(factories), len(factories)),
		Instances:   len(factories),
		Total:       totalFuncs,
		Strength:    safeStrength(len(factories), totalFuncs),
		Examples:    factories,
	}}
}

// collectRouterSymbols returns the ids of Go symbols that name router methods
// (Use, GET, Handle, …), the call sites a middleware factory flows through.
func collectRouterSymbols(symbols []symbolRow, filePathByID map[int64]string) map[int64]bool {
	routerSymbolIDs := map[int64]bool{}
	for _, s := range symbols {
		if routerMethodNames[s.name] && (s.kind == "method" || s.kind == "function") {
			if strings.HasSuffix(filePathByID[s.fileID], ".go") {
				routerSymbolIDs[s.id] = true
			}
		}
	}
	return routerSymbolIDs
}

// collectMiddlewareFactories returns the distinct functions a router method
// calls, ranked-input as Examples, skipping Test/Benchmark helpers and
// non-function targets.
func collectMiddlewareFactories(edges []edgeRow, routerSymbolIDs map[int64]bool, symbolByID map[int64]symbolRow, filePathByID map[int64]string) []Example {
	handlerCounts := map[int64]int{}
	for _, e := range edges {
		if e.kind != "calls" || !routerSymbolIDs[e.sourceID] {
			continue
		}
		handlerCounts[e.targetID]++
	}
	var factories []Example
	seen := map[string]bool{}
	for id, count := range handlerCounts {
		s, ok := symbolByID[id]
		if !ok || s.kind != "function" {
			continue
		}
		if strings.HasPrefix(s.name, "Test") || strings.HasPrefix(s.name, "Benchmark") {
			continue
		}
		if seen[s.name] {
			continue
		}
		seen[s.name] = true
		factories = append(factories, Example{Name: s.name, Path: filePathByID[s.fileID], EdgeCount: count})
	}
	return factories
}
