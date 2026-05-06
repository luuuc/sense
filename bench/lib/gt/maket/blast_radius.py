#!/usr/bin/env python3
"""Blast-radius GT for maket: what breaks if Listing::Item changes.

Maket is a Rails app. Listing::Item is a model. We look for:
1. Files that reference the Listing::Item constant
2. Files with associations to listing_items
3. Files that inherit from or compose Listing::Item

Output: file:Class or file:Class#method
"""

import json
import os
import re
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", ".."))
from gt.common import parse_args, output_json, read_file, is_test_file

repo_path, symbol, lang, commit, repo_name = parse_args()

affected = set()

# Listing::Item can appear as:
# - Listing::Item (constant)
# - listing_item / listing_items (association/table name)
PATTERNS = [
    re.compile(r'\bListing::Item\b'),
    re.compile(r'\blisting_item\b'),
    re.compile(r'\blisting_items\b'),
]

for root, dirs, files in os.walk(repo_path):
    dirs[:] = [d for d in dirs if d not in (
        ".git", "node_modules", "vendor", "tmp", "log",
        "spec", "test",
    )]
    for fname in files:
        if not fname.endswith(".rb"):
            continue
        filepath = os.path.join(root, fname)
        relpath = os.path.relpath(filepath, repo_path)

        content = read_file(repo_path, relpath)
        if not content:
            continue

        if not any(p.search(content) for p in PATTERNS):
            continue

        # Verify real reference (not just comments/strings)
        real_ref = False
        for line in content.splitlines():
            stripped = line.strip()
            if stripped.startswith("#"):
                continue
            no_strings = re.sub(r'''(["'`]).*?\1''', '', line)
            if any(p.search(no_strings) for p in PATTERNS):
                real_ref = True
                break

        if not real_ref:
            continue

        # Skip the definition file itself
        if "listing/item" in relpath.replace("\\", "/").lower():
            continue

        lines = content.splitlines()
        cls_stack = []
        for line in lines:
            cm = re.match(r'\s*(class|module)\s+(\S+)', line)
            if cm:
                cls_stack.append(cm.group(2).split('<')[0].strip().split('::')[-1])

        if cls_stack:
            affected.add(f"{relpath}:{cls_stack[-1]}")
        else:
            affected.add(relpath)

output_json(
    {"symbol": symbol, "affected": sorted(affected)},
    repo_name, commit,
    note_prefix="Blast-radius by Listing::Item constant/association reference analysis",
)
