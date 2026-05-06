#!/usr/bin/env python3
"""Callers GT for nextjs: who calls renderToHTMLOrFlight.

TypeScript scope extraction for a bare function name.
"""

import json
import os
import re
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", ".."))
from gt.common import (
    parse_args, output_json, grep_lines, read_lines,
    is_comment_line, is_definition_line, symbol_in_non_string_context,
    find_enclosing_typescript,
)

repo_path, symbol, lang, commit, repo_name = parse_args()

parts = re.split(r'[#.]', symbol)
class_name = parts[0] if len(parts) > 1 else None
base_name = parts[-1]

matches = grep_lines(repo_path, base_name)

callers = set()

for m in matches:
    filepath, lineno, content = m["file"], m["line"], m["content"]

    if is_comment_line(content):
        continue
    if is_definition_line(content, base_name, lang):
        continue
    if not symbol_in_non_string_context(content, base_name):
        continue

    lines = read_lines(repo_path, filepath)
    scope = find_enclosing_typescript(lines, lineno)
    entry = f"{filepath}:{scope}" if scope else f"{filepath}:{lineno}"
    callers.add(entry)

output_json(
    {"symbol": symbol, "callers": sorted(callers)},
    repo_name, commit,
    note_prefix="Callers by grep with TypeScript scope-walk",
)
