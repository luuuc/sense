#!/usr/bin/env bash
set -euo pipefail

# Generate ground truth from grep/static analysis — NOT from Sense.
#
# Usage:
#   bash bench/gen-ground-truth.sh --repo flask --task callers
#   bash bench/gen-ground-truth.sh --repo flask              # all tasks for flask
#   bash bench/gen-ground-truth.sh                            # all repos, all tasks
#
# Writes to ground-truth/<repo>/<task>.json with status "verified"
# and a _note explaining how it was generated.

BENCH_DIR="$(cd "$(dirname "$0")" && pwd)"
GT_DIR="$BENCH_DIR/ground-truth"
REPOS_DIR="$BENCH_DIR/repos"
TASKS_DIR="$BENCH_DIR/tasks"
LIB_DIR="$BENCH_DIR/lib"
PINNED="$REPOS_DIR/PINNED_COMMITS.json"

FILTER_REPOS=""
FILTER_TASKS=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo) FILTER_REPOS="$2"; shift 2 ;;
    --task) FILTER_TASKS="$2"; shift 2 ;;
    -h|--help)
      echo "Usage: gen-ground-truth.sh [--repo r1,r2] [--task t1,t2]"
      echo ""
      echo "Generates ground truth from grep/static analysis (not from Sense)."
      echo "Writes to ground-truth/<repo>/<task>.json"
      exit 0
      ;;
    *) echo "Unknown argument: $1" >&2; exit 1 ;;
  esac
done

matches_filter() {
  local value="$1" filter="$2"
  [[ -z "$filter" ]] && return 0
  echo "$filter" | tr ',' '\n' | grep -qx "$value"
}

log() { echo "[gen-gt] $*" >&2; }

repo_language() {
  local repo="$1"
  python3 -c "
import json,sys
v = json.load(open(sys.argv[1])).get(sys.argv[2], {})
if isinstance(v, dict): print(v.get('language','unknown'))
else: print('unknown')
" "$PINNED" "$repo"
}

repo_commit() {
  local rp="$1"
  (cd "$rp" && git rev-parse --short HEAD 2>/dev/null || echo "unknown")
}

# --- Callers: grep for symbol usage across the codebase ---

