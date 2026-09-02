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
//   DisabledTip.tsx      - tooltip wrapper for disabled controls
//   feedback.ts          - shared success-banner dismiss timing
//   chunkLoad.ts         - async-chunk load state + Retry
import * as React from 'react';
import { ClusterBaseline, ComplianceCheckResult } from '../models';
import Overview from './Overview';
import { ChunkGate } from './ChunkError';

type BaselineContextValue = {
  baseline?: ClusterBaseline;
  loaded: boolean;
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
  const { baseline, loaded, checkResults } = React.useContext(BaselineContext);
  return <Overview baseline={baseline} loaded={loaded} checkResults={checkResults} />;
}

export function ResultsRoute() {
  const { baseline, checkResults, checkResultsLoaded, checkResultsError } =
    React.useContext(BaselineContext);
  return (
    <ChunkGate load={loadResultsTab}>
      {(m) => {
        const ResultsTab = m.default;
        return (
          <ResultsTab
            baseline={baseline}
            results={checkResults}
            resultsLoaded={checkResultsLoaded}
            resultsError={checkResultsError}
          />
        );
      }}
    </ChunkGate>
  );
}

export function RemediationsRoute() {
  const { baseline, loaded } = React.useContext(BaselineContext);
  return (
    <ChunkGate load={loadRemediationsTab}>
      {(m) => {
        const RemediationsTab = m.default;
        return <RemediationsTab baseline={baseline} baselineLoaded={loaded} />;
      }}
    </ChunkGate>
  );
}

export function ProfilesRoute() {
  const { baseline, loaded } = React.useContext(BaselineContext);
  return (
    <ChunkGate load={loadProfilesTab}>
      {(m) => {
        const ProfilesTab = m.default;
        return <ProfilesTab baseline={baseline} loaded={loaded} />;
      }}
    </ChunkGate>
  );
}
