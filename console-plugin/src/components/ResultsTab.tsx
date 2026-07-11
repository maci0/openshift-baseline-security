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
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Label,
  FormGroup,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  Split,
  SplitItem,
  TextArea,
  TextInput,
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
  checkProfileLabel,
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
  effectiveStatus,
  errorMessage,
  findWaiver,
  inconsistentSources,
  isWaived,
  machineConfigPoolHref,
  nodeScanPool,
  removeWaiverPatch,
  resultFilterStatus,
  resultsCsv,
  waiverExpired,
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
  const [waiveRequestedBy, setWaiveRequestedBy] = React.useState('');
  const [waiveApprovedBy, setWaiveApprovedBy] = React.useState('');
  const [waiveExpiresAt, setWaiveExpiresAt] = React.useState('');
  const [busy, setBusy] = React.useState(false);
  const [waiveError, setWaiveError] = React.useState<string | null>(null);
  const [canWaive, canWaiveLoading] = useAccessReview({
    group: 'baselinesecurity.io',
    resource: 'clusterbaselines',
    verb: 'patch',
  });
  const waivers = baseline?.spec.waivers;
  // Offer the waiver controls for a FAIL (the only score-affecting status), and
  // for any already-waived check so a stale waiver can always be removed even
  // after the check starts passing.
  const showWaiver = (r: ComplianceCheckResult): boolean =>
    isWaived(r.metadata.name, waivers) || (!!baseline && r.status === 'FAIL');

  const closeModal = () => {
    setSelected(null);
    setWaiveReason('');
    setWaiveRequestedBy('');
    setWaiveApprovedBy('');
    setWaiveExpiresAt('');
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
      // The same rule appears in several benchmarks, so a Check title can repeat;
      // the Profile column tells otherwise-identical rows apart.
      { title: t('Profile'), id: 'profile', sort: "metadata.labels['compliance.openshift.io/suite']" },
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
      // Show the effective status: a benign INCONSISTENT (applies on some nodes,
      // not others) reads as PASS/NOT-APPLICABLE, not a scary "Inconsistent".
      const eff = effectiveStatus(obj) as CheckStatus;
      const s = statusLabel[eff] ?? { color: 'grey' as const, icon: <MinusCircleIcon /> };
      return (
        <>
          <TableData id="title" activeColumnIDs={activeColumnIDs}>
            {/* Single-line rows: VirtualizedTable virtualizes with a fixed row
                height; the raw check name lives in the detail modal. */}
            <Button variant="link" isInline onClick={() => setSelected(obj)}>
              {checkTitle(obj)}
            </Button>
          </TableData>
          <TableData id="profile" activeColumnIDs={activeColumnIDs}>
            {checkProfileLabel(obj.metadata.labels)}
          </TableData>
          <TableData id="status" activeColumnIDs={activeColumnIDs}>
            <Label isCompact color={s.color} icon={s.icon}>
              {eff}
            </Label>
            {isWaived(obj.metadata.name, waivers) && (
              <Label isCompact color="grey" style={{ marginLeft: 8 }}>
                {t('Waived')}
              </Label>
            )}
          </TableData>
          <TableData id="severity" activeColumnIDs={activeColumnIDs}>
            {obj.severity}
          </TableData>
        </>
      );
    },
    [setSelected, waivers, t],
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
        // FAIL+waiver -> WAIVED so FAIL chips/deep-links match Overview fail
        // counts (operator excludes waived fails from the Fail bucket).
        reducer: (r) => resultFilterStatus(r, waivers),
        filter: (input, r) =>
          !input.selected?.length ||
          input.selected.includes(resultFilterStatus(r, waivers)),
        items: [
          'PASS',
          'FAIL',
          'WAIVED',
          'MANUAL',
          'ERROR',
          'INFO',
          'INCONSISTENT',
          'SKIP',
          'NOT-APPLICABLE',
        ].map((s) => ({
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
    [t, profileItems, waivers],
  );

  const [data, filteredData, onFilterChange] = useListPageFilter(ownedResults, rowFilters);

  const downloadCsv = React.useCallback(() => {
    // Export the currently filtered rows so the download matches the view.
    // Pass waivers so the waived column matches score exclusions.
    const blob = new Blob([resultsCsv(filteredData, waivers)], {
      type: 'text/csv;charset=utf-8',
    });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'compliance-results.csv';
    a.style.display = 'none';
    document.body.appendChild(a);
    a.click();
    a.remove();
    window.setTimeout(() => URL.revokeObjectURL(url), 0);
  }, [filteredData, waivers]);

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
                  const pool = nodeScanPool(selected);
                  // A real PASS-vs-FAIL split needs review; a PASS/NOT-APPLICABLE
                  // split just means the rule applies to only some nodes.
                  const genuineConflict = effectiveStatus(selected) === 'INCONSISTENT';
                  return (
                    <>
                      <Title headingLevel="h3">{t('Per-node results')}</Title>
                      <Content component="p">
                        {genuineConflict
                          ? t('Nodes disagree on this rule; review each before acting.')
                          : t(
                              'This rule applies to only some nodes. It passes where it applies; the others report not-applicable.',
                            )}
                        {pool && (
                          <>
                            {' '}
                            {t('MachineConfigPool:')}{' '}
                            <a href={machineConfigPoolHref(pool)}>{pool}</a>
                          </>
                        )}
                      </Content>
                      <Table variant="compact" style={{ marginBottom: 'var(--pf-t--global--spacer--md)' }}>
                        <Thead>
                          <Tr>
                            <Th>{t('Node')}</Th>
                            <Th>{t('Status')}</Th>
                          </Tr>
                        </Thead>
                        <Tbody>
                          {sources.map((s, i) => (
                            // Index in the key: hostile data could repeat a node name.
                            <Tr key={`${s.node}-${i}`}>
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
                  Only FAIL affects the score, so waiving is offered for FAIL (and
                  any already-waived check, so a stale waiver stays removable). */}
              {showWaiver(selected) &&
                (() => {
                  const w = findWaiver(selected.metadata.name, waivers);
                  const expired = !!w && waiverExpired(w);
                  return (
                    <>
                      <Title
                        headingLevel="h3"
                        style={{ marginTop: 'var(--pf-t--global--spacer--md)' }}
                      >
                        {t('Waiver')}
                      </Title>
                      {w ? (
                        <>
                          <Content component="p">
                            {expired
                              ? t('This waiver has expired; the check is scored by its status again.')
                              : selected.status === 'FAIL'
                                ? t('This check is waived (excluded from the score).')
                                : t(
                                    'This check is waived, but it currently passes and counts toward the score. Remove the waiver if it is no longer needed.',
                                  )}
                          </Content>
                          <DescriptionList isCompact isHorizontal>
                            {w.reason && (
                              <DescriptionListGroup>
                                <DescriptionListTerm>{t('Reason')}</DescriptionListTerm>
                                <DescriptionListDescription>{w.reason}</DescriptionListDescription>
                              </DescriptionListGroup>
                            )}
                            {w.requestedBy && (
                              <DescriptionListGroup>
                                <DescriptionListTerm>{t('Requested by')}</DescriptionListTerm>
                                <DescriptionListDescription>{w.requestedBy}</DescriptionListDescription>
                              </DescriptionListGroup>
                            )}
                            {w.approvedBy && (
                              <DescriptionListGroup>
                                <DescriptionListTerm>{t('Approved by')}</DescriptionListTerm>
                                <DescriptionListDescription>{w.approvedBy}</DescriptionListDescription>
                              </DescriptionListGroup>
                            )}
                            {w.expiresAt && (
                              <DescriptionListGroup>
                                <DescriptionListTerm>{t('Expires')}</DescriptionListTerm>
                                <DescriptionListDescription>
                                  {new Date(w.expiresAt).toLocaleDateString()}
                                </DescriptionListDescription>
                              </DescriptionListGroup>
                            )}
                          </DescriptionList>
                        </>
                      ) : (
                        <>
                          <Content component="p">
                            {t('Accept this failing check as risk to exclude it from the score.')}
                          </Content>
                          <FormGroup label={t('Reason')} fieldId="waive-reason">
                            <TextArea
                              id="waive-reason"
                              aria-label={t('Waiver reason')}
                              placeholder={t('Reason (optional)')}
                              value={waiveReason}
                              onChange={(_e, v) => setWaiveReason(v)}
                              rows={2}
                            />
                          </FormGroup>
                          <Split hasGutter>
                            <SplitItem isFilled>
                              <FormGroup label={t('Requested by')} fieldId="waive-req">
                                <TextInput
                                  id="waive-req"
                                  value={waiveRequestedBy}
                                  onChange={(_e, v) => setWaiveRequestedBy(v)}
                                />
                              </FormGroup>
                            </SplitItem>
                            <SplitItem isFilled>
                              <FormGroup label={t('Approved by')} fieldId="waive-appr">
                                <TextInput
                                  id="waive-appr"
                                  value={waiveApprovedBy}
                                  onChange={(_e, v) => setWaiveApprovedBy(v)}
                                />
                              </FormGroup>
                            </SplitItem>
                            <SplitItem isFilled>
                              <FormGroup label={t('Expires')} fieldId="waive-exp">
                                <TextInput
                                  id="waive-exp"
                                  type="date"
                                  value={waiveExpiresAt}
                                  onChange={(_e, v) => setWaiveExpiresAt(v)}
                                />
                              </FormGroup>
                            </SplitItem>
                          </Split>
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
          showWaiver(selected) &&
          (() => {
            const waived = isWaived(selected.metadata.name, waivers);
            const idx = waivers?.findIndex((w) => w.name === selected.metadata.name) ?? -1;
            return (
              <ModalFooter>
                {waived ? (
                  <Button
                    variant="secondary"
                    isDisabled={!baseline || !canWaive || canWaiveLoading || busy || idx < 0}
                    isLoading={busy}
                    onClick={() => {
                      if (idx < 0) return;
                      void patchWaivers(
                        removeWaiverPatch(idx, selected.metadata.name),
                        t('Failed to remove waiver.'),
                      );
                    }}
                  >
                    {t('Remove waiver')}
                  </Button>
                ) : (
                  <Button
                    variant="primary"
                    isDisabled={!baseline || !canWaive || canWaiveLoading || busy}
                    isLoading={busy}
                    onClick={() => {
                      const data = addWaiverPatch(waivers, {
                        name: selected.metadata.name,
                        reason: waiveReason.trim(),
                        requestedBy: waiveRequestedBy.trim(),
                        approvedBy: waiveApprovedBy.trim(),
                        expiresAt: waiveExpiresAt
                          ? new Date(waiveExpiresAt).toISOString()
                          : undefined,
                      });
                      if (!data.length) return;
                      void patchWaivers(data, t('Failed to waive check.'));
                    }}
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
