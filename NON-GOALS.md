# What Sense Is Not

The non-goals are not a list of things we haven't gotten to. They are the shape of the product. Sense looks the way it does because of what it refuses to be. If you understand this page, you understand why a well-built feature PR can still be declined, and why that is not a judgement of the work.

The one-paragraph version lives in [README → What Sense Is Not](README.md#what-sense-is-not). This is the full account.

---

## The identity non-goals

These are load-bearing. Remove any one and Sense becomes a different, worse tool.

- **Not a code editor or modifier.** Read-only is the identity, not a limitation. Sense observes your codebase. It never modifies it. Your editor, your agent, your tools stay in control. Understanding shouldn't require permission to change things.

- **Not a token optimizer.** Token savings are a side effect of understanding, not the goal. If LLM costs dropped to zero tomorrow, Sense would still be valuable. The headline metric is reach (how much of the relevant structure the agent actually finds), measured at token parity, not the token count itself.

- **Not a search engine.** Sense is not something a human types queries into. Code search by meaning is one of four tools, not the product. The product is structural understanding that an AI queries programmatically and acts on.

- **Not a feature-count competitor.** Four tools is a choice, not a constraint. Your AI doesn't need a hundred tools to choose from. It needs a few that work. We will not win on tool count, and we are not trying to.

- **Not dependent on anything.** No API keys. No Ollama. No Docker. No Python. No SaaS account, no cloud, no external embedding provider. One binary, one local index, zero network calls after install. Your code never leaves your machine.

---

## Where Sense will not go

Sense is feature-complete for v1. The query surface and the CLI are deliberately small and considered done. The following are out of scope, even when well-built. Open an issue before investing time in anything here. An unsolicited PR in these areas will be closed with thanks rather than merged.

- **New commands.** The CLI surface is closed.
- **New query or output formats.** The four tools and their shapes are settled.
- **Configuration knobs.** Defaults that work beat options that have to be chosen.
- **Performance rewrites.** Sense already disappears into the workflow (queries in milliseconds). Speed is not a problem we are paying complexity to solve.
- **Dependency swaps.** Zero-dependency is a feature.
- **Net-new features.** The product is scoped. More surface is not more value.

This is not a moratorium on improvement. It is a refusal to grow surface area. The three lanes that ARE open (languages, frameworks and their dead-code idioms, AI-tool integration) all deepen the existing four tools rather than add new ones. See [CONTRIBUTING.md](CONTRIBUTING.md).

---

## Where Sense does not help (and won't pretend to)

Honesty about the boundary keeps the trust. Sense does not make the model smarter, it gives the model structure. It pulls ahead on tasks that require understanding relationships, not on tasks that are just text. For the cases where it offers little, see [FAQ → Where does Sense NOT help?](FAQ.md#where-does-sense-not-help).

---

## Why this is the right shape

Sense was built as a composable substrate: a thing that does one layer well and stays out of the way of the others. The temptation with any tool that understands code is to also edit it, also manage memory, also review, also configure. Each addition looks reasonable alone. Together they make a tool that is large, opinionated about your whole workflow, and impossible to drop into someone else's. Sense refuses that path on purpose.

A small, read-only, local, zero-dependency tool with four capabilities is something an AI can rely on and a human can trust without auditing. That reliability is the product. The non-goals are how it stays that way.
