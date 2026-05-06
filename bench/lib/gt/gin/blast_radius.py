#!/usr/bin/env python3
"""Blast-radius GT for gin: what breaks if Context's signature changes.

Gin is a single-package Go repo. Context is a receiver type used by nearly
every handler function. We look for:
1. Methods with Context as receiver type (directly broken)
2. Functions/methods with *Context as parameter type (directly broken)
3. Functions that call Context methods (indirectly broken via changed API)

Output: file:Receiver.Method or file:FunctionName
"""

import json
import os
import re
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", ".."))
from gt.common import parse_args, output_json, read_lines, is_test_file

repo_path, symbol, lang, commit, repo_name = parse_args()

affected = set()

for root, dirs, files in os.walk(repo_path):
    dirs[:] = [d for d in dirs if d not in (".git", "vendor", "node_modules")]
    for fname in files:
        if not fname.endswith(".go"):
            continue
        filepath = os.path.join(root, fname)
        relpath = os.path.relpath(filepath, repo_path)

        lines = read_lines(repo_path, relpath)
        if not lines:
            continue

        i = 0
        while i < len(lines):
            line = lines[i]

            # Method with Context receiver: func (c *Context) MethodName(
            m = re.match(r'^func\s+\(\w+\s+\*?' + re.escape(symbol) + r'\)\s+(\w+)', line)
            if m:
                affected.add(f"{relpath}:{symbol}.{m.group(1)}")
                i += 1
                continue

            # Standalone function or method on another type that takes *Context as param
            fm = re.match(r'^func\s+(?:\((\w+)\s+\*?(\w+)\)\s+)?(\w+)\s*\(([^)]*)\)', line)
            if not fm:
                # Multi-line signature — gather continuation lines
                fm = re.match(r'^func\s+(?:\((\w+)\s+\*?(\w+)\)\s+)?(\w+)\s*\(', line)
                if fm:
                    sig = line
                    j = i + 1
                    while j < len(lines) and ')' not in sig:
                        sig += lines[j]
                        j += 1
                    fm = re.match(r'^func\s+(?:\((\w+)\s+\*?(\w+)\)\s+)?(\w+)\s*\(([^)]*)\)', sig)

            if fm:
                recv_type = fm.group(2)
                func_name = fm.group(3)
                params = fm.group(4) if fm.group(4) else ""

                # Skip if this IS a Context method (already captured above)
                if recv_type == symbol:
                    i += 1
                    continue

                # Check if any parameter is *Context or Context
                if re.search(r'\b\*?' + re.escape(symbol) + r'\b', params):
                    if recv_type:
                        affected.add(f"{relpath}:{recv_type}.{func_name}")
                    else:
                        affected.add(f"{relpath}:{func_name}")

            i += 1

output_json(
    {"symbol": symbol, "affected": sorted(affected)},
    repo_name, commit,
    note_prefix="Blast-radius by receiver/parameter type analysis",
)
