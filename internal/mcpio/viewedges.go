package mcpio

import "strings"

// view_edges is the per-target honesty signal that replaces the old blanket
// "view & Hotwire dispatch is a blind spot" hedge. Sense DOES extract and
// resolve view-layer edges — ERB/Hotwire dispatch (data-controller /
// data-action / data-*-target / data-*-outlet), render partials, i18n keys,
// and embedded-Ruby helper/route calls — so a method reached only from a view
// is not invisible. The signal reports that fact at the level of the queried
// subject:
//
//   - "present" — at least one incoming edge originates in a view template.
//     The subject IS reached from the view layer, and that edge is shown in
//     the response.
//   - "none" — the subject is one whose liveness is plausibly view-dispatched
//     (a Rails controller/helper, or a Stimulus JS/TS controller) and no view
//     edge reaches it. This is an affirmative "we looked and the view layer
//     does not reach it," not a hedge.
//   - "" (omitted) — the question is not salient for this subject (a model,
//     service, or non-Ruby/JS symbol), so no claim is made either way.
//
// The distinction matters: "present" is reported for any subject a view
// reaches, but "none" is scoped to subjects where view-dispatch is a live
// question, so models, services, and Go code are not sprayed with a noisy
// "view_edges": "none" on every response.
const (
	viewEdgesPresent = "present"
	viewEdgesNone    = "none"
)

// isViewTemplate reports whether a file path is a view template — the source
// side of a Hotwire/Stimulus or embedded-Ruby view edge.
func isViewTemplate(file string) bool {
	return strings.HasSuffix(file, ".erb")
}

// viewReachQuestionRelevant reports whether view-dispatch is a live question
// for a subject defined in this file — i.e. whether a "none" answer carries
// information. True for Rails controllers and helpers (often reached only via
// routing or view rendering) and for Stimulus JS/TS controllers (reached only
// via data-controller/data-action). False for models, services, libraries,
// and everything else, where "no view edge" is the unremarkable default.
func viewReachQuestionRelevant(file string) bool {
	if strings.Contains(file, "app/controllers/") || strings.Contains(file, "app/helpers/") {
		return true
	}
	if strings.Contains(file, "controllers/") &&
		(strings.HasSuffix(file, ".js") || strings.HasSuffix(file, ".ts")) {
		return true
	}
	return false
}

// viewEdgesSignal classifies a subject's view reachability. present is whether
// a view template reaches the subject directly (depth-1); subjectFile decides
// whether a non-present answer is reported as "none" or omitted. See the
// view_edges doc comment for the three-way contract.
func viewEdgesSignal(subjectFile string, present bool) string {
	if present {
		return viewEdgesPresent
	}
	if viewReachQuestionRelevant(subjectFile) {
		return viewEdgesNone
	}
	return ""
}

// anyViewTemplate reports whether any of the given files is a view template —
// the "present" input to viewEdgesSignal.
func anyViewTemplate(files []string) bool {
	for _, f := range files {
		if isViewTemplate(f) {
			return true
		}
	}
	return false
}
