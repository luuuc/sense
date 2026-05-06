#!/usr/bin/env python3
"""Blast-radius GT for javalin: what breaks if Context class signature changes.

Javalin is a Java web framework. Context is the request/response object
passed to handlers. We look for:
1. Methods/classes that import or use io.javalin.http.Context
2. Methods with Context as parameter type
3. Lambda handlers that receive Context

Output: file:ClassName.methodName or file:ClassName
"""

import json
import os
import re
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", ".."))
from gt.common import parse_args, output_json, read_file, is_test_file

repo_path, symbol, lang, commit, repo_name = parse_args()

affected = set()

JAVA_EXTS = (".java", ".kt")

for root, dirs, files in os.walk(repo_path):
    dirs[:] = [d for d in dirs if d not in (".git", "build", "target", ".gradle")]
    for fname in files:
        if not any(fname.endswith(ext) for ext in JAVA_EXTS):
            continue
        filepath = os.path.join(root, fname)
        relpath = os.path.relpath(filepath, repo_path)

        content = read_file(repo_path, relpath)
        if not content:
            continue

        if symbol not in content:
            continue

        # Require actual Context usage: import, parameter type, or type reference
        has_import = bool(re.search(r'import\s+.*\b' + re.escape(symbol) + r'\b', content))
        has_param = bool(re.search(r'\b' + re.escape(symbol) + r'\s+\w+', content))
        has_type_ref = bool(re.search(r'\b' + re.escape(symbol) + r'\b\s*[<.\[]', content))

        if not (has_import or has_param or has_type_ref):
            continue

        lines = content.splitlines()
        current_class = None
        file_symbols = set()

        for line in lines:
            # Track class/interface definitions
            cm = re.match(r'\s*(?:public\s+)?(?:abstract\s+)?(?:class|interface|enum)\s+(\w+)', line)
            if cm:
                current_class = cm.group(1)

            # Find methods that use Context in their signature
            if re.search(r'\b' + re.escape(symbol) + r'\b', line):
                mm = re.match(r'\s*(?:public|private|protected)?\s*(?:static\s+)?(?:\w+\s+)?(\w+)\s*\(', line)
                if mm and current_class:
                    method = mm.group(1)
                    if method not in ("if", "while", "for", "switch", "catch"):
                        file_symbols.add(f"{current_class}.{method}")

        if file_symbols:
            for s in file_symbols:
                affected.add(f"{relpath}:{s}")
        elif current_class:
            affected.add(f"{relpath}:{current_class}")
        else:
            affected.add(relpath)

output_json(
    {"symbol": symbol, "affected": sorted(affected)},
    repo_name, commit,
    note_prefix="Blast-radius by Context import/parameter-type analysis",
)
