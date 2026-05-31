// Server-only entry (`@azex/ledger-react/server`). RSC / Route Handler hosts
// import from here to prefetch ledger data on the server and hydrate the
// client hooks with no client-side waterfall.
//
// This module must NOT be re-exported from `src/index.ts` (the client barrel):
// `createServerLedgerClient` takes the server API key, which must never enter
// the client bundle. No `"use client"` here — this is server code.

export { createServerLedgerClient } from "./server/client";

export {
  prefetchJournals,
  prefetchEntries,
  prefetchBalances,
  prefetchSystemHealth,
  prefetchSystemBalances,
  prefetchReservations,
  prefetchClassifications,
  prefetchCurrencies,
  prefetchJournalTypes,
  prefetchTemplates,
  prefetchSnapshots,
} from "./server/prefetch";

// Re-exported for advanced hosts that want to read/seed the cache directly.
// (Also importable internally from `./hooks/keys`.)
export { ledgerKeys } from "./hooks/keys";
