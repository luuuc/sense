#!/usr/bin/env python3
"""Blast-radius GT for flask: what breaks if Flask class signature changes.

Flask is a Python web framework. We look for:
1. Files that import Flask from the flask package
2. Classes that subclass Flask
3. Functions/methods that instantiate Flask or call Flask methods
4. Internal files that reference the Flask class directly

Output: file:class_or_function
"""

import json
import os
import re
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", ".."))
from gt.common import parse_args, output_json, read_file, read_lines, is_test_file

repo_path, symbol, lang, commit, repo_name = parse_args()

affected = set()

IMPORT_PATTERNS = [
    re.compile(r'from\s+flask\s+import\s+.*\bFlask\b'),
    re.compile(r'import\s+flask'),
    re.compile(r'from\s+\..*\s+import\s+.*\bFlask\b'),
    re.compile(r'from\s+flask\.app\s+import'),
]

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

        # Must have actual Flask reference
        if not re.search(r'\bFlask\b', content):
            continue

        # Verify via import or direct class reference
        has_import = any(p.search(content) for p in IMPORT_PATTERNS)
        has_direct_ref = bool(re.search(r'\bFlask\s*\(', content)) or bool(re.search(r'class\s+\w+\(.*Flask', content))

        # For internal flask source files, check for direct class usage
        is_internal = relpath.startswith("src/flask/")
        if not has_import and not has_direct_ref and not is_internal:
            continue

        if is_internal and not re.search(r'\bFlask\b', re.sub(r'''(["'`]).*?\1''', '', content)):
            continue

        lines = content.splitlines()
        file_symbols = set()

        for line in lines:
            if re.search(r'\bFlask\b', line):
                cm = re.match(r'^class\s+(\w+)', line)
                if cm:
                    file_symbols.add(cm.group(1))
                    continue
                fm = re.match(r'^def\s+(\w+)', line)
                if fm:
                    file_symbols.add(fm.group(1))
                    continue
                vm = re.match(r'^(\w+)\s*=', line)
                if vm:
                    file_symbols.add(vm.group(1))
                    continue

        if not file_symbols:
            for line in lines:
                cm = re.match(r'^class\s+(\w+)', line)
                if cm:
                    file_symbols.add(cm.group(1))
                    break
            if not file_symbols:
                for line in lines:
                    fm = re.match(r'^def\s+(\w+)', line)
                    if fm:
                        file_symbols.add(fm.group(1))
                        break

        for s in file_symbols:
            affected.add(f"{relpath}:{s}")
        if not file_symbols:
            affected.add(relpath)

output_json(
    {"symbol": symbol, "affected": sorted(affected)},
    repo_name, commit,
    note_prefix="Blast-radius by Flask import/reference analysis",
)
