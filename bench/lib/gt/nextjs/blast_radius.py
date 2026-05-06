#!/usr/bin/env python3
"""Blast-radius GT for nextjs: what breaks if NextConfig's signature changes.

Next.js is a TypeScript monorepo. NextConfig is a type/interface. We look for:
1. Files that import NextConfig
2. Files that use NextConfig as a type annotation
3. Files that reference NextConfig in type assertions or generics

Output: file:function_or_class
"""

import json
import os
import re
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", ".."))
from gt.common import parse_args, output_json, read_file, is_test_file

repo_path, symbol, lang, commit, repo_name = parse_args()

affected = set()

TS_EXTS = (".ts", ".tsx", ".js", ".jsx")

for root, dirs, files in os.walk(repo_path):
    dirs[:] = [d for d in dirs if d not in (".git", "node_modules", "vendor", "tmp", ".next")]
    for fname in files:
        if not any(fname.endswith(ext) for ext in TS_EXTS):
            continue
        filepath = os.path.join(root, fname)
        relpath = os.path.relpath(filepath, repo_path)

        content = read_file(repo_path, relpath)
        if not content:
            continue

        if symbol not in content:
            continue

        # Require actual import or type-level reference, not just string mention
        has_import = bool(re.search(r'import\s+.*\b' + re.escape(symbol) + r'\b', content))
        has_type_ref = bool(re.search(r':\s*\w*' + re.escape(symbol) + r'\b', content))
        has_generic = bool(re.search(r'<\s*\w*' + re.escape(symbol) + r'\b', content))
        has_typeof = bool(re.search(r'typeof\s+' + re.escape(symbol) + r'\b', content))
        has_cast = bool(re.search(r'\bas\s+' + re.escape(symbol) + r'\b', content))

        if not (has_import or has_type_ref or has_generic or has_typeof or has_cast):
            continue

        # Skip test files from blast-radius
        if is_test_file(relpath):
            continue

        lines = content.splitlines()
        file_symbols = set()

        for line in lines:
            if re.search(r'\b' + re.escape(symbol) + r'\b', line):
                m = re.match(r'\s*(?:export\s+)?(?:class|interface|type)\s+(\w+)', line)
                if m:
                    file_symbols.add(m.group(1))
                    continue
                m = re.match(r'\s*(?:export\s+)?(?:async\s+)?function\s+(\w+)', line)
                if m:
                    file_symbols.add(m.group(1))
                    continue
                m = re.match(r'\s*(?:export\s+)?(?:const|let|var)\s+(\w+)', line)
                if m:
                    file_symbols.add(m.group(1))
                    continue

        if not file_symbols:
            for line in lines:
                m = re.match(r'\s*(?:export\s+)?(?:class|interface|type)\s+(\w+)', line)
                if m:
                    file_symbols.add(m.group(1))
                    break
            if not file_symbols:
                for line in lines:
                    m = re.match(r'\s*(?:export\s+)?(?:async\s+)?function\s+(\w+)', line)
                    if m:
                        file_symbols.add(m.group(1))
                        break

        for s in file_symbols:
            affected.add(f"{relpath}:{s}")
        if not file_symbols:
            affected.add(relpath)

output_json(
    {"symbol": symbol, "affected": sorted(affected)},
    repo_name, commit,
    note_prefix="Blast-radius by NextConfig import/type-reference analysis",
)
