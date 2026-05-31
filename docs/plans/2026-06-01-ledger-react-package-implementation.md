# `@azex/ledger-react` Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Extract the standalone `web/` admin into a single importable React/Next.js package (`@azex/ledger-react`) that other apps install, theme via CSS variables, and mount with host-owned routing — with `web/` re-wired to consume it as a dogfood app.

**Architecture:** npm-workspace monorepo inside the `ledger` repo. The package holds a config-injected client (`createLedgerClient`), React Query hooks reading the client from context, the admin UI components/pages, a `<LedgerProvider>`, precompiled CSS scoped under `.ledger-root`, and a server entry (`./server`) for RSC prefetch. `web/` becomes a thin consumer.

**Tech Stack:** TypeScript, React 19, Next.js 16, @tanstack/react-query v5, Tailwind v4, shadcn (base-nova), tsup (build), MSW (tests), Vitest + React Testing Library.

**Design source:** `docs/plans/2026-05-31-ledger-react-package-design.md`

**Conventions for every task:** TDD (failing test → minimal impl → green → commit). Exact paths. DRY. YAGNI. Commit after each green step. All work on branch `feat/ledger-react-package`.

---

## Phase 0 — Workspace & package skeleton

### Task 0.1: Convert `web/` to an npm workspace root

**Files:**
- Modify: `web/package.json` (add `"workspaces": ["packages/*"]`)
- Create: `web/packages/ledger-react/package.json`

**Step 1:** Add to `web/package.json`: `"workspaces": ["packages/*"]`.

**Step 2:** Create `web/packages/ledger-react/package.json`:
```json
{
  "name": "@azex/ledger-react",
  "version": "0.0.0",
  "type": "module",
  "sideEffects": ["*.css"],
  "exports": {
    ".": { "types": "./dist/index.d.ts", "default": "./dist/index.js" },
    "./server": { "types": "./dist/server.d.ts", "default": "./dist/server.js" },
    "./styles.css": "./dist/styles.css"
  },
  "files": ["dist"],
  "scripts": { "build": "tsup", "test": "vitest run", "typecheck": "tsc --noEmit" },
  "peerDependencies": {
    "react": "^19", "react-dom": "^19", "@tanstack/react-query": "^5"
  },
  "devDependencies": {
    "tsup": "^8", "vitest": "^2", "msw": "^2",
    "@testing-library/react": "^16", "@testing-library/jest-dom": "^6",
    "jsdom": "^25", "typescript": "^5"
  },
  "publishConfig": { "registry": "https://npm.pkg.github.com" }
}
```

**Step 3:** Run `cd web && npm install`. Expected: workspace links, no errors.

**Step 4:** Commit: `chore(web): add npm workspace + ledger-react package skeleton`.

### Task 0.2: tsup build config preserving `"use client"`

**Files:**
- Create: `web/packages/ledger-react/tsup.config.ts`
- Create: `web/packages/ledger-react/src/index.ts` (temporary `export const VERSION = "0.0.0"`)
- Create: `web/packages/ledger-react/src/server.ts` (temporary `export const SERVER = true`)
- Create: `web/packages/ledger-react/tsconfig.json`

