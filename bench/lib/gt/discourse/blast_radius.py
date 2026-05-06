#!/usr/bin/env python3
"""Blast-radius GT for discourse: what breaks if Topic's signature changes.

Discourse is a Rails app. Topic is a core ActiveRecord model. We look for:
1. Classes that inherit from or reference Topic (has_many :topics, belongs_to :topic)
2. Methods that call Topic class methods or instance methods
3. Files with actual Topic constant references (not just the word "topic")

Output: file:Class#method or file:Class
"""

import json
import os
import re
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", ".."))
from gt.common import parse_args, output_json, read_file, read_lines, is_test_file

repo_path, symbol, lang, commit, repo_name = parse_args()

affected = set()

TOPIC_PATTERNS = [
    re.compile(r'\bTopic\b'),                          # Direct constant reference
    re.compile(r'\b:topic\b'),                          # Symbol reference
    re.compile(r'\btopic_id\b'),                        # Foreign key
    re.compile(r'\bbelongs_to\s+:topic\b'),             # Association
    re.compile(r'\bhas_(?:many|one)\s+:topics?\b'),     # Association
]

FALSE_POSITIVE_PATTERNS = [
    re.compile(r'^\s*#'),                               # Comment
    re.compile(r'^\s*//'),                               # Comment
    re.compile(r'topic_count'),                          # Counter field name only
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

        # Must have actual Topic constant reference (uppercase T), not just "topic" as string
        if not re.search(r'\bTopic\b', content):
            continue

        # Verify it's a real reference, not just inside strings/comments
        real_ref = False
        for line in content.splitlines():
            stripped = line.strip()
            if any(fp.search(stripped) for fp in FALSE_POSITIVE_PATTERNS):
                continue
            if re.search(r'\bTopic\b', line):
                # Exclude lines where Topic only appears in a string literal
                no_strings = re.sub(r'''(["'`]).*?\1''', '', line)
                if re.search(r'\bTopic\b', no_strings):
                    real_ref = True
                    break

        if not real_ref:
            continue

        lines = content.splitlines()
        cls_stack = []
        current_method = None

        for line in lines:
            cm = re.match(r'\s*(class|module)\s+(\S+)', line)
            if cm:
                cls_stack.append(cm.group(2).split('<')[0].strip().split('::')[-1])
            mm = re.match(r'\s*def\s+(\w+[?!]?)', line)
            if mm:
                current_method = mm.group(1)

        if cls_stack:
            cls_name = cls_stack[-1]
            # Avoid adding Topic model's own definition file
            if relpath.endswith("app/models/topic.rb"):
                continue
            affected.add(f"{relpath}:{cls_name}")
        else:
            affected.add(relpath)

output_json(
    {"symbol": symbol, "affected": sorted(affected)},
    repo_name, commit,
    note_prefix="Blast-radius by Topic constant reference analysis",
)
