package mcpio

import (
	"sort"
	"strings"
)

const DefaultTokenBudget = 6000

// ConventionsResponse is the shape of the sense_conventions tool's reply
// and the `sense conventions --json` CLI output.
type ConventionsResponse struct {
	KeySymbols   []KeySymbolEntry   `json:"key_symbols,omitempty"`
	Summary      string             `json:"summary,omitempty"`
	Conventions  []ConventionEntry  `json:"conventions"`
	Truncated    bool               `json:"truncated,omitempty"`
	TokenBudget  int                `json:"token_budget,omitempty"`
	SenseMetrics ConventionsMetrics `json:"-"`
	NextSteps    []NextStep         `json:"next_steps"`
}

// KeySymbolEntry is a high-reach type/interface with callers, emitted first
// in the conventions response to surface concrete symbol names.
type KeySymbolEntry struct {
	Name       string   `json:"name"`
	Kind       string   `json:"kind"`
	Snippet    string   `json:"snippet,omitempty"`
	References int      `json:"references"`
	Callers    []string `json:"callers,omitempty"`
}

// ConventionEntry is a single detected convention in the wire response.
type ConventionEntry struct {
	Category       string     `json:"category"`
	Description    string     `json:"description"`
	Strength       Confidence `json:"strength"`
	Instances      []string   `json:"instances"`
	TotalInstances int        `json:"total_instances"`
	KeySymbol      string     `json:"key_symbol,omitempty"`
	Snippets       []string   `json:"snippets,omitempty"`
}

// ConventionsMetrics is the observability footer.
type ConventionsMetrics struct {
	SymbolsAnalyzed           int `json:"symbols_analyzed"`
	EstimatedFileReadsAvoided int `json:"estimated_file_reads_avoided"`
	EstimatedTokensSaved      int `json:"estimated_tokens_saved"`
}

// ApplyTokenBudget drops the weakest conventions until the estimated
// token count (chars/4) fits within budget. Modifies r in place.
func ApplyTokenBudget(r *ConventionsResponse, budget int) {
	r.TokenBudget = budget
	for len(r.Conventions) > 0 && estimateJSONTokens(r) > budget {
		r.Conventions = r.Conventions[:len(r.Conventions)-1]
		r.Truncated = true
	}
}

// BuildConventionsSummary assembles a one-sentence summary from the
// 3 most type-diverse conventions — those whose instances name the
// most domain types rather than file patterns.
func BuildConventionsSummary(r *ConventionsResponse) {
	if len(r.Conventions) == 0 {
		return
	}

	type ranked struct {
		index     int
		typeNames int
	}
	ranks := make([]ranked, len(r.Conventions))
	for i, c := range r.Conventions {
		ranks[i] = ranked{index: i, typeNames: countTypeNames(c.Instances)}
	}
	sort.SliceStable(ranks, func(i, j int) bool {
		return ranks[i].typeNames > ranks[j].typeNames
	})

	n := 3
	if len(ranks) < n {
		n = len(ranks)
	}
	descs := make([]string, n)
	for i := 0; i < n; i++ {
		descs[i] = strings.TrimRight(r.Conventions[ranks[i].index].Description, ".")
	}
	r.Summary = strings.Join(descs, "; ") + "."
}

func countTypeNames(instances []string) int {
	count := 0
	for _, name := range instances {
		if !strings.Contains(name, ".") {
			count++
		}
	}
	return count
}

// MarshalConventions renders a ConventionsResponse with the same
// normalization + pretty-print contract as MarshalGraph.
func MarshalConventions(r ConventionsResponse) ([]byte, error) {
	normalizeConventionsResponse(&r)
	return marshalPretty(r)
}

// MarshalConventionsCompact is MarshalConventions's compact-JSON
// sibling for MCP transport.
func MarshalConventionsCompact(r ConventionsResponse) ([]byte, error) {
	normalizeConventionsResponse(&r)
	return marshalCompact(r)
}

func normalizeConventionsResponse(r *ConventionsResponse) {
	if r.KeySymbols == nil {
		r.KeySymbols = []KeySymbolEntry{}
	}
	for i := range r.KeySymbols {
		if r.KeySymbols[i].Callers == nil {
			r.KeySymbols[i].Callers = []string{}
		}
	}
	if r.Conventions == nil {
		r.Conventions = []ConventionEntry{}
	}
	for i := range r.Conventions {
		if r.Conventions[i].Instances == nil {
			r.Conventions[i].Instances = []string{}
		}
	}
	if r.NextSteps == nil {
		r.NextSteps = []NextStep{}
	}
}
