#!/usr/bin/env bash
# snapshot-result.sh — capture one repo's baseline-vs-sense result into the
# launch folder so it survives the next `rm -rf bench/results` and feeds the
# articles. Reads the session model from run_meta.json and files everything
# under .doc/launch/02-rails-vertical/results/<repo>/<model>/.
#
# Usage:  bash bench/lib/snapshot-result.sh <repo>
# Run it right after a bench run, before re-running another model.

set -euo pipefail

REPO="${1:-}"
[[ -n "$REPO" ]] || { echo "usage: snapshot-result.sh <repo>" >&2; exit 1; }

BENCH_DIR="$(cd "$(dirname "$0")/.." && pwd)"
PROJECT_ROOT="$(cd "$BENCH_DIR/.." && pwd)"
RESULTS_DIR="$BENCH_DIR/results"
DOC_RESULTS="$PROJECT_ROOT/.doc/launch/02-rails-vertical/results"

for arm in baseline sense; do
  [[ -f "$RESULTS_DIR/$arm/$REPO/run_meta.json" ]] || {
    echo "missing $RESULTS_DIR/$arm/$REPO/run_meta.json — run the bench first" >&2; exit 1; }
done

python3 - "$REPO" "$RESULTS_DIR" "$DOC_RESULTS" "$BENCH_DIR" <<'PY'
import json, os, sys, shutil

repo, results_dir, doc_results, bench_dir = sys.argv[1:5]

def load(p):
    with open(p) as f: return json.load(f)

meta = load(f"{results_dir}/sense/{repo}/run_meta.json")
model = (meta.get("model") or "unknown").replace("/", "_").replace(":", "_")
ts = meta.get("timestamp", "")

dest = f"{doc_results}/{repo}/{model}"
os.makedirs(dest, exist_ok=True)

# 1. Copy raw artifacts for both arms (the transcript is the article narrative).
for arm in ("baseline", "sense"):
    a = f"{dest}/{arm}"; os.makedirs(a, exist_ok=True)
    for fn in ("transcript.json", "judged.json", "scored.json", "run_meta.json", "claude.log", "channels.json"):
        src = f"{results_dir}/{arm}/{repo}/{fn}"
        if os.path.exists(src): shutil.copy2(src, f"{a}/{fn}")

# 2. Pull this repo's rows from report.json (fairness table).
rep = load(f"{bench_dir}/results/report.json")
rows = {}
for tbl in rep.get("tables", []):
    if tbl.get("repo") == repo:
        for r in tbl.get("rows", []):
            rows[r["tool"]] = r

def fnum(r, k, nd=3):
    v = r.get(k)
    return "—" if v is None else (f"{v:.{nd}f}" if isinstance(v, float) else str(v))

# 3. Per-step quality + tool usage from the raw files.
def steps_quality(arm):
    j = load(f"{results_dir}/{arm}/{repo}/judged.json")
    return [round(s.get("step_quality"), 4) for s in (j.get("steps") or []) if s.get("step_quality") is not None]

def tool_counts(arm):
    import re
    txt = open(f"{results_dir}/{arm}/{repo}/transcript.json").read()
    sense = {}
    for m in re.findall(r'"name":"mcp__sense__([a-z_]+)"', txt):
        sense[m] = sense.get(m, 0) + 1
    native = {}
    for m in re.findall(r'"name":"(Bash|Read|Grep|Glob)"', txt):
        native[m] = native.get(m, 0) + 1
    return sense, native

# 4. Write summary.md.
lines = []
lines.append(f"# {repo} — {model}\n")
lines.append(f"Scenario: **{meta.get('scenario','?')}**  ")
lines.append(f"Run: {ts}  ·  judge: claude-opus-4-7  ·  harness: {meta.get('harness','claude')}  ·  auth: {meta.get('auth_mode','?')}\n")
lines.append("## Fairness (baseline vs sense)\n")
lines.append("| Tool | Fairness | LLM Quality | Efficiency | Tokens | Time (s) | Cost | Grounded cites | Adoption |")
lines.append("|---|---|---|---|---|---|---|---|---|")
for tool in ("baseline", "sense"):
    r = rows.get(tool)
    if not r:
        lines.append(f"| {tool} | — | — | — | — | — | — | — | — |"); continue
    cg = f"{r.get('cites_grounded','?')}/{r.get('cites_total','?')}"
    lines.append(f"| {tool} | {fnum(r,'fairness_score')} | {fnum(r,'llm_quality',2)} | "
                 f"{fnum(r,'efficiency',2)} | {r.get('tokens','?')} | {fnum(r,'wall_time',1)} | "
                 f"${fnum(r,'cost_usd',2)} | {cg} | {fnum(r,'adoption_score',3)} |")

# Gold-target recall — the coverage metric (mention vs cited precision).
def gold(arm):
    return load(f"{results_dir}/{arm}/{repo}/scored.json").get("gold_recall")