gen_callers() {
  local repo="$1" rp="$2" symbol="$3" lang="$4" commit="$5"
  log "  callers: grepping for '$symbol' in $repo ($lang)..."

  local outfile="$GT_DIR/$repo/callers.json"
  mkdir -p "$(dirname "$outfile")"

  python3 - "$rp" "$symbol" "$lang" "$commit" "$repo" > "$outfile" << 'PYEOF'
import json, os, re, subprocess, sys

repo_path, symbol, lang, commit, repo_name = sys.argv[1:6]

# Build grep pattern based on language and symbol format
parts = re.split(r'[#.]', symbol)
base_name = parts[-1]  # method/function name
class_name = parts[0] if len(parts) > 1 else None

# Grep for the symbol name
cmd = ["grep", "-rn", "--include=*.rb", "--include=*.py", "--include=*.go",
       "--include=*.ts", "--include=*.tsx", "--include=*.js", "--include=*.jsx",
       "--include=*.java", "--include=*.rs",
       base_name, repo_path]
result = subprocess.run(cmd, capture_output=True, text=True, cwd=repo_path)

callers = []
seen = set()
for line in result.stdout.splitlines():
    # Parse grep output: file:line:content
    m = re.match(r'^(.+?):(\d+):(.+)$', line)
    if not m:
        continue
    filepath = m.group(1)
    lineno = m.group(2)
    content = m.group(3).strip()

    # Make path relative to repo
    if filepath.startswith(repo_path):
        filepath = filepath[len(repo_path):].lstrip("/")

    # Skip the definition itself (heuristic: def/func/class keywords)
    if re.search(r'\b(def|func|class|type|interface)\s+' + re.escape(base_name), content):
        continue

    # Try to extract the enclosing function/method name
    caller_name = None

    if lang == "python":
        # Look for def/class above this line
        try:
            with open(os.path.join(repo_path, filepath)) as f:
                lines = f.readlines()
            for i in range(int(lineno)-1, -1, -1):
                dm = re.match(r'\s*(def|class)\s+(\w+)', lines[i])
                if dm:
                    caller_name = dm.group(2)
                    break
        except (IOError, IndexError):
            pass

    elif lang == "go":
        try:
            with open(os.path.join(repo_path, filepath)) as f:
                lines = f.readlines()
            for i in range(int(lineno)-1, -1, -1):
                fm = re.match(r'^func\s+(?:\((\w+)\s+\*?(\w+)\)\s+)?(\w+)', lines[i])
                if fm:
                    receiver_type = fm.group(2)
                    func_name = fm.group(3)
                    caller_name = f"{receiver_type}.{func_name}" if receiver_type else func_name
                    break
        except (IOError, IndexError):
            pass

    elif lang == "ruby":
        try:
            with open(os.path.join(repo_path, filepath)) as f:
                lines = f.readlines()
            cls_stack = []
            method_name = None
            for i in range(int(lineno)):
                cm = re.match(r'\s*(class|module)\s+(\S+)', lines[i])
                if cm:
                    cls_stack.append(cm.group(2).split('<')[0].strip())
                mm = re.match(r'\s*def\s+(\w+[?!]?)', lines[i])
                if mm:
                    method_name = mm.group(1)
            if method_name:
                caller_name = method_name
                if cls_stack:
                    caller_name = f"{cls_stack[-1]}#{method_name}"
            elif cls_stack:
                caller_name = cls_stack[-1]
        except (IOError, IndexError):
            pass

    elif lang == "typescript":
        try:
            with open(os.path.join(repo_path, filepath)) as f:
                lines = f.readlines()
            for i in range(int(lineno)-1, -1, -1):
                fm = re.match(r'\s*(?:export\s+)?(?:async\s+)?function\s+(\w+)', lines[i])
                if fm:
                    caller_name = fm.group(1)
                    break
                fm = re.match(r'\s*(?:export\s+)?(?:class|interface)\s+(\w+)', lines[i])
                if fm:
                    caller_name = fm.group(1)
                    break
        except (IOError, IndexError):
            pass

    elif lang == "java":
        try:
            with open(os.path.join(repo_path, filepath)) as f:
                lines = f.readlines()
            for i in range(int(lineno)-1, -1, -1):
                fm = re.match(r'\s*(?:public|private|protected)?\s*(?:static\s+)?(?:\w+\s+)?(\w+)\s*\(', lines[i])
                if fm:
                    caller_name = fm.group(1)
                    break
                fm = re.match(r'\s*(?:public\s+)?(?:abstract\s+)?class\s+(\w+)', lines[i])
                if fm:
                    caller_name = fm.group(1)
                    break
        except (IOError, IndexError):
            pass

    elif lang == "rust":
        try:
            with open(os.path.join(repo_path, filepath)) as f:
                lines = f.readlines()
            for i in range(int(lineno)-1, -1, -1):
                fm = re.match(r'\s*(?:pub\s+)?(?:async\s+)?fn\s+(\w+)', lines[i])
                if fm:
                    caller_name = fm.group(1)
                    break
                fm = re.match(r'\s*(?:pub\s+)?(?:struct|enum|trait|impl)\s+(\w+)', lines[i])
                if fm:
                    caller_name = fm.group(1)
                    break
        except (IOError, IndexError):
            pass

    if caller_name:
        entry = f"{filepath}:{caller_name}"
    else:
        entry = f"{filepath}:{lineno}"

    if entry not in seen:
        seen.add(entry)
        callers.append(entry)

output = {
    "symbol": symbol,
    "callers": sorted(callers),
    "_note": f"Generated by grep across {repo_name} at {commit}. Verified independently of any indexing tool.",
    "_generated_by": "gen-ground-truth.sh",
    "status": "verified"
}
print(json.dumps(output, indent=2))
PYEOF

  local count
  count=$(python3 -c "import json; print(len(json.load(open('$outfile')).get('callers',[])))")
  log "  callers: found $count callers → $outfile"
}

# --- Blast radius: grep for symbol references ---

