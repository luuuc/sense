#!/usr/bin/env python3
"""Dead-code GT for axum: unused symbols in axum/src.

Axum is a Rust library. pub items are the public API — designed to be
called externally. We only flag:
1. Non-pub functions (internal helpers never referenced outside their file)
2. pub(crate) items only referenced in their own file

Output: qualified Rust paths like serve or into_make_service
"""

import os
import re

import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", ".."))
from gt.common import parse_args, output_json, read_file, is_test_file, EXCLUDE_DIRS, cap_dead_symbols, build_reference_map, has_same_file_usage

repo_path, domain, lang, commit, repo_name = parse_args()

domain_path = os.path.join(repo_path, domain)
if not os.path.isdir(domain_path):
    output_json(
        {"domain": domain, "dead_symbols": [], "status": "stub"},
        repo_name, commit, note_prefix="Domain not found",
    )
    sys.exit(0)

def _in_test_block(content, pos):
    """Check if position is inside a #[cfg(test)] module."""
    before = content[:pos]
    cfg_test_starts = [m.start() for m in re.finditer(r'#\[cfg\(test\)\]', before)]
    if not cfg_test_starts:
        return False
    last_cfg = cfg_test_starts[-1]
    after_cfg = content[last_cfg:]
    brace_pos = after_cfg.find('{')
    if brace_pos < 0:
        return False
    depth = 0
    abs_brace = last_cfg + brace_pos
    for i, ch in enumerate(content[abs_brace:], abs_brace):
        if ch == '{':
            depth += 1
        elif ch == '}':
            depth -= 1
            if depth == 0:
                return pos < i
    return pos > abs_brace


symbols = []  # (name, relpath, is_pub)

for root, dirs, files in os.walk(domain_path):
    dirs[:] = [d for d in dirs if d not in (".git", "target")]
    for fname in files:
        if not fname.endswith(".rs"):
            continue
        if is_test_file(fname):
            continue

        filepath = os.path.join(root, fname)
        if "/tests/" in filepath or filepath.endswith("/tests.rs"):
            continue
        relpath = os.path.relpath(filepath, repo_path)

        content = read_file(repo_path, relpath)
        if not content:
            continue

        for m in re.finditer(r'^\s*(pub(?:\([^)]*\))?\s+)?(?:async\s+)?fn\s+(\w+)', content, re.MULTILINE):
            if _in_test_block(content, m.start()):
                continue
            pub_prefix = m.group(1) or ""
            name = m.group(2)
            is_pub = pub_prefix.strip().startswith("pub") and "pub(crate)" not in pub_prefix
            if name.startswith("_"):
                continue
            symbols.append((name, relpath, is_pub))

        for m in re.finditer(r'^\s*(pub(?:\([^)]*\))?\s+)?(?:struct|enum|trait)\s+(\w+)', content, re.MULTILINE):
            if _in_test_block(content, m.start()):
                continue
            pub_prefix = m.group(1) or ""
            name = m.group(2)
            is_pub = pub_prefix.strip().startswith("pub") and "pub(crate)" not in pub_prefix
            symbols.append((name, relpath, is_pub))

# Check references — batch grep then in-memory matching
candidates = [(name, def_file) for name, def_file, is_pub in symbols
              if len(name) >= 3 and not is_pub]
symbol_names = [name for name, _ in candidates]
ref_map = build_reference_map(repo_path, symbol_names, include_args=["--include=*.rs"])

dead_scored = []
for name, def_file in candidates:
    ref_files = ref_map.get(name, set())
    external_refs = len(ref_files - {def_file})
    if external_refs == 0 and not has_same_file_usage(name, def_file, repo_path):
        dead_scored.append((name, external_refs))

output_json(
    {"domain": domain, "dead_symbols": cap_dead_symbols(dead_scored)},
    repo_name, commit,
    note_prefix="Dead-code by reference analysis (excludes pub API, capped at 100)",
)
