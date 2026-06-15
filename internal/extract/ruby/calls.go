package ruby

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

// emitCall produces a calls edge for one `call` node. The target is
// resolved from the receiver when possible: `self` and implicit calls
// are emitted as `self.name` so the resolver rewrites them to the
// enclosing class; constant receivers are emitted as `Const.name` for
// exact matching; local-variable receivers are resolved via a lightweight
// intra-method type map built from `X = Class.new` assignments; method
// chains are stripped to the trailing method name. `send` /
// `public_send` / `__send__` with a literal symbol or string first
// argument is emitted with confidence 0.7; anything else in that family
// is skipped.
func (w *walker) emitCall(n *sitter.Node, source string, scope []string, localTypes, ivarTypes map[string]string) error {
	methodNode := n.ChildByFieldName("method")
	if methodNode == nil {
		return nil
	}
	methodName := extract.Text(methodNode, w.source)
	if methodName == "" {
		return nil
	}

	if handled, err := w.tryEmitEnqueueEdge(n, methodName, source, scope, localTypes, ivarTypes); handled {
		return err
	}

	switch methodName {
	case "send", "public_send", "__send__":
		target, ok := literalSendTarget(n, w.source)
		if ok {
			line := extract.Line(n.StartPosition())
			return w.emit.Edge(extract.EmittedEdge{
				SourceQualified: source,
				TargetQualified: target,
				Kind:            model.EdgeCalls,
				Line:            &line,
				Confidence:      extract.ConfidenceDynamic,
			})
		}
		// Heuristic: variable-based dynamic dispatch on self.
		recv := n.ChildByFieldName("receiver")
		if recv == nil || recv.Kind() == "self" {
			if target, conf, ok := inferSendTargetFromVariable(n, w.source); ok {
				line := extract.Line(n.StartPosition())
				return w.emit.Edge(extract.EmittedEdge{
					SourceQualified: source,
					TargetQualified: target,
					Kind:            model.EdgeCalls,
					Line:            &line,
					Confidence:      conf,
				})
			}
		}
		return nil
	}

	recv := n.ChildByFieldName("receiver")
	target, confidence := w.resolveCallTarget(recv, methodName, scope, localTypes, ivarTypes)
	if target == "" {
		return nil
	}
	return w.emitCallWithConfidence(n, source, scope, localTypes, ivarTypes, confidence)
}

// emitCallWithConfidence is emitCall's core logic with an injectable
// confidence value. Used by both production-method emission (1.0 / 0.7)
// and test-block emission (0.8).
func (w *walker) emitCallWithConfidence(n *sitter.Node, source string, scope []string, localTypes, ivarTypes map[string]string, confidence float64) error {
	methodNode := n.ChildByFieldName("method")
	if methodNode == nil {
		return nil
	}
	methodName := extract.Text(methodNode, w.source)
	if methodName == "" {
		return nil
	}

	if handled, err := w.tryEmitEnqueueEdge(n, methodName, source, scope, localTypes, ivarTypes); handled {
		return err
	}
	line := extract.Line(n.StartPosition())

	switch methodName {
	case "send", "public_send", "__send__":
		target, ok := literalSendTarget(n, w.source)
		if ok {
			return w.emit.Edge(extract.EmittedEdge{
				SourceQualified: source,
				TargetQualified: target,
				Kind:            model.EdgeCalls,
				Line:            &line,
				Confidence:      confidence,
			})
		}
		// Heuristic: variable-based dynamic dispatch on self.
		recv := n.ChildByFieldName("receiver")
		if recv == nil || recv.Kind() == "self" {
			if target, conf, ok := inferSendTargetFromVariable(n, w.source); ok {
				return w.emit.Edge(extract.EmittedEdge{
					SourceQualified: source,
					TargetQualified: target,
					Kind:            model.EdgeCalls,
					Line:            &line,
					Confidence:      conf,
				})
			}
		}
		return nil
	}

	recv := n.ChildByFieldName("receiver")
	target, _ := w.resolveCallTarget(recv, methodName, scope, localTypes, ivarTypes)
	if target == "" {
		return nil
	}
	if err := w.emit.Edge(extract.EmittedEdge{
		SourceQualified: source,
		TargetQualified: target,
		Kind:            model.EdgeCalls,
		Line:            &line,
		Confidence:      confidence,
	}); err != nil {
		return err
	}

	return w.walkBlockCalls(n, source, scope, localTypes, ivarTypes)
}

