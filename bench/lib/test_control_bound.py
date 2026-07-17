#!/usr/bin/env python3
"""Behaviour pins for the arithmetic bound gate.

The load-bearing pin is `test_saleor_shaped_cell_survives`: a control at exactly the bound
MUST pass. saleor (control 0.50, sense 1.00, delta exactly +0.500) is a banked WIN, and every
threshold tighter than 0.50 throws it away. If someone "improves" this gate by tightening the
number, that test goes red and tells them what it costs.
"""

import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import control_bound
from control_bound import BAR, BOUND, evaluate, main


def _scenario(tmp, rows):
    p = os.path.join(tmp, "s.yaml")
    with open(p, "w") as fh:
        fh.write("name: t\nrepo: t\ngold:\n")
        for r in rows:
            fh.write(f"  - {{id: {r['id']}, group: {r['group']}, match: [{r['match']}]}}\n")
    return p


def _probe(tmp, name, text):
    p = os.path.join(tmp, name)
    with open(p, "w") as fh:
        fh.write(text)
    return p


GOLD4 = [{"id": f"dep:d{i}", "group": "dependents", "match": f"pkg/d{i}.go"} for i in range(4)]


class ArithmeticBoundTest(unittest.TestCase):
    def test_the_bound_is_the_bar_complement(self):
        # the whole gate in one line: sense <= 1.0, so delta >= BAR needs control <= 1 - BAR
        self.assertEqual(BOUND, 1.0 - BAR)
        self.assertEqual(BOUND, 0.50)

    def test_control_above_the_bound_kills(self):
        with tempfile.TemporaryDirectory() as t:
            s = _scenario(t, GOLD4)
            # cites 3 of 4 -> control 0.75 -> ceiling +0.25 -> dead
            a = _probe(t, "p1.md", "pkg/d0.go:1 pkg/d1.go:2 pkg/d2.go:3")
            b = _probe(t, "p2.md", "pkg/d0.go:1 pkg/d1.go:2 pkg/d2.go:3")
            _, means = evaluate(s, [a, b])
            self.assertAlmostEqual(means["dependents"], 0.75)
            self.assertEqual(main(["x", s, a, b]), 1)

    def test_saleor_shaped_cell_survives(self):
        """A control at EXACTLY the bound must PASS. saleor is a banked win at this point.

        control 0.50 + sense 1.00 = delta +0.500 = the bar. Tightening this gate below 0.50
        throws saleor away: measured, control > 0.25 kills 14 cells INCLUDING that win.
        """
        with tempfile.TemporaryDirectory() as t:
            s = _scenario(t, GOLD4)
            a = _probe(t, "p1.md", "pkg/d0.go:1 pkg/d1.go:2")   # 2/4 = 0.50, exactly the bound
            b = _probe(t, "p2.md", "pkg/d0.go:1 pkg/d1.go:2")
            _, means = evaluate(s, [a, b])
            self.assertAlmostEqual(means["dependents"], 0.50)
            self.assertEqual(main(["x", s, a, b]), 0, "a control at the bound is WINNABLE")

    def test_a_floored_control_passes(self):
        with tempfile.TemporaryDirectory() as t:
            s = _scenario(t, GOLD4)
            a = _probe(t, "p1.md", "nothing relevant here")
            self.assertEqual(main(["x", s, a]), 0)

    def test_kill_needs_EVERY_group_dead(self):
        """pergroup flags a win on ANY group, so one live group keeps the cell alive.

        saleor's banked win is on its `context` group, not `dependents`. A gate that judged
        one group would re-create exactly the false negative BAR=0.50 exists to avoid.
        """
        gold = GOLD4 + [{"id": f"ctx:c{i}", "group": "context", "match": f"pkg/c{i}.go"}
                        for i in range(4)]
        with tempfile.TemporaryDirectory() as t:
            s = _scenario(t, gold)
            # dependents aced (1.00, dead) but context floored (0.00, alive) -> PASS
            txt = "pkg/d0.go:1 pkg/d1.go:2 pkg/d2.go:3 pkg/d3.go:4"
            a, b = _probe(t, "p1.md", txt), _probe(t, "p2.md", txt)
            _, means = evaluate(s, [a, b])
            self.assertAlmostEqual(means["dependents"], 1.0)
            self.assertAlmostEqual(means["context"], 0.0)
            self.assertEqual(main(["x", s, a, b]), 0, "one live group keeps the cell alive")

    def test_all_groups_dead_kills(self):
        gold = GOLD4 + [{"id": f"ctx:c{i}", "group": "context", "match": f"pkg/c{i}.go"}
                        for i in range(4)]
        with tempfile.TemporaryDirectory() as t:
            s = _scenario(t, gold)
            txt = ("pkg/d0.go:1 pkg/d1.go:2 pkg/d2.go:3 pkg/d3.go:4 "
                   "pkg/c0.go:1 pkg/c1.go:2 pkg/c2.go:3")
            a, b = _probe(t, "p1.md", txt), _probe(t, "p2.md", txt)
            self.assertEqual(main(["x", s, a, b]), 1)

    def test_mean_not_min_decides_because_the_verdict_uses_the_mean(self):
        """dolt's real shape: 0.444 and 0.889 -> mean 0.667 -> DEAD (ceiling +0.333).

        min() would read 0.444 and pass a cell whose delta ceiling is +0.333, because
        pergroup's delta is computed on MEANS. This pins the aggregation.
        """
        gold = [{"id": f"dep:d{i}", "group": "dependents", "match": f"pkg/d{i}.go"}
                for i in range(9)]
        with tempfile.TemporaryDirectory() as t:
            s = _scenario(t, gold)
            a = _probe(t, "p1.md", " ".join(f"pkg/d{i}.go:1" for i in range(4)))   # 4/9=.444
            b = _probe(t, "p2.md", " ".join(f"pkg/d{i}.go:1" for i in range(8)))   # 8/9=.889
            _, means = evaluate(s, [a, b])
            self.assertAlmostEqual(means["dependents"], (4 / 9 + 8 / 9) / 2, places=3)
            self.assertEqual(main(["x", s, a, b]), 1, "dolt-shaped control is DEAD on the mean")


if __name__ == "__main__":
    unittest.main()
