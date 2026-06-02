// Badge is an exported PascalCase component in a .tsx file — a JSX component.
// It may be rendered as <Badge/> in a file the resolver could not bind here, so
// the TS voice keeps it possibly_dead (ts_jsx) rather than letting the absent
// edge earn `dead`.
export function Badge(): null {
  return null;
}
