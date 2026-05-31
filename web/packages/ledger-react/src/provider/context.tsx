import { createContext, useContext } from "react";
import type { LedgerClient } from "../client/client";

// Context holding the configured LedgerClient. Null when no provider is
// mounted — useLedgerClient() turns that into a loud error rather than
// silently handing back null.
export const LedgerClientContext = createContext<LedgerClient | null>(null);

export function useLedgerClient(): LedgerClient {
  const client = useContext(LedgerClientContext);
  if (client === null) {
    throw new Error("useLedgerClient must be used within <LedgerProvider>");
  }
  return client;
}
