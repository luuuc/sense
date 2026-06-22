# The Sense Guide

The map to everything else. Sense is a local MCP server that gives AI coding agents structural understanding of a codebase (symbols, relationships, conventions) without reading dozens of files. One binary, one local index, four tools. No SaaS, no API key, no cloud.

If you read nothing else, read [What Sense believes](README.md#what-sense-believes) and [What Sense Is Not](NON-GOALS.md). Everything below routes you to the single source of truth for each question, so nothing here is duplicated and nothing drifts.

---

## Start here, by who you are

### You are a human evaluating Sense

You want to know what it is, whether it's worth it, and what it won't do to your machine or your code.

| Question | Go to |
|---|---|
| What is it, in one screen? | [README](README.md) |
| What does it cost me, save me, leak? | [FAQ → Value and trade-offs](FAQ.md#value-and-trade-offs), [FAQ → Privacy, security, dependencies](FAQ.md#privacy-security-dependencies) |
| How do I install and point it at a project? | [FAQ → Getting started](FAQ.md#getting-started) |
| What do I actually have to maintain? | [FAQ → Living with Sense (setup & forget)](FAQ.md#living-with-sense-setup--forget) |
| What will Sense never become? | [NON-GOALS.md](NON-GOALS.md) |

### You are the AI using Sense

The honest framing: Sense isn't for the human reading this. It's for you. You call the tools, you act on the results.

| Question | Go to |
|---|---|
| When do I use which tool? | [AGENTS.md](AGENTS.md), [FAQ → For AI agents reading this](FAQ.md#for-ai-agents-reading-this) |
| What are the four tools, exactly? | [FAQ → What the AI gets](FAQ.md#what-the-ai-gets-the-four-tools) |
| When should I NOT reach for Sense? | [AGENTS.md](AGENTS.md), [FAQ → Where does Sense NOT help?](FAQ.md#where-does-sense-not-help) |
| Can the lists (dead code, blast radius) be trusted blindly? | [FAQ → What does `sense dead` report, and can I trust it?](FAQ.md#what-does-sense-dead-report-and-can-i-trust-it) |

### You are a contributor

Sense is feature-complete for v1. The door is open in three lanes only, and every behavioral change is proven by a bench before it ships.

| Question | Go to |
|---|---|
| What can I contribute? | [CONTRIBUTING.md](CONTRIBUTING.md) |
| How do I add a language? | [CONTRIBUTING-A-LANGUAGE.md](CONTRIBUTING-A-LANGUAGE.md) |
| How do I add a framework (and teach dead-code its idioms)? | [CONTRIBUTING-A-FRAMEWORK.md](CONTRIBUTING-A-FRAMEWORK.md) |
| How do I add or tune an AI tool? | [CONTRIBUTING-AN-AI-TOOL.md](CONTRIBUTING-AN-AI-TOOL.md) |
| How is every change judged? Why will my feature PR be closed? | [BENCHMARKING.md](BENCHMARKING.md), [NON-GOALS.md](NON-GOALS.md) |
| What are the quality gates? | [CONTRIBUTING → Quality gates](CONTRIBUTING.md) |

---

## The three things to grasp

Everything about Sense follows from three ideas. Each has one home.

1. **What Sense is, and believes.** A codebase is structure, not just text. Local because your code is yours, read-only because understanding shouldn't require permission to change things, four tools because your AI needs a few that work. See [README → What Sense believes](README.md#what-sense-believes) and [README → How It Works](README.md#how-it-works).

2. **What Sense is not, and where it will not go.** The non-goals are not omissions. They are the identity. Read [NON-GOALS.md](NON-GOALS.md) before proposing anything.

3. **How change is piloted.** Nothing behavioral ships on taste or a proxy. A vertical or global bench proves it, on the frontier model, judged against a reference. See [BENCHMARKING.md](BENCHMARKING.md).

---

## The full doc map

| File | What it is | Audience |
|---|---|---|
| [README.md](README.md) | What Sense is, what it believes, what changes | Human, first contact |
| [GUIDE.md](GUIDE.md) | This map | Everyone |
| [NON-GOALS.md](NON-GOALS.md) | What Sense is not, where it won't go | Everyone, especially contributors |
| [FAQ.md](FAQ.md) | Every common question, for humans and AIs | Human, AI |
| [AGENTS.md](AGENTS.md) | Tool-selection steering, written for the AI | AI |
| [CLI.md](CLI.md) | The human-facing command reference | Human |
| [BENCHMARKING.md](BENCHMARKING.md) | How a change earns the right to ship | Contributor |
| [CONTRIBUTING.md](CONTRIBUTING.md) | The three lanes, dev setup, quality gates | Contributor |
| [CONTRIBUTING-A-LANGUAGE.md](CONTRIBUTING-A-LANGUAGE.md) | Add a tree-sitter language | Contributor |
| [CONTRIBUTING-A-FRAMEWORK.md](CONTRIBUTING-A-FRAMEWORK.md) | Add a framework + its dead-code idioms | Contributor |
| [CONTRIBUTING-AN-AI-TOOL.md](CONTRIBUTING-AN-AI-TOOL.md) | Add or tune an AI client | Contributor |
| [SECURITY.md](SECURITY.md) | Reporting and handling | Everyone |
| [ARTICLES.md](ARTICLES.md) | Long-form writing and benchmarks | Human |
| [CHANGELOG.md](CHANGELOG.md) | What shipped, when | Everyone |
