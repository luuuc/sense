// orphanedTs is module-private (not exported), has no caller, and its name is
// mentioned nowhere else — the genuinely-dead shape the TS voice earns `dead`
// for. A .ts module proves the closed-world bet: a non-exported symbol is
// reachable only within this file.
function orphanedTs(): number {
  return 42;
}

// exportedHelper is exported, so a barrel re-export / dynamic import / external
// consumer may reach it. The TS voice never earns `dead` for it; in this
// library-shaped fixture the core voice labels it core_exported_api.
export function exportedHelper(): string {
  return "help";
}
