#!/usr/bin/env python3
"""Rebuild a repo's variance page across EVERY model on disk.

runs-variance.sh truncates `variance/<repo>.md` and appends only the models in
that invocation's $MODELS. A campaign that benches one model at a time therefore
ends with a page describing whichever model ran last -- the go vertical published
four pages that all read "1 runs per model" and named only the final ollama arm,
while report.md linked them as the run-to-run spread behind the headline.

This regenerates the whole page from the results tree, so the page says what the
disk says. Read-only over the runs, $0.

Usage: variance_report.py <vertical-results-root> [repo ...]
"""
import glob
import os
import subprocess
import sys

LIB_DIR = os.path.dirname(os.path.abspath(__file__))
ROW = os.path.join(LIB_DIR, "variance-row.py")


def model_dirs(root):
    """Model roots (those holding a baseline/ or sense/ arm), newest name order."""
    out = []
    for name in sorted(os.listdir(root)):
        d = os.path.join(root, name)
        if name.startswith((".", "_")) or not os.path.isdir(d):
            continue
        if os.path.isdir(os.path.join(d, "baseline")) or os.path.isdir(os.path.join(d, "sense")):
            out.append((name, d))
    return out


def runs_for(model_dir, repo):
    return sorted(glob.glob(os.path.join(model_dir, "*", repo, "run-*")))


def render(root, repo):
    """The full markdown page for one repo, every model that benched it."""
    blocks, counts = [], []
    for name, mdir in model_dirs(root):
        runs = runs_for(mdir, repo)
        if not runs:
            continue
        counts.append(len(runs))
        env = dict(os.environ, RESULTS_DIR=mdir)
        proc = subprocess.run([sys.executable, ROW, repo, name],
                              capture_output=True, text=True, env=env)
        blocks.append(proc.stdout if proc.returncode == 0 else
                      f"\n## {name}\n\n_row aggregation failed: {proc.stderr.strip()[:200]}_\n")
    if not blocks:
        return None
    # Runs-per-model varies by arm and model, so state the range rather than a
    # single number the page cannot honour.
    lo, hi = min(counts), max(counts)
    span = f"{lo}" if lo == hi else f"{lo}-{hi}"
    head = [f"# {repo} - variance ({span} runs per model)", "",
            f"**models:** {len(blocks)}  ·  **repo:** {repo}", ""]
    return "\n".join(head) + "".join(blocks) + "\n"


def repos_in(root):
    found = set()
    for _, mdir in model_dirs(root):
        for arm in ("baseline", "sense"):
            adir = os.path.join(mdir, arm)
            if os.path.isdir(adir):
                found.update(r for r in os.listdir(adir)
                             if os.path.isdir(os.path.join(adir, r)))
    return sorted(found)


def main(argv):
    if len(argv) < 2:
        sys.exit("usage: variance_report.py <vertical-results-root> [repo ...]")
    root = argv[1]
    repos = argv[2:] or repos_in(root)
    out_dir = os.path.join(root, "variance")
    os.makedirs(out_dir, exist_ok=True)
    for repo in repos:
        page = render(root, repo)
        if page is None:
            continue
        path = os.path.join(out_dir, f"{repo}.md")
        with open(path, "w") as f:
            f.write(page)
        print(f"wrote {path}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
