# Judge variance baseline

Two runs of judge.py against the same transcripts, same rubric, same prompt. `temperature` is omitted from the request — deprecated on `claude-opus-4-7` — so the model uses its default sampling mode and the deltas below characterise the residual non-determinism we get out of the box.

**Target (from pitch 20-05):** per-criterion stdev < 0.05. If we fail it, the next move is either to bump samples per call (deferred to 20-06), tighten the rubric question wording, or anchor the score scale (e.g. snap to {0.0, 0.25, 0.5, 0.75, 1.0}).

## Verdict

| Layer | Max stdev | Pass? |
|-------|----------:|:-----:|
| Per-criterion (raw 0.0–1.0 scores) | 0.071 | ✗ |
| Per-step `step_quality` (4-criterion weighted sum) | 0.048 | ✓ |
| Per-scenario `scenario_quality` (mean of 4 steps) | 0.010 | ✓ (max \|Δ\| 0.014) |

**Headline reads:** the judge is jittery at the criterion level — `uncertainty` is the worst offender (often shifting 0.10 between runs on a low-anchor 0.1–0.4 range), and `specificity` / `justification` flip ~0.05 once per run. The composite `step_quality` averages that jitter down to within target on every step but one (baseline/discourse Step 1, stdev 0.048). The scenario-level number is rock-solid for ranking tools (worst |Δ| 0.014 over four scenarios — half a fairness-formula weight unit).

**Practical decision:** trust `scenario_quality` and `step_quality` as scoring signals. Treat per-criterion rationales as **diagnostic**, not ranking inputs. Don't gate decisions on a single criterion-level delta of 0.05 between two tools.

### sense/discourse

Scenario quality: run1 = 0.7375, run2 = 0.7389, |Δ| = 0.0014

| Step | Criterion | run1 | run2 | |Δ| | stdev (n=2) |
|------|-----------|-----:|-----:|---:|-----:|
| Orient in the Discourse codebase | map_quality | 0.75 | 0.75 | 0.000 | 0.000 |
| Orient in the Discourse codebase | specificity | 0.70 | 0.80 | 0.100 | 0.071 ⚠ |
| Orient in the Discourse codebase | justification | 0.75 | 0.70 | 0.050 | 0.035 |
| Orient in the Discourse codebase | uncertainty | 0.30 | 0.30 | 0.000 | 0.000 |
| Orient in the Discourse codebase | step_quality | 0.67 | 0.69 | 0.015 | 0.011 |
| Trace topic creation from controller to persiste | map_quality | 0.85 | 0.85 | 0.000 | 0.000 |
| Trace topic creation from controller to persiste | specificity | 0.90 | 0.90 | 0.000 | 0.000 |
| Trace topic creation from controller to persiste | justification | 0.80 | 0.80 | 0.000 | 0.000 |
| Trace topic creation from controller to persiste | uncertainty | 0.30 | 0.30 | 0.000 | 0.000 |
| Trace topic creation from controller to persiste | step_quality | 0.77 | 0.77 | 0.000 | 0.000 |
| Understand Guardian authorization | map_quality | 0.90 | 0.92 | 0.020 | 0.014 |
| Understand Guardian authorization | specificity | 0.95 | 0.95 | 0.000 | 0.000 |
| Understand Guardian authorization | justification | 0.65 | 0.60 | 0.050 | 0.035 |
| Understand Guardian authorization | uncertainty | 0.20 | 0.15 | 0.050 | 0.035 |
| Understand Guardian authorization | step_quality | 0.76 | 0.75 | 0.009 | 0.007 |
| Assess impact of adding a permission check | map_quality | 0.80 | 0.80 | 0.000 | 0.000 |
| Assess impact of adding a permission check | specificity | 0.85 | 0.85 | 0.000 | 0.000 |
| Assess impact of adding a permission check | justification | 0.80 | 0.80 | 0.000 | 0.000 |
| Assess impact of adding a permission check | uncertainty | 0.40 | 0.40 | 0.000 | 0.000 |
| Assess impact of adding a permission check | step_quality | 0.75 | 0.75 | 0.000 | 0.000 |

### baseline/discourse

Scenario quality: run1 = 0.6794, run2 = 0.6937, |Δ| = 0.0143

