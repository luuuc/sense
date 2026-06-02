package eval

// Corpus is the hand-labeled synthetic unit corpus. Every label is a
// verdict a *trustworthy* engine must produce — the ground truth the
// arbiter rebuild is measured against, not a snapshot of current behavior.
// It deliberately includes the hard live-but-unreferenced cases that were
// confidently lied about (duck-typed value-object predicates of the
// `pending?` family), a genuinely-dead private (the one symbol that earns
// `dead`), and alive controls — because a corpus of only obvious cases
// measures nothing (.doc/pitches/25-13, rabbit hole "eval corpus that
// proves nothing").
//
// Ground-truth rule for Ruby (pitch decision #2): only a private method
// that is not reflection-reachable can earn `dead`; every public method
// stays `possibly_dead` because it may be reached by duck-typed dispatch.
// Rails-framework predicates (controller/model `pending?`) need framework
// detection and are added with the Rails voice; here the same class of lie
// is captured through the framework-independent value-object path.
func Corpus() []Fixture {
	c := append(rubyCorpus(), tsCorpus()...)
	return append(c, pythonCorpus()...)
}

// rubyCorpus is the Ruby slice of the trust corpus (see Corpus).
func rubyCorpus() []Fixture {
	return []Fixture{
		{
			Name: "genuine_dead_private",
			Files: map[string]string{
				"report_builder.rb": `class ReportBuilder
  def render
    format_row
  end

  private

  def format_row
    "row"
  end

  def orphaned_helper
    "no caller, private, name not reflection-reachable"
  end
end
`,
			},
			Want: []Sym{
				{"ReportBuilder#orphaned_helper", Dead,
					"private, zero callers, name never dispatched — the rare earned dead"},
				{"ReportBuilder#format_row", Alive,
					"called by #render via implicit self"},
				{"ReportBuilder#render", PossiblyDead,
					"public and unreferenced — a hidden duck-typed caller could exist"},
			},
		},
		{
			Name: "value_object_predicate",
			Files: map[string]string{
				"payment_result.rb": `PaymentResult = Struct.new(:status) do
  def success?
    status == :ok
  end

  def pending?
    status == :pending
  end
end
`,
			},
			Want: []Sym{
				{"PaymentResult#success?", PossiblyDead,
					"value-object predicate reached via x.success? on a duck-typed local"},
				{"PaymentResult#pending?", PossiblyDead,
					"the pending? class of lie — live but statically unreferenced"},
			},
		},
		{
			Name: "alive_referenced",
			Files: map[string]string{
				"inventory.rb": `class Inventory
  def report
    count_items + low_stock_penalty
  end

  def count_items
    5
  end

  def low_stock_penalty
    count_items < 3 ? 1 : 0
  end
end
`,
			},
			Want: []Sym{
				{"Inventory#count_items", Alive,
					"called by #report and #low_stock_penalty"},
				{"Inventory#low_stock_penalty", Alive,
					"called by #report"},
				{"Inventory#report", PossiblyDead,
					"public entry, no static caller — could be invoked dynamically"},
			},
		},
		{
			Name: "service_call",
			Files: map[string]string{
				"notify_user_service.rb": `class NotifyUserService
  def call
    deliver
  end

  private

  def deliver
    true
  end
end
`,
			},
			Want: []Sym{
				{"NotifyUserService#call", PossiblyDead,
					"service-object entry point invoked via Klass.new.call / .() — duck-dispatched"},
				{"NotifyUserService#deliver", Alive,
					"called by #call via implicit self"},
			},
		},
	}
}
