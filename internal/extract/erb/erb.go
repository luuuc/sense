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
	reTurboFrameTag = regexp.MustCompile(`<turbo-frame[^>]+id="([^"]+)"`)
	reTurboFrameHelper = regexp.MustCompile(`turbo_frame_tag\s+["']([^"']+)["']`)

	// <turbo-stream-from> or turbo_stream_from
	reTurboStreamFrom = regexp.MustCompile(`<turbo-stream-from[^>]+signed-stream-name="([^"]+)"`)
	reTurboStreamHelper = regexp.MustCompile(`turbo_stream_from\s+[@:]?([a-zA-Z0-9_.]+)`)

	// Individual action parsing: event->controller#method
	reStimulusAction = regexp.MustCompile(`(?:([a-z]+)->)?([a-z0-9-]+(?:--[a-z0-9-]+)*)#([a-zA-Z0-9_]+)`)
)

type walker struct {
	source   []byte
	filePath string
	emit     extract.Emitter
}

func (w *walker) walk() error {
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

