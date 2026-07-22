# go scenarios: one disclosure about `scenario_version`

Each `run_meta.json` records a `scenario_version`, which the runner computes as the
sha256 of the scenario file (plus its `.rubric.yaml` sibling, when one existed at run
time). It hashes the WHOLE file, comments included.

Before these scenarios were committed, their comment lines were stripped of internal
campaign history (maintainer names, internal codenames, private doc paths, dated diary
notes). That edit moves the hash. So for consul, dolt, nomad, pebble and teleport, the
`scenario_version` in `run_meta.json` hashes the pre-strip file and will NOT recompute
from the file in this repo.

What did NOT change: every line that is scored or shown. The `description`, each
`steps[].prompt`, and every `check`, `match` and gold row are byte-identical to the
file each run was produced from. Only comment lines differ, and comments are never
rendered to the agent, never rendered to the judge, and never read by the scorer.

Nothing in the toolchain compares `scenario_version` to a file: `select_final.py`
compares run metadata to other run metadata, to catch runs of one repo that span two
different scenario shapes. That check is unaffected.
