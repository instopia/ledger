# Design: `@azex/ledger-react` — a consumable frontend package

- **Date:** 2026-05-31
- **Status:** Approved design (not yet implemented)
- **Topic:** Make the ledger frontend consumable the way the Go backend is — importable into other React/Next.js apps, not just a standalone app.

## 1. Context & Problem

The backend is genuinely **dual-mode**: a consumer can `import "github.com/azex-ai/ledger/core"` and wire it in, or run `ledgerd` as a standalone HTTP service. The frontend is **single-mode**: `web/` is `"name": "web", "private": true` — a standalone Next.js app with no `exports` and no library build. There is no `npm install` that drops a ledger admin UI into another app. CLAUDE.md's "dual-mode" sentence only ever describes the backend.

This design closes that asymmetry for the **frontend admin (back-office) UI**, so internal products (e.g. Azex, PerpX, and the CCA rewrite) can import the ledger admin like a package.

## 2. Goals / Non-Goals

**Goals**
- One `npm install` gives a consumer the typed API client + React Query hooks + admin UI.
- Re-themeable per product via CSS variables (color/theme only).
- Single source of truth for the API contract (no per-product client forks).
- Performance: support SSR/RSC prefetch where the Next.js server is network-close to the ledger API.

**Non-Goals**
- No non-React consumers (no Node SDK, no other framework). Confirmed: every consumer is React/Next.js.
- No structural customization of the UI per product. Back-office pages are uniform by design; "more generic = better". Theming is color-only.
- No SEO/first-paint concern — SSR is purely for internal performance (collapsing the client waterfall).

## 3. Key Decisions (and why)

| Decision | Choice | Rationale |
|---|---|---|
| Distribution model | **Single black-box npm package** (not a shadcn registry, not a monorepo) | Back-office + theme-only + "more generic = better" ⇒ a versioned single-source-of-truth package. A registry (copy-in) would create N drifting copies and break bug-fix propagation — the opposite of "generic". |
| Package granularity | **One package** (`@azex/ledger-react`), not split client/UI | No non-React consumer exists, so splitting the data layer out is YAGNI. |
| Config | **Injected via Provider / `createClient()`**, package never reads `process.env` | Composition-root pattern; fixes the current `api.ts` module-load `NEXT_PUBLIC_API_URL` read (config embedded → injected). Hard prerequisite for cross-app reuse. |
| Routing | **Host owns routes (option A)** + convenience `<LedgerAdmin/>` wrapper | Back-office needs deep links ("look at this journal"); native to Next App Router. `<LedgerAdmin/>` covers the one-mount case. |
| Data fetching | **CSR default + hydration-ready hooks + optional server prefetch** | CSR works everywhere with zero wiring; SSR/RSC prefetch is opt-in per page to collapse the client waterfall when the Next server sits next to the ledger API. |
| Theming | **CSS variables scoped under `.ledger-root`** | Color-only theming; scoping avoids colliding with the host's own design tokens while keeping shadcn's standard token names internally (minimal source change). |

## 4. Architecture & Repo Layout

Publish target: **GitHub Packages, `azex-ai` org (private)** — per infra rule "no resources under personal accounts".

The package is extracted from the current `web/` source; `web/` becomes a **dogfood app** that consumes the package exactly as an external consumer would.

```
web/
  packages/ledger-react/          # the published package
    src/
      client/        # from lib/api.ts → createClient(config); no process.env
      hooks/         # from lib/hooks/* → read client from context
      components/
        ui/          # shadcn primitives (themed via CSS vars)
        dashboard/   # composite widgets
        pages/       # per-domain pages as mountable components
      provider.tsx   # <LedgerProvider config> (client component)
      server.ts      # createServerLedgerClient() + prefetch helpers (server-only)
      styles.css     # precompiled CSS + default OKLCH tokens (scoped)
      index.ts       # public exports
    package.json     # exports map, peerDependencies
  src/               # current app → re-implemented to import the package
```

Structural invariants:
1. **Config injected, never embedded** — no `process.env` inside the package.
2. **`"use client"` boundaries** — Provider and all hook-using components are client components; preserved in the build output.
3. **`peerDependencies`** — `react`, `react-dom`, `@tanstack/react-query` (one copy lives in the host).
4. **Theming via CSS variables only** — no structural props.

## 5. Public API (Consumer Surface)

### Provider — the single config injection point

```tsx
import { LedgerProvider } from "@azex/ledger-react"
import "@azex/ledger-react/styles.css"

<LedgerProvider
  config={{
    baseUrl: process.env.NEXT_PUBLIC_LEDGER_API_URL!, // host reads env, passes in
    apiKey:  process.env.LEDGER_API_KEY,              // optional
    // queryClient?: reuse host's React Query; omitted → package creates its own
    // onError?: (err: ApiError) => void               // host wires its own toast
  }}
>
  {children}
</LedgerProvider>
```