// walkBlockCalls walks a call's block body (`foo(x) do |y| ... end`) for
// nested call edges. Block parameter types are inferred from the receiver's
// collection type when possible; when inference is not possible (non-
// collection method, destructuring params, unknown receiver type) the block
// is still walked so calls inside are not lost. A call with no block is a
// no-op.
func (w *walker) walkBlockCalls(n *sitter.Node, source string, scope []string, localTypes, ivarTypes map[string]string) error {
	block := n.ChildByFieldName("block")
	if block == nil {
		return nil
	}
	paramTypes := w.inferBlockParamTypes(n, scope, localTypes, ivarTypes)
	blockTypes := localTypes
	if paramTypes != nil {
		blockTypes = mergeMaps(localTypes, paramTypes)
	}
	return extract.WalkNamedDescendants(block, "call", func(c *sitter.Node) error {
		if isInsideNestedBlock(c, block) {
			return nil
		}
		return w.emitCall(c, source, scope, blockTypes, ivarTypes)
	})
}

// coreNoiseMethods are ubiquitous core-Ruby / ActiveSupport methods
// whose bare name (receiver type unknown) matches far too many unrelated
// symbols. When the receiver type cannot be inferred we drop the call
// edge rather than let the resolver's unqualified fallback bind it to an
// arbitrary same-named method elsewhere — e.g. `count.zero?` resolving
// to a `Money.zero` singleton. A missing low-confidence edge is cheaper
// than a false caller that inflates blast radius. The ERB analogue, for
// framework context accessors referenced bare in a template, is
// erbHelperSkip in internal/extract/erb/erb.go.
var coreNoiseMethods = map[string]bool{
	"zero?": true, "positive?": true, "negative?": true, "nonzero?": true,
	"present?": true, "blank?": true, "nil?": true, "empty?": true,
	"to_s": true, "to_i": true, "to_a": true, "to_h": true, "to_sym": true,
	"freeze": true, "dup": true, "clone": true, "presence": true, "call": true,
	// Object/reflection methods: a bare `x.is_a?` / `x.respond_to?` on an
	// unknown receiver otherwise binds to a coincidental same-named app symbol
	// (e.g. a test fake's `#is_a?`). Never a meaningful caller edge.
	"is_a?": true, "kind_of?": true, "instance_of?": true, "respond_to?": true,
	"frozen?": true, "itself": true, "inspect": true, "object_id": true,
}

// unresolvedCall is the emit decision for a call whose receiver type is
// unknown: drop ubiquitous core-method names, otherwise emit the bare
// name at ConfidenceUnresolved for the resolver's unqualified fallback.
func unresolvedCall(methodName string) (string, float64) {
	if coreNoiseMethods[methodName] {
		return "", 0
	}
	return methodName, extract.ConfidenceUnresolved
}

