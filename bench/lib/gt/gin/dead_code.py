#!/usr/bin/env python3
"""Dead-code GT for gin: unused symbols in the single-package repo.

Gin is a Go library. Exported (uppercase) symbols are the public API —
they're designed to be called externally, not internally. We only flag:
1. Unexported symbols with zero internal references outside their file
2. Exported symbols in ginS/ (deprecated wrapper — legitimate dead code)

Output: qualified names like gin.BasicAuth or ginS.LoadHTMLGlob
"""

import os
import re

import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", ".."))
from gt.common import parse_args, output_json, read_file, is_test_file, EXCLUDE_DIRS, cap_dead_symbols, build_reference_map, has_same_file_usage

repo_path, domain, lang, commit, repo_name = parse_args()

domain_path = os.path.join(repo_path, domain) if domain != "." else repo_path

symbols = []  # (name, relpath, package, is_exported)

for root, dirs, files in os.walk(domain_path):
    dirs[:] = [d for d in dirs if d not in (".git", "vendor", "node_modules", "testdata")]
    for fname in files:
        if not fname.endswith(".go"):
            continue
        if is_test_file(fname):
            continue

        filepath = os.path.join(root, fname)
        relpath = os.path.relpath(filepath, repo_path)

        content = read_file(repo_path, relpath)
        if not content:
            continue

        # Determine package from file
        pkg_match = re.search(r'^package\s+(\w+)', content, re.MULTILINE)
        pkg = pkg_match.group(1) if pkg_match else "main"

        # Collect function and type definitions
        for m in re.finditer(r'^func\s+(?:\(\w+\s+\*?(\w+)\)\s+)?(\w+)', content, re.MULTILINE):
            recv_type = m.group(1)
            func_name = m.group(2)
            if func_name == "init" or func_name == "main":
                continue
            is_exported = func_name[0].isupper()
            if recv_type:
                qualified = f"{pkg}.{recv_type}.{func_name}"
            else:
                qualified = f"{pkg}.{func_name}"
            symbols.append((func_name, relpath, pkg, is_exported, qualified))

        for m in re.finditer(r'^type\s+(\w+)\s+(?:struct|interface)', content, re.MULTILINE):
            name = m.group(1)
            is_exported = name[0].isupper()
            symbols.append((name, relpath, pkg, is_exported, f"{pkg}.{name}"))

# Check references — batch grep then in-memory matching
# Exclude exported symbols from the top-level gin package (public API).
# Sub-package exports (binding, render, etc.) stay as candidates since they
# may be internally dead even if technically importable.
candidates = [(name, def_file, pkg, is_exported, qualified)
              for name, def_file, pkg, is_exported, qualified in symbols
              if len(name) >= 3 and not (is_exported and pkg == "gin")]
symbol_names = [name for name, _, _, _, _ in candidates]
ref_map = build_reference_map(repo_path, symbol_names, include_args=["--include=*.go"])

dead_scored = []
for name, def_file, pkg, is_exported, qualified in candidates:
    ref_files = ref_map.get(name, set())
    external_refs = len(ref_files - {def_file})
    if external_refs == 0 and not has_same_file_usage(name, def_file, repo_path):
        dead_scored.append((qualified, external_refs))

output_json(
    {"domain": domain, "dead_symbols": cap_dead_symbols(dead_scored)},
    repo_name, commit,
    note_prefix="Dead-code by reference analysis (excludes public API, capped at 100)",
)