gen_blast_radius() {
  local repo="$1" rp="$2" symbol="$3" lang="$4" commit="$5"
  log "  blast-radius: grepping for '$symbol' references in $repo..."

  local outfile="$GT_DIR/$repo/blast-radius.json"
  mkdir -p "$(dirname "$outfile")"

  python3 - "$rp" "$symbol" "$lang" "$commit" "$repo" > "$outfile" << 'PYEOF'
import json, os, re, subprocess, sys

repo_path, symbol, lang, commit, repo_name = sys.argv[1:6]

# Grep for the symbol
cmd = ["grep", "-rln", "--include=*.rb", "--include=*.py", "--include=*.go",
       "--include=*.ts", "--include=*.tsx", "--include=*.js", "--include=*.jsx",
       "--include=*.java", "--include=*.rs",
       symbol, repo_path]
result = subprocess.run(cmd, capture_output=True, text=True, cwd=repo_path)

affected = []
seen_files = set()
for line in result.stdout.splitlines():
    filepath = line.strip()
    if filepath.startswith(repo_path):
        filepath = filepath[len(repo_path):].lstrip("/")
    if filepath in seen_files:
        continue
    seen_files.add(filepath)

    # Extract the primary symbol defined in this file
    file_symbol = None
    try:
        with open(os.path.join(repo_path, filepath)) as f:
            content = f.read(8000)  # first 8KB

        if lang == "python":
            m = re.search(r'^class\s+(\w+)', content, re.MULTILINE)
            if m: file_symbol = m.group(1)
            else:
                m = re.search(r'^def\s+(\w+)', content, re.MULTILINE)
                if m: file_symbol = m.group(1)
        elif lang == "go":
            m = re.search(r'^type\s+(\w+)\s+struct', content, re.MULTILINE)
            if m: file_symbol = m.group(1)
            else:
                m = re.search(r'^func\s+(\w+)', content, re.MULTILINE)
                if m: file_symbol = m.group(1)
        elif lang == "ruby":
            m = re.search(r'^\s*class\s+(\S+)', content, re.MULTILINE)
            if m: file_symbol = m.group(1).split('<')[0].strip()
            else:
                m = re.search(r'^\s*module\s+(\S+)', content, re.MULTILINE)
                if m: file_symbol = m.group(1)
        elif lang == "typescript":
            m = re.search(r'(?:export\s+)?(?:class|interface|type)\s+(\w+)', content)
            if m: file_symbol = m.group(1)
            else:
                m = re.search(r'(?:export\s+)?function\s+(\w+)', content)
                if m: file_symbol = m.group(1)
        elif lang == "java":
            m = re.search(r'(?:public\s+)?(?:abstract\s+)?class\s+(\w+)', content)
            if m: file_symbol = m.group(1)
            else:
                m = re.search(r'(?:public\s+)?interface\s+(\w+)', content)
                if m: file_symbol = m.group(1)
        elif lang == "rust":
            m = re.search(r'(?:pub\s+)?(?:struct|enum|trait)\s+(\w+)', content)
            if m: file_symbol = m.group(1)
            else:
                m = re.search(r'(?:pub\s+)?(?:async\s+)?fn\s+(\w+)', content)
                if m: file_symbol = m.group(1)
    except IOError:
        pass

    entry = f"{filepath}:{file_symbol}" if file_symbol else filepath
    affected.append(entry)

output = {
    "symbol": symbol,
    "affected": sorted(affected),
    "_note": f"Files referencing '{symbol}' found by grep -rln across {repo_name} at {commit}. Verified independently of any indexing tool.",
    "_generated_by": "gen-ground-truth.sh",
    "status": "verified"
}
print(json.dumps(output, indent=2))
PYEOF

  local count
  count=$(python3 -c "import json; print(len(json.load(open('$outfile')).get('affected',[])))")
  log "  blast-radius: found $count affected files → $outfile"
}

# --- Dead code: find symbols defined but never referenced elsewhere ---

