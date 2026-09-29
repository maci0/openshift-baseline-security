// Page shell context and HorizontalNav route wrappers.
//
// Component map (where UI work should go):
//   CompliancePage.tsx   - page entry, watches, rescan/export actions
//   BaselineContext.tsx  - shared CR/CCR context + route components
//   Overview.tsx         - score, schedule, composition, history
//   OverviewCharts.tsx   - Victory donut/trend (async chunk)
//   ResultsTab.tsx       - check results table, waive, CSV
//   RemediationsTab.tsx  - remediation list, apply/batch
//   ProfilesTab.tsx      - built-in + tailored profile management
//   ClusterScoreItem.tsx - cluster Overview details score item
//   LoadingCards.tsx     - skeleton placeholders while the baseline watch has
//                          not delivered yet (chunk loads have their own gate)
//   BaselineNotConfigured.tsx - empty state when no ClusterBaseline exists
//   BaselineUnavailable.tsx - danger state when the baseline watch failed
//   ChunkError.tsx       - ChunkGate, renders ChunkError + Retry on a failed GET
//   TabErrorBoundary.tsx - per-tab boundary: reports a render throw, keeps the nav
//   renderError.ts       - the browser-console report a render throw produces
//   DisabledTip.tsx      - tooltip wrapper for disabled controls
//   ConsoleLink.tsx      - in-SPA link to another console route
//   useAutoDismiss.ts    - shared success-banner dismiss timing
//   useWaiverExpiryClock.ts - ticking clock driving waiver-expiry countdowns
//   chunkLoad.ts         - async-chunk load state + Retry
//   focus.ts             - focus restore after a modal closes
import * as React from 'react';
import { ClusterBaseline, ComplianceCheckResult } from '../models';
import Overview from './Overview';
import { ChunkGate } from './ChunkError';
import { TabErrorBoundary } from './TabErrorBoundary';

type BaselineContextValue = {
  baseline?: ClusterBaseline;
  loaded: boolean;
  // Set when the baseline watch failed. `loaded` is forced true on an error so
  // the tabs stop skeletoning, which means a missing `baseline` and a failed
  // watch are otherwise indistinguishable: the tabs would claim the CR does not
  // exist and offer to create it. Every tab that gates on `baseline` reads
  // this to tell the two apart.
  baselineError?: unknown;
  // Single shared watch of ComplianceCheckResults (CompliancePage owns it).
  // Overview and Results re-use the list instead of opening parallel watches.
  // Pre-filtered to baseline-owned suites so tabs do not re-scan foreign CCRs.
  checkResults?: ComplianceCheckResult[];
  checkResultsLoaded?: boolean;
  checkResultsError?: unknown;
};

export const BaselineContext = React.createContext<BaselineContextValue>({ loaded: false });

const loadResultsTab = () =>
  import(/* webpackChunkName: "results-tab" */ './ResultsTab');
const loadRemediationsTab = () =>
  import(/* webpackChunkName: "remediations-tab" */ './RemediationsTab');
const loadProfilesTab = () =>
  import(/* webpackChunkName: "profiles-tab" */ './ProfilesTab');

// Module-level route components keep HorizontalNav page types stable across
// CR watch updates while still re-rendering when the context value changes.
export function OverviewRoute() {
  const { baseline, loaded, baselineError, checkResults, checkResultsError } =
    React.useContext(BaselineContext);
  return (
    <TabErrorBoundary name="Overview">
      <Overview
        baseline={baseline}
        loaded={loaded}
        baselineError={baselineError}
        checkResults={checkResults}
        checkResultsError={checkResultsError}
      />
    </TabErrorBoundary>
  );
}

export function ResultsRoute() {
  const { baseline, baselineError, checkResults, checkResultsLoaded, checkResultsError } =
    React.useContext(BaselineContext);
  return (
    <TabErrorBoundary name="Results">
      <ChunkGate load={loadResultsTab}>
        {(m) => {
          const ResultsTab = m.default;
          return (
            <ResultsTab
              baseline={baseline}
              baselineError={baselineError}
              results={checkResults}
              resultsLoaded={checkResultsLoaded}
              resultsError={checkResultsError}
            />
          );
        }}
      </ChunkGate>
    </TabErrorBoundary>
  );
}

export function RemediationsRoute() {
  const { baseline, loaded, baselineError } = React.useContext(BaselineContext);
  return (
    <TabErrorBoundary name="Remediations">
      <ChunkGate load={loadRemediationsTab}>
        {(m) => {
          const RemediationsTab = m.default;
          return (
            <RemediationsTab
              baseline={baseline}
              baselineLoaded={loaded}
              baselineError={baselineError}
            />
          );
        }}
      </ChunkGate>
    </TabErrorBoundary>
  );
}

export function ProfilesRoute() {
  const { baseline, loaded, baselineError } = React.useContext(BaselineContext);
  return (
    <TabErrorBoundary name="Profiles">
      <ChunkGate load={loadProfilesTab}>
        {(m) => {
          const ProfilesTab = m.default;
          return (
            <ProfilesTab baseline={baseline} loaded={loaded} baselineError={baselineError} />
          );
        }}
      </ChunkGate>
    </TabErrorBoundary>
  );
}
