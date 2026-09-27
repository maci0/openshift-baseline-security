# Agent rules: console-plugin

Narrows the repo-root `AGENTS.md`. OpenShift console dynamic plugin: React 18,
PatternFly 6, `@openshift-console/dynamic-plugin-sdk` on the 4.22 line.

## Toolchain

**Yarn 4, not bun.** This package is standardized on Yarn (`packageManager` in
`package.json` is the single source; `corepack prepare` activates it) with
`enableImmutableInstalls`, `checksumBehavior: throw`, and `enableScripts: false`
(no registry lifecycle scripts at install). Do not migrate it, and do not add
a `package-lock.json` beside `yarn.lock`.

Node 22 exactly, pinned by `.nvmrc` to the same patch as the digest-pinned
`ubi9/nodejs-22` build image. `yarn build` refuses any other major.

`i18next` and `react-i18next` are the one pair held on a tilde range, and it
has to stay that way. The console supplies both at runtime and the plugin's
copy has to be the one it binds to: `react-i18next` is a no-fallback singleton
shared module, so a version the console 4.22 line does not provide
(`react-i18next` ~16.5.8, `i18next` ~25.6.2) builds clean and then breaks in
the browser. No unit test can catch that mismatch.

## Gate

```sh
yarn lint          # eslint ./src ./e2e ./tools/attribution ./tools/size webpack.config.ts (type-aware except webpack)
yarn lint:oxlint   # oxlint: @rikalabs/oxlint-standards strict + test-jest preset, perf, react plugin, local anti-slop
yarn typecheck     # tsc --noEmit
yarn test          # jest
yarn ci            # the four above plus the production webpack build
```

One file: `yarn test src/scoring.test.ts`. Watch: `yarn test:watch`.
`make help` lists the same commands.

`yarn licenses` is a build step, not a report: it walks the installed
`node_modules` closure, writes `dist/THIRD-PARTY-NOTICES.txt`, and exits
non-zero on a package whose license is missing, unrecognised, or copyleft, or
that ships no license text. It runs after webpack in `yarn build` (webpack's
`output.clean` wipes `dist/`) and the Dockerfile copies the result to
`/licenses/`. Adding a dependency means adding it to `PERMISSIVE_SPDX` in
`tools/attribution/spdx.ts` with the reason, never silencing the failure.

`yarn size` is a build step for the same reason: it walks `dist/`, gzips every
file, prints the transferred size, and fails over the ceilings in
`tools/size/budget.ts`. What it measures is the initial JS (every
`*-bundle-*.min.js`), the first-paint download (initial JS plus the manifest
and locale the console fetches ahead of it, printed without its own ceiling),
the largest async chunk, and the whole served tree. `THIRD-PARTY-NOTICES.txt`
is counted out of the totals: it lives in `dist/` because the build writes it
there, no page links it, and the Dockerfile serves the same bytes from
`/licenses/`. A `.js` file that matches neither
output name template fails the gate rather than escaping the initial-JS
ceiling, so renaming a webpack output template breaks the check instead of
silently voiding it. Raising a ceiling is a deliberate edit carrying the reason;
the printed table in the CI log is the only record of the numbers, so keep it
in the build log rather than committing a baseline.

Both linters are required and neither subsumes the other: oxlint carries the
type-aware anti-slop rules eslint has no port of. `tools/oxlint/anti-slop/` is
a local oxlint plugin (its own `package.json` marks that subtree ESM); it is
excluded from linting itself via `ignorePatterns`.

`tsconfig.json` runs `strict` plus `noFallthroughCasesInSwitch`,
`noImplicitOverride`, `noImplicitReturns`, `noUnusedLocals`,
`noUnusedParameters`, and `isolatedModules`. Do not loosen a flag to make an
error go away.

## Dev server

`yarn start` serves the plugin on :9001 for a console on another origin, so it
needs CORS, and it serves source maps, so its CORS and Host policy name that
console instead of admitting every origin (`allowedHosts: 'all'` is open to DNS
rebinding). `PLUGIN_DEV_ALLOWED_ORIGIN` is the one knob: a bare http(s) origin,
default `http://localhost:3000`, validated when the dev server options are
built, and its hostname joins the Host allowlist. A LAN console or an
SSH-forwarded one sets it. It is dev-only: a production build never reads it.

## Module layout

Domain logic lives in flat modules under `src/` with a colocated
`<module>.test.ts`. Components under `src/components/` are presentation and
data-fetching only.

`src/clock.ts` is the plugin's only wall-clock source, mirroring
`operator/internal/controller/clock.go`. Read "now" through it (or take the
`now: Date = now()` parameter) rather than calling `Date.now()` / `new Date()`;
that is what lets a frozen-clock run decide waiver expiry, the rescan token,
and the report timestamp identically every time. `setClock` exists for tests
and simulation, which are its only callers.

There is no barrel: import from the owning module, never re-export through an
`index` or a `utils`. `src/testing/` holds test-only helpers (the deterministic
fuzz PRNG) and nothing production imports.

`src/layout.ts` owns the measurements a PatternFly token does not cover: card
widths, reserved chart-slot heights, and the width a form field wraps below.
They are shared because two files have to agree on them (a card's loading
placeholder and the card it stands in for; a reserved sparkline slot and the
chart that fills it). A new one goes there rather than back into a view as a
literal. Spacing, color, and type stay on `--pf-t--global--*` tokens where the
view uses them.