gen_dead_code() {
  local repo="$1" rp="$2" domain="$3" lang="$4" commit="$5"
  log "  dead-code: scanning '$domain' in $repo for unreferenced symbols..."

  local outfile="$GT_DIR/$repo/dead-code.json"
  mkdir -p "$(dirname "$outfile")"

  python3 - "$rp" "$domain" "$lang" "$commit" "$repo" > "$outfile" << 'PYEOF'
import json, os, re, subprocess, sys

repo_path, domain, lang, commit, repo_name = sys.argv[1:6]

domain_path = os.path.join(repo_path, domain)
if not os.path.isdir(domain_path):
    print(json.dumps({"domain": domain, "dead_symbols": [], "status": "stub",
                       "_note": f"Domain {domain} not found"}))
    sys.exit(0)

# Collect defined symbols in the domain
symbols = []

for root, dirs, files in os.walk(domain_path):
    for fname in files:
        filepath = os.path.join(root, fname)
        relpath = os.path.relpath(filepath, repo_path)

        ext = os.path.splitext(fname)[1]
        if ext not in {'.py', '.go', '.rb', '.ts', '.tsx', '.js', '.jsx', '.java', '.rs'}:
            continue
        if '_test.' in fname or 'test_' in fname or '_spec.' in fname or 'spec_' in fname or fname.endswith('Test.java') or fname.endswith('Tests.java'):
            continue

        try:
            with open(filepath) as f:
                content = f.read()
        except IOError:
            continue

        if lang == "python":
            for m in re.finditer(r'^(?:class|def)\s+(\w+)', content, re.MULTILINE):
                symbols.append((m.group(1), relpath))
        elif lang == "go":
            for m in re.finditer(r'^(?:func|type)\s+(\w+)', content, re.MULTILINE):
                name = m.group(1)
                if name[0].isupper():
                    symbols.append((name, relpath))
        elif lang == "ruby":
            for m in re.finditer(r'^\s*(?:def|class|module)\s+(\w+[?!]?)', content, re.MULTILINE):
                symbols.append((m.group(1), relpath))
        elif lang in ("typescript", "javascript"):
            for m in re.finditer(r'(?:export\s+)?(?:function|class|interface|type|const|let|var)\s+(\w+)', content):
                symbols.append((m.group(1), relpath))
        elif lang == "java":
            for m in re.finditer(r'(?:public|private|protected)?\s*(?:static\s+)?(?:class|interface|enum)\s+(\w+)', content):
                symbols.append((m.group(1), relpath))
            for m in re.finditer(r'(?:public|private|protected)\s+(?:static\s+)?(?:\w+\s+)(\w+)\s*\(', content):
                symbols.append((m.group(1), relpath))
        elif lang == "rust":
            for m in re.finditer(r'(?:pub\s+)?(?:struct|enum|trait)\s+(\w+)', content):
                symbols.append((m.group(1), relpath))
            for m in re.finditer(r'(?:pub\s+)?(?:async\s+)?fn\s+(\w+)', content):
                name = m.group(1)
                if name[0].islower() and name.startswith('_'):
                    continue
                symbols.append((name, relpath))

# Check each symbol for references outside its defining file.
# Single-pass: grep once for all symbols, then count per-symbol file hits.
import tempfile

unique_names = sorted({s[0] for s in symbols if len(s[0]) >= 3})
sym_ref_files = {name: set() for name in unique_names}

if unique_names:
    with tempfile.NamedTemporaryFile(mode='w', suffix='.txt', delete=False) as pf:
        for name in unique_names:
            pf.write(name + '\n')
        patterns_path = pf.name

    # Single grep pass: find all files containing any of the symbols
    cmd = ["grep", "-rlw", "--include=*.rb", "--include=*.py", "--include=*.go",
           "--include=*.ts", "--include=*.tsx", "--include=*.js", "--include=*.jsx",
           "--include=*.java", "--include=*.rs",
           "-f", patterns_path, repo_path]
    result = subprocess.run(cmd, capture_output=True, text=True)

    candidate_files = []
    for line in result.stdout.splitlines():
        fp = line.strip()
        if fp.startswith(repo_path):
            fp = fp[len(repo_path):].lstrip("/")
        if '_test.' in fp or 'test_' in fp or '_spec.' in fp or 'spec_' in fp or fp.endswith('Test.java') or fp.endswith('Tests.java'):
            continue
        candidate_files.append(fp)

    # For each file, check which symbols it contains
    name_set = set(unique_names)
    for fp in candidate_files:
        try:
            with open(os.path.join(repo_path, fp)) as f:
                content = f.read()
        except IOError:
            continue
        for name in unique_names:
            if re.search(r'\b' + re.escape(name) + r'\b', content):
                sym_ref_files[name].add(fp)

    os.unlink(patterns_path)

dead = []
for sym_name, sym_file in symbols:
    if len(sym_name) < 3:
        continue
    if len(sym_ref_files.get(sym_name, set())) <= 1:
        dead.append(sym_name)

output = {
    "domain": domain,
    "dead_symbols": sorted(set(dead)),
    "_note": f"Symbols in {domain} referenced only in their defining file. Found by grep -w across {repo_name} at {commit}. Excludes test files. Verified independently of any indexing tool.",
    "_generated_by": "gen-ground-truth.sh",
    "status": "verified"
}
print(json.dumps(output, indent=2))
PYEOF

  local count
  count=$(python3 -c "import json; print(len(json.load(open('$outfile')).get('dead_symbols',[])))")
  log "  dead-code: found $count dead symbols → $outfile"
}

