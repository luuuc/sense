#!/usr/bin/env python3
"""Callers GT for axum: who calls Router.route.

Rust fn/impl scope walk for a compound type.method symbol.
"""

import json
import os
import re
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", ".."))
from gt.common import (
    parse_args, output_json, grep_lines, read_lines, read_file,
    is_comment_line, is_definition_line, symbol_in_non_string_context,
    find_enclosing_rust,
)

repo_path, symbol, lang, commit, repo_name = parse_args()

parts = re.split(r'[#.]', symbol)
class_name = parts[0] if len(parts) > 1 else None
base_name = parts[-1]

matches = grep_lines(repo_path, base_name)

callers = set()
file_cache = {}

for m in matches:
    filepath, lineno, content = m["file"], m["line"], m["content"]

    if is_comment_line(content):
        continue
    if is_definition_line(content, base_name, lang):
        continue
    if not symbol_in_non_string_context(content, base_name):
        continue

    if class_name:
        if filepath not in file_cache:
            file_cache[filepath] = read_file(repo_path, filepath)
        if not re.search(r'\b' + re.escape(class_name) + r'\b', file_cache[filepath]):
            continue

    lines = read_lines(repo_path, filepath)
    scope = find_enclosing_rust(lines, lineno)
    entry = f"{filepath}:{scope}" if scope else f"{filepath}:{lineno}"
    callers.add(entry)

output_json(
    {"symbol": symbol, "callers": sorted(callers)},
    repo_name, commit,
    note_prefix="Callers by grep with Rust scope-walk",
)
