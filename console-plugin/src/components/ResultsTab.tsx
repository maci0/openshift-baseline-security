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
  useListPageFilter,
  VirtualizedTable,
} from '@openshift-console/dynamic-plugin-sdk';
import {
  Alert,
  AlertActionCloseButton,
  Button,
  Content,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Flex,
  FlexItem,
  Label,
  FormGroup,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
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
  ClusterBaseline,
  ClusterBaselineModel,
  ComplianceCheckResult,
  profileTitle,
  suiteFilterKey,
  suiteProfileKey,
  suiteTailoredName,
  WAIVER_MAX_ITEMS,
} from '../models';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';
import { downloadBlob } from '../download';
import { errorMessage } from '../errors';
import { checkResultHref, machineConfigPoolHref } from '../links';
import { addWaiverPatch, removeWaiverPatch, resourceVersionTest } from '../patches';
import {
  checkBody,
  checkTitle,
  nodeScanPool,
  resultsCsv,
  severityDisplayTitle,
} from '../results';
import { checkSeverity } from '../scoring';
import { effectiveStatus, inconsistentSources } from '../status';
import {
  dateInputEndOfDayIso,
  formatCount,
  formatLocalDate,
  localDateInputValue,
  safeLocale,
} from '../dates';
import {
  activeWaivedNames,
  findWaiver,
  waiverExpired,
} from '../waivers';
import { withDisabledTip } from './DisabledTip';

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

// Stable empty list for optional results prop (avoids new [] each render).
const EMPTY_RESULTS: ComplianceCheckResult[] = [];

// Filter ids and CR status values stay English enums; only the visible title is
// localized so chips, labels, and deep-links keep matching.
const statusDisplayTitle = (status: string, t: (k: string) => string): string => {
  switch (status) {
    case 'PASS':
      return t('Pass');
    case 'FAIL':
      return t('Fail');
    case 'ERROR':
      return t('Error');
    case 'MANUAL':
      return t('Manual');
    case 'INFO':
      return t('Info');
    case 'INCONSISTENT':
      return t('Inconsistent');
    case 'SKIP':
    case 'NOT-APPLICABLE':
      return t('Not applicable');
    case 'WAIVED':
      return t('Waived');
    default:
      return status;
  }
};

