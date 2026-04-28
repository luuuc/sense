package mcpio

const DefaultTokenBudget = 4000

// ConventionsResponse is the shape of the sense.conventions tool's reply
// and the `sense conventions --json` CLI output.
type ConventionsResponse struct {
	Conventions  []ConventionEntry    `json:"conventions"`
	Truncated    bool                 `json:"truncated,omitempty"`
	TokenBudget  int                  `json:"token_budget,omitempty"`
	SenseMetrics ConventionsMetrics   `json:"sense_metrics"`
	NextSteps    []NextStep           `json:"next_steps"`
}

// ConventionEntry is a single detected convention in the wire response.
type ConventionEntry struct {
	Category       string     `json:"category"`
	Description    string     `json:"description"`
	Strength       Confidence `json:"strength"`
	Instances      []string   `json:"instances"`
	TotalInstances int        `json:"total_instances"`
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
	for len(r.Conventions) > 0 && estimateTokens(r) > budget {
		r.Conventions = r.Conventions[:len(r.Conventions)-1]
		r.Truncated = true
	}
}

func estimateTokens(r *ConventionsResponse) int {
	out, err := marshalPretty(r)
	if err != nil {
		return 0
	}
	return len(out) / 4
}

// MarshalConventions renders a ConventionsResponse with the same
// normalization + pretty-print contract as MarshalGraph.
func MarshalConventions(r ConventionsResponse) ([]byte, error) {
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
	return marshalPretty(r)
}
