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
  EmptyState,
  EmptyStateBody,
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
  checkProfileLabel,
  ClusterBaseline,
  ClusterBaselineModel,
  ComplianceCheckResult,
  suiteFilterKey,
  suiteFilterKeyTitle,
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
import { effectiveStatus, inconsistentSources, resultFilterStatus } from '../status';
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
  futureWaiverDeadlineMs,
  soonestDeadlineDelayMs,
  waiverExpired,
} from '../waivers';
import BaselineNotConfigured from './BaselineNotConfigured';
import { withDisabledTip } from './DisabledTip';
import { restoreFocus } from './focus';
import { SUCCESS_DISMISS_MS } from './feedback';

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

// Style for a row-filter status. WAIVED (not a CheckStatus key) and any unknown
// status fall through to the grey/minus default.
const statusStyle = (status: string) =>
  statusLabel[status as CheckStatus] ?? { color: 'grey' as const, icon: <MinusCircleIcon /> };

// Stable empty list for optional results prop (avoids new [] each render).
const EMPTY_RESULTS: ComplianceCheckResult[] = [];

// A single-value chip filter: no chips -> show all; one chip -> === (lets
// multi-thousand-row deep-links skip Array.includes); many -> includes. getValue
// derives the row's value once per filtered row. Shared by the status, severity,
// and profile facets so their behavior cannot drift.
const chipFilter =
  (getValue: (r: ComplianceCheckResult) => string) =>
  (input: { selected?: string[] }, r: ComplianceCheckResult): boolean => {
    const sel = input.selected;
    if (!sel?.length) {
      return true;
    }
    const v = getValue(r);
    return sel.length === 1 ? v === sel[0] : sel.includes(v);
  };

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
}> = ({ baseline, results, resultsLoaded: loaded = false, resultsError }) => {
  const { t, i18n } = useTranslation('plugin__baseline-security-console-plugin');
  const [selected, setSelected] = React.useState<ComplianceCheckResult | null>(null);
  const [waiveReason, setWaiveReason] = React.useState('');
  const [waiveRequestedBy, setWaiveRequestedBy] = React.useState('');
  const [waiveApprovedBy, setWaiveApprovedBy] = React.useState('');
  const [waiveExpiresAt, setWaiveExpiresAt] = React.useState('');
  const [waiveReviewBy, setWaiveReviewBy] = React.useState('');
  const [busy, setBusy] = React.useState(false);
  // Sync guard: React state alone cannot block a second click before re-render.
  const busyRef = React.useRef(false);
  // Return focus to the row control that opened the detail modal (WCAG 2.4.3).
  const returnFocusRef = React.useRef<HTMLElement | null>(null);
  // Sentinel focus target for when the trigger row was virtualized out of the DOM
  // while the modal was open (VirtualizedTable), so focus never drops to <body>.
  const regionRef = React.useRef<HTMLDivElement>(null);
  const detailWasOpen = React.useRef(false);
  const [waiveError, setWaiveError] = React.useState<string | null>(null);
  // Page-level (not modal-only): CSV export failures must surface outside the detail modal.
  const [exportError, setExportError] = React.useState<string | null>(null);
  // Success feedback after the detail modal closes so waive/unwaive is not a silent no-op.
  const [waiveSuccess, setWaiveSuccess] = React.useState<string | null>(null);
  // Auto-dismiss success so the banner does not stick after the user moves on.
  React.useEffect(() => {
    if (!waiveSuccess) return;
    const id = window.setTimeout(() => setWaiveSuccess(null), SUCCESS_DISMISS_MS);
    return () => window.clearTimeout(id);
  }, [waiveSuccess]);
  const [canWaive, canWaiveLoading] = useAccessReview({
    group: 'baselinesecurity.openshift.io',
    resource: 'clusterbaselines',
    verb: 'patch',
  });
  const waivers = baseline?.spec.waivers;
  // Content key: status-only CR updates reallocate the waivers array with the
  // same membership; identity deps would rebuild the Set (and rowFilters) on
  // every reconcile even when score exclusions did not change.
  const waiversKey = (waivers ?? [])
    .map((w) => `${w.name ?? ''}\0${w.expiresAt ?? ''}`)
    .join('\x01');
  // Active waivers are time-sensitive: membership alone is not enough. A waiver
  // can expire with no CR edit, and operator status-only updates do not change
  // waiversKey. Without a clock tick at the soonest expiry, Results would keep
  // showing WAIVED (and hide the row from FAIL chips/deep-links) after the
  // operator has already returned the check to the Fail bucket.
  const [waiverClock, setWaiverClock] = React.useState(0);
  React.useEffect(() => {
    const now = Date.now();
    const delay = soonestDeadlineDelayMs(now, futureWaiverDeadlineMs(waivers, now));
    if (delay === 0) {
      return;
    }
    const id = window.setTimeout(() => setWaiverClock((c) => c + 1), delay);
    return () => window.clearTimeout(id);
    // waivers read when key or clock changes (content-stable + expiry).
    // eslint-disable-next-line react-hooks/exhaustive-deps -- content key
  }, [waiversKey, waiverClock]);
  // Active (non-expired) waiver names as a Set so row filters and cells are O(1)
  // per check instead of scanning the waiver list on every result.
  const activeWaived = React.useMemo(
    () => activeWaivedNames(waivers),
    // waivers read when key or expiry clock changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps -- content key + clock
    [waiversKey, waiverClock],
  );
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
    // Empty mutation ops: a bare resourceVersion test would succeed without
    // changing waivers and look like a real add/remove. Callers should already
    // refuse empty patches; guard here so success is never a silent no-op.
    if (!data.length) {
      setWaiveError(failMsg);
      return;
    }
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

  // Gate the waiver add-form/button on the status captured when the modal OPENED
  // (the snapshot `selected`), not the live object. Otherwise a check that
  // self-heals FAIL -> PASS while an admin is typing a reason would flip
  // showWaiver false and unmount the form, silently discarding the typed input.
  // showWaiver still reads live `waivers`, so removing an existing waiver stays
  // available even after the check starts passing.
  const showWaiverForm = !!selected && showWaiver(selected);

  // Restore focus to the check-title control when the detail modal closes.
  React.useEffect(() => {
    if (selectedLive) {
      detailWasOpen.current = true;
      return;
    }
    if (!detailWasOpen.current) return;
    detailWasOpen.current = false;
    const el = returnFocusRef.current;
    returnFocusRef.current = null;
    // Defer until the modal unmounts so focus is not stolen by the backdrop; if
    // the row was virtualized away while the modal was open, the trigger is
    // detached and restoreFocus falls back to the region sentinel rather than
    // dropping focus to <body>.
    restoreFocus(el, regionRef);
  }, [selectedLive]);

  // Named event handlers (not inline IIFE onClick) so react-hooks/refs does not
  // treat the busyRef read inside patchWaivers as render-time access. Defined
  // after selectedLive so the compiler can preserve that memoization.
  const removeSelectedWaiver = () => {
    if (!selectedLive) return;
    const idx = waivers?.findIndex((w) => w.name === selectedLive.metadata.name) ?? -1;
    if (idx < 0) return;
    const data = removeWaiverPatch(idx, selectedLive.metadata.name);
    // Empty patch (invalid index / empty name): surface failure instead of an
    // RV-only patch that would report success without removing anything.
    if (!data.length) {
      setWaiveError(t('Failed to remove waiver.'));
      return;
    }
    void patchWaivers(
      data,
      t('Failed to remove waiver.'),
      t('Waiver removed. The check counts toward the score again.'),
    );
  };

  // Waivers whose check has no current result (its rule was removed or its
  // profile unbound): they still exclude from scoring but have no table row, so
  // there is otherwise no way to remove them but hand-editing the CR. Only when
  // results are loaded and not errored, so a loading/failed list does not flag
  // every waiver as orphaned.
  const orphanWaivers = React.useMemo(() => {
    if (!loaded || resultsError || !waivers?.length) {
      return [] as { name: string; index: number }[];
    }
    const names = new Set((results ?? []).map((r) => r.metadata?.name).filter(Boolean));
    return waivers.flatMap((w, index) =>
      w.name && !names.has(w.name) ? [{ name: w.name, index }] : [],
    );
    // waivers read via waiversKey (content-stable); results identity is stable per page.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loaded, resultsError, waiversKey, results]);

  const removeWaiverByIndex = (index: number, name: string) => {
    const data = removeWaiverPatch(index, name);
    if (!data.length) {
      setWaiveError(t('Failed to remove waiver.'));
      return;
    }
    void patchWaivers(
      data,
      t('Failed to remove waiver.'),
      t('Waiver removed. The check counts toward the score again.'),
    );
  };

  // Rendered in both the main list and the empty-results early return, so that
  // when every check is gone (the case where orphan waivers matter most) the
  // removal affordance is still reachable.
  const orphanWaiverAlert =
    canWaive && orphanWaivers.length > 0 ? (
      <Alert
        variant="warning"
        isInline
        title={t('Waivers referencing a removed check ({{formattedCount}})', {
          formattedCount: formatCount(orphanWaivers.length, i18n.language),
        })}
        style={{ marginBottom: 'var(--pf-t--global--spacer--md)' }}
      >
        <p>
          {t(
            'The rule was removed or its profile unbound, so these waivers no longer match a result but still exclude it from scoring. Remove any that are no longer needed.',
          )}
        </p>
        <Flex
          gap={{ default: 'gapSm' }}
          flexWrap={{ default: 'wrap' }}
          style={{ marginTop: 'var(--pf-t--global--spacer--sm)' }}
        >
          {orphanWaivers.map(({ name, index }) => (
            <FlexItem key={name}>
              <Button
                variant="secondary"
                isDisabled={busy}
                onClick={() => removeWaiverByIndex(index, name)}
              >
                {t('Remove waiver for {{name}}', { name })}
              </Button>
            </FlexItem>
          ))}
        </Flex>
        {/* This flow runs with no detail modal open, where waiveError's other
            render site lives; without this a failed removal is silent. */}
        {waiveError && !selected && (
          <Alert
            variant="danger"
            isInline
            title={waiveError}
            style={{ marginTop: 'var(--pf-t--global--spacer--sm)' }}
          />
        )}
      </Alert>
    ) : null;

  const addSelectedWaiver = () => {
    if (!selectedLive) return;
    // MaxItems is a different failure mode from field validation; do not
    // conflate them into one message.
    if ((waivers?.length ?? 0) >= WAIVER_MAX_ITEMS) {
      setWaiveError(
        t('Maximum of {{max}} waivers reached. Remove one before adding another.', {
          max: WAIVER_MAX_ITEMS,
          formattedMax: formatCount(WAIVER_MAX_ITEMS, i18n.language),
        }),
      );
      return;
    }
    // Non-empty but unparseable dates must fail closed: dateInputEndOfDayIso
    // returns undefined, which would omit expiresAt/reviewBy and create a
    // permanent waiver when the admin thought they set an expiry.
    const expiresRaw = waiveExpiresAt.trim();
    const reviewRaw = waiveReviewBy.trim();
    const expiresAt = expiresRaw ? dateInputEndOfDayIso(expiresRaw) : undefined;
    const reviewBy = reviewRaw ? dateInputEndOfDayIso(reviewRaw) : undefined;
    if ((expiresRaw && !expiresAt) || (reviewRaw && !reviewBy)) {
      setWaiveError(t('Expiry or review date is invalid. Use a valid calendar date.'));
      return;
    }
    const data = addWaiverPatch(waivers, {
      name: selectedLive.metadata.name,
      reason: waiveReason.trim(),
      requestedBy: waiveRequestedBy.trim(),
      approvedBy: waiveApprovedBy.trim(),
      expiresAt,
      reviewBy,
    });
    // Empty patch is client-side MaxLength/name validation: surface it so
    // over-long fields are not a silent no-op.
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
  };

  // FAIL+active-waiver -> WAIVED so FAIL chips/deep-links match Overview fail
  // counts (operator excludes waived fails from the Fail bucket). Uses the
  // shared resultFilterStatus + activeWaived Set (built once) for O(1) per row.
  // Defined before columns so status sort uses the same key as filters.
  const rowFilterStatus = React.useCallback(
    (r: ComplianceCheckResult): string => resultFilterStatus(r, activeWaived),
    [activeWaived],
  );

  // Sort by the same values the cells show (and filters use), not raw CR fields.
  // Raw `status` leaves benign INCONSISTENT / waived FAIL out of visual order;
  // raw `severity` ignores the check-severity label fallback; raw `description`
  // puts empty-description rows under "" while the cell shows the check name.
  // Precompute keys + index order (no {r,k} object per row): keyOf can walk
  // INCONSISTENT annotations or descriptions; multi-thousand CCR sorts must not
  // re-eval keyOf O(n log n) times or allocate a decorate object per check.
  const sortByString = React.useCallback(
    (keyOf: (r: ComplianceCheckResult) => string) =>
      (data: ComplianceCheckResult[], sortDirection: string): ComplianceCheckResult[] => {
        const mul = sortDirection === 'desc' ? -1 : 1;
        // Match profile-chip sort: console locale, never throw on a bad i18n tag.
        const locale = safeLocale(i18n.language);
        // One collator, not a fresh one per localeCompare call: sorting thousands
        // of CCRs pays collator setup on every one of the O(n log n) comparisons.
        const collator = new Intl.Collator(locale);
        const n = data.length;
        const keys = new Array<string>(n);
        const order = new Array<number>(n);
        for (let i = 0; i < n; i++) {
          keys[i] = keyOf(data[i]);
          order[i] = i;
        }
        order.sort((a, b) => mul * collator.compare(keys[a], keys[b]));
        const out = new Array<ComplianceCheckResult>(n);
        for (let i = 0; i < n; i++) {
          out[i] = data[order[i]];
        }
        return out;
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
        // Optional-chain: partial list items must not throw mid-sort.
        sort: sortByString((r) => suiteFilterKey(r.metadata?.labels) ?? ''),
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
      const s = statusStyle(status);
      // Title once: aria-label and visible text (avoids dual description scans).
      const title = checkTitle(obj);
      // Field + check-severity label; empty normalizes to "unknown".
      const sev = checkSeverity(obj);
      // Optional-chain: partial/tampered list items must not crash the row.
      const name = obj.metadata?.name ?? '';
      return (
        <>
          <TableData id="title" activeColumnIDs={activeColumnIDs}>
            {/* Single-line rows: VirtualizedTable virtualizes with a fixed row
                height; the raw check name lives in the detail modal. */}
            <Button
              variant="link"
              isInline
              title={title}
              aria-label={t('View details for {{title}}', { title })}
              onClick={(e) => {
                returnFocusRef.current = e.currentTarget;
                setWaiveSuccess(null);
                // A stale error from the orphan-removal flow must not appear
                // inside an unrelated check's waive form.
                setWaiveError(null);
                setSelected(obj);
              }}
              // Fixed-height virtualized rows: ellipsis + title tooltip so long
              // check names do not wrap/overflow the cell.
              style={{
                display: 'inline-block',
                maxWidth: '100%',
                overflow: 'hidden',
                textOverflow: 'ellipsis',
                whiteSpace: 'nowrap',
                verticalAlign: 'bottom',
              }}
            >
              {title}
            </Button>
          </TableData>
          <TableData id="profile" activeColumnIDs={activeColumnIDs}>
            {/* Same path as the detail modal and report (checkProfileLabel + t). */}
            {t(checkProfileLabel(obj.metadata?.labels))}
          </TableData>
          <TableData id="status" activeColumnIDs={activeColumnIDs}>
            <Label isCompact color={s.color} icon={s.icon}>
              {statusDisplayTitle(status, t)}
            </Label>
            {/* Stale waiver on a non-FAIL (e.g. self-healed PASS): keep a badge
                so the waiver can still be found; FAIL+waiver is already WAIVED. */}
            {status !== 'WAIVED' && name !== '' && activeWaived.has(name) && (
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

  // Content keys: status-only baseline updates reallocate profile arrays with
  // the same membership; avoid rebuilding chips (and rowFilters) every tick.
  const profilesKey = (profiles ?? []).join('\0');
  const tailoredKey = (tailored ?? []).join('\0');
  // When the baseline already lists suites, chips come from those lists alone
  // (CompliancePage suite-selects CCRs to the same set). Drop ownedResults from
  // deps so multi-thousand CCR watch ticks do not rebuild filter chips.
  const suiteKeysFromBaseline = profilesKey.length > 0 || tailoredKey.length > 0;
  const profileItems = React.useMemo(() => {
    // Ids match suiteFilterKey / Overview resultsHref: built-in key or "tp-<name>".
    const keys = new Set(profiles ?? []);
    for (const name of tailored ?? []) {
      keys.add(`tp-${name}`);
    }
    // Scan results only when baseline lists are empty (tests / partial CRs)
    // so chips still appear from watched data.
    if (keys.size === 0) {
      for (const r of ownedResults) {
        const key = suiteFilterKey(r.metadata?.labels);
        if (key !== undefined) {
          keys.add(key);
        }
      }
    }
    // Keep the id as the filter key (reducer + resultsHref depend on it) but
    // show tailored profiles by their clean name and built-ins by localized title.
    // Sort by display title (console locale) so chip order matches what users read,
    // not the English-ish profile key / tp- prefix.
    return [...keys]
      .map((k) => ({
        id: k,
        title: t(suiteFilterKeyTitle(k)),
      }))
      .sort((a, b) => a.title.localeCompare(b.title, safeLocale(i18n.language)));
    // profiles/tailored read when keys change; ownedResults only when discovering.
    // eslint-disable-next-line react-hooks/exhaustive-deps -- content keys
  }, [profilesKey, tailoredKey, suiteKeysFromBaseline ? null : ownedResults, i18n, t]);

  const rowFilters: RowFilter<ComplianceCheckResult>[] = React.useMemo(
    () => [
      {
        filterGroupName: t('Status'),
        type: 'result-status',
        reducer: rowFilterStatus,
        filter: chipFilter(rowFilterStatus),
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
        filter: chipFilter(checkSeverity),
        items: ['high', 'medium', 'low', 'info', 'unknown'].map((s) => ({
          id: s,
          title: severityDisplayTitle(s, t),
        })),
      },
      {
        filterGroupName: t('Profiles'),
        type: 'result-profile',
        reducer: (r) => suiteFilterKey(r.metadata?.labels) ?? '',
        filter: chipFilter((r) => suiteFilterKey(r.metadata?.labels) ?? ''),
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
    // Reuse the table's active-waiver Set (O(1) per row; no second Set build).
    setExportError(null);
    setWaiveSuccess(null);
    try {
      const blob = new Blob([resultsCsv(filteredData, activeWaived)], {
        type: 'text/csv;charset=utf-8',
      });
      downloadBlob(blob, 'compliance-results.csv');
      // Browser downloads are silent; confirm so the click is not a no-op.
      setWaiveSuccess(t('Results downloaded as compliance-results.csv.'));
    } catch (e) {
      // DOM / serialization failures must not look like a silent no-op click.
      setExportError(errorMessage(e) ?? t('Failed to export results CSV.'));
    }
  }, [filteredData, activeWaived, t]);

  // Empty / misconfigured baselines: a bare table with no rows leaves first-time
  // admins without a next step (Overview already explains; Results must too).
  if (loaded && !resultsError && !baseline) {
    return (
      <ListPageBody>
        <BaselineNotConfigured />
      </ListPageBody>
    );
  }
  if (loaded && !resultsError && ownedResults.length === 0) {
    const scanningDisabled =
      (baseline?.spec.profiles?.length ?? 0) === 0 &&
      (baseline?.spec.tailoredProfiles?.length ?? 0) === 0;
    return (
      <ListPageBody>
        {orphanWaiverAlert}
        <EmptyState
          titleText={
            scanningDisabled ? t('Scanning is disabled') : t('No check results yet')
          }
          headingLevel="h2"
        >
          <EmptyStateBody>
            {scanningDisabled ? (
              <>
                {t('No profiles are selected. Enable a profile to resume scanning.')}{' '}
                <a href="/baseline-security/profiles">{t('Go to Profiles')}</a>
              </>
            ) : (
              t('Results appear after a scan completes. Use Rescan now above to start a scan.')
            )}
          </EmptyStateBody>
        </EmptyState>
      </ListPageBody>
    );
  }

  return (
    <ListPageBody>
      {/* Real DOM focus fallback: when the detail modal's trigger row is
          virtualized out of the table while the modal is open, restoreFocus
          targets this sentinel instead of dropping focus to <body>. */}
      <div ref={regionRef} tabIndex={-1} />
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
      {orphanWaiverAlert}
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
        aria-label={t('Results')}
        EmptyMsg={() => (
          <EmptyState titleText={t('No matching results')} headingLevel="h2">
            <EmptyStateBody>
              {t(
                'No check results match the current filters. Clear or change filters to see more.',
              )}{' '}
              {/* Query-less path drops rowFilter-* chips (ListPageFilter reads URL). */}
              <a href="/baseline-security/results">{t('Clear filters')}</a>
            </EmptyStateBody>
          </EmptyState>
        )}
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
                const s = statusStyle(status);
                const sev = checkSeverity(selectedLive);
                // Same path as the Profile column / report (checkProfileLabel + t).
                const profileText = t(checkProfileLabel(selectedLive.metadata.labels));
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
              <Content
                component="p"
                style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}
              >
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
                      <Table
                        variant="compact"
                        aria-label={t('Per-node results')}
                        style={{ marginBottom: 'var(--pf-t--global--spacer--md)' }}
                      >
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
                            const style = statusStyle(st);
                            // Index in the key: hostile data could repeat a node name.
                            return (
                              <Tr key={`${s.node}-${i}`}>
                                <Td>{s.node}</Td>
                                <Td>
                                  <Label isCompact color={style.color} icon={style.icon}>
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
                                {(() => {
                                  const up = mostCommon.toUpperCase();
                                  const style = statusStyle(up);
                                  return (
                                    <Label isCompact color={style.color} icon={style.icon}>
                                      {statusDisplayTitle(up, t)}
                                    </Label>
                                  );
                                })()}
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
                  <Content
                    component="pre"
                    style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}
                  >
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
              {showWaiverForm &&
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
                              value={waiveReason}
                              onChange={(_e, v) => {
                                setWaiveReason(v);
                                // Stale submit errors must clear once the admin edits again.
                                if (waiveError) setWaiveError(null);
                              }}
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
                                  onChange={(_e, v) => {
                                    setWaiveRequestedBy(v);
                                    if (waiveError) setWaiveError(null);
                                  }}
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
                                  onChange={(_e, v) => {
                                    setWaiveApprovedBy(v);
                                    if (waiveError) setWaiveError(null);
                                  }}
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
                                  onChange={(_e, v) => {
                                    setWaiveExpiresAt(v);
                                    if (waiveError) setWaiveError(null);
                                  }}
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
                                  onChange={(_e, v) => {
                                    setWaiveReviewBy(v);
                                    if (waiveError) setWaiveError(null);
                                  }}
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
                          actionClose={
                            <AlertActionCloseButton
                              aria-label={t('Close')}
                              onClose={() => setWaiveError(null)}
                            />
                          }
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
            {showWaiverForm &&
              (findWaiver(selectedLive.metadata.name, waivers)
                ? withDisabledTip(
                    waiveDisabled && waiveDisabledReason ? waiveDisabledReason : undefined,
                    <Button
                      variant="secondary"
                      isDisabled={waiveDisabled}
                      isLoading={busy}
                      onClick={removeSelectedWaiver}
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
                      onClick={addSelectedWaiver}
                    >
                      {t('Waive check')}
                    </Button>,
                  ))}
            <Button variant="link" isDisabled={busy} onClick={closeModal}>
              {showWaiverForm ? t('Cancel') : t('Close')}
            </Button>
          </ModalFooter>
        )}
      </Modal>
    </ListPageBody>
  );
};

export default ResultsTab;
