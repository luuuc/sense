#!/usr/bin/env python3
"""Dead-code GT for maket: unused symbols in app/models.

Extract from generic gen_dead_code. Ruby qualified names.
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

symbols = []  # (qualified_name, bare_name, relpath)

for root, dirs, files in os.walk(domain_path):
    dirs[:] = [d for d in dirs if d not in (".git", "node_modules", "vendor", "tmp")]
    for fname in files:
        if not fname.endswith(".rb"):
            continue
        if is_test_file(fname):
            continue

        filepath = os.path.join(root, fname)
        relpath = os.path.relpath(filepath, repo_path)

        content = read_file(repo_path, relpath)
        if not content:
            continue

        current_class = None
        for line in content.splitlines():
            cm = re.match(r'\s*class\s+(\w+)', line)
            if cm:
                current_class = cm.group(1)
                symbols.append((cm.group(1), cm.group(1), relpath))
                continue
            mm = re.match(r'\s*module\s+(\w+)', line)
            if mm:
                current_class = mm.group(1)
                symbols.append((mm.group(1), mm.group(1), relpath))
                continue
            dm = re.match(r'\s*def\s+(\w+[?!]?)', line)
            if dm:
                bare = dm.group(1)
                qualified = f"{current_class}#{bare}" if current_class else bare
                symbols.append((qualified, bare, relpath))

# Check references using bare names
bare_names = [bare for _, bare, _ in symbols if len(bare) >= 3]
ref_map = build_reference_map(repo_path, bare_names, include_args=["--include=*.rb"])

dead_scored = []
for qualified, bare, def_file in symbols:
    if len(bare) < 3:
        continue
    ref_files = ref_map.get(bare, set())
    external_refs = len(ref_files - {def_file})
    if external_refs == 0 and not has_same_file_usage(bare, def_file, repo_path):
        dead_scored.append((qualified, external_refs))

output_json(
    {"domain": domain, "dead_symbols": cap_dead_symbols(dead_scored)},
    repo_name, commit,
    note_prefix="Dead-code by reference analysis for Ruby models (capped at 100)",
)