| Step | Criterion | run1 | run2 | |Δ| | stdev (n=2) |
|------|-----------|-----:|-----:|---:|-----:|
| Orient in the Discourse codebase | map_quality | 0.65 | 0.70 | 0.050 | 0.035 |
| Orient in the Discourse codebase | specificity | 0.70 | 0.75 | 0.050 | 0.035 |
| Orient in the Discourse codebase | justification | 0.60 | 0.70 | 0.100 | 0.071 ⚠ |
| Orient in the Discourse codebase | uncertainty | 0.10 | 0.20 | 0.100 | 0.071 ⚠ |
| Orient in the Discourse codebase | step_quality | 0.57 | 0.64 | 0.068 | 0.048 |
| Trace topic creation from controller to persiste | map_quality | 0.85 | 0.85 | 0.000 | 0.000 |
| Trace topic creation from controller to persiste | specificity | 0.90 | 0.90 | 0.000 | 0.000 |
| Trace topic creation from controller to persiste | justification | 0.70 | 0.75 | 0.050 | 0.035 |
| Trace topic creation from controller to persiste | uncertainty | 0.20 | 0.20 | 0.000 | 0.000 |
| Trace topic creation from controller to persiste | step_quality | 0.73 | 0.74 | 0.010 | 0.007 |
| Understand Guardian authorization | map_quality | 0.75 | 0.75 | 0.000 | 0.000 |
| Understand Guardian authorization | specificity | 0.90 | 0.90 | 0.000 | 0.000 |
| Understand Guardian authorization | justification | 0.45 | 0.40 | 0.050 | 0.035 |
| Understand Guardian authorization | uncertainty | 0.10 | 0.10 | 0.000 | 0.000 |
| Understand Guardian authorization | step_quality | 0.63 | 0.62 | 0.010 | 0.007 |
| Assess impact of adding a permission check | map_quality | 0.90 | 0.85 | 0.050 | 0.035 |
| Assess impact of adding a permission check | specificity | 0.85 | 0.85 | 0.000 | 0.000 |
| Assess impact of adding a permission check | justification | 0.75 | 0.80 | 0.050 | 0.035 |
| Assess impact of adding a permission check | uncertainty | 0.40 | 0.40 | 0.000 | 0.000 |
| Assess impact of adding a permission check | step_quality | 0.78 | 0.77 | 0.010 | 0.007 |

### sense/flask

Scenario quality: run1 = 0.7837, run2 = 0.7800, |Δ| = 0.0037

| Step | Criterion | run1 | run2 | |Δ| | stdev (n=2) |
|------|-----------|-----:|-----:|---:|-----:|
| Trace the request dispatch pipeline | map_quality | 0.90 | 0.90 | 0.000 | 0.000 |
| Trace the request dispatch pipeline | specificity | 0.95 | 0.95 | 0.000 | 0.000 |
| Trace the request dispatch pipeline | justification | 0.80 | 0.75 | 0.050 | 0.035 |
| Trace the request dispatch pipeline | uncertainty | 0.30 | 0.40 | 0.100 | 0.071 ⚠ |
| Trace the request dispatch pipeline | step_quality | 0.80 | 0.81 | 0.005 | 0.004 |
| Find internal callers of wsgi_app | map_quality | 0.90 | 0.90 | 0.000 | 0.000 |
| Find internal callers of wsgi_app | specificity | 0.95 | 0.95 | 0.000 | 0.000 |
| Find internal callers of wsgi_app | justification | 0.40 | 0.40 | 0.000 | 0.000 |
| Find internal callers of wsgi_app | uncertainty | 0.30 | 0.30 | 0.000 | 0.000 |
| Find internal callers of wsgi_app | step_quality | 0.72 | 0.72 | 0.000 | 0.000 |
| Locate test coverage for the dispatch pipeline | map_quality | 0.90 | 0.85 | 0.050 | 0.035 |
| Locate test coverage for the dispatch pipeline | specificity | 0.95 | 0.95 | 0.000 | 0.000 |
| Locate test coverage for the dispatch pipeline | justification | 0.85 | 0.85 | 0.000 | 0.000 |
| Locate test coverage for the dispatch pipeline | uncertainty | 0.50 | 0.40 | 0.100 | 0.071 ⚠ |
| Locate test coverage for the dispatch pipeline | step_quality | 0.84 | 0.81 | 0.035 | 0.025 |
| Assess impact of adding a debug parameter | map_quality | 0.70 | 0.70 | 0.000 | 0.000 |
| Assess impact of adding a debug parameter | specificity | 0.85 | 0.85 | 0.000 | 0.000 |
| Assess impact of adding a debug parameter | justification | 0.85 | 0.85 | 0.000 | 0.000 |
| Assess impact of adding a debug parameter | uncertainty | 0.70 | 0.80 | 0.100 | 0.071 ⚠ |
| Assess impact of adding a debug parameter | step_quality | 0.77 | 0.78 | 0.015 | 0.011 |

