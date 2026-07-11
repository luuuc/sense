# Admission-gate calibration backtest — 2026-07-11

Gate: `bench/lib/admission_gate.py` (bars 2/3/4 measured per candidate contract on its built index;
verdict = mechanical half only, Event A stays human). Binary: repo `bin/sense` built 2026-07-11
(post-v1.11.20). Known history is the answer key (`bench/results/loss-anatomy.md` is the companion
anatomy; its laws shaped the battery).

## Result: 8/8 classifications match history (wagtail = the documented false-positive)

| repo / contract | history | verdict | invisible ratio | token precision | affected | scatter | kill rule |
|---|---|---|---|---|---|---|---|
| sentry / Group | WIN class (3 confirmation arms; headline cell revised, see note) | WIN-VIABLE | 0.000 | 0.034 | 1419 | 3 | — |
| netbox / Device | WIN +0.11/+0.33 | WIN-VIABLE | 0.074 | 0.117 | 561 | 4 | — |
| saleor / ProductVariant | WIN +0.15/+0.50 | WIN-VIABLE | 0.196 | 0.219 | 1120 | 9 | — |
| wagtail / Page | control TIE | WIN-VIABLE ⚠ | 0.024 | 0.062 | 580 | 14 | (false positive, see residual) |
| healthchecks / Transport | control TIE | BALLAST-ONLY | 0.000 | 0.861 | 66 | 2 | K1 (3 usable covers) + K2 |
| litellm / BaseConfig | control TIE | BALLAST-ONLY | 0.237 | 0.625 | 736 | 6 | K3 (subclass prec 0.976) |
| haystack / Document | killed pre-bench | BALLAST-ONLY | 0.042 | 0.004 | 697 | 3 | K4 (seam-thin) |
| pretix / Team | killed pre-bench | BALLAST-ONLY | 0.188 | 0.144 | 198 | 1 | K2 (volume/colocation) |

## The calibrated rules (now encoded in `slot_verdict()`)

- **Win signature** (all three banked wins, no non-win except wagtail): no USABLE covering pattern
  (cover ≥0.8 AND precision ≥0.3), token precision ≤ 0.3, total_affected ≥ 500.
- **K1** usable covering pattern → healthchecks class (token 0.86, `.transports` 0.94, `transport.py`
  layout 1.0 — three independent enumerators).
- **K2** total_affected < 250 OR scatter ≤ 1 → pretix class (198 affected, one directory family).
- **K3** subclass pattern precision ≥ 0.7 at cover ≥ 0.6 → litellm class: a declared hierarchy that
  precise is mechanized-enumerable (the baseline literally wrote `ast.walk`); depth is not hostility.
- **K4** invisible ratio < 0.05 AND token cover ≥ 0.95 AND deps < 30 → haystack class (the 0/1256
  sweep's per-contract shadow).

## Calibration lessons (they contradicted two prior assumptions)

1. **Naive grep-invisibility would have REJECTED sentry** (0 invisible deps: every dep declares a
   `group: Group` field). The seam metric for the win class is NOISE (token precision 0.034: 1988
   files say "Group", 68 are dependents), plus volume. Bar 2 is two-component: invisibility AND
   precision.
2. **Haystack's token precision (0.004) is LOWER than sentry's** ("Document" is an English word), so
   precision alone also misleads; K4's invisibility+cover+size composite is what kills it. No single
   number separates the classes; the composite does.

## Residual (documented, not hidden)

- **wagtail** is measurement-identical to a win at this gate's granularity (noisy token, no usable
  cover, affected 580). It died at GOLD-level analysis (the medium-slot dig: the 3 grep-dark deps are
  also absent from blast∪graph at both confidences → no discriminator) plus satisficing shape. That
  analysis needs a proposed gold, which exists downstream at Loop 3 scout time. The gate is the coarse
  sieve; wagtail-class candidates are why the scout dig and Event A remain in the pipeline.

## Caveats

- haystack + pretix were re-cloned at 2026-07-11 HEAD (the killed slots were never pinned; the
  original clones were gone). The measured properties are structural, not SHA-sensitive; still, these
  two cells are HEAD-of-day, not pinned-history.
- **saleor bar-4 must-FAIL did NOT reproduce:** the anchor break (#191 synthetic edge trips
  foldMemberCallers) was expected to fail blast on current main, but `blast ProductVariant
  --file saleor/product/models.py` resolved cleanly at 0.3 AND 0.7 (51 deps) on this binary + the
  pinned index. Either the fold-gate fix is in this build or the break needs its exact original repro
  form. VERIFY with the fold-gate branch owner before counting bar 4 as regression-tested; bar 4's
  loud-fail behavior itself is proven (a wrong `--file` hint fails exactly as designed).
- Gate cells run single-contract; a repo with a different viable contract can re-enter with it.
- **Sentry history note (resolved 2026-07-11):** the +0.60 headline cell did not derive from the
  on-disk runs (`sentry-provenance-incident-2026-07-11.md`, resolved by Luc: archives deleted as temp
  cleanup, standing repo-side cell = deps +0.10 / overall +0.03 / ◆). Sentry's must-PASS status in
  this backtest rests on its three confirmation-arm wins (Kimi +0.24, Devstral +0.29, GPT-5.5 +0.35),
  which are provenance-clean in their own results trees.
