import * as React from 'react';
import { useTranslation } from 'react-i18next';
import {
  ListPageBody,
  ListPageFilter,
  RowFilter,
  RowProps,
  TableColumn,
  TableData,
  useK8sWatchResource,
  useListPageFilter,
  VirtualizedTable,
} from '@openshift-console/dynamic-plugin-sdk';
import {
  Button,
  Content,
  Label,
  Modal,
  ModalBody,
  ModalHeader,
  Split,
  SplitItem,
  Title,
} from '@patternfly/react-core';
import {
  CheckCircleIcon,
  DownloadIcon,
  ExclamationCircleIcon,
  ExclamationTriangleIcon,
  InfoCircleIcon,
  MinusCircleIcon,
} from '@patternfly/react-icons';
import {
  CheckStatus,
  ClusterBaseline,
  ComplianceCheckResult,
  ComplianceCheckResultGVK,
  isOwnedByBaseline,
  suiteProfileKey,
} from '../models';
import { checkBody, checkResultHref, checkTitle, resultsCsv } from '../utils';

const statusLabel: Record<
  CheckStatus,
  { color: React.ComponentProps<typeof Label>['color']; icon: React.ReactElement }
> = {
  PASS: { color: 'green', icon: <CheckCircleIcon /> },
  FAIL: { color: 'red', icon: <ExclamationCircleIcon /> },
  ERROR: { color: 'red', icon: <ExclamationCircleIcon /> },
  MANUAL: { color: 'orange', icon: <ExclamationTriangleIcon /> },
  INFO: { color: 'blue', icon: <InfoCircleIcon /> },
  INCONSISTENT: { color: 'orange', icon: <ExclamationTriangleIcon /> },
  'NOT-APPLICABLE': { color: 'grey', icon: <MinusCircleIcon /> },
};

const ResultsTab: React.FC<{ baseline?: ClusterBaseline }> = ({ baseline }) => {
  const { t } = useTranslation('plugin__baseline-security-console-plugin');
  const [results, loaded, error] = useK8sWatchResource<ComplianceCheckResult[]>({
    groupVersionKind: ComplianceCheckResultGVK,
    isList: true,
    namespace: 'openshift-compliance',
  });
  const [selected, setSelected] = React.useState<ComplianceCheckResult | null>(null);

  const columns: TableColumn<ComplianceCheckResult>[] = React.useMemo(
    () => [
      { title: t('Check'), id: 'title', sort: 'description' },
      { title: t('Status'), id: 'status', sort: 'status' },
      { title: t('Severity'), id: 'severity', sort: 'severity' },
    ],
    [t],
  );

  const profiles = baseline?.spec.profiles;
  const tailored = baseline?.spec.tailoredProfiles;
  const ownedResults = React.useMemo(
    () => (results ?? []).filter((r) => isOwnedByBaseline(r.metadata.labels, profiles, tailored)),
    [results, profiles, tailored],
  );

  const Row = React.useCallback(
    ({ obj, activeColumnIDs }: RowProps<ComplianceCheckResult>) => {
      const s = statusLabel[obj.status] ?? { color: 'grey' as const, icon: <MinusCircleIcon /> };
      return (
        <>
          <TableData id="title" activeColumnIDs={activeColumnIDs}>
            {/* Single-line rows: VirtualizedTable virtualizes with a fixed row
                height; the raw check name lives in the detail modal. */}
            <Button variant="link" isInline onClick={() => setSelected(obj)}>
              {checkTitle(obj)}
            </Button>
          </TableData>
          <TableData id="status" activeColumnIDs={activeColumnIDs}>
            <Label isCompact color={s.color} icon={s.icon}>
              {obj.status}
            </Label>
          </TableData>
          <TableData id="severity" activeColumnIDs={activeColumnIDs}>
            {obj.severity}
          </TableData>
        </>
      );
    },
    [setSelected],
  );

  const profileItems = React.useMemo(() => {
    const keys = new Set(profiles ?? []);
    for (const r of ownedResults) {
      const key = suiteProfileKey(r.metadata.labels);
      if (key !== undefined) {
        keys.add(key);
      }
    }
    return [...keys].sort().map((k) => ({ id: k, title: k }));
  }, [profiles, ownedResults]);

  const rowFilters: RowFilter<ComplianceCheckResult>[] = React.useMemo(
    () => [
      {
        filterGroupName: t('Status'),
        type: 'result-status',
        reducer: (r) => r.status,
        filter: (input, r) => !input.selected?.length || input.selected.includes(r.status),
        items: ['PASS', 'FAIL', 'MANUAL', 'ERROR', 'INFO', 'INCONSISTENT', 'NOT-APPLICABLE'].map((s) => ({
          id: s,
          title: s,
        })),
      },
      {
        filterGroupName: t('Severity'),
        type: 'result-severity',
        reducer: (r) => r.severity,
        filter: (input, r) => !input.selected?.length || input.selected.includes(r.severity),
        items: ['high', 'medium', 'low', 'info', 'unknown'].map((s) => ({ id: s, title: s })),
      },
      {
        filterGroupName: t('Profiles'),
        type: 'result-profile',
        reducer: (r) => suiteProfileKey(r.metadata.labels) ?? '',
        filter: (input, r) =>
          !input.selected?.length ||
          input.selected.includes(suiteProfileKey(r.metadata.labels) ?? ''),
        items: profileItems,
      },
    ],
    [t, profileItems],
  );

  const [data, filteredData, onFilterChange] = useListPageFilter(ownedResults, rowFilters);

  const downloadCsv = React.useCallback(() => {
    // Export the currently filtered rows so the download matches the view.
    const blob = new Blob([resultsCsv(filteredData)], { type: 'text/csv;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'compliance-results.csv';
    a.click();
    URL.revokeObjectURL(url);
  }, [filteredData]);

  return (
    <ListPageBody>
      <Split hasGutter>
        <SplitItem isFilled>
          <ListPageFilter
            data={data}
            loaded={loaded}
            rowFilters={rowFilters}
            onFilterChange={onFilterChange}
          />
        </SplitItem>
        <SplitItem>
          <Button
            variant="secondary"
            icon={<DownloadIcon />}
            isDisabled={!loaded || filteredData.length === 0}
            onClick={downloadCsv}
          >
            {t('Export CSV')}
          </Button>
        </SplitItem>
      </Split>
      <VirtualizedTable<ComplianceCheckResult>
        data={filteredData}
        unfilteredData={data}
        loaded={loaded}
        loadError={error}
        columns={columns}
        Row={Row}
      />
      <Modal
        variant="medium"
        isOpen={!!selected}
        onClose={() => setSelected(null)}
        aria-labelledby="check-detail-title"
      >
        <ModalHeader
          title={selected ? checkTitle(selected) : ''}
          labelId="check-detail-title"
          description={selected?.metadata.name}
        />
        <ModalBody>
          {selected && (
            <>
              <Content component="p" style={{ whiteSpace: 'pre-wrap' }}>
                {checkBody(selected) || t('No description provided.')}
              </Content>
              {selected.instructions && (
                <>
                  <Title headingLevel="h3">{t('How to verify')}</Title>
                  <Content component="pre" style={{ whiteSpace: 'pre-wrap' }}>
                    {selected.instructions}
                  </Content>
                </>
              )}
              <Content component="p" style={{ marginTop: 'var(--pf-t--global--spacer--md)' }}>
                <a href={checkResultHref(selected.metadata.name)}>
                  {t('View ComplianceCheckResult resource')}
                </a>
              </Content>
            </>
          )}
        </ModalBody>
      </Modal>
    </ListPageBody>
  );
};

export default ResultsTab;
