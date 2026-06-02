// Page is the default export of a Next.js `app/` route file. File-system routing
// renders it with no import edge anywhere, so the TS voice keeps it
// possibly_dead (ts_framework_route) — recognized by the app/ + page convention,
// not by a caller.
export default function Page(): null {
  return null;
}
