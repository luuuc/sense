#!/usr/bin/env python3
"""Dead-code GT for discourse: unused symbols in app/services.

Discourse is a Rails app. We look for service classes/methods defined in
app/services that are never referenced from outside their own file.
Tighter than the generic approach: we check for actual constant/method
references, not just word matches.

Output: qualified names like TopicCreator#create
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

symbols = []  # (class_name, method_name, relpath, qualified)

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

        # Extract class/module definitions and their methods
        current_class = None
        for line in content.splitlines():
            cm = re.match(r'\s*(class|module)\s+(\S+)', line)
            if cm:
                current_class = cm.group(2).split('<')[0].strip()
                # Register the class itself
                symbols.append((current_class, None, relpath, current_class))

            mm = re.match(r'\s*def\s+(\w+[?!]?)', line)
            if mm and current_class:
                method = mm.group(1)
                if method == "initialize":
                    continue
                qualified = f"{current_class}#{method}"
                symbols.append((current_class, method, relpath, qualified))

# Check references — batch grep then in-memory matching
all_search_terms = set()
for class_name, method_name, def_file, qualified in symbols:
    term = method_name if method_name else class_name
    if len(term) >= 3:
        all_search_terms.add(term)
    if method_name and len(class_name) >= 3:
        all_search_terms.add(class_name)

ref_map = build_reference_map(repo_path, list(all_search_terms), include_args=["--include=*.rb"])

dead_scored = []
for class_name, method_name, def_file, qualified in symbols:
    search_term = method_name if method_name else class_name
    if len(search_term) < 3:
        continue

    ref_files = ref_map.get(search_term, set())
    external_refs = len(ref_files - {def_file})

    if method_name and external_refs > 0:
        class_refs = ref_map.get(class_name, set())
        real_refs = len((ref_files & class_refs) - {def_file})
        external_refs = real_refs

    if external_refs == 0 and not has_same_file_usage(search_term, def_file, repo_path):
        dead_scored.append((qualified, external_refs))

output_json(
    {"domain": domain, "dead_symbols": cap_dead_symbols(dead_scored)},
    repo_name, commit,
    note_prefix="Dead-code by class/method reference analysis (capped at 100)",
)