- Host reads env and passes values; the package never reads env.
- QueryClient defaults to package-internal, can be injected to share the host's.

### Mounting — host owns routes (A), with an all-in-one fallback

```tsx
// app/admin/journals/page.tsx
import { JournalsPage } from "@azex/ledger-react"
export default function Page() { return <JournalsPage /> }
```

The package also exports `<LedgerAdmin/>` (sidebar + internal section switching) for consumers who want a single mount point.

### Theming — zero source changes

```css
/* host globals.css */
.ledger-root { --primary: oklch(0.65 0.2 250); --background: oklch(0.15 0 0); }
```

Or via a `theme` prop on `<LedgerProvider>` that writes inline CSS vars on the `.ledger-root` wrapper.

## 6. Data Flow

- **Client via context:** hooks call `useLedgerClient()` (throws if used outside `<LedgerProvider>` — fail loud, never silently return empty). No module singleton.
- **Query-key namespacing:** every key is prefixed `["ledger", ...]` so it cannot collide with the host's own React Query cache.
- **Mutations:** read client from context, attach `Idempotency-Key` (required for writes), invalidate the matching `["ledger", ...]` keys.
- **CSR default + hydration-ready:** hooks work client-side with no wiring; React Query's native `dehydrate`/`hydrate` lets a server-prefetched cache seed them with zero client waterfall.
- **Optional server prefetch (perf opt-in):**

```tsx
// app/admin/journals/page.tsx (RSC)
const qc = new QueryClient()
await prefetchJournals(qc, createServerLedgerClient(), params)
return <HydrationBoundary state={dehydrate(qc)}><JournalsPage /></HydrationBoundary>
```

- **Two-sided config (safety-critical):** the browser Provider gets *public* config only; `createServerLedgerClient()` reads *server-only* env (internal baseUrl + secret key) inside RSC. **The server API key must never reach the client bundle.**
- **Errors:** client throws `ApiError {code, message}`; surfaced via React Query error state; package does not bundle a toast library — host wires its own via `onError`.

## 7. Styling / Theme Packaging (Tailwind v4)

1. **Precompiled, self-contained CSS:** the package compiles its Tailwind utility classes at build time into `styles.css`; the host imports it and does not need to scan the package source with its own Tailwind.
2. **Token scope isolation:** internal shadcn components use standard token names (`--primary`, etc.) but all tokens are defined under `.ledger-root` (rendered by Provider / `<LedgerAdmin/>`). No collision with the host's own `--primary`; component source unchanged.
3. **No global preflight/reset:** the package build strips Tailwind's preflight so it only ships component utilities + tokens — it must not clobber the host's global styles.
4. **Dark by default, switchable:** crypto-native dark (zinc/slate, OKLCH); `<LedgerProvider theme="light|dark">` or `.ledger-root.dark`.

## 8. Testing

Mock at the network boundary, never mock React Query internals (mirrors the backend's "mock at the right layer" ethos).

| Layer | Tooling | Coverage |
|---|---|---|
| client | MSW | response mapping, `ApiError {code}` handling, `Idempotency-Key` on writes |
| hooks | RTL + test QueryClient + MSW | loading/error/success, key namespacing, invalidate-on-mutation |
| components | RTL + Provider + MSW | per-page smoke render (data/skeleton/error), key interactions |
| E2E (optional) | Playwright on the dogfood app vs a real `ledgerd` (docker compose) | real end-to-end smoke |

## 9. Publishing

- Build with tsup/rollup → **ESM + `.d.ts` + precompiled `styles.css`**; **preserve `"use client"` directives** in output (bundlers strip them by default — must be configured, or Next RSC consumers break).
- `package.json` exports: main entry (client components + hooks + Provider), `./server` (server client + prefetch helpers, server-only), `./styles.css`.
- `peerDependencies`: `react`, `react-dom`, `@tanstack/react-query`.
- Publish to GitHub Packages (azex-ai, private), semver; CI publishes on tag.
- **Types generated from `docs/openapi.yaml`** so the package types track the contract; a backend API change regenerates types and surfaces breaks at compile time. The OpenAPI spec is the seam.

## 10. Dogfood Guarantee

`web/` depends on the local package via a workspace link and renders the admin only through the package's public API (`<LedgerProvider>` + pages). If `web/` can render only through the package, the package's public API cannot silently break. `web/` is also where E2E runs.

## 11. CI Gates (aligned with backend)

typecheck · lint · MSW integration tests · build (verify `"use client"` preserved & `styles.css` emitted) · optional Playwright E2E.

## 12. Open Items / Future

- Confirm GitHub Packages vs an alternative private registry.
- Decide the exact monorepo tooling for `web/packages/*` (npm/pnpm workspaces).
- OpenAPI → TypeScript generator choice (e.g. openapi-typescript) and where generation runs in CI.
- First real consumer: the CCA rewrite will import this package — use it as the first integration test of the consumer story.
