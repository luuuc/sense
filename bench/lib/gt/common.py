"""Shared helpers for per-repo ground-truth generators.

Each per-repo generator imports what it needs. Generators own accuracy —
if a helper doesn't fit a repo, the generator does it differently.
"""

import json
import os
import re
import subprocess
import sys

EXCLUDE_DIRS = [
    "--exclude-dir=.git",
    "--exclude-dir=node_modules",
    "--exclude-dir=vendor",
    "--exclude-dir=site-packages",
    "--exclude-dir=__pycache__",
    "--exclude-dir=tmp",
    "--exclude-dir=log",
    "--exclude-dir=.bench-crg-venv",
    "--exclude-dir=.bench-sense-venv",
    "--exclude-dir=.bench-grepai-venv",
]

SOURCE_EXTENSIONS = [
    "--include=*.rb", "--include=*.py", "--include=*.go",
    "--include=*.ts", "--include=*.tsx", "--include=*.js", "--include=*.jsx",
    "--include=*.java", "--include=*.kt", "--include=*.rs",
]

TEST_FILE_PATTERNS = (
    "_test.", "test_", "_spec.", "spec_",
    "Test.java", "Tests.java",
    "test.ts", "test.tsx", "test.js",
)


def is_test_file(filepath):
    base = os.path.basename(filepath)
    return any(p in base for p in TEST_FILE_PATTERNS)


def is_comment_line(line):
    stripped = line.lstrip()
    return (
        stripped.startswith("//")
        or stripped.startswith("#")
        or stripped.startswith("*")
        or stripped.startswith("/*")
        or stripped.startswith("<!--")
    )


def strip_strings(line):
    return re.sub(r'''(["'`]).*?\1''', '', line)


def symbol_in_non_string_context(line, symbol):
    no_strings = strip_strings(line)
    return bool(re.search(r'\b' + re.escape(symbol) + r'\b', no_strings))


def grep_files(repo_path, pattern, extra_args=None):
    cmd = ["grep", "-rln"] + SOURCE_EXTENSIONS + EXCLUDE_DIRS
    if extra_args:
        cmd.extend(extra_args)
    cmd.extend([pattern, repo_path])
    result = subprocess.run(cmd, capture_output=True, text=True, cwd=repo_path)
    files = []
    for line in result.stdout.splitlines():
        fp = line.strip()
        if fp.startswith(repo_path):
            fp = fp[len(repo_path):].lstrip("/")
        files.append(fp)
    return files


def grep_lines(repo_path, pattern, extra_args=None):
    cmd = ["grep", "-rnw"] + SOURCE_EXTENSIONS + EXCLUDE_DIRS
    if extra_args:
        cmd.extend(extra_args)
    cmd.extend([pattern, repo_path])
    result = subprocess.run(cmd, capture_output=True, text=True, cwd=repo_path)
    matches = []
    for line in result.stdout.splitlines():
        m = re.match(r'^(.+?):(\d+):(.+)$', line)
        if not m:
            continue
        filepath = m.group(1)
        if filepath.startswith(repo_path):
            filepath = filepath[len(repo_path):].lstrip("/")
        matches.append({
            "file": filepath,
            "line": int(m.group(2)),
            "content": m.group(3).strip(),
        })
    return matches


def read_file(repo_path, filepath, max_bytes=102400):
    full_path = os.path.join(repo_path, filepath)
    try:
        size = os.path.getsize(full_path)
        with open(full_path, errors="replace") as f:
            return f.read() if size <= max_bytes else f.read(max_bytes)
    except (IOError, OSError):
        return ""


def read_lines(repo_path, filepath):
    full_path = os.path.join(repo_path, filepath)
    try:
        with open(full_path, errors="replace") as f:
            return f.readlines()
    except (IOError, OSError):
        return []


# --- Scope-walk: find enclosing function/method from a line number ---

def find_enclosing_go(lines, lineno):
    for i in range(lineno - 1, -1, -1):
        fm = re.match(r'^func\s+(?:\((\w+)\s+\*?(\w+)\)\s+)?(\w+)', lines[i])
        if fm:
            receiver_type = fm.group(2)
            func_name = fm.group(3)
            return f"{receiver_type}.{func_name}" if receiver_type else func_name
    return None


def find_enclosing_python(lines, lineno):
    for i in range(lineno - 1, -1, -1):
        dm = re.match(r'\s*(def|class)\s+(\w+)', lines[i])
        if dm:
            return dm.group(2)
    return None


def find_enclosing_ruby(lines, lineno):
    cls_stack = []
    method_name = None
    for i in range(lineno):
        cm = re.match(r'\s*(class|module)\s+(\S+)', lines[i])
        if cm:
            cls_stack.append(cm.group(2).split('<')[0].strip())
        mm = re.match(r'\s*def\s+(\w+[?!]?)', lines[i])
        if mm:
            method_name = mm.group(1)
    if method_name:
        if cls_stack:
            return f"{cls_stack[-1]}#{method_name}"
        return method_name
    if cls_stack:
        return cls_stack[-1]
    return None


def find_enclosing_typescript(lines, lineno):
    for i in range(lineno - 1, -1, -1):
        fm = re.match(r'\s*(?:export\s+)?(?:async\s+)?function\s+(\w+)', lines[i])
        if fm:
            return fm.group(1)
        fm = re.match(r'\s*(?:export\s+)?(?:class|interface)\s+(\w+)', lines[i])
        if fm:
            return fm.group(1)
    return None


_JAVA_KEYWORDS = {"if", "while", "for", "switch", "catch", "when", "return", "throw", "synchronized"}