// resolveCallTarget decides what target string to emit for a call node.
// It returns the target string and the confidence level.
func (w *walker) resolveCallTarget(recv *sitter.Node, methodName string, scope []string, localTypes, ivarTypes map[string]string) (string, float64) {
	if recv == nil {
		return "self." + methodName, 1.0
	}

	switch recv.Kind() {
	case "self":
		return "self." + methodName, 1.0
	case "constant", "scope_resolution":
		if recvText := strings.TrimSpace(extract.Text(recv, w.source)); recvText != "" {
			return recvText + "." + methodName, 1.0
		}
	case "identifier":
		name := extract.Text(recv, w.source)
		if typ, ok := localTypes[name]; ok {
			return typ + "#" + methodName, extract.ConfidenceDynamic
		}
		return unresolvedCall(methodName)
	case "instance_variable":
		name := extract.Text(recv, w.source)
		if typ, ok := localTypes[name]; ok {
			return typ + "#" + methodName, extract.ConfidenceDynamic
		}
		if typ, ok := ivarTypes[name]; ok {
			return typ + "#" + methodName, extract.ConfidenceDynamic
		}
		return unresolvedCall(methodName)
	case "call":
		// Method chain — strip to the trailing method unless the inner
		// call is `.new` on a constant or `self`, in which case we can
		// infer the result type is an instance of that class.
		if typ := typeFromNewCall(recv, w.source, scope); typ != "" {
			return typ + "#" + methodName, extract.ConfidenceDynamic
		}
		// Try to resolve multi-hop chains via return-type map.
		if typ := w.resolveChainReceiver(recv, scope, localTypes, 1); typ != "" {
			return typ + "#" + methodName, extract.ConfidenceDynamic
		}
		return unresolvedCall(methodName)
	}

	return unresolvedCall(methodName)
}

// resolveChainReceiver recursively resolves a call-chain receiver to a type
// by looking up local-variable types and method return types. It caps at 3
// hops to avoid exponential lookups and absurd qualified names.
func (w *walker) resolveChainReceiver(recv *sitter.Node, scope []string, localTypes map[string]string, depth int) string {
	if depth > 3 || recv == nil || recv.Kind() != "call" {
		return ""
	}
	methodNode := recv.ChildByFieldName("method")
	if methodNode == nil {
		return ""
	}
	methodName := extract.Text(methodNode, w.source)
	innerRecv := recv.ChildByFieldName("receiver")

	recvType := w.chainReceiverType(innerRecv, scope, localTypes, depth)
	if recvType == "" {
		return ""
	}
	if ret, ok := w.returnTypes[recvType+"#"+methodName]; ok {
		return ret
	}
	return ""
}

// chainReceiverType resolves the type of a chain receiver's inner receiver,
// the object the chained method is invoked on. The three mutually-exclusive
// cases mirror resolveCallTarget: a typed local variable, the enclosing
// class scope (implicit/`self`), or a recursively-resolved inner chain.
// Returns "" when the type cannot be determined.
func (w *walker) chainReceiverType(innerRecv *sitter.Node, scope []string, localTypes map[string]string, depth int) string {
	switch {
	case innerRecv != nil && innerRecv.Kind() == "identifier":
		return localTypes[extract.Text(innerRecv, w.source)]
	case innerRecv == nil || innerRecv.Kind() == "self":
		return strings.Join(scope, "::")
	case innerRecv.Kind() == "call":
		return w.resolveChainReceiver(innerRecv, scope, localTypes, depth+1)
	default:
		return ""
	}
}

// literalSendTarget extracts the target name from `send(:name)` /
// `public_send("name")` / `__send__(:name)` when the first argument is
// a bare symbol or string literal. Everything else is unresolvable.
// The tree-sitter-ruby grammar exposes a string's payload as a named
// `string_content` child (not a named field), and a symbol node
// carries a leading colon; both are looked up structurally. If the
// grammar shape drifts, we return false visibly rather than falling
// back to quote stripping — explicit failure beats degraded output.
func literalSendTarget(call *sitter.Node, source []byte) (string, bool) {
	args := call.ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() == 0 {
		return "", false
	}
	first := args.NamedChild(0)
	if first == nil {
		return "", false
	}
	switch first.Kind() {
	case "simple_symbol":
		return strings.TrimPrefix(extract.Text(first, source), ":"), true
	case "string":
		count := first.NamedChildCount()
		for i := uint(0); i < count; i++ {
			c := first.NamedChild(i)
			if c != nil && c.Kind() == "string_content" {
				return extract.Text(c, source), true
			}
		}
	}
	return "", false
}

