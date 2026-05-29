// Package erb extracts Stimulus, Turbo, and Turbo Frame references from
// ERB/HTML template files using regex-based parsing. This is a RawExtractor —
// it operates on source bytes directly without tree-sitter.
//
// Extracted references:
//   - data-controller → calls edge to Stimulus controller
//   - data-action → calls edge to controller#method
//   - data-*-target → calls edge to controller target
//   - data-*-outlet → calls edge to outlet controller
//   - turbo-stream-from / turbo_stream_from → calls edge (subscriber)
//   - turbo-frame id → symbol (frame identifier)
package erb

import (
	"bytes"
	"path"
	"regexp"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

// Extractor is the ERB/HTML implementation of extract.Extractor + extract.RawExtractor.
type Extractor struct{}

func (Extractor) Grammar() *sitter.Language { return nil }
func (Extractor) Language() string          { return "erb" }
func (Extractor) Extensions() []string      { return []string{".erb"} }
func (Extractor) Tier() extract.Tier        { return extract.TierBasic }

func (Extractor) Extract(_ *sitter.Tree, source []byte, filePath string, emit extract.Emitter) error {
	return Extractor{}.ExtractRaw(source, filePath, emit)
}

func (Extractor) ExtractRaw(source []byte, filePath string, emit extract.Emitter) error {
	w := &walker{source: source, filePath: filePath, emit: emit}
	return w.walk()
}

func init() { extract.Register(Extractor{}) }

// --- regex patterns ---

var (
	// data-controller="name1 name2"
	reDataController = regexp.MustCompile(`data-controller="([^"]+)"`)

	// data-action="event->controller#method event->controller#method"
	reDataAction = regexp.MustCompile(`data-action="([^"]+)"`)

	// data-<controller>-target="name"
	reDataTarget = regexp.MustCompile(`data-([a-z0-9-]+)-target="([^"]+)"`)

	// data-<controller>-outlet="selector"
	reDataOutlet = regexp.MustCompile(`data-([a-z0-9-]+)-outlet="([^"]+)"`)

	// <turbo-frame id="name"> or turbo_frame_tag "name"
	reTurboFrameTag    = regexp.MustCompile(`<turbo-frame[^>]+id="([^"]+)"`)
	reTurboFrameHelper = regexp.MustCompile(`turbo_frame_tag\s+["']([^"']+)["']`)

	// <turbo-stream-from> or turbo_stream_from
	reTurboStreamFrom   = regexp.MustCompile(`<turbo-stream-from[^>]+signed-stream-name="([^"]+)"`)
	reTurboStreamHelper = regexp.MustCompile(`turbo_stream_from\s+[@:]?([a-zA-Z0-9_.]+)`)

	// Individual action parsing: event->controller#method
	reStimulusAction = regexp.MustCompile(`(?:([a-z]+)->)?([a-z0-9-]+(?:--[a-z0-9-]+)*)#([a-zA-Z0-9_]+)`)

	// ERB tag inner Ruby: <% ... %>, <%= ... %>, with optional trim markers.
	// Non-greedy so multiple tags on one line are captured separately. A
	// leading '#' marks a comment tag, which extractTemplateRuby skips.
	reErbTag = regexp.MustCompile(`<%([=#]?[-]?.*?)[-]?%>`)

	// render "users/profile" / render partial: "users/profile" /
	// render(template: "x"). Captures the first partial-path string literal.
	reRender = regexp.MustCompile(`\brender\b\s*\(?\s*(?:partial:|template:|layout:)?\s*["']([\w./-]+)["']`)

	// t(".key") / t("a.b.c") / translate("...") / I18n.t("..."). Keys with
	// interpolation (#{...}) are skipped — the literal isn't a stable key.
	reI18n = regexp.MustCompile(`\b(?:I18n\.)?(?:t|translate)\b\s*\(?\s*["']([a-zA-Z0-9_.]+)["']`)

	// A method-call identifier in stripped tag content: a lowercase-leading
	// name not preceded by '.', '@', ':' or '$' (so receivers, ivars, and
	// symbols are excluded), optionally ending in '?'/'!'.
	reHelperName = regexp.MustCompile(`(?:^|[^.@:$\w])([a-z_][a-zA-Z0-9_]*[!?]?)`)

	// String / symbol literals stripped before helper-name scanning so words
	// inside copy ("edit profile") aren't mistaken for method calls.
	reRubyLiteral = regexp.MustCompile(`"[^"]*"|'[^']*'|:[a-zA-Z_][a-zA-Z0-9_]*`)
)

// erbHelperSkip holds Ruby keywords and the helpers handled by dedicated
// passes (render / t / translate) so they aren't re-emitted as bare calls.
var erbHelperSkip = map[string]bool{
	"if": true, "unless": true, "while": true, "until": true, "for": true,
	"in": true, "do": true, "end": true, "then": true, "else": true,
	"elsif": true, "case": true, "when": true, "begin": true, "rescue": true,
	"ensure": true, "def": true, "class": true, "module": true, "nil": true,
	"true": true, "false": true, "self": true, "and": true, "or": true,
	"not": true, "return": true, "yield": true, "break": true, "next": true,
	"super": true, "render": true, "t": true, "translate": true,
	// Turbo/Stimulus DSL helpers handled by the dedicated passes above.
	"turbo_stream_from": true, "turbo_frame_tag": true,
}

type walker struct {
	source   []byte
	filePath string
	emit     extract.Emitter
}

func (w *walker) walk() error {
	if err := w.emitPartialSymbol(); err != nil {
		return err
	}

	lines := bytes.Split(w.source, []byte("\n"))

	for i, rawLine := range lines {
		lineNum := i + 1
		line := string(rawLine)

		if err := w.extractControllers(line, lineNum); err != nil {
			return err
		}
		if err := w.extractActions(line, lineNum); err != nil {
			return err
		}
		if err := w.extractTargets(line, lineNum); err != nil {
			return err
		}
		if err := w.extractOutlets(line, lineNum); err != nil {
			return err
		}
		if err := w.extractTurboFrames(line, lineNum); err != nil {
			return err
		}
		if err := w.extractTurboStreams(line, lineNum); err != nil {
			return err
		}
		if err := w.extractTemplateRuby(line, lineNum); err != nil {
			return err
		}
	}
	return nil
}

func (w *walker) extractControllers(line string, lineNum int) error {
	for _, match := range reDataController.FindAllStringSubmatch(line, -1) {
		controllers := strings.Fields(match[1])
		for _, name := range controllers {
			target := extract.StimulusControllerQualified(name)
			ln := lineNum
			if err := w.emit.Edge(extract.EmittedEdge{
				SourceQualified: w.filePath,
				TargetQualified: target,
				Kind:            model.EdgeCalls,
				Line:            &ln,
				Confidence:      extract.ConfidenceConvention,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *walker) extractActions(line string, lineNum int) error {
	for _, match := range reDataAction.FindAllStringSubmatch(line, -1) {
		actions := strings.Fields(match[1])
		for _, action := range actions {
			parts := reStimulusAction.FindStringSubmatch(action)
			if parts == nil {
				continue
			}
			controllerName := parts[2]
			methodName := parts[3]
			target := extract.StimulusControllerQualified(controllerName) + "." + methodName
			ln := lineNum
			if err := w.emit.Edge(extract.EmittedEdge{
				SourceQualified: w.filePath,
				TargetQualified: target,
				Kind:            model.EdgeCalls,
				Line:            &ln,
				Confidence:      extract.ConfidenceConvention,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *walker) extractTargets(line string, lineNum int) error {
	for _, match := range reDataTarget.FindAllStringSubmatch(line, -1) {
		controllerName := match[1]
		targetName := match[2]
		target := extract.StimulusControllerQualified(controllerName) + ".target:" + targetName
		ln := lineNum
		if err := w.emit.Edge(extract.EmittedEdge{
			SourceQualified: w.filePath,
			TargetQualified: target,
			Kind:            model.EdgeCalls,
			Line:            &ln,
			Confidence:      extract.ConfidenceConvention,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (w *walker) extractOutlets(line string, lineNum int) error {
	for _, match := range reDataOutlet.FindAllStringSubmatch(line, -1) {
		// data-[owner]-[outlet-controller]-outlet="selector"
		// match[1] is the full middle segment which includes the outlet controller name.
		// Without knowing the owning controller's boundary, we treat the full
		// captured name as the outlet controller reference.
		outletName := match[1]
		target := extract.StimulusControllerQualified(outletName)
		ln := lineNum
		if err := w.emit.Edge(extract.EmittedEdge{
			SourceQualified: w.filePath,
			TargetQualified: target,
			Kind:            model.EdgeCalls,
			Line:            &ln,
			Confidence:      extract.ConfidenceConvention,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (w *walker) extractTurboFrames(line string, lineNum int) error {
	for _, match := range reTurboFrameTag.FindAllStringSubmatch(line, -1) {
		if err := w.emit.Symbol(extract.EmittedSymbol{
			Name:       match[1],
			Qualified:  extract.PrefixTurboFrame + match[1],
			Kind:       model.KindConstant,
			Visibility: "public",
			LineStart:  lineNum,
			LineEnd:    lineNum,
		}); err != nil {
			return err
		}
	}
	for _, match := range reTurboFrameHelper.FindAllStringSubmatch(line, -1) {
		if err := w.emit.Symbol(extract.EmittedSymbol{
			Name:       match[1],
			Qualified:  extract.PrefixTurboFrame + match[1],
			Kind:       model.KindConstant,
			Visibility: "public",
			LineStart:  lineNum,
			LineEnd:    lineNum,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (w *walker) extractTurboStreams(line string, lineNum int) error {
	for _, match := range reTurboStreamFrom.FindAllStringSubmatch(line, -1) {
		ln := lineNum
		if err := w.emit.Edge(extract.EmittedEdge{
			SourceQualified: w.filePath,
			TargetQualified: extract.PrefixTurboChannel + match[1],
			Kind:            model.EdgeCalls,
			Line:            &ln,
			Confidence:      0.8,
		}); err != nil {
			return err
		}
	}
	for _, match := range reTurboStreamHelper.FindAllStringSubmatch(line, -1) {
		ln := lineNum
		if err := w.emit.Edge(extract.EmittedEdge{
			SourceQualified: w.filePath,
			TargetQualified: extract.PrefixTurboChannel + match[1],
			Kind:            model.EdgeCalls,
			Line:            &ln,
			Confidence:      0.8,
		}); err != nil {
			return err
		}
	}
	return nil
}

// extractTemplateRuby pulls Ruby-level references out of the embedded code in
// each ERB tag on a line: rendered partials, i18n keys, and bare helper
// calls. These open up the view layer to graph queries ("who renders this
// partial", "which view uses this i18n key", "where is this helper used")
// that the Stimulus/Turbo passes don't cover.
func (w *walker) extractTemplateRuby(line string, lineNum int) error {
	for _, tag := range reErbTag.FindAllStringSubmatch(line, -1) {
		inner := tag[1]
		if strings.HasPrefix(strings.TrimSpace(inner), "#") {
			continue // comment tag
		}
		if err := w.extractRender(inner, lineNum); err != nil {
			return err
		}
		if err := w.extractI18n(inner, lineNum); err != nil {
			return err
		}
		if err := w.extractHelperCalls(inner, lineNum); err != nil {
			return err
		}
	}
	return nil
}

// extractRender emits a calls edge to each rendered partial. The target is the
// partial's render path: absolute when the literal contains a slash, otherwise
// resolved against the rendering view's directory (Rails' relative-partial
// rule). The matching partial file emits a symbol with the same qualified name
// via emitPartialSymbol, so the edge resolves.
func (w *walker) extractRender(inner string, lineNum int) error {
	for _, m := range reRender.FindAllStringSubmatch(inner, -1) {
		ln := lineNum
		if err := w.emit.Edge(extract.EmittedEdge{
			SourceQualified: w.filePath,
			TargetQualified: partialRenderTarget(m[1], w.filePath),
			Kind:            model.EdgeCalls,
			Line:            &ln,
			Confidence:      extract.ConfidenceConvention,
		}); err != nil {
			return err
		}
	}
	return nil
}

// extractI18n emits a symbol for each translation key a view references, so
// semantic search can surface the view behind a given piece of copy. Relative
// keys (".title") are expanded against the view's lazy-lookup scope.
func (w *walker) extractI18n(inner string, lineNum int) error {
	for _, m := range reI18n.FindAllStringSubmatch(inner, -1) {
		key := i18nKey(m[1], w.filePath)
		if err := w.emit.Symbol(extract.EmittedSymbol{
			Name:       key,
			Qualified:  extract.PrefixI18n + key,
			Kind:       model.KindConstant,
			Visibility: "public",
			LineStart:  lineNum,
			LineEnd:    lineNum,
		}); err != nil {
			return err
		}
	}
	return nil
}

// extractHelperCalls emits a self-call edge for each bare, receiverless method
// call in a tag — the view-helper / controller-concern methods (current_user,
// current_currency, link_to, …) that the resolver binds back to their
// definition. String and symbol literals are stripped first so words inside
// copy aren't mistaken for calls; the resolver drops any name that matches no
// defined symbol, so unknown framework helpers fall away.
func (w *walker) extractHelperCalls(inner string, lineNum int) error {
	stripped := reRubyLiteral.ReplaceAllString(inner, " ")
	seen := map[string]bool{}
	for _, m := range reHelperName.FindAllStringSubmatch(stripped, -1) {
		name := m[1]
		if erbHelperSkip[name] || seen[name] {
			continue
		}
		seen[name] = true
		ln := lineNum
		if err := w.emit.Edge(extract.EmittedEdge{
			SourceQualified: w.filePath,
			TargetQualified: "self." + name,
			Kind:            model.EdgeCalls,
			Line:            &ln,
			Confidence:      extract.ConfidenceConvention,
		}); err != nil {
			return err
		}
	}
	return nil
}

// emitPartialSymbol emits a symbol for a partial template (basename starting
// with "_") qualified by its render path, so `render`-edge targets resolve to
// it. Non-partial templates emit nothing here.
func (w *walker) emitPartialSymbol() error {
	name := partialSelfPath(w.filePath)
	if name == "" {
		return nil
	}
	return w.emit.Symbol(extract.EmittedSymbol{
		Name:       path.Base(name),
		Qualified:  extract.PrefixPartial + name,
		Kind:       model.KindConstant,
		Visibility: "public",
		LineStart:  1,
		LineEnd:    1,
	})
}

// viewDir returns the directory of a view file relative to the views root:
// "app/views/users/show.html.erb" → "users". Empty when no "views/" segment
// is present (the render path can't be anchored).
func viewDir(filePath string) string {
	dir := path.Dir(filePath)
	if i := strings.LastIndex(dir, "views/"); i >= 0 {
		return dir[i+len("views/"):]
	}
	return ""
}

// templateBaseName returns a view file's logical name: the basename with the
// leading "_" (partials) and all extensions stripped. "_profile.html.erb" →
// "profile".
func templateBaseName(filePath string) string {
	base := strings.TrimPrefix(path.Base(filePath), "_")
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	return base
}

// partialSelfPath returns the render path of a partial file, or "" when the
// file is not a partial. "app/views/users/_profile.html.erb" → "users/profile".
func partialSelfPath(filePath string) string {
	if !strings.HasPrefix(path.Base(filePath), "_") {
		return ""
	}
	name := templateBaseName(filePath)
	if dir := viewDir(filePath); dir != "" {
		return dir + "/" + name
	}
	return name
}

// partialRenderTarget builds the qualified render target for a `render`
// argument. A path with a slash is absolute; a bare name is resolved against
// the rendering view's directory, matching Rails' relative-partial lookup.
func partialRenderTarget(arg, filePath string) string {
	if strings.Contains(arg, "/") {
		return extract.PrefixPartial + arg
	}
	if dir := viewDir(filePath); dir != "" {
		return extract.PrefixPartial + dir + "/" + arg
	}
	return extract.PrefixPartial + arg
}

// i18nKey expands a lazy-lookup key (leading ".") against the view's scope —
// "app/views/users/show.html.erb" with ".title" → "users.show.title" — and
// returns absolute keys unchanged.
func i18nKey(raw, filePath string) string {
	if !strings.HasPrefix(raw, ".") {
		return raw
	}
	scope := templateBaseName(filePath)
	if dir := viewDir(filePath); dir != "" {
		scope = strings.ReplaceAll(dir, "/", ".") + "." + scope
	}
	return scope + raw
}