def find_enclosing_java(lines, lineno):
    for i in range(lineno - 1, -1, -1):
        fm = re.match(r'\s*(?:public|private|protected)?\s*(?:static\s+)?(?:\w+\s+)?(\w+)\s*\(', lines[i])
        if fm and fm.group(1) not in _JAVA_KEYWORDS:
            return fm.group(1)
        fm = re.match(r'\s*(?:public\s+)?(?:abstract\s+)?class\s+(\w+)', lines[i])
        if fm:
            return fm.group(1)
    return None


def find_enclosing_rust(lines, lineno):
    for i in range(lineno - 1, -1, -1):
        fm = re.match(r'\s*(?:pub\s+)?(?:async\s+)?fn\s+(\w+)', lines[i])
        if fm:
            return fm.group(1)
        fm = re.match(r'\s*(?:pub\s+)?(?:struct|enum|trait|impl)\s+(\w+)', lines[i])
        if fm:
            return fm.group(1)
    return None


SCOPE_FINDERS = {
    "go": find_enclosing_go,
    "python": find_enclosing_python,
    "ruby": find_enclosing_ruby,
    "typescript": find_enclosing_typescript,
    "java": find_enclosing_java,
    "rust": find_enclosing_rust,
}


def find_enclosing_scope(lang, lines, lineno):
    finder = SCOPE_FINDERS.get(lang)
    if finder:
        return finder(lines, lineno)
    return None


# --- Definition-line exclusion patterns ---

def is_definition_line(line, symbol, lang):
    esc = re.escape(symbol)
    if re.search(r'\b(def|func|class|type|interface)\s+' + esc + r'\b', line):
        return True
    if lang == "go" and re.search(r'\bfunc\s*\([^)]*\)\s+' + esc + r'\b', line):
        return True
    if lang == "ruby" and re.search(r'\bdef\s+self\.' + esc + r'\b', line):
        return True
    if lang == "python" and re.search(r'\basync\s+def\s+' + esc + r'\b', line):
        return True
    return False


def build_reference_map(repo_path, symbol_names, include_args=None):
    """Build {symbol: set(relative_files)} with one grep + in-memory matching.

    Instead of N subprocess calls (one per symbol), does:
    1. One `grep -rlwF -f patterns` to find candidate files
    2. One read per candidate file, intersect words with symbol set
    """
    import tempfile

    unique = set(symbol_names)
    if not unique:
        return {}

    with tempfile.NamedTemporaryFile(mode='w', suffix='.txt', delete=False) as f:
        f.write('\n'.join(sorted(unique)))
        patterns_file = f.name

    try:
        cmd = ["grep", "-rlwF", "-f", patterns_file]
        if include_args:
            cmd.extend(include_args)
        cmd.extend(EXCLUDE_DIRS)
        cmd.append(repo_path)

        result = subprocess.run(cmd, capture_output=True, text=True)
        candidate_files = []
        for line in result.stdout.splitlines():
            fp = line.strip()
            if fp.startswith(repo_path):
                fp = fp[len(repo_path):].lstrip("/")
            if not is_test_file(fp):
                candidate_files.append(fp)
    finally:
        os.unlink(patterns_file)

    ref_map = {name: set() for name in unique}
    word_re = re.compile(r'\w+')

    for fp in candidate_files:
        content = read_file(repo_path, fp)
        if not content:
            continue
        words_in_file = set(word_re.findall(content))
        for name in unique & words_in_file:
            ref_map[name].add(fp)

    return ref_map


def has_same_file_usage(name, def_file, repo_path):
    """Check if symbol is referenced within its definition file beyond the definition itself."""
    full_path = os.path.join(repo_path, def_file)
    result = subprocess.run(
        ["grep", "-cw", name, full_path],
        capture_output=True, text=True,
    )
    count = int(result.stdout.strip()) if result.returncode == 0 else 0
    return count > 1


DEAD_CODE_CAP = 100


def cap_dead_symbols(dead_with_scores, cap=DEAD_CODE_CAP):
    """Cap dead symbols to the most clearly dead (fewest external refs).

    Input: list of (symbol_name, ref_count) tuples where ref_count is the
    number of non-test files referencing the symbol (0 = strongest dead signal).
    Returns: list of symbol names, sorted, capped at `cap`.
    """
    seen = set()
    deduped = []
    for name, score in dead_with_scores:
        if name not in seen:
            seen.add(name)
            deduped.append((name, score))
    deduped.sort(key=lambda x: (x[1], x[0]))
    return sorted([name for name, _ in deduped[:cap]])


# --- JSON output formatting ---

def output_json(data, repo_name, commit, note_prefix="Generated"):
    data["_note"] = f"{note_prefix} for {repo_name} at {commit}. Verified independently of any indexing tool."
    data["_generated_by"] = "gen-ground-truth.sh"
    data["status"] = "verified"
    print(json.dumps(data, indent=2))


# --- CLI argument parsing (standard for all generators) ---

def parse_args():
    if len(sys.argv) < 5:
        print(f"Usage: {sys.argv[0]} <repo_path> <symbol_or_domain> <lang> <commit> [<repo_name>]",
              file=sys.stderr)
        sys.exit(1)
    repo_path = sys.argv[1]
    symbol_or_domain = sys.argv[2]
    lang = sys.argv[3]
    commit = sys.argv[4]
    repo_name = sys.argv[5] if len(sys.argv) > 5 else os.path.basename(repo_path)
    return repo_path, symbol_or_domain, lang, commit, repo_name
