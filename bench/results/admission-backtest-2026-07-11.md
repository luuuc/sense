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

## Addendum 2026-07-12 — bar 4 recalibrated with a graph-side fold probe

### The blind spot (proven, then closed)

Bar 4 was blast-only, and the 2026-07-11 caveat above ("saleor bar-4 must-FAIL did NOT reproduce")
is now explained: **a blast-only probe can never see a graph-side fold collapse.** On the SAME
pinned saleor index, the pre-fix v1.11.16 binary (the #191 fold collapse, fixed by PR #192) vs
current main:

- `graph ProductVariant --file saleor/product/models.py --direction callers`: called_by = **1**
  (v1.11.16, collapsed) vs **126** (main, healthy);
- `blast ProductVariant --file saleor/product/models.py` at 0.3 AND 0.7: **byte-identical dep
  sets** between the two binaries (27518 / 27494 bytes both sides).

The incident's gold deps were GRAPH-only (blast cap-evicts them under the production-first cap),
which is exactly why the oracle's blast∪graph union (`resolve_oracle.py`) caught the collapse
(union dropped to 1/4) while bar 4 stayed green. Bar 4 now carries the cheap half of that union.

### The new component and its calibrated rule

`admission_gate.py` bar 4 now also runs `sense graph <anchor> [--file HINT] --direction callers
--json` at the default floor and records `called_by` next to blast's `direct_callers` at 0.3.

**FAIL (fold collapse) when: `called_by <= 5 AND direct_callers >= 10 × max(called_by, 1)`.**

Calibration on the 8-cell slate (main binary, healthy): min called_by = 25 (healthchecks), max
direct/called_by ratio = 1.08 (healthchecks 27/25). The collapse cell: called_by = 1, direct = 60,
ratio 60×. The floor (5) and ratio (10×) sit with wide margin on both sides, and the conjunction
is what discriminates collapse from small-but-honest: a healthy small seam has BOTH numbers small,
so it never trips the ratio arm. If the graph probe itself errors (e.g. ambiguous naive symbol),
the component records the error and does not fire; the blast checks stand as before.

| cell (main binary) | called_by | blast direct @0.3 | fold_collapse |
|---|---|---|---|
| sentry / Group | 473 | 60 | no |
| netbox / Device | 267 | 33 | no |
| saleor / ProductVariant | 126 | 60 | no |
| wagtail / Page | 717 | 60 | no |
| healthchecks / Transport | 25 | 27 | no |
| litellm / BaseConfig | 42 | 40 | no |
| haystack / Document | 722 | 60 | no |
| pretix / Team | 105 | 26 | no |
| **saleor on v1.11.16** | **1** | **60** | **FAIL** |

### Verification (all three required, all green)

1. **v1.11.16 must-FAIL, now reproduces:** gate pointed at the v1.11.16 binary on the pinned
   saleor index emits `BAR 4 FAIL (graph fold-collapse: called_by=1 vs blast direct=60)` and
   returns before bars 2/3, the loud-fail path bar 4 was designed for.
2. **8/8 backtest verdicts PRESERVED** on main: sentry / netbox / saleor WIN-VIABLE (wagtail
   WIN-VIABLE, still the documented false positive), healthchecks K1+K2, litellm K3, pretix K2,
   haystack K4. All bar-2 numbers match the 2026-07-11 table exactly; haystack and pretix
   re-verified on their surviving 2026-07-11 HEAD-of-day clones and indexes.
3. **Sanity:** saleor on main passes bar 4 (called_by=126 healthy, fold_collapse=False) and keeps
   its WIN-VIABLE verdict.

`slot_verdict()` semantics and the measured/checklist split are unchanged for every cell the new
component does not fire on; the probe only adds a `bar4.graph` block and one render line.

## Addendum 2026-07-13 — calibration v3: precision numerator fix, verified-edge floor, K5, broken-index guard

Trigger: the post-31-05 hunt v2 board carried a bar-2 `precision > 1` anomaly on three rows
(pebble versionSet 6.0, grpc-go ChannelTrace 6.29, restic indexMap 12.0) and 24 residual bar-4
fold fires flagged as needing a verified-edge floor (loopA-gaps/go.md G-3 recount). Four changes
to `admission_gate.py`, all $0-verified same day:

1. **Bar-2 precision numerator = intersection.** Was `prod_dependent_files ÷ grep_hits`, which
   counts grep-INVISIBLE deps in the numerator — the metric exceeded 1 exactly on high-invisibility
   seams (the win class!). Now `deps grep actually finds ÷ grep_hits`, matching the battery rows.
   Verdicts unaffected (`slot_verdict` uses the battery token precision, which was always
   intersection-based). The three anomalous rows re-vetted: all three are REAL win shapes
   (corrected precisions 0.167 / 0.286 / 0.0; verified backing called_by 775 / 548 / 496).
2. **Bar-4 fold ratio runs on blast direct @0.7 (verified band), not @0.3.** Tiny internal types
   ride the 0.3 bare-name band to the 60-cap and faked all 24 hunt-v2 residual fires (called_by
   0-4 AND direct@0.7 0-4 → ratio can't trip). Positive control preserved: v1.11.16 binary on the
   saleor index (the #191 collapse) reads called_by=1 / direct@0.7=60 → still FAILS loudly; main
   reads called_by=126 → clean. Blast byte-identical across binaries, as in the 07-12 addendum.
   Hunt v3/v4 recount: fold fires 24 → **0** on the 84-gate medium board. G-3 reading (b)
   CONFIRMED for the entire residual class — no product defect behind those fires.
3. **K5 — verified-band starved.** The floor change re-routed the fabricated class from bar-4 FAIL
   to the WIN SIGNATURE (perfect invisibility + no cover + huge affected, all fabricated by
   0.3-band noise: etcd zapRaftLogger, grpc noCopy/componentData, nats waitQueue, pebble
   Frontiers/flusherCond, etcd concurrency.Mutex). Kill when `survive@0.7 < 5`: below that, the
   8-10-file gold bar is impossible under the blast-both-confidences law. Calibration: banked wins
   measure survive@0.7 = 68 (sentry) / 22 (netbox) / 51 (saleor); the fabricated class ≤ 3; the
   gap is clean. wagtail (41) correctly NOT killed — it stays the documented gold-level false
   positive no mechanical bar can see.
4. **MEASUREMENT-INVALID guard.** The litellm re-verification cell returned all-zeros (blast
   affected=0, graph called_by=0) and the gate confidently emitted K2 BALLAST-ONLY — on an index
   that `sense status` reports as BROKEN (schema v0 mismatch, 0 edges; plausibly the rescan Luc
   stopped mid-session on 2026-07-12). Zero-edges-everywhere now routes to MEASUREMENT-INVALID
   ("check index health"), never to a verdict.

### Re-verification on the fixed gate (fresh runs, 2026-07-13)

| cell | verdict | survive@0.7 | note |
|---|---|---|---|
| sentry / Group | WIN-VIABLE ✓ | 68 | numbers match 07-11 table |
| netbox / Device | WIN-VIABLE ✓ | 22 | post-rescan (v1.11.22) index |
| saleor / ProductVariant | WIN-VIABLE ✓ | 51 | pre-rescan index |
| wagtail / Page | WIN-VIABLE ⚠ ✓ | 41 | post-rescan index; still the documented FP; anchor moved to `wagtail/models/pages.py` on this HEAD |
| healthchecks / Transport | K1+K2 ✓ | 30 | post-rescan index |
| litellm / BaseConfig | MEASUREMENT-INVALID | — | broken index (schema v0, 0 edges); RE-RUN owed after its rescan-all slot heals it |
| haystack / Document | K4 ✓ | 23 | 07-11 clone |
| pretix / Team | K2 ✓ | 26 | 07-11 clone |

7/8 verdicts preserved with margins intact; the 8th is an honest instrument refusal on a broken
index, not a classification change. K5 fired on ZERO historical cells.

## Addendum 2026-07-13 (later) — K6 + the name-family union flag; the probe is the decider

Post-dolt-dry-run additions to `admission_gate.py` (the GO-NAMING LAW: Go accessor/provider
names inherit the type name, so holder-enumeration composes from one substring family):

- **battery row `type-accessor`** (`[.\t (]\w*<Type>\w*\(`) — the type-named call family.
- **K6 kill** when that family ALONE covers ≥0.8 of the deps, regardless of precision
  (receiver checks are window-resolvable — THE BATCHING LAW; noise does not protect).
  Fires on healthchecks (cover 0.839, already K1+K2-dead — correct-in-class). No banked
  win affected.
- **⚠ NAME-FAMILY UNION flag** (token ∪ type-accessor cover ≥0.8, NOT a kill): fires on
  netbox (0.926) and wagtail (0.976) — epistemically correct, both needed behavioral
  verification historically (wagtail died at gold; netbox won only via gold-vs-noise
  curation). sentry-class anchors will fire it too by construction; that is the point:
  high name-family cover = the probe decides, the gate cannot.
- **THE HONEST LIMIT, measured:** dolt itself — the case that motivated all of this —
  escapes BOTH the K6 kill and the union flag (its baseline composed idiom patterns like
  `DbData.Ddb` and `GetRemoteDB` that carry NO type substring). Conclusion, now law in the
  manifesto (§8 step 3b), Loop 3, and prompts/02: **the $0 ADVERSARY PROBE runs on EVERY
  anchor before gold curation, unconditionally.** The mechanical additions are detectors
  and priority flags, never a substitute.

Verdict-preservation battery (sweep-fresh indexes where available): dolt/versionSet/Batch/
client.Client/ChannelTrace/ignore.cache/indexMap/nats-Server WIN-VIABLE unchanged; etcd
GRAY; healthchecks/haystack/pretix BALLAST unchanged; netbox/wagtail WIN-VIABLE + flag.
sentry/saleor/litellm cells re-run when Luc's sweep reaches them.
