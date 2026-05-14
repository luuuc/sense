# Scenarios

Each `*.yaml` file describes one realistic, multi-step developer session for
a single repository. The scorer (`bench/lib/scorer.py`) reads a scenario's
checklist and matches each check against the transcript a tool produced.

## Check types

All text checks match against `answer_text` — the assistant's actual text
blocks, not its tool inputs or tool outputs. A query like
`Grep(pattern="TopicCreator")` cannot, by typing alone, satisfy a check for
`TopicCreator`; the model has to mention it in its answer.

| Type                  | Match semantics                                                                                                                       | When to use                                                                                                                                       |
|-----------------------|---------------------------------------------------------------------------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------|
| `word`                | Case-insensitive, non-word boundary on both sides. `ensure` does NOT match `EnsureMagic`.                                             | Single identifiers and short tokens (`PostCreator`, `Guardian`, `topic`, `find_topic`).                                                           |
| `phrase`              | Same boundary semantics as `word`. Distinguished only by intent.                                                                      | Multi-token strings or qualified names where you want the same boundary safety (`PostCreator#create`, `topic-creation flow`).                     |
| `contains`            | Plain case-insensitive substring. `ensure` DOES match `EnsureMagic`.                                                                  | Genuine substring searches where the inner match is the point — e.g. `queue` inside `QueueBackend` is the intended hit.                           |
| `transcript_contains` | Alias for `contains`.                                                                                                                 | Legacy. Prefer `contains`.                                                                                                                        |
| `exact`               | Verbatim, case-sensitive substring.                                                                                                   | Code snippets, exact phrases that should appear unmodified.                                                                                       |
| `starts_with`         | Any line in `answer_text` starts with the value (case-insensitive).                                                                   | Structured response markers (`## Summary`, `Step 1:`).                                                                                            |
| `mcp_tool_used`       | Substring match on a tool call's name in the session.                                                                                 | Confirm the tool reached for the MCP server, not grep.                                                                                            |
| `no_grep`             | No grep/Glob/find/Agent/rg invocations in the session at all.                                                                         | Adoption checks that demand the tool replace conventional search entirely.                                                                        |
| `response_richness`   | At least `value` distinct source files appear in `file.ext:ref` form across the answer.                                               | Forces a file-cited answer rather than a vague summary. `value` is the minimum count.                                                             |
| `diff_contains`       | Value appears in `git diff --unified=0` taken in the repo path.                                                                       | Modification steps where the assistant should have edited files.                                                                                  |

## Picking the right one

- **Default: `word`.** It's the safe match for identifiers.
- Reach for `phrase` when the value has dots, hashes, slashes, or spaces —
  it signals "I want this exact-ish sequence" to a future reader.
- Reach for `contains` only when you *want* substring leakage. If you find
  yourself writing `type: contains, value: ensure` to check a Ruby concept,
  that's the bug `phrase` exists to fix — use `phrase` instead.
- `exact` is for code: braces, colons, signatures that should appear
  byte-for-byte.

## Layers

Each check can be tagged `layer: fairness` (default) or `layer: adoption`.
Fairness checks measure answer quality regardless of which tool was used;
adoption checks (`mcp_tool_used`, `no_grep`, sometimes `response_richness`)
measure tool fluency and only matter when comparing code-intelligence tools
against each other. Reporter splits the two so casual readers see the
fairness number first.
