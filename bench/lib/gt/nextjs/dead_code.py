#!/usr/bin/env python3
"""Dead-code GT for nextjs: unused symbols in packages/next/src/server.

Next.js is a TypeScript library. Public API exports are the product, not
dead code. We only flag symbols that are:
1. Not exported from the package (no 'export' keyword)
2. Or exported but never imported by any other file in the repo
3. Excluding type-only exports (interfaces, type aliases)

Output: qualified names like renderToHTMLOrFlight
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

TS_EXTS = (".ts", ".tsx", ".js", ".jsx")

symbols = []  # (name, relpath, is_exported)

for root, dirs, files in os.walk(domain_path):
    dirs[:] = [d for d in dirs if d not in (".git", "node_modules", ".next", "dist", "__tests__")]
    for fname in files:
        if not any(fname.endswith(ext) for ext in TS_EXTS):
            continue
        if is_test_file(fname):
            continue

        filepath = os.path.join(root, fname)
        relpath = os.path.relpath(filepath, repo_path)

        content = read_file(repo_path, relpath)
        if not content:
            continue

        for m in re.finditer(
            r'(?:export\s+)?(?:async\s+)?function\s+(\w+)',
            content,
        ):
            name = m.group(1)
            is_exported = 'export' in content[max(0, m.start()-20):m.start()+10]
            symbols.append((name, relpath, is_exported))

        for m in re.finditer(
            r'(?:export\s+)?class\s+(\w+)',
            content,
        ):
            name = m.group(1)
            is_exported = 'export' in content[max(0, m.start()-20):m.start()+10]
            symbols.append((name, relpath, is_exported))

# Check references — batch grep then in-memory matching
symbol_names = [name for name, _, _ in symbols if len(name) >= 3]
ref_map = build_reference_map(repo_path, symbol_names,
    include_args=["--include=*.ts", "--include=*.tsx", "--include=*.js", "--include=*.jsx"])

dead_scored = []
for name, def_file, is_exported in symbols:
    if len(name) < 3:
        continue
    ref_files = ref_map.get(name, set())
    external_refs = len(ref_files - {def_file})
    if external_refs == 0 and not has_same_file_usage(name, def_file, repo_path):
        dead_scored.append((name, external_refs))

output_json(
    {"domain": domain, "dead_symbols": cap_dead_symbols(dead_scored)},
    repo_name, commit,
    note_prefix="Dead-code by reference analysis (excludes test files, capped at 100)",
)
