// Dynamic imports and re-exports in plain JavaScript: the same edge
// shapes the TypeScript fixtures cover, but through the JS grammar (no
// type annotations) to guard the JS-only export paths.
export { Button } from "./button";
export * from "./widgets";

export async function load() {
  const mod = await import("./heavy");
  return mod.default;
}

export const lazyPanel = () => import("./panel");
