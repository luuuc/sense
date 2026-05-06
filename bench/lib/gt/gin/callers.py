#!/usr/bin/env python3
"""Callers GT for gin: who calls Engine.ServeHTTP.

Go-specific scope walk for a receiver method. Grep for ServeHTTP,
exclude the definition, find enclosing func.
"""

import json
import os
import re
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", ".."))
from gt.common import (
    parse_args, output_json, grep_lines, read_lines,
    is_comment_line, is_definition_line, symbol_in_non_string_context,
    find_enclosing_go,
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

    # Go receiver method definition
    if re.search(r'\bfunc\s*\([^)]*\)\s+' + re.escape(base_name) + r'\b', content):
        continue

    if class_name:
        if filepath not in file_cache:
            file_cache[filepath] = ''.join(read_lines(repo_path, filepath))
        fc = file_cache[filepath]
        if lang == "go":
            # Exclude calls on non-Engine types (http.FileServer)
            if re.search(r'(?:FileServer|fileServer)', content):
                continue
            # Go uses type inference (r := New()), so the type name rarely appears.
            # Check for type name, standalone constructors, or gin import.
            has_type = bool(re.search(r'\b' + re.escape(class_name) + r'\b', fc))
            has_constructor = bool(re.search(r'(?:^|[^.\w])(?:New|Default)\s*\(', fc, re.MULTILINE))
            has_gin_import = bool(re.search(r'"github\.com/gin-gonic/gin"', fc))
            if not has_type and not has_constructor and not has_gin_import:
                continue
        else:
            if not re.search(r'\b' + re.escape(class_name) + r'\b', fc):
                continue

    lines = read_lines(repo_path, filepath)
    scope = find_enclosing_go(lines, lineno)
    entry = f"{filepath}:{scope}" if scope else f"{filepath}:{lineno}"
    callers.add(entry)

output_json(
    {"symbol": symbol, "callers": sorted(callers)},
    repo_name, commit,
    note_prefix="Callers by grep with Go scope-walk",
)