### sense/axum

Scenario quality: run1 = 0.8025, run2 = 0.8106, |Δ| = 0.0081

| Step | Criterion | run1 | run2 | |Δ| | stdev (n=2) |
|------|-----------|-----:|-----:|---:|-----:|
| Find all Handler trait implementations | map_quality | 0.90 | 0.90 | 0.000 | 0.000 |
| Find all Handler trait implementations | specificity | 0.95 | 0.95 | 0.000 | 0.000 |
| Find all Handler trait implementations | justification | 0.85 | 0.85 | 0.000 | 0.000 |
| Find all Handler trait implementations | uncertainty | 0.70 | 0.70 | 0.000 | 0.000 |
| Find all Handler trait implementations | step_quality | 0.87 | 0.87 | 0.000 | 0.000 |
| Trace the extractor chain | map_quality | 0.90 | 0.90 | 0.000 | 0.000 |
| Trace the extractor chain | specificity | 0.85 | 0.90 | 0.050 | 0.035 |
| Trace the extractor chain | justification | 0.90 | 0.85 | 0.050 | 0.035 |
| Trace the extractor chain | uncertainty | 0.40 | 0.40 | 0.000 | 0.000 |
| Trace the extractor chain | step_quality | 0.81 | 0.81 | 0.002 | 0.002 |
| Trace the full serve-to-response lifecycle | map_quality | 0.90 | 0.90 | 0.000 | 0.000 |
| Trace the full serve-to-response lifecycle | specificity | 0.90 | 0.90 | 0.000 | 0.000 |
| Trace the full serve-to-response lifecycle | justification | 0.70 | 0.70 | 0.000 | 0.000 |
| Trace the full serve-to-response lifecycle | uncertainty | 0.30 | 0.40 | 0.100 | 0.071 ⚠ |
| Trace the full serve-to-response lifecycle | step_quality | 0.77 | 0.79 | 0.015 | 0.011 |
| Assess adding a request context layer | map_quality | 0.85 | 0.85 | 0.000 | 0.000 |
| Assess adding a request context layer | specificity | 0.80 | 0.80 | 0.000 | 0.000 |
| Assess adding a request context layer | justification | 0.85 | 0.85 | 0.000 | 0.000 |
| Assess adding a request context layer | uncertainty | 0.30 | 0.40 | 0.100 | 0.071 ⚠ |
| Assess adding a request context layer | step_quality | 0.76 | 0.77 | 0.015 | 0.011 |

## Pooled stdev across all steps

| Criterion | n | mean stdev | max stdev | passes (<0.05) |
|-----------|--:|----------:|---------:|:-------:|
| map_quality | 16 | 0.008 | 0.035 | ✓ |
| specificity | 16 | 0.009 | 0.071 | ✗ |
| justification | 16 | 0.020 | 0.071 | ✗ |
| uncertainty | 16 | 0.029 | 0.071 | ✗ |
| step_quality | 16 | 0.009 | 0.048 | ✓ |

## Scenario-quality stability

| Transcript | run1 quality | run2 quality | |Δ| |
|------------|-------------:|-------------:|---:|
| sense/discourse | 0.7375 | 0.7389 | 0.0014 |
| baseline/discourse | 0.6794 | 0.6937 | 0.0143 |
| sense/flask | 0.7837 | 0.7800 | 0.0037 |
| sense/axum | 0.8025 | 0.8106 | 0.0081 |

