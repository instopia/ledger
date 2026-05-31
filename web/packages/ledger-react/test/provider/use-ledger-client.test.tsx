import { renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, test } from "vitest";
import {
  LedgerClientContext,
  useLedgerClient,
} from "../../src/provider/context";
import type { LedgerClient } from "../../src/client/client";

// A minimal fake satisfying the LedgerClient shape for identity assertions.
const fakeClient = { getHealth: () => Promise.resolve(null) } as unknown as
  LedgerClient;

describe("useLedgerClient", () => {
  test("throws when used outside a provider", () => {
    expect(() => renderHook(() => useLedgerClient())).toThrow(
      "useLedgerClient must be used within <LedgerProvider>",
    );
  });

  test("returns the client when used inside the context", () => {
    const wrapper = ({ children }: { children: ReactNode }) => (
      <LedgerClientContext.Provider value={fakeClient}>
        {children}
      </LedgerClientContext.Provider>
    );
    const { result } = renderHook(() => useLedgerClient(), { wrapper });
    expect(result.current).toBe(fakeClient);
  });
});
