import { useQuery, useMutation } from "@tanstack/react-query";
import { useLedgerClient } from "../provider/context";
import { ledgerKeys } from "./keys";

export function useHealth() {
  const client = useLedgerClient();
  return useQuery({
    queryKey: ledgerKeys.health(),
    queryFn: () => client.getHealth(),
    refetchInterval: 10_000,
  });
}

export function useSystemBalances() {
  const client = useLedgerClient();
  return useQuery({
    queryKey: ledgerKeys.systemBalances(),
    queryFn: () => client.getSystemBalances(),
  });
}

export function useReconcileGlobal() {
  const client = useLedgerClient();
  return useMutation({
    mutationFn: () => client.reconcileGlobal(),
  });
}

export function useReconcileAccount() {
  const client = useLedgerClient();
  return useMutation({
    mutationFn: ({ holder, currencyId }: { holder: number; currencyId: number }) =>
      client.reconcileAccount(holder, currencyId),
  });
}

export function useSnapshots(params: {
  holder?: number;
  currency_id?: number;
  start?: string;
  end?: string;
}) {
  const client = useLedgerClient();
  return useQuery({
    queryKey: ledgerKeys.snapshots(params),
    queryFn: () => client.listSnapshots(params),
    // Negative holders (system accounts) are valid; only 0/undefined disables.
    enabled: params.holder !== undefined && params.holder !== 0,
  });
}