# --- Semantic search: human-curated stubs (cannot be grep-generated) ---

gen_semantic_search() {
  local repo="$1" rp="$2" concept="$3" lang="$4" commit="$5"
  local outfile="$GT_DIR/$repo/semantic-search.json"

  if [[ -f "$outfile" ]]; then
    local existing_gen
    existing_gen=$(python3 -c "import json; print(json.load(open('$outfile')).get('_generated_by',''))" 2>/dev/null || echo "")
    if [[ "$existing_gen" != "gen-ground-truth.sh" ]]; then
      log "  semantic-search: keeping existing hand-curated ground truth (review manually for Sense bias)"
      return
    fi
  fi
  log "  semantic-search: SKIPPED — requires human curation, not generatable by grep"
  log "  semantic-search: review $outfile manually for Sense-sourced bias"
}

# --- Qualitative tasks: keep existing keywords (not generatable) ---

gen_qualitative() {
  local task="$1" repo="$2"
  local outfile="$GT_DIR/$repo/$task.json"
  if [[ -f "$outfile" ]]; then
    log "  $task: keeping existing keywords (qualitative — not generatable by grep)"
  else
    log "  $task: no ground truth file exists — create manually"
  fi
}

# --- Main ---

for taskfile in "$TASKS_DIR"/*.yaml; do
  task=$(basename "$taskfile" .yaml)
  matches_filter "$task" "$FILTER_TASKS" || continue

  task_json=$(python3 "$LIB_DIR/parse_task.py" "$taskfile")
  scoring_type=$(echo "$task_json" | python3 -c "import sys,json; print(json.load(sys.stdin).get('scoring',{}).get('type',''))")

  for repo in $(echo "$task_json" | python3 -c "import sys,json; print(' '.join(json.load(sys.stdin).get('repos',{}).keys()))"); do
    matches_filter "$repo" "$FILTER_REPOS" || continue

    rp="$REPOS_DIR/$repo"
    if [[ ! -d "$rp" ]]; then
      log "SKIP $repo/$task — repo not cloned"
      continue
    fi

    lang=$(repo_language "$repo")
    commit=$(repo_commit "$rp")
    log "$repo/$task (lang=$lang, commit=$commit)"

    # Get task-specific variable
    var_value=$(echo "$task_json" | python3 -c "
import sys,json
t=json.load(sys.stdin)
params=t.get('repos',{}).get('$repo',{})
for v in t.get('variables',[]):
  print(params.get(v,''))
  break
else:
  print('')
")

    case "$task" in
      callers)         gen_callers "$repo" "$rp" "$var_value" "$lang" "$commit" ;;
      blast-radius)    gen_blast_radius "$repo" "$rp" "$var_value" "$lang" "$commit" ;;
      dead-code)       gen_dead_code "$repo" "$rp" "$var_value" "$lang" "$commit" ;;
      semantic-search) gen_semantic_search "$repo" "$rp" "$var_value" "$lang" "$commit" ;;
      *)               gen_qualitative "$task" "$repo" ;;
    esac
  done
done

log ""
log "Done. Review generated files in $GT_DIR/"
log "Semantic-search ground truth must be curated manually — grep cannot determine relevance rankings."
