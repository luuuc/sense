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
	"strconv"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/extract/ruby"
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
	w := &walker{source: source, filePath: filePath, emit: emit, embedSeen: map[string]bool{}}
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

	// render @posts / render collection: @posts / render(@posts) — an implicit
	// collection render with no explicit partial. Captures the ivar name; the
	// partial path is derived by Rails' to_partial_path convention.
	reRenderCollection = regexp.MustCompile(`\brender\b\s*\(?\s*(?:collection:\s*)?@([a-z_][a-zA-Z0-9_]*)`)

	// An explicit partial:/template: keyword means the render path is given
	// literally (handled by reRender); the collection convention must not also
	// guess a path from the ivar, which would be a phantom.
	reRenderPartialKw = regexp.MustCompile(`\b(?:partial|template):`)

	// form_with / form_for context, and the two shapes that name a model:
	// `form_for @order` (positional) and `model: @order` (keyword).
	reFormTag    = regexp.MustCompile(`\bform_(?:with|for)\b`)
	reFormForArg = regexp.MustCompile(`\bform_for\s*\(?\s*@([a-z_][a-zA-Z0-9_]*)`)
	reModelArg   = regexp.MustCompile(`\bmodel:\s*@([a-z_][a-zA-Z0-9_]*)`)

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
	// ActionView / ActionController context accessors. These are framework
	// objects, never application methods, so a bare reference (request.path,
	// params[:id]) must not emit a self-call that the resolver then binds to a
	// coincidental same-named app symbol (e.g. a test fake's #request). The
	// receiver-unknown analogue for the Ruby fragment walker is
	// coreNoiseMethods in internal/extract/ruby/ruby.go.
	"request": true, "response": true, "params": true, "session": true,
	"cookies": true, "flash": true, "controller": true,
}

type walker struct {
	source   []byte
	filePath string
	emit     extract.Emitter
	// embedSeen is the dedup key set shared by the two passes that parse a
	// tag's embedded Ruby — the regex helper pass and the tree-sitter walker.
	// A call both find collapses to one edge (see dedupEmitter).
	embedSeen map[string]bool
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

// dedupEmitter wraps the real emitter for the two passes that parse a tag's
// embedded Ruby — the regex helper pass and the tree-sitter fragment walker.
// The two overlap on plain receiverless calls; dedupEmitter collapses a call
// both find (keyed by target/kind/line — the source is always this file) so it
// is emitted once. It also drops a `self.<name>` edge whose name is in
// erbHelperSkip: a Ruby keyword the fragment walker mis-parsed as a bare call
// in a split tag (`<% end %>` → `self.end`), or a helper that has a dedicated
// cross-language pass (render → partial:, turbo_stream_from → turbo-channel:,
// …). Symbols pass through untouched.
type dedupEmitter struct {
	inner extract.Emitter
	seen  map[string]bool
}

func (d dedupEmitter) Symbol(s extract.EmittedSymbol) error { return d.inner.Symbol(s) }

func (d dedupEmitter) Edge(e extract.EmittedEdge) error {
	name, isSelf := strings.CutPrefix(e.TargetQualified, "self.")
	if isSelf && erbHelperSkip[name] {
		return nil
	}
	// A *_path / *_url reference is a Rails route helper: retarget it at the
	// reserved route: symbol so it chains view → route-helper → controller and
	// can never phantom-match an application method of the same name (e.g. a
	// model's own verifications_path). Applies whether the walker emitted it as
	// a receiverless self-call (self.orders_path) or a bare unresolved call.
	if isRouteHelperName(name) {
		e.TargetQualified = extract.PrefixRoute + name
		e.Confidence = extract.ConfidenceConvention
	}
	line := 0
	if e.Line != nil {
		line = *e.Line
	}
	key := e.TargetQualified + "\x00" + string(e.Kind) + "\x00" + strconv.Itoa(line)
	if d.seen[key] {
		return nil
	}
	d.seen[key] = true
	return d.inner.Edge(e)
}

// isRouteHelperName reports whether a target name is a bare Rails route-helper
// identifier — an identifier ending in _path or _url with no receiver dot.
// `orders_path` qualifies; `Money.orders_path` (a real method on a constant)
// does not, so a genuine method call is never mistaken for a route helper.
func isRouteHelperName(s string) bool {
	if !strings.HasSuffix(s, "_path") && !strings.HasSuffix(s, "_url") {
		return false
	}
	for _, r := range s {
		valid := r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !valid {
			return false
		}
	}
	return true
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
		if err := w.extractRenderCollection(inner, lineNum); err != nil {
			return err
		}
		if err := w.extractFormModel(inner, lineNum); err != nil {
			return err
		}
		if err := w.extractI18n(inner, lineNum); err != nil {
			return err
		}
		// Helper pass first: it claims the bare receiverless calls (and
		// receiver-position helpers the walker can't see) at convention
		// confidence; the embedded walker then adds the calls the regex misses
		// (chains, blocks, args), skipping anything the helper pass already
		// recorded via the shared dedup set.
		if err := w.extractHelperCalls(inner, lineNum); err != nil {
			return err
		}
		if err := w.extractEmbeddedRuby(inner, lineNum); err != nil {
			return err
		}
	}
	return nil
}

