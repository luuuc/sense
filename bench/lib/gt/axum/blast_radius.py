#!/usr/bin/env python3
"""Blast-radius GT for axum: what breaks if Handler trait signature changes.

Axum uses Rust traits heavily. Handler is implemented via trait bounds,
not direct calls. We look for:
1. impl Handler blocks
2. where T: Handler bounds
3. Functions that reference Handler in their signature
4. Files with use/import of Handler

Output: file:function_or_impl
"""

import json
import os
import re
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", ".."))
from gt.common import parse_args, output_json, read_file, is_test_file

repo_path, symbol, lang, commit, repo_name = parse_args()

affected = set()

for root, dirs, files in os.walk(repo_path):
    dirs[:] = [d for d in dirs if d not in (".git", "target", "vendor")]
    for fname in files:
        if not fname.endswith(".rs"):
            continue
        filepath = os.path.join(root, fname)
        relpath = os.path.relpath(filepath, repo_path)

        content = read_file(repo_path, relpath)
        if not content:
            continue

        if symbol not in content:
            continue

        # Require actual type-level reference to Handler
        has_impl = bool(re.search(r'\bimpl\b.*\b' + re.escape(symbol) + r'\b', content))
        has_bound = bool(re.search(r':\s*.*\b' + re.escape(symbol) + r'\b', content))
        has_where = bool(re.search(r'\bwhere\b.*\b' + re.escape(symbol) + r'\b', content))
        has_use = bool(re.search(r'\buse\b.*\b' + re.escape(symbol) + r'\b', content))
        has_fn_sig = bool(re.search(r'\bfn\b.*\b' + re.escape(symbol) + r'\b', content))

        if not (has_impl or has_bound or has_where or has_use or has_fn_sig):
            continue

        lines = content.splitlines()
        file_symbols = set()

        for line in lines:
            if re.escape(symbol) and re.search(r'\b' + re.escape(symbol) + r'\b', line):
                # Find the enclosing fn or impl
                fm = re.match(r'\s*(?:pub\s+)?(?:async\s+)?fn\s+(\w+)', line)
                if fm:
                    file_symbols.add(fm.group(1))
                im = re.match(r'\s*impl(?:<[^>]*>)?\s+(\w+)', line)
                if im:
                    file_symbols.add(im.group(1))

        # Also scan for fn definitions that contain Handler in their signature block
        in_fn = None
        for line in lines:
            fm = re.match(r'\s*(?:pub\s+)?(?:async\s+)?fn\s+(\w+)', line)
            if fm:
                in_fn = fm.group(1)
            if in_fn and re.search(r'\b' + re.escape(symbol) + r'\b', line):
                file_symbols.add(in_fn)
            if in_fn and line.strip().startswith('{'):
                in_fn = None

        if file_symbols:
            for s in file_symbols:
                affected.add(f"{relpath}:{s}")
        else:
            # Fallback: file-level entry with primary symbol
            best = None
            for line in lines:
                m = re.match(r'\s*(?:pub\s+)?(?:struct|enum|trait)\s+(\w+)', line)
                if m:
                    best = m.group(1)
                    break
            if not best:
                for line in lines:
                    m = re.match(r'\s*(?:pub\s+)?(?:async\s+)?fn\s+(\w+)', line)
                    if m:
                        best = m.group(1)
                        break
            affected.add(f"{relpath}:{best}" if best else relpath)

output_json(
    {"symbol": symbol, "affected": sorted(affected)},
    repo_name, commit,
    note_prefix="Blast-radius by Handler trait bound/impl analysis",
)
