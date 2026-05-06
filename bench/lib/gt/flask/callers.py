#!/usr/bin/env python3
"""Callers GT for flask: who calls Scaffold.route.

Scaffold.route is a compound symbol — we need actual call sites of the
route method on Scaffold instances (or Flask/Blueprint which inherit from
Scaffold). This means:
1. Direct calls: app.route(...), blueprint.route(...)
2. The file must have a Flask/Blueprint/Scaffold import or reference
3. The .route() must be a method call, not a route string or comment

Output: file:function_or_class
"""

import json
import os
import re
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", ".."))
from gt.common import (
    parse_args, output_json, read_file, read_lines,
    is_comment_line, find_enclosing_python,
)

repo_path, symbol, lang, commit, repo_name = parse_args()

parts = re.split(r'[#.]', symbol)
class_name = parts[0]
method_name = parts[-1] if len(parts) > 1 else symbol

callers = set()

# Scaffold.route is inherited by Flask and Blueprint
SCAFFOLD_CLASSES = {"Scaffold", "Flask", "Blueprint"}

for root, dirs, files in os.walk(repo_path):
    dirs[:] = [d for d in dirs if d not in (".git", "node_modules", "vendor", "__pycache__", "site-packages")]
    for fname in files:
        if not fname.endswith(".py"):
            continue
        filepath = os.path.join(root, fname)
        relpath = os.path.relpath(filepath, repo_path)

        content = read_file(repo_path, relpath)
        if not content:
            continue

        # File must reference route as a method call: .route(
        if not re.search(r'\.' + re.escape(method_name) + r'\s*\(', content):
            continue

        # File must have some connection to Scaffold/Flask/Blueprint
        has_scaffold_ref = any(
            re.search(r'\b' + re.escape(cls) + r'\b', content)
            for cls in SCAFFOLD_CLASSES
        )
        has_flask_import = bool(re.search(r'(?:from|import)\s+flask', content))
        is_internal = relpath.startswith("src/flask/")

        if not (has_scaffold_ref or has_flask_import or is_internal):
            continue

        lines = read_lines(repo_path, relpath)

        for lineno_0, line in enumerate(lines):
            lineno = lineno_0 + 1
            stripped = line.strip()

            if is_comment_line(stripped):
                continue

            # Must be a .route( call
            if not re.search(r'\.' + re.escape(method_name) + r'\s*\(', line):
                continue

            # Skip definitions
            if re.search(r'\bdef\s+' + re.escape(method_name) + r'\b', stripped):
                continue

            # Skip string-only mentions
            no_strings = re.sub(r'''(["'`]).*?\1''', '', line)
            if not re.search(r'\.' + re.escape(method_name) + r'\s*\(', no_strings):
                continue

            scope = find_enclosing_python(lines, lineno)
            if scope:
                callers.add(f"{relpath}:{scope}")
            else:
                callers.add(f"{relpath}:{lineno}")

output_json(
    {"symbol": symbol, "callers": sorted(callers)},
    repo_name, commit,
    note_prefix="Callers by .route() method call analysis on Scaffold/Flask/Blueprint instances",
)
