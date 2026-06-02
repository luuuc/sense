// legacy.js is an ES module (it exports), so orphanedJs is genuinely
// module-private. orphanedJs is the .js counterpart of orphanedTs: no caller,
// name unmentioned. In TypeScript this shape earns `dead`, but plain JavaScript
// is looser (CommonJS interop, no types), so the TS voice holds it open-world
// (js_dynamic) — the honest TS/JS precision split.
export function legacyEntry() {
  return "entry";
}

function orphanedJs() {
  return 42;
}
