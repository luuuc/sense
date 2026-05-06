#!/usr/bin/env python3
"""Dead-code GT for flask: unused symbols in src/flask.

Extract from generic gen_dead_code. Python module-qualified names.
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

symbols = []  # (name, relpath, module_name)

for root, dirs, files in os.walk(domain_path):
    dirs[:] = [d for d in dirs if d not in (".git", "__pycache__", "node_modules")]
    for fname in files:
        if not fname.endswith(".py"):
            continue
        if is_test_file(fname):
            continue

        filepath = os.path.join(root, fname)
        relpath = os.path.relpath(filepath, repo_path)

        content = read_file(repo_path, relpath)
        if not content:
            continue

        # Derive module name from path
        module = relpath.replace("/", ".").replace(".py", "")

        for m in re.finditer(r'^(?:class|def)\s+(\w+)', content, re.MULTILINE):
            name = m.group(1)
            symbols.append((name, relpath, module))

# Check references — batch grep then in-memory matching
symbol_names = [name for name, _, _ in symbols if len(name) >= 3]
ref_map = build_reference_map(repo_path, symbol_names, include_args=["--include=*.py"])

dead_scored = []
for name, def_file, module in symbols:
    if len(name) < 3:
        continue
    ref_files = ref_map.get(name, set())
    external_refs = len(ref_files - {def_file})
    if external_refs == 0 and not has_same_file_usage(name, def_file, repo_path):
        dead_scored.append((name, external_refs))

output_json(
    {"domain": domain, "dead_symbols": cap_dead_symbols(dead_scored)},
    repo_name, commit,
    note_prefix="Dead-code by reference analysis for Python module (capped at 100)",
)
