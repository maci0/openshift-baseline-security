import * as React from 'react';
import { useTranslation } from 'react-i18next';
import {
  k8sPatch,
  ListPageBody,
  ListPageFilter,
  RowFilter,
  RowProps,
  TableColumn,
  TableData,
  useAccessReview,
  useK8sWatchResource,
  useListPageFilter,
  VirtualizedTable,
} from '@openshift-console/dynamic-plugin-sdk';
import {
  Alert,
  Button,
  Content,
  Label,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  Split,
  SplitItem,
  TextArea,
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
  ClusterBaselineModel,
  ComplianceCheckResult,
  ComplianceCheckResultGVK,
  isOwnedByBaseline,
  suiteFilterKey,
} from '../models';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';
import {
  addWaiverPatch,
  checkBody,
  checkResultHref,
  checkTitle,
  errorMessage,
  inconsistentSources,
  isWaived,
  removeWaiverPatch,
  resultsCsv,
} from '../utils';

const statusLabel: Record<
  CheckStatus,
  { color: React.ComponentProps<typeof Label>['color']; icon: React.ReactElement }
> = {
  PASS: { color: 'green', icon: <CheckCircleIcon /> },
  FAIL: { color: 'red', icon: <ExclamationCircleIcon /> },
  ERROR: { color: 'red', icon: <ExclamationCircleIcon /> },
  MANUAL: { color: 'orange', icon: <ExclamationTriangleIcon /> },
  INFO: { color: 'blue', icon: <InfoCircleIcon /> },
  // Distinct from MANUAL (orange): multi-node result disagreement.
  INCONSISTENT: { color: 'purple', icon: <ExclamationTriangleIcon /> },
  SKIP: { color: 'grey', icon: <MinusCircleIcon /> },
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
  const [waiveReason, setWaiveReason] = React.useState('');
  const [busy, setBusy] = React.useState(false);
  const [waiveError, setWaiveError] = React.useState<string | null>(null);
  const [canWaive, canWaiveLoading] = useAccessReview({
    group: 'baselinesecurity.io',
    resource: 'clusterbaselines',
    verb: 'patch',
  });
  const waivers = baseline?.spec.waivers;

  const closeModal = () => {
    setSelected(null);
    setWaiveReason('');
    setWaiveError(null);
  };

  const patchWaivers = async (
    data: Parameters<typeof k8sPatch>[0]['data'],
    failMsg: string,
  ): Promise<void> => {
    if (!baseline) return;
    setBusy(true);
    setWaiveError(null);
    try {
      await k8sPatch({ model: ClusterBaselineModel, resource: baseline, data });
      closeModal();
    } catch (e) {
      setWaiveError(errorMessage(e) ?? failMsg);
    } finally {
      setBusy(false);
    }
  };

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
    // Ids match suiteFilterKey / Overview resultsHref: built-in key or "tp-<name>".
    const keys = new Set(profiles ?? []);
    for (const name of tailored ?? []) {
      keys.add(`tp-${name}`);
    }
    for (const r of ownedResults) {
      const key = suiteFilterKey(r.metadata.labels);
      if (key !== undefined) {
        keys.add(key);
      }
    }
    // Keep the id as the filter key (reducer + resultsHref depend on it) but
    // show tailored profiles by their clean name, not the "tp-" prefix.
    return [...keys].sort().map((k) => ({ id: k, title: k.startsWith('tp-') ? k.slice(3) : k }));
  }, [profiles, tailored, ownedResults]);

  const rowFilters: RowFilter<ComplianceCheckResult>[] = React.useMemo(
    () => [
      {
        filterGroupName: t('Status'),
        type: 'result-status',
        reducer: (r) => r.status,
        filter: (input, r) => !input.selected?.length || input.selected.includes(r.status),
        items: ['PASS', 'FAIL', 'MANUAL', 'ERROR', 'INFO', 'INCONSISTENT', 'SKIP', 'NOT-APPLICABLE'].map((s) => ({
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
        reducer: (r) => suiteFilterKey(r.metadata.labels) ?? '',
        filter: (input, r) =>
          !input.selected?.length ||
          input.selected.includes(suiteFilterKey(r.metadata.labels) ?? ''),
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
    a.style.display = 'none';
    document.body.appendChild(a);
    a.click();
    a.remove();
    window.setTimeout(() => URL.revokeObjectURL(url), 0);
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
        onClose={closeModal}
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
              {selected.status === 'INCONSISTENT' &&
                (() => {
                  const { sources, mostCommon } = inconsistentSources(selected);
                  return (
                    <>
                      <Title headingLevel="h3">{t('Per-node results')}</Title>
                      <Content component="p">
                        {t('Nodes disagree on this rule; review each before acting.')}
                      </Content>
                      <Table variant="compact" style={{ marginBottom: 'var(--pf-t--global--spacer--md)' }}>
                        <Thead>
                          <Tr>
                            <Th>{t('Node')}</Th>
                            <Th>{t('Status')}</Th>
                          </Tr>
                        </Thead>
                        <Tbody>
                          {sources.map((s) => (
                            <Tr key={s.node}>
                              <Td>{s.node}</Td>
                              <Td>
                                <Label isCompact color={statusLabel[s.status as CheckStatus]?.color ?? 'grey'}>
                                  {s.status || '—'}
                                </Label>
                              </Td>
                            </Tr>
                          ))}
                          {mostCommon && (
                            <Tr>
                              <Td>{t('all other nodes')}</Td>
                              <Td>
                                <Label isCompact color={statusLabel[mostCommon as CheckStatus]?.color ?? 'grey'}>
                                  {mostCommon}
                                </Label>
                              </Td>
                            </Tr>
                          )}
                        </Tbody>
                      </Table>
                    </>
                  );
                })()}
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
              {/* Waivers: accept a failing check as risk so it leaves the score.
                  Waiving a passing check is pointless, so only offer it for
                  non-PASS/NOT-APPLICABLE results. */}
              {!['PASS', 'NOT-APPLICABLE'].includes(selected.status) &&
                (() => {
                  const waived = isWaived(selected.metadata.name, waivers);
                  const reason = waivers?.find((w) => w.name === selected.metadata.name)?.reason;
                  return (
                    <>
                      <Title
                        headingLevel="h3"
                        style={{ marginTop: 'var(--pf-t--global--spacer--md)' }}
                      >
                        {t('Waiver')}
                      </Title>
                      {waived ? (
                        <Content component="p">
                          {t('This check is waived (excluded from the score).')}
                          {reason ? ` — ${reason}` : ''}
                        </Content>
                      ) : (
                        <>
                          <Content component="p">
                            {t('Accept this failing check as risk to exclude it from the score.')}
                          </Content>
                          <TextArea
                            aria-label={t('Waiver reason')}
                            placeholder={t('Reason (optional)')}
                            value={waiveReason}
                            onChange={(_e, v) => setWaiveReason(v)}
                            rows={2}
                          />
                        </>
                      )}
                      {waiveError && (
                        <Alert
                          variant="danger"
                          isInline
                          title={waiveError}
                          style={{ marginTop: 'var(--pf-t--global--spacer--sm)' }}
                        />
                      )}
                    </>
                  );
                })()}
            </>
          )}
        </ModalBody>
        {selected &&
          !['PASS', 'NOT-APPLICABLE'].includes(selected.status) &&
          (() => {
            const waived = isWaived(selected.metadata.name, waivers);
            const idx = waivers?.findIndex((w) => w.name === selected.metadata.name) ?? -1;
            return (
              <ModalFooter>
                {waived ? (
                  <Button
                    variant="secondary"
                    isDisabled={!canWaive || canWaiveLoading || busy}
                    isLoading={busy}
                    onClick={() =>
                      void patchWaivers(
                        removeWaiverPatch(idx, selected.metadata.name),
                        t('Failed to remove waiver.'),
                      )
                    }
                  >
                    {t('Remove waiver')}
                  </Button>
                ) : (
                  <Button
                    variant="primary"
                    isDisabled={!canWaive || canWaiveLoading || busy}
                    isLoading={busy}
                    onClick={() =>
                      void patchWaivers(
                        addWaiverPatch(!!waivers?.length, selected.metadata.name, waiveReason.trim()),
                        t('Failed to waive check.'),
                      )
                    }
                  >
                    {t('Waive check')}
                  </Button>
                )}
                <Button variant="link" isDisabled={busy} onClick={closeModal}>
                  {t('Cancel')}
                </Button>
              </ModalFooter>
            );
          })()}
      </Modal>
    </ListPageBody>
  );
};

export default ResultsTab;
