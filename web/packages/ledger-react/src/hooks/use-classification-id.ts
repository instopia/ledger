import { useQuery } from "@tanstack/react-query";
import { useLedgerClient } from "../provider/context";

/**
 * Resolve the classification ID for a given code (e.g. "deposit", "withdraw").
 *
 * The classification list is small and stable, so it's cached for a long time.
 * Returns 0 (falsy) until classifications have loaded. Internal helper — shared
 * by the deposit/withdrawal hooks, not part of the public package surface.
 */
export function useClassificationIdByCode(code: string): number {
  const client = useLedgerClient();
  const { data } = useQuery({
    queryKey: ["ledger", "classifications", true],
    queryFn: () => client.listClassifications(true),
    staleTime: 5 * 60_000,
  });
  return data?.find((c) => c.code === code)?.id ?? 0;
}
