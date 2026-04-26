package mcpio

// ConventionsResponse is the shape of the sense.conventions tool's reply
// and the `sense conventions --json` CLI output.
type ConventionsResponse struct {
	Conventions  []ConventionEntry    `json:"conventions"`
	SenseMetrics ConventionsMetrics   `json:"sense_metrics"`
	NextSteps    []NextStep           `json:"next_steps"`
}

// ConventionEntry is a single detected convention in the wire response.
type ConventionEntry struct {
	Category    string     `json:"category"`
	Description string     `json:"description"`
	Instances   int        `json:"instances"`
	Total       int        `json:"total"`
	Strength    Confidence `json:"strength"`
}

// ConventionsMetrics is the observability footer.
type ConventionsMetrics struct {
	SymbolsAnalyzed           int `json:"symbols_analyzed"`
	EstimatedFileReadsAvoided int `json:"estimated_file_reads_avoided"`
	EstimatedTokensSaved      int `json:"estimated_tokens_saved"`
}

// MarshalConventions renders a ConventionsResponse with the same
// normalization + pretty-print contract as MarshalGraph.
func MarshalConventions(r ConventionsResponse) ([]byte, error) {
	if r.Conventions == nil {
		r.Conventions = []ConventionEntry{}
	}
	if r.NextSteps == nil {
		r.NextSteps = []NextStep{}
	}
	return marshalPretty(r)
}