gb, gs = gold("baseline"), gold("sense")
if gb or gs:
    lines.append("\n## Gold-target recall\n")
    lines.append("Mention = named the target at all (completeness); cited = pinned it to an exact `path:line` (precision).\n")
    lines.append("| Tool | Mention | Cited | By group (cited) |")
    lines.append("|---|---|---|---|")
    for tool, g in (("baseline", gb), ("sense", gs)):
        if not g:
            lines.append(f"| {tool} | — | — | — |"); continue
        grp = "; ".join(f"{k} {v['cited']}/{v['total']}" for k, v in g["groups"].items())
        lines.append(f"| {tool} | {g['mentioned']}/{g['total']} ({g['mention_recall']:.0%}) | "
                     f"{g['cited']}/{g['total']} ({g['cited_recall']:.0%}) | {grp} |")

# Local composite score — what we use for THIS campaign (not the locked fairness).
# Gold cited-recall weighted above the judge, per the decision to measure
# "did it find everything, precisely" over prose quality.
if (gb or gs) and rows:
    lines.append("\n## Local score (gold-weighted)\n")
    lines.append("`0.45 * gold_cited_recall + 0.35 * llm_quality + 0.20 * efficiency`. "
                 "Local only; the locked fairness_score is unchanged.\n")
    lines.append("| Tool | Local score | gold_cited | llm_quality | efficiency |")
    lines.append("|---|---|---|---|---|")
    for tool, g in (("baseline", gb), ("sense", gs)):
        r = rows.get(tool)
        if not (g and r):
            lines.append(f"| {tool} | — | — | — | — |"); continue
        q = r.get("llm_quality") or 0.0
        eff = r.get("efficiency") or 0.0
        gc = g["cited_recall"]
        local = 0.45 * gc + 0.35 * q + 0.20 * eff
        lines.append(f"| {tool} | {local:.3f} | {gc:.0%} | {q:.2f} | {eff:.2f} |")

# Token detail — billed (cost) vs context loaded (the real "pulls in less" story).
def metrics(arm):
    return load(f"{results_dir}/{arm}/{repo}/scored.json").get("metrics", {})
mb, ms = metrics("baseline"), metrics("sense")
if mb and ms:
    lines.append("\n## Tokens\n")
    lines.append("Billed = uncached input + output (what you pay after caching). "
                 "Context = cache_read + uncached input (how much codebase the agent pulled in).\n")
    lines.append("| Tool | Billed | Context loaded | Output |")
    lines.append("|---|---|---|---|")
    for tool, m in (("baseline", mb), ("sense", ms)):
        billed = m.get("token_total_billed", 0)
        ctx = m.get("token_cache_read", 0) + m.get("token_input_uncached", 0)
        lines.append(f"| {tool} | {billed:,} | {ctx:,} | {m.get('token_output',0):,} |")
    def delta(a, b):
        return f"{(b-a)/a*100:+.0f}%" if a else "—"
    lines.append(f"\nSense vs baseline: billed {delta(mb.get('token_total_billed',0), ms.get('token_total_billed',0))}, "
                 f"context {delta(mb.get('token_cache_read',0)+mb.get('token_input_uncached',0), ms.get('token_cache_read',0)+ms.get('token_input_uncached',0))}.")

lines.append("\n## Per-step LLM quality\n")
lines.append("| Tool | " + " | ".join(f"step {i+1}" for i in range(max(len(steps_quality('baseline')), len(steps_quality('sense'))))) + " |")
lines.append("|---|" + "---|" * max(len(steps_quality('baseline')), len(steps_quality('sense'))))
for tool in ("baseline", "sense"):
    lines.append(f"| {tool} | " + " | ".join(str(q) for q in steps_quality(tool)) + " |")

lines.append("\n## Tool usage\n")
for tool in ("baseline", "sense"):
    s, n = tool_counts(tool)
    senses = ", ".join(f"{k}×{v}" for k, v in sorted(s.items())) or "none"
    natives = ", ".join(f"{k}×{v}" for k, v in sorted(n.items())) or "none"
    lines.append(f"- **{tool}** — sense: {senses}  ·  native: {natives}")

# Sense channel split (codex/opencode reach Sense via MCP *or* the `sense` CLI;
# the CLI calls hide inside the Bash count above, so the MCP-only adoption_score
# undercounts them). channels.json, when present, is the harness-correct metric.
def channels(arm):
    p = f"{results_dir}/{arm}/{repo}/channels.json"
    return load(p) if os.path.exists(p) else None
cb, cs = channels("baseline"), channels("sense")
if cb or cs:
    lines.append("\n## Sense channels (MCP vs CLI)\n")
    lines.append("For non-Claude harnesses Sense is reachable two ways; this is the true adoption.\n")
    lines.append("| Tool | MCP | CLI | total | preferred |")
    lines.append("|---|---|---|---|---|")
    for tool, c in (("baseline", cb), ("sense", cs)):
        if not c:
            lines.append(f"| {tool} | — | — | — | — |"); continue
        ch = c["channels"]
        lines.append(f"| {tool} | {ch['mcp_sense']} | {ch['cli_sense']} | "
                     f"{c['sense_total']} | {c['preferred']} |")

lines.append("\n## Finding\n")
lines.append("_(one honest paragraph: who won, on what axis, and why — fill in for the article)_\n")

with open(f"{dest}/summary.md", "w") as f:
    f.write("\n".join(lines) + "\n")

print(f"snapshot → {dest}/summary.md")
PY