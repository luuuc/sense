package mcpio

import (
	"bytes"
	"flag"
	"os"
	"testing"
)

// update regenerates the testdata/*.json goldens from the fixtures
// below. Run `go test ./internal/mcpio/... -update` after a
// deliberate schema change, then inspect the diff and commit it.
// Without -update, the test asserts byte-for-byte equality against
// the committed goldens — the pitch's acceptance criterion.
var update = flag.Bool("update", false, "update testdata golden files")

// TestContractGraphCheckoutService pins MarshalGraph's output shape
// against the documented `sense.graph` example in
// .doc/definition/06-mcp-and-cli.md. The fixture mirrors the doc's
// example field-for-field; a drift in either the types or the
// encoder settings breaks this test.
func TestContractGraphCheckoutService(t *testing.T) {
	got, err := MarshalGraph(fixtureGraphCheckoutService())
	if err != nil {
		t.Fatalf("MarshalGraph: %v", err)
	}
	assertGolden(t, "testdata/graph_checkout_service.json", got)
}

// TestContractBlastUserEmailVerified pins MarshalBlast against the
// documented `sense.blast` example (symbol form). Sibling to the
// graph test: the wire shape, not the values, is what matters.
func TestContractBlastUserEmailVerified(t *testing.T) {
	got, err := MarshalBlast(fixtureBlastUserEmailVerified())
	if err != nil {
		t.Fatalf("MarshalBlast: %v", err)
	}
	assertGolden(t, "testdata/blast_user_email_verified.json", got)
}

// TestContractGraphEmpty pins the slice-normalization policy at the
// golden level: a subject with no edges must emit `"calls": []`,
// `"called_by": []`, etc. — not `null` for any slice field. The
// unit test TestMarshalZeroValueEmptySlices asserts this against
// an in-memory response; this golden asserts the same against a
// committed file so a future normalizer regression fails visibly
// under `git diff`.
func TestContractGraphEmpty(t *testing.T) {
	got, err := MarshalGraph(fixtureGraphEmpty())
	if err != nil {
		t.Fatalf("MarshalGraph: %v", err)
	}
	assertGolden(t, "testdata/graph_empty.json", got)
}

// TestContractBlastDiff pins the diff-form wire shape: same
// BlastResponse type, but `symbol` is the synthesized "diff:<ref>"
// string and `risk_factors` collapses to one aggregate line. The
// documented schema reuses the blast response for both forms; this
// golden makes sure the two shapes stay byte-equivalent except for
// the fields that semantically differ.
func TestContractBlastDiff(t *testing.T) {
	got, err := MarshalBlast(fixtureBlastDiff())
	if err != nil {
		t.Fatalf("MarshalBlast: %v", err)
	}
	assertGolden(t, "testdata/blast_diff.json", got)
}

// assertGolden compares `got` to the file at path. With -update, the
// file is rewritten from `got`; the trailing newline is POSIX
// courtesy and is stripped before comparison so Go's newline-less
// MarshalGraph output stays clean.
func assertGolden(t *testing.T, path string, got []byte) {
	t.Helper()
	if *update {
		content := append([]byte{}, got...)
		content = append(content, '\n')
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		t.Logf("updated golden: %s", path)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with -update to create)", path, err)
	}
	want = bytes.TrimRight(want, "\n")
	if !bytes.Equal(got, want) {
		t.Fatalf("golden mismatch for %s\n--- want ---\n%s\n--- got ---\n%s\n(run with -update to accept)", path, want, got)
	}
}

// ---------------------------------------------------------------
// Fixtures — one per documented example. Kept as plain constructors
// so a reader can see every field value inline without chasing
// helpers.
// ---------------------------------------------------------------

// strptr lets fixtures inline nullable file paths (`File:
// strptr("...")`) instead of scattering temp variables. Go has no
// addressable literal for a composite field, so this 20-character
// helper earns its keep.
func strptr(s string) *string { return &s }

