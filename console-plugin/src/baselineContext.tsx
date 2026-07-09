import * as React from 'react';
import { ClusterBaseline } from './models';
import Overview from './components/Overview';
import ResultsTab from './components/ResultsTab';
import RemediationsTab from './components/RemediationsTab';
import ProfilesTab from './components/ProfilesTab';

export type BaselineContextValue = {
  baseline?: ClusterBaseline;
  loaded: boolean;
};

export const BaselineContext = React.createContext<BaselineContextValue>({ loaded: false });

// Module-level route components keep HorizontalNav page types stable across
// CR watch updates while still re-rendering when the context value changes.
export function OverviewRoute() {
  const { baseline, loaded } = React.useContext(BaselineContext);
  return <Overview baseline={baseline} loaded={loaded} />;
}

export function ResultsRoute() {
  const { baseline } = React.useContext(BaselineContext);
  return <ResultsTab baseline={baseline} />;
}

export function RemediationsRoute() {
  const { baseline } = React.useContext(BaselineContext);
  return <RemediationsTab baseline={baseline} />;
}

export function ProfilesRoute() {
  const { baseline } = React.useContext(BaselineContext);
  return <ProfilesTab baseline={baseline} />;
}
