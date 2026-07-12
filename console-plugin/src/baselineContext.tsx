import * as React from 'react';
import { ClusterBaseline, ComplianceCheckResult } from './models';
import Overview from './components/Overview';
import ResultsTab from './components/ResultsTab';
import RemediationsTab from './components/RemediationsTab';
import ProfilesTab from './components/ProfilesTab';

export type BaselineContextValue = {
  baseline?: ClusterBaseline;
  loaded: boolean;
  // Single shared watch of ComplianceCheckResults (CompliancePage owns it).
  // Overview and Results re-use the list instead of opening parallel watches.
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
  const { baseline } = React.useContext(BaselineContext);
  return <RemediationsTab baseline={baseline} />;
}

export function ProfilesRoute() {
  const { baseline } = React.useContext(BaselineContext);
  return <ProfilesTab baseline={baseline} />;
}
