# Can an AI agent navigate Ruby? The results, in plain language

AI agents write Ruby well. This benchmark asks a different question: can they *navigate* it? Given a real codebase and a change that touches a central model, can an agent find every place that depends on it, before the change silently breaks one?

Across 13 real codebases and 5 models, the answer splits cleanly in two. On small, readable gems, the agent's normal tools are enough. On real applications, the cold agent finds a fraction of the dependents and confidently declares the audit done. Give the same agent a structural map of the codebase and the gap closes: the strongest model gained a mean **+0.26** in cited recall, and the gain concentrates almost entirely on the hard part, what depends on what (**+0.48** on the dependents group).

Disclosure: I build [Sense](https://github.com/luuuc/sense), the code map in the "sense" arm, and the bench is designed so you don't have to care: hand-built answer keys, pinned commits, mechanically verified citations, published variance, replayable transcripts. Every number on this page comes from [the raw report](results/report.md) and the per-model reports linked at the end.

## The task

One scenario shape, every repo:

> You are about to change how a central model is torn down. Before you touch it, find every place in the codebase that depends on it.

Chatwoot's `Inbox`. Mastodon's `Status`. GitLab's `MergeRequest`. Discourse's `Upload`. This is the question a senior engineer asks before any risky refactor, and it is the question that decides whether a green review becomes a broken deploy.

Each repo runs the task twice with the **same model, same prompt, same pinned commit**:

- **baseline** arm: the agent's normal tools (file reads, grep, shell)
- **sense** arm: the same tools, plus the ability to query a structural map of the repo: a persisted graph of its symbols, call edges, and framework-level relationships, answerable in single queries instead of file walks

The only variable is the map.

## How it's scored

Before any run, each scenario gets a **must-find set**: the exact code locations a complete answer has to surface, built by hand from source. The agent never sees it.

The headline metric is **cited recall**: of that set, the share the answer pinned to an exact `path:line` an engineer could jump straight to. A reference-aware judge grades coverage against the key, so a confident-sounding but incomplete answer is penalized for what it leaves out. Then every `path:line` the answer printed is mechanically resolved against the repo at the pinned commit; anything that doesn't resolve is flagged, per model, in a public [citation check](results/report.md#per-model-reports).

Why cited recall? Because the failure mode that matters is **honest omission**. Neither arm hallucinates much. The cold agent finds 2 of 11 dependents and calls the audit finished. A metric that rewarded prose quality would miss exactly that.

## The headline

Five models, 13 repos each, both arms. On the headline arm every repo ran twice per arm; run-to-run spread is published in [per-repo variance](results/report.md#per-repo-variance). The delta is sense minus baseline, so positive means the map helped.

| model | mean lift (overall) | mean lift (dependents) |
|---|---|---|
| Claude Opus 4.8 | **+0.26** | **+0.48** |
| Devstral Small 24B | +0.25 | +0.36 |
| Qwen3 Coder Next | +0.18 | +0.24 |
| Kimi K2.7 | +0.14 | +0.18 |
| GPT-5.5 | +0.13 | +0.29 |

On the headline arm (Opus): **12 wins, 1 tie, 0 losses** across the 13 repos.

Two things in that table matter more than the totals.

**The lift lands on the hard column.** Every model's dependents lift beats its overall lift. Naming a model's public API is easy; finding the worker that touches it through a polymorphic association is the part that pages someone at 3am, and that is where the map pays.

**The weakest model gained about as much as the strongest.** Devstral, a 24B open model, posted +0.25 against Opus's +0.26, from a far lower floor. The map compensates for what a model cannot hold in working memory, and the less it holds, the more the map is worth.

## Where the gap opens

Sort the repos by size and the pattern is not subtle. These are the Opus deltas against the indexed file count of each repo:

| repo | what it is | files indexed | lift |
|---|---|---:|---:|
| gitlabhq | DevOps platform | 36,829 | +0.41 |
| discourse | forum platform | 12,388 | +0.40 |
| forem | powers dev.to | 5,216 | +0.29 |
| mastodon | social network | 4,102 | +0.54 |
| rails | the framework itself | 4,014 | +0.25 |
| chatwoot | support platform | 3,347 | +0.68 |
| solidus | e-commerce (6 gems) | 2,677 | +0.28 |
| redmine | issue tracker | 1,759 | +0.11 |
| lobsters | link aggregator | 578 | +0.12 |
| llm.rb | LLM client gem | 302 | -0.00 |
| ruby_llm | LLM client gem | 296 | +0.04 |
| langchainrb | LLM framework gem | 247 | +0.17 |
| raix | AI mixin gem | 38 | +0.13 |

Every repo above ~2,500 files gained at least +0.25. Every repo below 2,000 files gained at most +0.17. The map is worth exactly the slice of the codebase the model cannot afford to read for itself, and not a line more.

Four rows tell the whole story:

- **Chatwoot, +0.68.** Cold, the agent found 2 of 11 scattered dependents of the `Inbox` model and declared the audit done. On the rerun it found 0 of 11. With the map, 11 of 11, both runs. The dependents it missed cold share no grep token with the model; they reach it through concerns, services, and polymorphic associations, and grep cannot see relationships.
- **GitLab, +0.41.** 36,829 files, 1,121,147 edges in the index. The agent's single `sense_blast MergeRequest` call came back with the resolved dependent set, 354 affected symbols, in one step. At this size, "just read the code" is not a plan.
- **Rails, +0.25.** The most instructive row. Cold, the model is far stronger here than on any of the big applications, because it has effectively memorized Rails: it pinned all 5 contract items of `ActiveRecord::Relation` in both cold runs. What it could not recall are the query-compiler internals no tutorial ever wrote about: its worst cold run cited 1 of the 6 internal dependents; with the map, 5 of 6 in both runs. An agent's confidence on famous code is recall, and it runs out exactly at the boundary of what got written about. Your private repo is entirely on the far side of that boundary.
- **llm.rb, -0.00.** The lone tie, kept on the board on purpose. A gem small enough to read whole needs no map; the sense arm answered the same question on about a quarter fewer billed tokens, and that's all. A benchmark that only ever flatters the thing it measures is a brochure.

## The cells that cut the other way

The full [model-by-repo table](results/report.md#overall-cited-recall-δ-sense--baseline-by-model--repo) has 65 cells and not all of them are wins. GPT-5.5 posts -0.03 on langchainrb and three flat zeros. Qwen posts -0.03 on redmine. Devstral posts **-0.20 on raix**, the smallest repo in the set: a 38-file gem where the map's overhead outweighs its value for a model weak at tool orchestration. The pattern holds anyway. Lift scales with codebase size, the negative cells sit at the small end, and they are in the table because a table with only green cells is an ad.

Running the series also sharpened both sides of the bench, which is what a benchmark is for. Mastodon exposed a symbol-ambiguity dead end (`Status` is both a Ruby model and a React component), GitLab exposed a nondeterministic result cap at hub scale, and Solidus exposed a resolver gap on classes reopened across gems; each fix shipped before the next repo ran, and each now ships to every user. The grading side hardened the same way: the first judge rated a 44%-complete audit "exhaustive," so it was replaced with reference-aware grading against the hand-built keys. Every one of those changes made the numbers more honest, and three of them made the product better.

## What it cost

Recall is the goal; tokens are a side effect. Averaged per model across all 13 repos, billed tokens (what you actually pay for) moved like this with the map:

| model | billed tokens, baseline → sense | change |
|---|---|---:|
| Devstral Small 24B | 2,044,783 → 1,595,901 | -22% |
| Kimi K2.7 | 139,539 → 125,416 | -10% |
| GPT-5.5 | 152,879 → 139,693 | -9% |
| Claude Opus 4.8 | 23,198 → 23,203 | +0% |
| Qwen3 Coder Next | 1,067,682 → 1,215,844 | +14% |

Three models got cheaper, one stayed flat, one paid more. Reported either way, because the trade on offer is not a smaller bill. On GitLab, 19% more billed tokens bought a +0.41 lift in cited recall on a 36,829-file monolith. That is a rounding error against the incident you didn't have.

(Token figures are within-model only. Never compare them across models; the harnesses and providers count differently.)

## Why you can trust a benchmark its author built

You shouldn't have to, which was the design constraint:

- **Answer keys built by hand from source, before any run.** The agent never sees them.
- **Commits pinned.** Every run is against a fixed, reproducible tree.
- **Citations mechanically verified.** Every `path:line` in every answer is resolved against the repo; failures are published per model.
- **Variance published.** Each repo's run-to-run spread is in [per-repo variance](results/report.md#per-repo-variance), so a headline is trusted only when it is stable.
- **Failures published.** The tie, the negative cells, the judge that got replaced, the three tool defects the bench surfaced. All of it is above, and all of it is in the raw data.
- **Everything replayable.** Transcripts, scenarios, and scripts are in this repo. If you think an LSP arm or a different metric would change the picture, the harness is sitting right here.

## Run it on your own repo in 10 minutes

The bench's claim is falsifiable on your codebase this afternoon:

1. Ask your agent, cold: *"Before I change how [your busiest model] is torn down, find every place that depends on it."* Save the list.
2. Install Sense: one-liner in the [README](https://github.com/luuuc/sense), or build from source.
3. `sense scan` in the repo root, then `sense setup` to connect your agent.
4. Ask the same question, word for word. Diff the two lists against what you know is true.

Sense is free, open source, one binary. Everything stays on your machine and sends nothing anywhere.

If your repo is a small gem, expect a tie. If it is an application, expect the first list to be missing the dependents you'd least like to discover in production.

## The full data

- [Raw report](results/report.md): methodology, the full 5-model × 13-repo delta matrix, efficiency tables
- Per-model reports with absolute scores per repo and citation checks: [Claude Opus 4.8](results/claude-opus-4-8/report.md) · [GPT-5.5](results/gpt-5.5/report.md) · [Kimi K2.7](results/kimi-for-coding_k2p7/report.md) · [Devstral Small 24B](results/ollama-cloud_devstral-small-2_24b/report.md) · [Qwen3 Coder Next](results/ollama-cloud_qwen3-coder-next/report.md)
- [Per-repo variance](results/report.md#per-repo-variance): is each headline stable or a lucky draw?
- [Scenarios and must-find sets](scenarios/): the questions and the answer keys

---

The [guide](../../../GUIDE.md) is the doc map for the whole project: what Sense does, how an agent uses it on a codebase, where a contribution belongs, and how a one-binary code index works.
