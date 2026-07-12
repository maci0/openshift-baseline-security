import * as React from 'react';
import { ClusterBaseline, ComplianceCheckResult } from '../models';
import Overview from './Overview';
import ResultsTab from './ResultsTab';
import RemediationsTab from './RemediationsTab';
import ProfilesTab from './ProfilesTab';

export type BaselineContextValue = {
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
    <ResultsTab
      baseline={baseline}
      results={checkResults}
      resultsLoaded={checkResultsLoaded}
      resultsError={checkResultsError}
    />
  );
}

export function RemediationsRoute() {
  const { baseline, loaded } = React.useContext(BaselineContext);
  return <RemediationsTab baseline={baseline} baselineLoaded={loaded} />;
}

export function ProfilesRoute() {
  const { baseline, loaded } = React.useContext(BaselineContext);
  return <ProfilesTab baseline={baseline} loaded={loaded} />;
}