const ResultsTab: React.FC<{
  baseline?: ClusterBaseline;
  // Shared list from CompliancePage so this tab does not open a second watch.
  results?: ComplianceCheckResult[];
  resultsLoaded?: boolean;
  resultsError?: unknown;
}> = ({ baseline, results, resultsLoaded = false, resultsError }) => {
  const { t, i18n } = useTranslation('plugin__baseline-security-console-plugin');
  const loaded = resultsLoaded;
  const [selected, setSelected] = React.useState<ComplianceCheckResult | null>(null);
  const [waiveReason, setWaiveReason] = React.useState('');
  const [waiveRequestedBy, setWaiveRequestedBy] = React.useState('');
  const [waiveApprovedBy, setWaiveApprovedBy] = React.useState('');
  const [waiveExpiresAt, setWaiveExpiresAt] = React.useState('');
  const [waiveReviewBy, setWaiveReviewBy] = React.useState('');
  const [busy, setBusy] = React.useState(false);
  // Sync guard: React state alone cannot block a second click before re-render.
  const busyRef = React.useRef(false);
  const [waiveError, setWaiveError] = React.useState<string | null>(null);
  // Page-level (not modal-only): CSV export failures must surface outside the detail modal.
  const [exportError, setExportError] = React.useState<string | null>(null);
  // Success feedback after the detail modal closes so waive/unwaive is not a silent no-op.
  const [waiveSuccess, setWaiveSuccess] = React.useState<string | null>(null);
  const [canWaive, canWaiveLoading] = useAccessReview({
    group: 'baselinesecurity.io',
    resource: 'clusterbaselines',
    verb: 'patch',
  });
  const waivers = baseline?.spec.waivers;
  // Active (non-expired) waiver names as a Set so row filters and cells are O(1)
  // per check instead of scanning the waiver list on every result.
  const activeWaived = React.useMemo(() => activeWaivedNames(waivers), [waivers]);
  // Offer the waiver controls for a FAIL (the only score-affecting status), and
  // for any already-waived check so a stale waiver can always be removed even
  // after the check starts passing.
  const showWaiver = (r: ComplianceCheckResult): boolean =>
    !!findWaiver(r.metadata.name, waivers) || (!!baseline && effectiveStatus(r) === 'FAIL');

  const resetWaiverForm = () => {
    setSelected(null);
    setWaiveReason('');
    setWaiveRequestedBy('');
    setWaiveApprovedBy('');
    setWaiveExpiresAt('');
    setWaiveReviewBy('');
    setWaiveError(null);
  };

  // User dismiss (Escape/X/Cancel): block while a patch is in flight so form
  // state and the error context are not wiped mid-request. Use the ref, not
  // React state: setBusy is async and a dismiss between busyRef=true and the
  // re-render would still see busy===false.
  const closeModal = () => {
    if (busyRef.current) return;
    resetWaiverForm();
  };

  const patchWaivers = async (
    data: Parameters<typeof k8sPatch>[0]['data'],
    failMsg: string,
    successMsg: string,
  ): Promise<void> => {
    if (!baseline || busyRef.current) return;
    busyRef.current = true;
    setBusy(true);
    setWaiveError(null);
    try {
      await k8sPatch({
        model: ClusterBaselineModel,
        resource: baseline,
        data: [...resourceVersionTest(baseline.metadata.resourceVersion), ...data],
      });
      // Success path bypasses the busy guard on closeModal.
      resetWaiverForm();
      setWaiveSuccess(successMsg);
    } catch (e) {
      setWaiveError(errorMessage(e) ?? failMsg);
    } finally {
      busyRef.current = false;
      setBusy(false);
    }
  };

  const waiveDisabled = !baseline || !canWaive || canWaiveLoading || busy;
  let waiveDisabledReason: string | undefined;
  if (!busy) {
    if (canWaiveLoading) {
      waiveDisabledReason = t('Checking permissions…');
    } else if (!canWaive) {
      waiveDisabledReason = t('You do not have permission to waive checks.');
    } else if (!baseline) {
      waiveDisabledReason = t('Baseline not configured');
    }
  }

  const profiles = baseline?.spec.profiles;
  const tailored = baseline?.spec.tailoredProfiles;
  // CompliancePage already suite-selects / ownership-filters this list; do not
  // re-scan thousands of CCRs on every Results render.
  const ownedResults = results ?? EMPTY_RESULTS;

  // Prefer the latest watched object for the open detail modal so status,
  // severity, and waiver state stay current while the dialog is open.
  // Index by name once when the modal is open (avoids O(n) find per update).
  const selectedLive = React.useMemo(() => {
    if (!selected) return null;
    const want = selected.metadata.name;
    for (const r of ownedResults) {
      if (r.metadata.name === want) {
        return r;
      }
    }
    return selected;
  }, [ownedResults, selected]);

  // FAIL+active-waiver -> WAIVED so FAIL chips/deep-links match Overview fail
  // counts (operator excludes waived fails from the Fail bucket). Uses the
  // activeWaived Set (built once) instead of scanning waivers per row.
  // Defined before columns so status sort uses the same key as filters.
  const rowFilterStatus = React.useCallback(
    (r: ComplianceCheckResult): string => {
      const eff = effectiveStatus(r);
      return eff === 'FAIL' && activeWaived.has(r.metadata.name) ? 'WAIVED' : eff;
    },
    [activeWaived],
  );

  // Sort by the same values the cells show (and filters use), not raw CR fields.
  // Raw `status` leaves benign INCONSISTENT / waived FAIL out of visual order;
  // raw `severity` ignores the check-severity label fallback; raw `description`
  // puts empty-description rows under "" while the cell shows the check name.
  const sortByString = React.useCallback(
    (keyOf: (r: ComplianceCheckResult) => string) =>
      (data: ComplianceCheckResult[], sortDirection: string): ComplianceCheckResult[] => {
        const mul = sortDirection === 'desc' ? -1 : 1;
        // Match profile-chip sort: console locale, never throw on a bad i18n tag.
        const locale = safeLocale(i18n.language);
        return [...data].sort((a, b) => mul * keyOf(a).localeCompare(keyOf(b), locale));
      },
    [i18n.language],
  );

  const columns: TableColumn<ComplianceCheckResult>[] = React.useMemo(
    () => [
      { title: t('Check'), id: 'title', sort: sortByString(checkTitle) },
      // The same rule appears in several benchmarks, so a Check title can repeat;
      // the Profile column tells otherwise-identical rows apart.
      {
        title: t('Profile'),
        id: 'profile',
        sort: sortByString((r) => suiteFilterKey(r.metadata.labels) ?? ''),
      },
      { title: t('Status'), id: 'status', sort: sortByString(rowFilterStatus) },
      { title: t('Severity'), id: 'severity', sort: sortByString(checkSeverity) },
    ],
    [t, sortByString, rowFilterStatus],
  );

  const Row = React.useCallback(
    ({ obj, activeColumnIDs }: RowProps<ComplianceCheckResult>) => {
      // Same key as filters/sort: FAIL+active-waiver is WAIVED (not red FAIL) so
      // the table matches Overview waived/fail counts and WAIVED chip deep-links.
      // Benign INCONSISTENT collapses via effectiveStatus inside rowFilterStatus.
      const status = rowFilterStatus(obj);
      const s =
        status === 'WAIVED'
          ? { color: 'grey' as const, icon: <MinusCircleIcon /> }
          : (statusLabel[status as CheckStatus] ?? {
              color: 'grey' as const,
              icon: <MinusCircleIcon />,
            });
      // Title once: aria-label and visible text (avoids dual description scans).
      const title = checkTitle(obj);
      // Field + check-severity label; empty normalizes to "unknown".
      const sev = checkSeverity(obj);
      return (
        <>
          <TableData id="title" activeColumnIDs={activeColumnIDs}>
            {/* Single-line rows: VirtualizedTable virtualizes with a fixed row
                height; the raw check name lives in the detail modal. */}
            <Button
              variant="link"
              isInline
              aria-label={t('View details for {{title}}', { title })}
              onClick={() => {
                setWaiveSuccess(null);
                setSelected(obj);
              }}
            >
              {title}
            </Button>
          </TableData>
          <TableData id="profile" activeColumnIDs={activeColumnIDs}>
            {(() => {
              const tailored = suiteTailoredName(obj.metadata.labels);
              if (tailored !== undefined) {
                return tailored;
              }
              const key = suiteProfileKey(obj.metadata.labels);
              return key ? t(profileTitle(key)) : '—';
            })()}
          </TableData>
          <TableData id="status" activeColumnIDs={activeColumnIDs}>
            <Label isCompact color={s.color} icon={s.icon}>
              {statusDisplayTitle(status, t)}
            </Label>
            {/* Stale waiver on a non-FAIL (e.g. self-healed PASS): keep a badge
                so the waiver can still be found; FAIL+waiver is already WAIVED. */}
            {status !== 'WAIVED' && activeWaived.has(obj.metadata.name) && (
              <Label isCompact color="grey" style={{ marginInlineStart: 8 }}>
                {t('Waived')}
              </Label>
            )}
          </TableData>
          <TableData id="severity" activeColumnIDs={activeColumnIDs}>
            {severityDisplayTitle(sev, t)}
          </TableData>
        </>
      );
    },
    [setSelected, activeWaived, t, rowFilterStatus],
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
    // show tailored profiles by their clean name and built-ins by localized title.
    // Sort by display title (console locale) so chip order matches what users read,
    // not the English-ish profile key / tp- prefix.
    return [...keys]
      .map((k) => ({
        id: k,
        title: k.startsWith('tp-') ? k.slice(3) : t(profileTitle(k)),
      }))
      .sort((a, b) => a.title.localeCompare(b.title, safeLocale(i18n.language)));
  }, [profiles, tailored, ownedResults, i18n, t]);

  const rowFilters: RowFilter<ComplianceCheckResult>[] = React.useMemo(
    () => [
      {
        filterGroupName: t('Status'),
        type: 'result-status',
        reducer: rowFilterStatus,
        // selected is small (chip count); still avoid re-running rowFilterStatus
        // when no chip is active (common default: show all).
        filter: (input, r) => {
          const sel = input.selected;
          return !sel?.length || sel.includes(rowFilterStatus(r));
        },
        // SKIP is folded into NOT-APPLICABLE by effectiveStatus (operator
        // ResultCounts.notApplicable); a separate SKIP chip would never match.
        items: [
          'PASS',
          'FAIL',
          'WAIVED',
          'MANUAL',
          'ERROR',
          'INFO',
          'INCONSISTENT',
          'NOT-APPLICABLE',
        ].map((s) => ({
          id: s,
          title: statusDisplayTitle(s, t),
        })),
      },
      {
        filterGroupName: t('Severity'),
        type: 'result-severity',
        reducer: (r) => checkSeverity(r),
        filter: (input, r) => {
          const sel = input.selected;
          return !sel?.length || sel.includes(checkSeverity(r));
        },
        items: ['high', 'medium', 'low', 'info', 'unknown'].map((s) => ({
          id: s,
          title: severityDisplayTitle(s, t),
        })),
      },
      {
        filterGroupName: t('Profiles'),
        type: 'result-profile',
        reducer: (r) => suiteFilterKey(r.metadata.labels) ?? '',
        filter: (input, r) => {
          const sel = input.selected;
          if (!sel?.length) {
            return true;
          }
          // Parse suite once per filtered row when chips are active.
          return sel.includes(suiteFilterKey(r.metadata.labels) ?? '');
        },
        items: profileItems,
      },
    ],
    [t, profileItems, rowFilterStatus],
  );

  const [data, filteredData, onFilterChange] = useListPageFilter(ownedResults, rowFilters);

  const exportCsvDisabled = !loaded || filteredData.length === 0;
  let exportCsvDisabledReason: string | undefined;
  if (!loaded) {
    exportCsvDisabledReason = t('Waiting for check results to load.');
  } else if (filteredData.length === 0) {
    exportCsvDisabledReason = t('No results to export.');
  }

  const downloadCsv = React.useCallback(() => {
    // Export the currently filtered rows so the download matches the view.
    // Pass waivers so the waived column matches score exclusions.
    setExportError(null);
    try {
      const blob = new Blob([resultsCsv(filteredData, waivers)], {
        type: 'text/csv;charset=utf-8',
      });
      downloadBlob(blob, 'compliance-results.csv');
    } catch (e) {
      // DOM / serialization failures must not look like a silent no-op click.
      setExportError(errorMessage(e) ?? t('Failed to export results CSV.'));
    }
  }, [filteredData, waivers, t]);

  return (
    <ListPageBody>
      {waiveSuccess && (
        <Alert
          variant="success"
          isInline
          isLiveRegion
          title={waiveSuccess}
          style={{ marginBottom: 'var(--pf-t--global--spacer--md)' }}
          actionClose={
            <AlertActionCloseButton
              aria-label={t('Close')}
              onClose={() => setWaiveSuccess(null)}
            />
          }
        />
      )}
      {exportError && (
        <Alert
          variant="danger"
          isInline
          isLiveRegion
          title={exportError}
          style={{ marginBottom: 'var(--pf-t--global--spacer--md)' }}
          actionClose={
            <AlertActionCloseButton
              aria-label={t('Close')}
              onClose={() => setExportError(null)}
            />
          }
        />
      )}
      <Flex
        justifyContent={{ default: 'justifyContentSpaceBetween' }}
        alignItems={{ default: 'alignItemsFlexStart' }}
        flexWrap={{ default: 'wrap' }}
        gap={{ default: 'gapMd' }}
        style={{ marginBottom: 'var(--pf-t--global--spacer--md)' }}
      >
        <FlexItem grow={{ default: 'grow' }} style={{ minWidth: 200 }}>
          <ListPageFilter
            data={data}
            loaded={loaded}
            rowFilters={rowFilters}
            onFilterChange={onFilterChange}
          />
        </FlexItem>
        <FlexItem>
          {withDisabledTip(
            exportCsvDisabled ? exportCsvDisabledReason : undefined,
            <Button
              variant="secondary"
              icon={<DownloadIcon />}
              isDisabled={exportCsvDisabled}
              onClick={downloadCsv}
            >
              {t('Export CSV')}
            </Button>,
          )}
        </FlexItem>
      </Flex>
      <VirtualizedTable<ComplianceCheckResult>
        data={filteredData}
        unfilteredData={data}
        loaded={loaded}
        loadError={resultsError}
        columns={columns}
        Row={Row}
      />
      <Modal
        variant="medium"
        isOpen={!!selectedLive}
        onClose={closeModal}
        aria-labelledby="check-detail-title"
      >
        <ModalHeader
          title={selectedLive ? checkTitle(selectedLive) : ''}
          labelId="check-detail-title"
          description={selectedLive?.metadata.name}
        />
        <ModalBody>
          {selectedLive && (
            <>
              {/* Status / severity / profile: table cells are covered by the
                  modal; surface them here so the detail view is self-contained. */}
              {(() => {
                // Same status key as the table / filters (FAIL+waiver => WAIVED).
                const status = rowFilterStatus(selectedLive);
                const s =
                  status === 'WAIVED'
                    ? { color: 'grey' as const, icon: <MinusCircleIcon /> }
                    : (statusLabel[status as CheckStatus] ?? {
                        color: 'grey' as const,
                        icon: <MinusCircleIcon />,
                      });
                const sev = checkSeverity(selectedLive);
                const tailoredName = suiteTailoredName(selectedLive.metadata.labels);
                const profileKey = suiteProfileKey(selectedLive.metadata.labels);
                const profileText = tailoredName
                  ? tailoredName
                  : profileKey
                    ? t(profileTitle(profileKey))
                    : '—';
                return (
                  <Flex
                    gap={{ default: 'gapSm' }}
                    alignItems={{ default: 'alignItemsCenter' }}
                    style={{ marginBottom: 'var(--pf-t--global--spacer--md)' }}
                    flexWrap={{ default: 'wrap' }}
                  >
                    <FlexItem>
                      <Label isCompact color={s.color} icon={s.icon}>
                        {statusDisplayTitle(status, t)}
                      </Label>
                    </FlexItem>
                    {status !== 'WAIVED' && activeWaived.has(selectedLive.metadata.name) && (
                      <FlexItem>
                        <Label isCompact color="grey">
                          {t('Waived')}
                        </Label>
                      </FlexItem>
                    )}
                    <FlexItem>
                      <Label isCompact color="grey">
                        {severityDisplayTitle(sev, t)}
                      </Label>
                    </FlexItem>
                    <FlexItem>
                      <Label isCompact color="blue">
                        {profileText}
                      </Label>
                    </FlexItem>
                  </Flex>
                );
              })()}
              <Content component="p" style={{ whiteSpace: 'pre-wrap' }}>
                {checkBody(selectedLive) || t('No description provided.')}
              </Content>
              {selectedLive.status === 'INCONSISTENT' &&
                (() => {
                  const { sources, mostCommon } = inconsistentSources(selectedLive);
                  const pool = nodeScanPool(selectedLive);
                  // A real PASS-vs-FAIL split needs review; a PASS/NOT-APPLICABLE
                  // split just means the rule applies to only some nodes.
                  const genuineConflict = effectiveStatus(selectedLive) === 'INCONSISTENT';
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
                          {sources.map((s, i) => {
                            // Uppercase for label/title tables (CO is usually already
                            // uppercased; normalize so hostile/odd data still maps).
                            const st = s.status.toUpperCase() as CheckStatus;
                            // Index in the key: hostile data could repeat a node name.
                            return (
                              <Tr key={`${s.node}-${i}`}>
                                <Td>{s.node}</Td>
                                <Td>
                                  <Label isCompact color={statusLabel[st]?.color ?? 'grey'}>
                                    {s.status ? statusDisplayTitle(st, t) : '—'}
                                  </Label>
                                </Td>
                              </Tr>
                            );
                          })}
                          {mostCommon && (
                            <Tr>
                              <Td>{t('all other nodes')}</Td>
                              <Td>
                                <Label
                                  isCompact
                                  color={
                                    statusLabel[mostCommon.toUpperCase() as CheckStatus]?.color ??
                                    'grey'
                                  }
                                >
                                  {statusDisplayTitle(mostCommon.toUpperCase(), t)}
                                </Label>
                              </Td>
                            </Tr>
                          )}
                        </Tbody>
                      </Table>
                    </>
                  );
                })()}
              {selectedLive.instructions && (
                <>
                  <Title headingLevel="h3">{t('How to verify')}</Title>
                  <Content component="pre" style={{ whiteSpace: 'pre-wrap' }}>
                    {selectedLive.instructions}
                  </Content>
                </>
              )}
              <Content component="p" style={{ marginTop: 'var(--pf-t--global--spacer--md)' }}>
                <a href={checkResultHref(selectedLive.metadata.name)}>
                  {t('View full check details in OpenShift')}
                </a>
              </Content>
              {/* Waivers: accept a failing check as risk so it leaves the score.
                  Only FAIL affects the score, so waiving is offered for FAIL (and
                  any already-waived check, so a stale waiver stays removable). */}
              {showWaiver(selectedLive) &&
                (() => {
                  const w = findWaiver(selectedLive.metadata.name, waivers);
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
                              : // Waivers only exclude FAIL from the score (operator tally).
                                // Use effective status so a collapsed INCONSISTENT matches.
                                effectiveStatus(selectedLive) === 'FAIL'
                                ? t('This check is waived (excluded from the score).')
                                : t(
                                    'This check is waived, but it is not currently failing, so it is not excluded from the score. Remove the waiver if it is no longer needed.',
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
                                  {formatLocalDate(w.expiresAt, i18n.language)}
                                </DescriptionListDescription>
                              </DescriptionListGroup>
                            )}
                            {w.reviewBy && (
                              <DescriptionListGroup>
                                <DescriptionListTerm>{t('Review by')}</DescriptionListTerm>
                                <DescriptionListDescription>
                                  {formatLocalDate(w.reviewBy, i18n.language)}
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
                          <FormGroup label={t('Reason (optional)')} fieldId="waive-reason">
                            <TextArea
                              id="waive-reason"
                              aria-label={t('Waiver reason')}
                              value={waiveReason}
                              onChange={(_e, v) => setWaiveReason(v)}
                              // Match ClusterBaseline CRD waiver field MaxLength.
                              maxLength={1024}
                              rows={2}
                            />
                          </FormGroup>
                          {/* Wrap on narrow viewports so four fields do not squash. */}
                          <Flex
                            gap={{ default: 'gapMd' }}
                            flexWrap={{ default: 'wrap' }}
                            style={{ marginTop: 'var(--pf-t--global--spacer--sm)' }}
                          >
                            <FlexItem flex={{ default: 'flex_1' }} style={{ minWidth: 140 }}>
                              <FormGroup label={t('Requested by (optional)')} fieldId="waive-req">
                                <TextInput
                                  id="waive-req"
                                  value={waiveRequestedBy}
                                  onChange={(_e, v) => setWaiveRequestedBy(v)}
                                  maxLength={253}
                                  autoComplete="name"
                                />
                              </FormGroup>
                            </FlexItem>
                            <FlexItem flex={{ default: 'flex_1' }} style={{ minWidth: 140 }}>
                              <FormGroup label={t('Approved by (optional)')} fieldId="waive-appr">
                                <TextInput
                                  id="waive-appr"
                                  value={waiveApprovedBy}
                                  onChange={(_e, v) => setWaiveApprovedBy(v)}
                                  maxLength={253}
                                  autoComplete="name"
                                />
                              </FormGroup>
                            </FlexItem>
                            <FlexItem flex={{ default: 'flex_1' }} style={{ minWidth: 140 }}>
                              <FormGroup label={t('Expires (optional)')} fieldId="waive-exp">
                                <TextInput
                                  id="waive-exp"
                                  type="date"
                                  // Past expiry creates an immediately expired waiver.
                                  // Local calendar day (not UTC) so min matches the date picker.
                                  min={localDateInputValue()}
                                  value={waiveExpiresAt}
                                  onChange={(_e, v) => setWaiveExpiresAt(v)}
                                  aria-label={t('Expires (optional)')}
                                />
                              </FormGroup>
                            </FlexItem>
                            <FlexItem flex={{ default: 'flex_1' }} style={{ minWidth: 140 }}>
                              <FormGroup label={t('Review by (optional)')} fieldId="waive-review">
                                <TextInput
                                  id="waive-review"
                                  type="date"
                                  // A review deadline in the past is not schedulable; match Expires.
                                  min={localDateInputValue()}
                                  value={waiveReviewBy}
                                  onChange={(_e, v) => setWaiveReviewBy(v)}
                                  aria-label={t('Review by (optional)')}
                                />
                              </FormGroup>
                            </FlexItem>
                          </Flex>
                        </>
                      )}
                      {waiveError && (
                        <Alert
                          variant="danger"
                          isInline
                          isLiveRegion
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
        {selectedLive && (
          <ModalFooter>
            {showWaiver(selectedLive) &&
              (() => {
                const idx = waivers?.findIndex((w) => w.name === selectedLive.metadata.name) ?? -1;
                const storedWaiver = idx >= 0;
                return storedWaiver
                  ? withDisabledTip(
                      waiveDisabled && waiveDisabledReason ? waiveDisabledReason : undefined,
                      <Button
                        variant="secondary"
                        isDisabled={waiveDisabled}
                        isLoading={busy}
                        onClick={() => {
                          void patchWaivers(
                            removeWaiverPatch(idx, selectedLive.metadata.name),
                            t('Failed to remove waiver.'),
                            t('Waiver removed. The check counts toward the score again.'),
                          );
                        }}
                      >
                        {t('Remove waiver')}
                      </Button>,
                    )
                  : withDisabledTip(
                      waiveDisabled && waiveDisabledReason ? waiveDisabledReason : undefined,
                      <Button
                        variant="primary"
                        isDisabled={waiveDisabled}
                        isLoading={busy}
                        onClick={() => {
                          // MaxItems is a different failure mode from field
                          // validation; do not conflate them into one message.
                          if ((waivers?.length ?? 0) >= WAIVER_MAX_ITEMS) {
                            setWaiveError(
                              t(
                                'Maximum of {{max}} waivers reached. Remove one before adding another.',
                                {
                                  max: WAIVER_MAX_ITEMS,
                                  formattedMax: formatCount(WAIVER_MAX_ITEMS, i18n.language),
                                },
                              ),
                            );
                            return;
                          }
                          const data = addWaiverPatch(waivers, {
                            name: selectedLive.metadata.name,
                            reason: waiveReason.trim(),
                            requestedBy: waiveRequestedBy.trim(),
                            approvedBy: waiveApprovedBy.trim(),
                            expiresAt: dateInputEndOfDayIso(waiveExpiresAt),
                            reviewBy: dateInputEndOfDayIso(waiveReviewBy),
                          });
                          // Empty patch is client-side MaxLength/name validation:
                          // surface it so over-long fields are not a silent no-op.
                          if (!data.length) {
                            setWaiveError(
                              t(
                                'Waiver fields are invalid or exceed length limits (name 253, reason 1024, attribution 253).',
                              ),
                            );
                            return;
                          }
                          void patchWaivers(
                            data,
                            t('Failed to waive check.'),
                            t('Check waived. It is excluded from the score.'),
                          );
                        }}
                      >
                        {t('Waive check')}
                      </Button>,
                    );
              })()}
            <Button variant="link" isDisabled={busy} onClick={closeModal}>
              {showWaiver(selectedLive) ? t('Cancel') : t('Close')}
            </Button>
          </ModalFooter>
        )}
      </Modal>
    </ListPageBody>
  );
};

export default ResultsTab;
