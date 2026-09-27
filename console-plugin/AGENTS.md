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

## Gate

```sh
yarn lint          # eslint ./src ./e2e ./tools/attribution webpack.config.ts (type-aware except webpack)
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

Both linters are required and neither subsumes the other: oxlint carries the
type-aware anti-slop rules eslint has no port of. `tools/oxlint/anti-slop/` is
a local oxlint plugin (its own `package.json` marks that subtree ESM); it is
excluded from linting itself via `ignorePatterns`.

`tsconfig.json` runs `strict` plus `noFallthroughCasesInSwitch`,
`noImplicitOverride`, `noImplicitReturns`, `noUnusedLocals`,
`noUnusedParameters`, and `isolatedModules`. Do not loosen a flag to make an
error go away.

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
  annotation values, `status.errorMessage`) renders with `dir="auto"`, so an
  RTL value does not reorder the punctuation around it.
- `'—'` is the rendered placeholder for an absent or unscoreable value, and it
  is distinct from an error state. It is a UI string, not prose: the em-dash
  ban does not reach it.
- Victory charts live in `OverviewCharts.tsx` and load after the Overview
  cards paint. Results, Remediations, and Profiles are async chunks. A failed
  chunk GET must show Retry (`ChunkError`), not a blank tab.
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

`.env` carries only the four keys in `.env.example`; `e2e/dotenv.ts` rejects an
unknown, duplicate, malformed, or unterminated-quote line with the file and line
number, so a typo cannot surface later as a missing value. The `*.test.ts` files
in `e2e/` are the pieces jest runs (`e2e/dotenv.test.ts`, `e2e/helpers.test.ts`);
the `*.spec.ts` files there belong to Playwright and stay out of the jest gate.

`shot()` hides the console masthead account control before the capture and
restores it after, so a suite run under a real SSO account cannot commit that
account's name and avatar initials into `docs/screenshots/`. Keep captures
going through `shot()` rather than a bare `page.screenshot`, and do not add a
docs image that captures an identity.