// methodNamePatterns lists variable-name substrings that suggest the
// variable holds a method name. Used by the dynamic-dispatch heuristic
// to avoid emitting edges for every variable-based send() call.
var methodNamePatterns = []string{
	"callback", "handler", "method", "action", "hook",
	"event", "listener", "processor", "task", "job",
	"name", "attr",
}

// ConfidenceHeuristicDispatch is the confidence for variable-inferred
// dynamic dispatch edges. Very low — we're guessing the method name from
// a variable assignment, which could be wrong.
const ConfidenceHeuristicDispatch = extract.ConfidenceUnresolved / 2

// findEnclosingMethodBody walks up from n to the nearest "method" node
// and returns its "body" child, or nil if none is found.
func findEnclosingMethodBody(n *sitter.Node) *sitter.Node {
	for p := n.Parent(); p != nil; p = p.Parent() {
		if p.Kind() == "method" || p.Kind() == "singleton_method" {
			return p.ChildByFieldName("body")
		}
	}
	return nil
}

// traceVariableAssignment scans body for assignments to varName that
// appear before the given call node, and returns the literal symbol or
// string value from the RHS of the last such assignment. It only looks
// at direct children (not nested blocks or methods) to keep the heuristic
// simple and fast.
func traceVariableAssignment(body *sitter.Node, varName string, source []byte, call *sitter.Node) (string, bool) {
	if body == nil || call == nil {
		return "", false
	}
	callRow := call.StartPosition().Row
	var result string
	found := false
	for _, kind := range []string{"assignment", "operator_assignment"} {
		_ = extract.WalkNamedDescendants(body, kind, func(n *sitter.Node) error {
			// Skip assignments that appear after the send call.
			if n.StartPosition().Row > callRow {
				return nil
			}
			lhs := n.ChildByFieldName("left")
			rhs := n.ChildByFieldName("right")
			if lhs == nil || rhs == nil {
				return nil
			}
			if lhs.Kind() != "identifier" || extract.Text(lhs, source) != varName {
				return nil
			}
			switch rhs.Kind() {
			case "simple_symbol":
				result = strings.TrimPrefix(extract.Text(rhs, source), ":")
				found = true
			case "string":
				count := rhs.NamedChildCount()
				for i := uint(0); i < count; i++ {
					c := rhs.NamedChild(i)
					if c != nil && c.Kind() == "string_content" {
						result = extract.Text(c, source)
						found = true
						break
					}
				}
			}
			return nil
		})
	}
	return result, found
}

// inferSendTargetFromVariable applies a heuristic to variable-based
// dynamic dispatch: if the first argument to send/public_send/__send__
// is an identifier whose name suggests a method name, and we can trace
// the variable back to a literal symbol or string assignment, return
// the inferred target with very low confidence.
func inferSendTargetFromVariable(call *sitter.Node, source []byte) (string, float64, bool) {
	args := call.ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() == 0 {
		return "", 0, false
	}
	first := args.NamedChild(0)
	if first == nil || first.Kind() != "identifier" {
		return "", 0, false
	}
	varName := extract.Text(first, source)

	// Only apply heuristic when the variable name suggests a method name.
	lowerVar := strings.ToLower(varName)
	matchesPattern := false
	for _, pat := range methodNamePatterns {
		if strings.Contains(lowerVar, pat) {
			matchesPattern = true
			break
		}
	}
	if !matchesPattern {
		return "", 0, false
	}

	body := findEnclosingMethodBody(call)
	if body == nil {
		return "", 0, false
	}
	if target, ok := traceVariableAssignment(body, varName, source, call); ok {
		return target, ConfidenceHeuristicDispatch, true
	}
	return "", 0, false
}