func fixtureGraphCheckoutService() GraphResponse {
	return GraphResponse{
		Symbol: GraphSymbol{
			Name:      "CheckoutService",
			Qualified: "App::Services::CheckoutService",
			File:      "app/services/checkout_service.rb",
			LineStart: 12,
			LineEnd:   85,
			Kind:      "class",
		},
		Edges: GraphEdges{
			Calls: []CallEdgeRef{
				{Symbol: "PaymentGateway#charge", File: strptr("app/services/payment_gateway.rb"), Confidence: 1.0},
				{Symbol: "Order#finalize", File: strptr("app/models/order.rb"), Confidence: 1.0},
				{Symbol: "Beacon.track", File: nil, Confidence: 0.9},
			},
			CalledBy: []CallEdgeRef{
				{Symbol: "OrdersController#create", File: strptr("app/controllers/orders_controller.rb"), Confidence: 1.0},
				{Symbol: "CheckoutJob#perform", File: strptr("app/jobs/checkout_job.rb"), Confidence: 1.0},
			},
			Inherits: []InheritEdgeRef{
				{Symbol: "ApplicationService", File: strptr("app/services/application_service.rb")},
			},
			Tests: []TestEdgeRef{
				{File: "test/services/checkout_service_test.rb", Confidence: 0.8},
			},
		},
		SenseMetrics: GraphMetrics{
			SymbolsReturned:           7,
			EstimatedFileReadsAvoided: 5,
			EstimatedTokensSaved:      4100,
		},
	}
}

// fixtureGraphEmpty represents the minimum-viable graph response:
// a real subject with no edges of any kind and zero metric values.
// The golden exists to pin that every Edges slice renders as `[]`
// and no field collapses to `null`.
func fixtureGraphEmpty() GraphResponse {
	return GraphResponse{
		Symbol: GraphSymbol{
			Name:      "Orphan",
			Qualified: "App::Orphan",
			File:      "app/models/orphan.rb",
			LineStart: 1,
			LineEnd:   3,
			Kind:      "class",
		},
	}
}

// fixtureBlastDiff mirrors the symbol-form fixture's values so the
// diff golden is visually comparable. In practice a diff response's
// numbers come from a real run; the fixture exists to pin the wire
// shape, not any specific repo's state.
func fixtureBlastDiff() BlastResponse {
	return BlastResponse{
		Symbol:      "diff:HEAD~1",
		Risk:        "medium",
		RiskFactors: []string{"3 modified symbols; 5 direct callers"},
		DirectCallers: []BlastCaller{
			{Symbol: "OrdersController#create", File: "app/controllers/orders_controller.rb"},
			{Symbol: "CheckoutJob#perform", File: "app/jobs/checkout_job.rb"},
			{Symbol: "SessionsController#create", File: "app/controllers/sessions_controller.rb"},
			{Symbol: "RegistrationMailer#welcome", File: "app/mailers/registration_mailer.rb"},
			{Symbol: "Admin::UsersController#index", File: "app/controllers/admin/users_controller.rb"},
		},
		IndirectCallers: []BlastIndirect{
			{Symbol: "WebhookJob#process", Via: "OrdersController#create", Hops: 2},
		},
		AffectedTests: []string{
			"test/services/checkout_service_test.rb",
			"test/controllers/orders_controller_test.rb",
			"test/controllers/sessions_controller_test.rb",
		},
		TotalAffected: 6,
		SenseMetrics: BlastMetrics{
			SymbolsTraversed:          9,
			EstimatedFileReadsAvoided: 8,
			EstimatedTokensSaved:      2400,
		},
	}
}

func fixtureBlastUserEmailVerified() BlastResponse {
	return BlastResponse{
		Symbol:      "User#email_verified?",
		Risk:        "medium",
		RiskFactors: []string{"hub node", "4 direct callers", "touches auth + admin"},
		DirectCallers: []BlastCaller{
			{Symbol: "SessionsController#create", File: "app/controllers/sessions_controller.rb"},
			{Symbol: "RegistrationMailer#welcome", File: "app/mailers/registration_mailer.rb"},
			{Symbol: "User::Onboarding#complete", File: "app/models/user/onboarding.rb"},
			{Symbol: "Admin::UsersController#index", File: "app/controllers/admin/users_controller.rb"},
		},
		IndirectCallers: []BlastIndirect{
			{Symbol: "OrdersController#new", Via: "SessionsController#create", Hops: 2},
		},
		AffectedTests: []string{
			"test/models/user_test.rb",
			"test/controllers/sessions_controller_test.rb",
			"test/mailers/registration_mailer_test.rb",
			"test/models/user/onboarding_test.rb",
			"test/controllers/admin/users_controller_test.rb",
			"test/controllers/orders_controller_test.rb",
		},
		TotalAffected: 11,
		SenseMetrics: BlastMetrics{
			SymbolsTraversed:          47,
			EstimatedFileReadsAvoided: 14,
			EstimatedTokensSaved:      12400,
		},
	}
}