**Step 1:** `tsup.config.ts` with `entry: ["src/index.ts", "src/server.ts"]`, `format: ["esm"]`, `dts: true`, `external: ["react","react-dom","@tanstack/react-query"]`, and the **banner/esbuild option to preserve `"use client"`** (use `esbuildOptions` to keep directive, or `tsup`'s `banner`/`outExtension`; verify directive survives — this is the known RSC pitfall).

**Step 2:** Run `npm run -w @azex/ledger-react build`. Expected: `dist/index.js`, `dist/server.js`, `.d.ts` emitted.

**Step 3:** Add a build assertion test `test/build.test.ts` that reads `dist/index.js` after build (or assert via a smoke import). Minimal for now.

**Step 4:** Commit: `chore(ledger-react): tsup build with use-client preservation`.

### Task 0.3: Vitest + MSW harness

**Files:**
- Create: `web/packages/ledger-react/vitest.config.ts` (jsdom env, setup file)
- Create: `web/packages/ledger-react/test/setup.ts` (MSW server start/stop, `@testing-library/jest-dom`)
- Create: `web/packages/ledger-react/test/msw-handlers.ts` (shared handlers)

**Step 1–4:** Stand up MSW `setupServer`, wire `beforeAll/afterEach/afterAll`. Add one trivial passing test to prove the harness runs. Commit: `test(ledger-react): vitest + MSW harness`.

---

## Phase 1 — Config-injected client (`createLedgerClient`)

The current `web/src/lib/api.ts` is a module singleton reading `process.env` at load. Refactor into a factory. **Also sync types to the backend** (currency now has `is_active`, `listCurrencies(activeOnly)`, and `deactivateCurrency` exist after commit `8a25473`; the client is stale).

### Task 1.1: `LedgerClientConfig` + `createLedgerClient` skeleton + `request`

**Files:**
- Create: `web/packages/ledger-react/src/client/types.ts` (move all `interface` types from `api.ts`; **add** `is_active` to `Currency`)
- Create: `web/packages/ledger-react/src/client/client.ts`
- Test: `web/packages/ledger-react/test/client/request.test.ts`

**Step 1 (failing test):** With MSW serving `/api/v1/system/health`, assert `createLedgerClient({baseUrl}).getHealth()` returns the unwrapped `data`, and that a non-2xx envelope throws `ApiRequestError` with `code`.

**Step 2:** Run → FAIL (createLedgerClient undefined).

**Step 3 (impl):** Port `request`/`qs`/`Envelope`/`ApiRequestError` into a closure over `config: {baseUrl, apiKey?, fetch?}`. No `process.env` anywhere. Mutating methods attach `Authorization: Bearer ${config.apiKey}` only when set. `createLedgerClient` returns an object literal of all methods.

**Step 4:** Run → PASS.

**Step 5:** Commit: `feat(ledger-react): config-injected createLedgerClient + request core`.

### Task 1.2: Port all endpoint methods onto the client

**Pattern (apply once per endpoint group; DRY — do not duplicate test scaffolding):** for each group below, add the method to `createLedgerClient`'s returned object and one MSW-backed test asserting URL + verb + payload shaping.

Groups (from current `api.ts`): system, journals (+entries), balances, reservations, bookings, events, classifications, journal-types, templates, **currencies (sync: `listCurrencies(activeOnly?)`, add `deactivateCurrency(id)`)**, reconciliation, snapshots.

**Per group:** test → impl → green → commit `feat(ledger-react): client <group> methods`.

---

## Phase 2 — Provider, context, theming wrapper

### Task 2.1: client context + `useLedgerClient`

**Files:**
- Create: `web/packages/ledger-react/src/provider/context.tsx`
- Test: `test/provider/use-ledger-client.test.tsx`

**Step 1 (failing test):** `useLedgerClient()` rendered outside provider throws `"useLedgerClient must be used within <LedgerProvider>"`; inside, returns the injected client.

**Steps 2–5:** Implement `LedgerClientContext` + `useLedgerClient` (throw on null — fail loud). Commit.

### Task 2.2: `<LedgerProvider>` (config → client + QueryClient + `.ledger-root` + theme)

**Files:**
- Create: `web/packages/ledger-react/src/provider/provider.tsx` (`"use client"`)
- Test: `test/provider/provider.test.tsx`

**Step 1 (failing test):** Rendering `<LedgerProvider config={{baseUrl}}>` provides a client (a child calling `useLedgerClient()` works); when no `queryClient` prop is passed it creates one; when passed it reuses it; renders a `<div className="ledger-root">` wrapper; `theme` prop sets inline CSS vars on that wrapper.

**Steps 2–5:** Implement. `config` accepts `{baseUrl, apiKey?, fetch?, queryClient?, onError?, theme?}`. Wrap children in `QueryClientProvider` (own or injected) + client context + `.ledger-root` div. Wire `onError` into the QueryClient's default `mutations.onError`/`queries` via `QueryCache`. Commit.

---

## Phase 3 — Hooks migration (context client + key namespacing)

Move `web/src/lib/hooks/*` into `src/hooks/*`. **Two mechanical changes per file:** (a) replace `import * as api from "@/lib/api"` + `api.fn()` with `const client = useLedgerClient(); client.fn()`; (b) **prefix every query key with `"ledger"`** (`["journals"]` → `["ledger","journals"]`, `["balances"]` → `["ledger","balances"]`, etc.).

### Task 3.1: `useLedgerMutation` namespacing

**Files:** Create `src/hooks/use-ledger-mutation.ts`; Test `test/hooks/use-ledger-mutation.test.tsx`.

**Step 1 (failing test):** After a successful mutation, the hook invalidates `["ledger", ...invalidateKeys]`, `["ledger","balances"]`, `["ledger","system-balances"]` (assert via a spied QueryClient). 

**Steps 2–5:** Port wrapper; keys take the `ledger` prefix; `invalidateKeys` callers pass bare segments, wrapper prepends `"ledger"`. Commit.

### Task 3.2–3.8: Per-hook-file migration (pattern repeats)

**Files (migrate each, with an MSW-backed test for the primary query + any mutation):**
- `use-journals.ts`, `use-balances.ts`, `use-deposits.ts`, `use-withdrawals.ts`, `use-metadata.ts`, `use-reservations.ts`, `use-system.ts`

**Per file:** test (loading→success via MSW; key is `["ledger",...]`; mutation invalidates) → migrate → green → commit `feat(ledger-react): migrate <hook> to context client + ledger keys`.

---

## Phase 4 — UI components & pages

### Task 4.1: shadcn primitives + utils

**Files:** Move `src/components/ui/*` (14) and `src/lib/utils/*` into `packages/.../src/components/ui/*` and `src/lib/utils/*`. Fix internal import aliases (`@/lib/utils` → relative). Test: a render smoke test for 2–3 primitives. Commit.

### Task 4.2: sidebar + dashboard widgets

**Files:** Move `src/components/sidebar.tsx`, `src/components/dashboard/*`. Sidebar must take routing as **props/slots** (it cannot own Next `<Link>` hrefs rigidly — accept a `linkComponent`/`items` prop so the host wires routes). Test: renders nav items; calls provided link component. Commit.

### Task 4.3: page components (host-owned routing model A)

**Files:** Move each `src/app/<x>/_components/<x>-client.tsx` → `src/components/pages/<X>Page.tsx`, exported by name. These stay `"use client"`. Drop the Next-specific `page.tsx` wrappers (host supplies those).

**Pages:** Balances, Journals, JournalDetail, Entries, Reservations, Bookings(Deposits/Withdrawals), Classifications, JournalTypes, Templates, Currencies(+deactivate button — new), Reconciliation, Snapshots, Dashboard(home).

**Per page:** move → fix imports (hooks/ui now package-internal) → RTL smoke test (renders skeleton then MSW data) → commit `feat(ledger-react): <X>Page component`.

### Task 4.4: `<LedgerAdmin/>` all-in-one fallback + public exports

**Files:** Create `src/components/LedgerAdmin.tsx` (sidebar + internal section switch); finalize `src/index.ts` exporting `LedgerProvider`, `useLedgerClient`, all `*Page`, `LedgerSidebar`, `LedgerAdmin`, client types, `createLedgerClient`, `ApiRequestError`. Test: `<LedgerAdmin/>` inside provider renders default section. Commit.

---

## Phase 5 — Styling pipeline

### Task 5.1: scope tokens under `.ledger-root`, strip preflight, precompile

**Files:**
- Create: `src/styles/theme.css` (the OKLCH tokens from `web/src/app/globals.css`, **moved under `.ledger-root { ... }`** and `.ledger-root.dark { ... }`)
- Modify: tsup/postcss build to compile component CSS **without Tailwind preflight** into `dist/styles.css`
- Test: a build-output assertion that `dist/styles.css` contains `.ledger-root` and does **not** contain preflight reset selectors (e.g. no global `*,::before` reset).

**Steps:** configure Tailwind v4 build for the package (CSS-first `@theme` scoped), exclude preflight, emit `dist/styles.css`. Verify components reference `var(--…)` only. Commit `feat(ledger-react): scoped, preflight-free precompiled styles`.

---

## Phase 6 — SSR / RSC prefetch (`./server`)

### Task 6.1: `createServerLedgerClient` + prefetch helpers

**Files:**
- Create: `src/server/client.ts` — `createServerLedgerClient(config)` (server-only; reads nothing itself — host passes server config). Re-uses `request` core.
- Create: `src/server/prefetch.ts` — `prefetchJournals(qc, client, params)` etc. for the prefetchable pages.
- Modify: `src/server.ts` to export both.
- Test: `prefetchJournals` populates the QueryClient cache under the same `["ledger","journals",...]` key the hook reads (so hydration is a cache hit, zero client refetch).

**Steps:** test → impl → green → commit. Document in the design that **server API key must never be imported into a client component** (the `./server` entry is server-only by convention; do not re-export it from `index.ts`).

---

## Phase 7 — Dogfood: re-wire `web/` onto the package

### Task 7.1: replace `web/src/lib` + `web/src/components` with package imports

**Files:**
- Modify: `web/src/app/layout.tsx` → wrap in `<LedgerProvider config={{ baseUrl: process.env.NEXT_PUBLIC_API_URL!, apiKey: process.env.NEXT_PUBLIC_API_KEY }}>`; import `LedgerSidebar` from the package; import `@azex/ledger-react/styles.css`.
- Modify: each `web/src/app/<x>/page.tsx` → import the corresponding `*Page` from the package; for prefetchable pages, add the RSC `prefetch* + HydrationBoundary` pattern using `createServerLedgerClient`.
- Delete: `web/src/lib/api.ts`, `web/src/lib/hooks/*`, `web/src/components/ui/*`, `web/src/components/sidebar.tsx`, `web/src/components/dashboard/*`, `web/src/components/providers.tsx`, `web/src/lib/prefetch.ts` (now provided by the package).

**Steps:** wire → `cd web && npm run build` (Expected: builds; RSC boundaries intact) → `npm run lint` → commit `refactor(web): consume @azex/ledger-react (dogfood)`.

### Task 7.2: E2E smoke (optional, if Playwright present)

Run the dogfood app against a real `ledgerd` (docker compose) for one happy-path smoke. Commit if added.

---

## Phase 8 — Publish & CI

### Task 8.1: GitHub Packages publish config + CI

**Files:**
- Create/Modify: `.github/workflows/*` — add a job: install → `typecheck` → `test` (vitest+MSW) → `build` (assert `"use client"` preserved + `styles.css` emitted) → on tag, `npm publish` to GitHub Packages (azex-ai).
- Create: `web/packages/ledger-react/README.md` (consumer quickstart: install, Provider, mount a page, theme override, SSR prefetch).

**Steps:** add workflow → verify locally (`npm run -w @azex/ledger-react typecheck && test && build`) → commit `ci(ledger-react): typecheck/test/build/publish pipeline`.

---

## Phase 9 — (Optional, deferred) OpenAPI type generation

Generate `src/client/types.ts` from `docs/openapi.yaml` (e.g. `openapi-typescript`) so client types track the contract. Deferred until the hand-written types prove a maintenance cost. YAGNI for v0.

---

## Done criteria

- `web/` renders the full admin **only** through `@azex/ledger-react` (no duplicated client/hooks/components remain).
- `npm run -w @azex/ledger-react build && test && typecheck` all green; `dist/` has `index.js`, `server.js`, `styles.css`, `.d.ts`, with `"use client"` preserved.
- A consumer can: `npm i @azex/ledger-react`, wrap `<LedgerProvider config>`, import `styles.css`, mount a `*Page` (host route) or `<LedgerAdmin/>`, and re-theme by overriding `.ledger-root` CSS vars.
- No `process.env` read anywhere inside the package.
