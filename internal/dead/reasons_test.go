package dead

import "testing"

func TestRegisterReasonsAddsCode(t *testing.T) {
	const code = "test_added_reason_unique"
	// Clean up so the test is repeatable and does not pollute the catalog.
	defer delete(reasonCatalog, code)

	registerReasons(map[string]reasonSpec{
		code: {priority: 42, hint: "test hint", verify: "test verify"},
	})
	if reasonPriority(code) != 42 {
		t.Errorf("priority = %d, want 42", reasonPriority(code))
	}
	if ReasonPriority(code) != 42 {
		t.Errorf("ReasonPriority = %d, want 42", ReasonPriority(code))
	}
	if ReasonGroupVerify(code) != "test verify" {
		t.Errorf("ReasonGroupVerify = %q, want %q", ReasonGroupVerify(code), "test verify")
	}
	if newReason(code).Hint != "test hint" {
		t.Errorf("newReason hint = %q, want %q", newReason(code).Hint, "test hint")
	}
}

func TestRegisterReasonsDuplicatePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("registerReasons with a duplicate code should panic")
		}
	}()
	// ReasonReflection already exists in the catalog.
	registerReasons(map[string]reasonSpec{
		ReasonReflection: {priority: 1, hint: "dup", verify: "dup"},
	})
}

func TestReasonGroupVerifyUnknownEmpty(t *testing.T) {
	if v := ReasonGroupVerify("no_such_code"); v != "" {
		t.Errorf("ReasonGroupVerify(unknown) = %q, want empty", v)
	}
}
