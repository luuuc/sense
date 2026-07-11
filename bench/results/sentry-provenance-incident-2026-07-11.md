# INCIDENT — sentry headline cell: report.json +0.60 does not derive from on-disk runs

_Opened 2026-07-11 by the Loop 3 evaluator fixtures (negative control + tie diagnosis, two independent
agents). **RESOLVED same day, option 2, by Luc:** the `.bak` archives were deliberately deleted earlier
today as bench temp-file cleanup (an operator decision on a closed vertical, not a never-delete
violation), and python-django is CLOSED — it now serves, like rails, as the loop-test fixture corpus.
**The standing repo-side sentry headline cell is what the disk supports: dependents +0.10, overall
+0.03, efficiency-at-parity ◆ (billed −7%, wall −27%).** Published article numbers stay frozen per the
snapshot rule; this file records the drift. Sentry's win-class standing rests on the three
confirmation arms (Kimi +0.24, Devstral +0.29, GPT-5.5 +0.35), which are untouched. report.json was
NOT regenerated (frozen tree; this record is the reconciliation)._

## The discrepancy

- `results/claude-opus-4-8/report.json` (mtime Jul 11 08:05:40): sentry `deps_delta = 0.60`,
  `sense_overall = 1.0` — the published headline cell.
- Recomputed from the on-disk runs, two independent ways: `pergroup.py` → dependents +0.10 (baseline
  5/5 then 4/5; sense 5/5, 5/5), overall +0.03, WITH the efficiency-at-parity ◆ footer (billed −7%,
  wall −27%, strictly ordered runs); `matrix.collect()` → deps_delta +0.10, overall +0.029.
- Baseline run-1's 5/5 dependents credits are REAL: hand-verified citations in the clone at
  `7f129bb1` (e.g. `rules/history/base.py:18`, `issue_link_requester.py:77`). The yaml's own comment
  ("dep:sentryapp-link missed by the baseline in ALL THREE sessions") does not describe these runs.

## Timeline (all mechanical facts)

| When | What |
|---|---|
| Jul 4 10:20 | `scenarios/sentry.yaml` current content (the gold-retarget era) |
| Jul 7 08:21-08:35 | the four on-disk sentry runs (run_meta timestamps, repo 7f129bb1, current scenario name) |
| Jul 11 08:05:40 | ALL FOUR `scored.json` + `report.json` + `citation-hallucinations.md` rewritten in the same second (the v1.11.20 no-regress session's re-score/report pass) |
| Jul 11 08:05 | citation-hallucinations.md still lists `baseline/sentry.produce-occurrence-tie.bak`, `sense/saleor.8dep-drop.bak`, `litellm.pre-l1-dedup.bak` |
| now | NO `.bak` / `.prev` / `produce-occurrence` dirs exist anywhere under `bench/`, `sense-benchmark/`, or Spotlight-wide |

## The open question

Which runs produced +0.60? Candidates: (a) the "ALL THREE sessions" the yaml comments describe, which
predate Jul 7 and whose dirs may have been what the archive names pointed at (moved out per the
report.sh `.bak`-wins-row workaround, current location unknown); (b) these same Jul 7 runs under an
earlier scorer/gold state that credited the baseline less (contradicted by the hand-verified baseline
citations); (c) a report.sh row-carry quirk (report.json's consumption numbers match no current run:
billed 29,389.5/29,378.5 vs actual 30,191.5/32,302.5).

## Closing fact (found at the Loop 6 tamper test, same day)

**Nothing published ever drifted.** The sentry article pack's validated headline block already reads
`deps_delta: 0.10, overall 0.85→0.88` with outcome "TIE ◆ efficiency-at-parity … separates on every
cross-agent arm" — the pack validator (which checks `headline:` against live scored.json) forced the
pack to match the disk all along. The +0.60 lived ONLY in report.json's sentry row (the `.bak`-era
artifact) and in readings taken from it. Blast radius: one stale report row, zero published claims.

## What this does NOT touch

- The sentry cross-arm wins (Kimi +0.24, Devstral +0.29, GPT-5.5 +0.35) live in their own results
  trees and are unaffected. "Sentry = 4-arm win" survives on three arms regardless.
- Published article numbers are under the snapshot rule; nothing chases them until this reconciles.

## Reconciliation options (Luc's call)

1. Locate the moved archives (the `.bak` dirs referenced at 08:05) and restore provenance for +0.60.
2. If unrecoverable: the standing headline cell is what the disk supports — dependents +0.10, overall
   +0.03, efficiency-at-parity ◆ — and the drift is recorded per the snapshot rule, with the tie
   re-entering Loop 3 dispatch at branch 1 (the fixture's diagnosis of THESE runs already exists: 14/17
   diluters, 93 sense-only cited files as the retarget pool).
3. Either way: the never-delete-clean-runs law needs a look at whatever happened to the archives
   between 08:05 and now.
