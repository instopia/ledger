// Single source of truth for React Query keys.
//
// Both the hooks (`src/hooks/*`) and the server prefetch helpers
// (`src/server/prefetch.ts`) build their query keys here. If a prefetch key
// drifts from the hook key, hydration silently misses (cache key mismatch →
// the client refetches anyway), so the keys MUST be defined exactly once.
//
// Every entry returns the same array shape the corresponding hook used inline
// before this refactor — the VALUES are unchanged.

export const ledgerKeys = {
  // System
  health: () => ["ledger", "health"] as const,
  systemBalances: () => ["ledger", "system-balances"] as const,

  // Journals + entries
  journals: (limit: number) => ["ledger", "journals", limit] as const,
  journal: (id: number) => ["ledger", "journal", id] as const,
  entries: (params: { holder?: number; currency_id?: number }) =>
    ["ledger", "entries", params] as const,

  // Balances
  balances: (holder: number) => ["ledger", "balances", holder] as const,
  balancesByCurrency: (holder: number, currency: number) =>
    ["ledger", "balances", holder, currency] as const,

  // Reservations
  reservations: (params: { holder?: number; status?: string }) =>
    ["ledger", "reservations", params] as const,

  // Snapshots
  snapshots: (params: {
    holder?: number;
    currency_id?: number;
    start?: string;
    end?: string;
  }) => ["ledger", "snapshots", params] as const,

  // Metadata
  classifications: (activeOnly?: boolean) =>
    ["ledger", "classifications", activeOnly] as const,
  journalTypes: (activeOnly?: boolean) =>
    ["ledger", "journal-types", activeOnly] as const,
  templates: (activeOnly?: boolean) =>
    ["ledger", "templates", activeOnly] as const,
  currencies: (activeOnly?: boolean) =>
    ["ledger", "currencies", activeOnly] as const,

  // Bookings (deposit / withdraw views). The third segment is the
  // classification CODE; the params object carries the resolved numeric
  // `classificationId` the hook computes at runtime.
  bookings: (
    code: string,
    params: { holder?: number; status?: string; classificationId: number },
  ) => ["ledger", "bookings", code, params] as const,
} as const;
