import {
  createLedgerClient,
  type LedgerClient,
  type LedgerClientConfig,
} from "../client/client";

/**
 * Server-only ledger client for RSC / Route Handler prefetching.
 *
 * It delegates to the same framework-agnostic `createLedgerClient` — the
 * distinct name + the `./server` subpath entry exist to (a) signal server-only
 * usage and (b) keep the prefetch helpers off the client barrel (`src/index.ts`).
 *
 * Pass SERVER config here: the internal/private `baseUrl` and the server API
 * key. NEVER import this from a Client Component and NEVER re-export it from
 * `src/index.ts` — doing either would pull the server API key into the client
 * bundle.
 */
export function createServerLedgerClient(
  config: LedgerClientConfig,
): LedgerClient {
  return createLedgerClient(config);
}