// extractEmbeddedRuby parses the inner Ruby of one ERB tag with the Ruby
// grammar itself, so receivers, method chains, and block bodies resolve —
// `@cart.items.each { |i| i.listing.title }` emits its chain of calls, none of
// which the receiver-stripping regex pass below can see. Edges are routed
// through the dedup buffer, so the plain calls this and the regex pass both
// find collapse to one; the regex pass survives only for the receiver-position
// helpers (`current_user` in `current_user.email`) the walker doesn't emit.
//
// lineNum-1 is the line offset: the tag's content lives on ERB line lineNum,
// so a fragment-line-1 call maps back to lineNum.
func (w *walker) extractEmbeddedRuby(inner string, lineNum int) error {
	code := embeddedRubyCode(inner)
	if code == "" {
		return nil
	}
	emit := dedupEmitter{inner: w.emit, seen: w.embedSeen}
	return ruby.ExtractEmbeddedCalls([]byte(code), lineNum-1, w.filePath, emit)
}

// embeddedRubyCode strips a tag's ERB output/trim markers to leave parseable
// Ruby. The leading `=` (output, `<%=`) or flush `-` (whitespace trim, `<%-`)
// marker is dropped; a `-` that follows whitespace is a unary minus in code
// (`<% -1 %>`), not a marker, so the check runs on the untrimmed input.
func embeddedRubyCode(inner string) string {
	s := inner
	switch {
	case strings.HasPrefix(s, "="):
		s = s[1:]
	case strings.HasPrefix(s, "-"):
		s = s[1:]
	}
	return strings.TrimSpace(s)
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

// extractRenderCollection emits a calls edge for an implicit collection render
// (`render @posts`, `render collection: @posts`) to the partial that Rails'
// to_partial_path convention resolves it to: `posts/_post`, i.e. the render
// path `posts/post`. The directory is the (plural) ivar name; the partial name
// is its singular form. Only plural ivars are handled — a singular ivar
// (`render @post`) would need pluralization to build the directory, and
// guessing wrong is a phantom, so it is skipped. A render with an explicit
// partial:/template: keyword is left to extractRender; the convention does not
// override a literal path.
func (w *walker) extractRenderCollection(inner string, lineNum int) error {
	if reRenderPartialKw.MatchString(inner) {
		return nil
	}
	for _, m := range reRenderCollection.FindAllStringSubmatch(inner, -1) {
		ivar := m[1]
		singular := ruby.Singularize(ivar)
		if singular == ivar {
			continue // singular ivar — cannot build the plural directory safely
		}
		ln := lineNum
		if err := w.emit.Edge(extract.EmittedEdge{
			SourceQualified: w.filePath,
			TargetQualified: extract.PrefixPartial + ivar + "/" + singular,
			Kind:            model.EdgeCalls,
			Line:            &ln,
			Confidence:      extract.ConfidenceConvention,
		}); err != nil {
			return err
		}
	}
	return nil
}

// extractFormModel emits a references edge from the view to the model a form
// binds to: `form_with model: @order` / `form_for @order` → `Order`, by
// classifying the ivar name. The edge resolves only if that model class is
// indexed (unresolved edges are dropped at write time), so a form for a model
// that does not exist emits nothing downstream. Both the positional
// (`form_for @x`) and keyword (`model: @x`) shapes are covered; the keyword
// scan is gated on form context so a stray `model:` elsewhere can't match.
func (w *walker) extractFormModel(inner string, lineNum int) error {
	if !reFormTag.MatchString(inner) {
		return nil
	}
	seen := map[string]bool{}
	emitModel := func(ivar string) error {
		class := ruby.Classify(ivar) // never empty: the ivar regex guarantees a name
		if seen[class] {
			return nil
		}
		seen[class] = true
		ln := lineNum
		return w.emit.Edge(extract.EmittedEdge{
			SourceQualified: w.filePath,
			TargetQualified: class,
			Kind:            model.EdgeReferences,
			Line:            &ln,
			Confidence:      extract.ConfidenceConvention,
		})
	}
	for _, m := range reFormForArg.FindAllStringSubmatch(inner, -1) {
		if err := emitModel(m[1]); err != nil {
			return err
		}
	}
	for _, m := range reModelArg.FindAllStringSubmatch(inner, -1) {
		if err := emitModel(m[1]); err != nil {
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
	emit := dedupEmitter{inner: w.emit, seen: w.embedSeen}
	seen := map[string]bool{}
	for _, m := range reHelperName.FindAllStringSubmatch(stripped, -1) {
		name := m[1]
		if erbHelperSkip[name] || seen[name] {
			continue
		}
		seen[name] = true
		ln := lineNum
		if err := emit.Edge(extract.EmittedEdge{
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