An export with no production caller is dead code, not coverage. Delete it and
point its test at the shipped path instead.

## Untrusted data

Everything off a cluster object is untrusted: `ClusterBaseline` spec fields,
Compliance Operator labels, annotations, and status strings. Narrow through the
guards in `src/parse.ts` (`isString`, `isFiniteNumber`); do not scatter fresh
`typeof` probes. A malformed value must not throw where it would blank the page
or abort a CSV or HTML report export.

Every type assertion carries a `SAFETY:` comment stating the checked invariant.
`anti-slop/require-safety-comment-for-type-assertion` enforces it; chained and
double casts are rejected outright.

## UI conventions

- Every write is gated on `useAccessReview`, and the gate is the write itself,
  not the control that launched it: each mutation calls `mayWrite` from
  `src/permissions.ts` before it sends, so a modal opened while permitted
  cannot spend the request after a revocation. An unresolved review denies. The
  plugin never patches `spec.scoring.mode`; that is an out-of-band CR edit.
- All user-visible text goes through `t()` and lands in
  `locales/en/plugin__baseline-security-console-plugin.json`. Extension titles
  in `console-extensions.json` use the `%key%` form.
- Numbers, dates, sorted lists, and list punctuation come from `src/dates.ts`
  and `src/text.ts` (`formatCount`, `formatList`, `listSeparators`,
  `textCollator`) with the console locale from `i18n.language`. Never a
  literal `", "`, `toFixed`, or `localeCompare`. A comparator or formatter that
  binds the runtime default locale at module load ignores the console locale
  for the life of the tab, so locale is a parameter, not a constant.
- Untrusted text off a cluster object (check titles, waiver reasons, CO
  annotation values, tailored profile names, `status.errorMessage`) and any
  `errorMessage()` result (apiserver `Status.message`, a chunk-load rejection
  reason) renders with `dir="auto"`, so an RTL value does not reorder the
  punctuation around it. An untrusted name that a sentence has to mention is
  its own element with the translated sentence beside it, not a `{{name}}`
  interpolation inside the key.
- A counted string's key carries the raw `{{count}}` (that is what i18next
  matches for a plural form) and its value carries the locale-formatted
  `{{formattedCount}}`; both `_one` and `_other` exist in the English file, and
  `yarn test src/i18n.test.ts` fails on a half-formed base. Numbers quoted in
  prose come from the constant through `formatCount`, never written into the
  key.
- `'—'` is the rendered placeholder for an absent or unscoreable value, and it
  is distinct from an error state. It is a UI string, not prose: the em-dash
  ban does not reach it.
- Victory charts live in `OverviewCharts.tsx` and load after the Overview
  cards paint. Results, Remediations, and Profiles are async chunks. A failed
  chunk GET must show Retry (`ChunkError`), not a blank tab, and must name the
  rejection reason: a stale chunk id and an unreachable CDN need different fixes.
- The console mounts an extension page with no error boundary, so a render
  throw used to blank the route with nothing in the browser console. Every tab
  route and the page shell are wrapped in `TabErrorBoundary`, which names the
  view, reports through `reportRenderError` (the browser console is the only
  sink a dynamic plugin has), and offers Retry so a bad object an operator
  fixes does not need a full page reload. Add the wrap to a new tab route
  rather than a second boundary elsewhere.
- `CompliancePage` forces `loaded` true when the baseline watch errors, so a
  missing `baseline` no longer means "no CR". Every tab that gates on `baseline`
  reads `baselineError` off `BaselineContext` and renders
  `BaselineUnavailable`, never `BaselineNotConfigured`, on a watch failure.
- A score that cannot be computed renders as no score. Never `NaN`, never
  `Infinity`: a `NaN` compares false against every threshold and paints the
  badge green.

## Screenshots

`../docs/screenshots/` is generated by `yarn test-e2e` against a live console
(`SCREENSHOT_DIR` defaults there). Adding an image means adding the `shot()`
call that produces it; a capture with no producer and no README reference does
not belong in the repo. The Playwright `use` block pins animations, caret,
reduced motion, and device scale factor so a re-capture of the same page
produces the same PNG on any runner.

`yarn test-e2e` needs `yarn playwright install chromium` first: `enableScripts:
false` in `.yarnrc.yml` means no install script fetches the browser build.
`e2e/global-setup.ts` names that command when the binary is missing.

`.env` carries only the four keys `E2E_ENV_KEYS` allows: `CONSOLE_URL` and
`KUBEADMIN_PASSWORD` (set in `.env.example`), `KUBEADMIN_USER` and
`SCREENSHOT_DIR` (documented there as optional). `e2e/dotenv.ts` rejects an
unknown, duplicate, malformed, or unterminated-quote line with the file and line
number, so a typo cannot surface later as a missing value. The `*.test.ts` files
in `e2e/` are the pieces jest runs (`e2e/dotenv.test.ts`, `e2e/helpers.test.ts`);
the `*.spec.ts` files there belong to Playwright and stay out of the jest gate.

`shot()` hides the console masthead account control before the capture and
restores it after, so a suite run under a real SSO account cannot commit that
account's name and avatar initials into `docs/screenshots/`. Keep captures
going through `shot()` rather than a bare `page.screenshot`, and do not add a
docs image that captures an identity.
