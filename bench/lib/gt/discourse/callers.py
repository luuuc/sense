#!/usr/bin/env python3
"""Callers GT for discourse: who calls TopicCreator#create.

The compound symbol means we need BOTH TopicCreator and create to
co-occur in the same expression context. Plain grep for 'create' matches
everything in Rails. We require:
1. The file references TopicCreator (the class)
2. A call to .create or #create appears on a line that is plausibly
   calling TopicCreator's create method (not any other create)

Output: file:Class#method
"""

import json
import os
import re
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", ".."))
from gt.common import (
    parse_args, output_json, read_file, read_lines,
    is_comment_line, is_test_file, find_enclosing_ruby,
)

repo_path, symbol, lang, commit, repo_name = parse_args()

parts = re.split(r'[#.]', symbol)
class_name = parts[0]
method_name = parts[-1] if len(parts) > 1 else symbol

callers = set()
files_without_scope = set()

for root, dirs, files in os.walk(repo_path):
    dirs[:] = [d for d in dirs if d not in (".git", "node_modules", "vendor", "tmp", "log")]
    for fname in files:
        if not fname.endswith(".rb"):
            continue
        filepath = os.path.join(root, fname)
        relpath = os.path.relpath(filepath, repo_path)

        content = read_file(repo_path, relpath)
        if not content:
            continue

        # File must reference the class name
        if not re.search(r'\b' + re.escape(class_name) + r'\b', content):
            continue

        lines = read_lines(repo_path, relpath)

        for lineno_0, line in enumerate(lines):
            lineno = lineno_0 + 1
            stripped = line.strip()

            if is_comment_line(stripped):
                continue

            # Must have the method name on this line
            if not re.search(r'\b' + re.escape(method_name) + r'\b', line):
                continue

            # Must be a plausible call to class_name.method_name:
            # - TopicCreator.create(...)
            # - TopicCreator.new(...).create
            # - variable = TopicCreator.new; variable.create
            # - Or just .create on a line where TopicCreator is in scope
            is_direct_call = bool(re.search(
                re.escape(class_name) + r'.*\b' + re.escape(method_name) + r'\b', line
            ))

            # Check if class_name appears within ±5 lines (method chaining, variable assignment)
            is_nearby = False
            if not is_direct_call:
                window_start = max(0, lineno_0 - 5)
                window_end = min(len(lines), lineno_0 + 5)
                window = ''.join(lines[window_start:window_end])
                is_nearby = bool(re.search(r'\b' + re.escape(class_name) + r'\b', window))

            if not is_direct_call and not is_nearby:
                continue

            # Skip the definition itself
            if re.search(r'\bdef\s+' + re.escape(method_name) + r'\b', stripped):
                continue
            if re.search(r'\bclass\s+' + re.escape(class_name) + r'\b', stripped):
                continue

            # Find enclosing scope — deduplicate to one entry per file
            # when no named scope is found (common in RSpec DSL blocks)
            scope = find_enclosing_ruby(lines, lineno)
            if scope:
                callers.add(f"{relpath}:{scope}")
            elif relpath not in files_without_scope:
                files_without_scope.add(relpath)
                callers.add(relpath)

output_json(
    {"symbol": symbol, "callers": sorted(callers)},
    repo_name, commit,
    note_prefix="Callers by compound symbol co-occurrence analysis",
)
