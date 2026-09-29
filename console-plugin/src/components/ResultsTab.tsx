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
  HelperText,
  HelperTextItem,
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
  checkProfileLabel,
  ClusterBaseline,
  ClusterBaselineModel,
  ComplianceCheckResult,
  clusterBaselinePatchAccess,
  scanningDisabled,
  suiteFilterKey,
  suiteFilterKeyTitle,
  WAIVER_MAX_ITEMS,
} from '../models';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';
import { downloadBlob } from '../download';
import { errorMessage } from '../errors';
import { stripControlAndFormat, stripInvisibleText } from '../parse';
import { AccessGate, mayWrite } from '../permissions';
import { encodeKeyList } from '../contentKey';
import { checkResultHref, machineConfigPoolHref } from '../links';
import {
  addWaiverPatch,
  removeWaiverPatch,
  resourceVersionTest,
  WAIVER_ATTRIBUTION_MAX_LEN,
  WAIVER_REASON_MAX_LEN,
} from '../patches';
import { K8S_NAME_MAX_LEN } from '../names';
import {
  checkBody,
  checkTitle,
  nodeScanPool,
  resultsCsv,
  RESULT_SEVERITIES,
  severityDisplayTitle,
} from '../results';
import { checkSeverity } from '../scoring';
import {
  effectiveStatus,
  inconsistentSources,
  RESULT_FILTER_STATUSES,
  resultFilterStatus,
  statusDisplayTitle,
} from '../status';
import {
  dateInputEndOfDayIso,
  formatCount,
  formatLocalDate,
  localDateInputValue,
} from '../dates';
import { bidiIsolate, compareForDisplay } from '../text';
import { FILTER_FIELD_MIN_WIDTH, WAIVER_FIELD_MIN_WIDTH } from '../layout';
import {
  activeWaivedNames,
  findWaiver,
  waiverExpired,
} from '../waivers';
import BaselineNotConfigured from './BaselineNotConfigured';
import BaselineUnavailable from './BaselineUnavailable';
import ConsoleLink from './ConsoleLink';
import { withDisabledTip } from './DisabledTip';
import { restoreFocus } from './focus';
import { useAutoDismiss } from './useAutoDismiss';
import { useWaiverExpiryClock } from './useWaiverExpiryClock';

// Color + icon per status so state is not color-only. The index signature
// keeps arbitrary runtime tokens readable while required keys fail typecheck
// if a CheckStatus member or WAIVED (the synthetic filter status) is missing.
interface StatusVisual {
  color: React.ComponentProps<typeof Label>['color'];
  icon: React.ReactElement;
}

interface StatusVisualMap {
  [status: string]: StatusVisual | undefined;
  PASS: StatusVisual;
  FAIL: StatusVisual;
  ERROR: StatusVisual;
  MANUAL: StatusVisual;
  INFO: StatusVisual;
  INCONSISTENT: StatusVisual;
  SKIP: StatusVisual;
  'NOT-APPLICABLE': StatusVisual;
  WAIVED: StatusVisual;
}

const statusLabel: StatusVisualMap = {
  PASS: { color: 'green', icon: <CheckCircleIcon /> },
  FAIL: { color: 'red', icon: <ExclamationCircleIcon /> },
  ERROR: { color: 'red', icon: <ExclamationCircleIcon /> },
  MANUAL: { color: 'orange', icon: <ExclamationTriangleIcon /> },
  INFO: { color: 'blue', icon: <InfoCircleIcon /> },
  // Distinct from MANUAL (orange): multi-node result disagreement.
  INCONSISTENT: { color: 'purple', icon: <ExclamationTriangleIcon /> },
  SKIP: { color: 'grey', icon: <MinusCircleIcon /> },
  'NOT-APPLICABLE': { color: 'grey', icon: <MinusCircleIcon /> },
  // Teal matches the Overview composition donut so a waived FAIL is not
  // painted the same grey as N/A (WAIVED is a filter status, not a CO token).
  WAIVED: { color: 'teal', icon: <MinusCircleIcon /> },
};

// Style for a row-filter status. Unknown tokens fall through to grey/minus.
const statusStyle = (status: string) =>
  statusLabel[status] ?? { color: 'grey' as const, icon: <MinusCircleIcon /> };

// Stable empty list for optional results prop (avoids new [] each render).
const EMPTY_RESULTS: ComplianceCheckResult[] = [];

// A waiver whose check vanished from the current result set; index is its
// position in the CR spec.waivers list for targeted removal patches.
interface OrphanedWaiver {
  name: string;
  index: number;
}

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

const ResultsTab: React.FC<{
  baseline?: ClusterBaseline;
  // Shared list from CompliancePage so this tab does not open a second watch.
  results?: ComplianceCheckResult[];
  resultsLoaded?: boolean;
  resultsError?: unknown;
  // Baseline watch failure. CompliancePage forces `loaded` true on an error, so
  // without this a failed watch reaches BaselineNotConfigured and claims the
  // CR does not exist.
  baselineError?: unknown;
}> = ({ baseline, results, resultsLoaded: loaded = false, resultsError, baselineError }) => {
  const { t, i18n } = useTranslation('plugin__baseline-security-console-plugin');
  const [selected, setSelected] = React.useState<ComplianceCheckResult | null>(null);
  const [waiveReason, setWaiveReason] = React.useState('');
  const [waiveRequestedBy, setWaiveRequestedBy] = React.useState('');
  const [waiveApprovedBy, setWaiveApprovedBy] = React.useState('');
  const [waiveExpiresAt, setWaiveExpiresAt] = React.useState('');
  const [waiveReviewBy, setWaiveReviewBy] = React.useState('');
  const [busy, setBusy] = React.useState(false);
  // Which orphan waiver is mid-removal. busy alone greys every button in the
  // group with nothing showing which request is in flight.
  const [removingWaiverName, setRemovingWaiverName] = React.useState<string | null>(null);
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
  useAutoDismiss(waiveSuccess, false, () => setWaiveSuccess(null));
  const [canWaive, canWaiveLoading] = useAccessReview(clusterBaselinePatchAccess);
  const waiveGate: AccessGate = { allowed: canWaive };
  const waivers = baseline?.spec.waivers;
  // Active waivers are time-sensitive: membership alone is not enough. A waiver
  // can expire with no CR edit, and operator status-only updates do not change
  // waiversKey. Without a clock tick at the soonest expiry, Results would keep
  // showing WAIVED (and hide the row from FAIL chips/deep-links) after the
  // operator has already returned the check to the Fail bucket.
  const { key: waiversKey, tick: waiverClock } = useWaiverExpiryClock(waivers);
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
  // after the check starts passing. metadata is optional-chained: selectedLive
  // deliberately lets a nameless list item through only so the detail modal can
  // close cleanly, and this runs on `selected` (the raw click target), so an
  // unguarded read would throw during render and blank the whole tab.
  const showWaiver = (r: ComplianceCheckResult): boolean =>
    !!findWaiver(r.metadata?.name ?? '', waivers) ||
    (!!baseline && effectiveStatus(r) === 'FAIL');

  const resetWaiverForm = () => {
    setSelected(null);
    setWaiveReason('');
    setWaiveRequestedBy('');
    setWaiveApprovedBy('');
    setWaiveExpiresAt('');
    setWaiveReviewBy('');
    setWaiveError(null);
  };

  // Waiver form onChange: store the edit and drop a stale submit error, so the
  // admin is not still reading the last failed attempt's message mid-typing.
  // The event is unused; it is typed as the shared base so one handler serves
  // both the TextInput and TextArea form controls.
  const waiveEdit =
    (set: React.Dispatch<React.SetStateAction<string>>) =>
    (_event: React.FormEvent<HTMLElement>, value: string) => {
      set(value);
      if (waiveError) setWaiveError(null);
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
    if (!baseline) return;
    // A second control in the same frame reaches here while the first patch is
    // still in flight (the buttons gate on the `busy` state, which only lands on
    // the next render, while busyRef is set synchronously). Returning silently
    // made that click a dead no-op; say why instead.
    if (busyRef.current) {
      setWaiveError(t('Another waiver change is already in progress.'));
      return;
    }
    // Same gate the waiver controls carry, so a modal opened while permitted
    // cannot spend the patch after the review flipped to denied.
    if (!mayWrite(waiveGate)) {
      setWaiveError(t('You do not have permission to waive checks.'));
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
    // A partial or tampered list item can reach the row (the row itself
    // optional-chains metadata so it renders), so a click on it hands us an
    // object with no name. The modal keys everything on the name (header,
    // waiver lookup, live refresh), so leave it closed rather than dereference
    // metadata here and take down the whole tab.
    const want = selected?.metadata?.name;
    if (!want) return null;
    for (const r of ownedResults) {
      if (r.metadata?.name === want) {
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
    return restoreFocus(el, regionRef);
  }, [selectedLive]);

  // Named event handlers (not inline IIFE onClick) so react-hooks/refs does not
  // treat the busyRef read inside patchWaivers as render-time access. Defined
  // after selectedLive so the compiler can preserve that memoization.
  const removeSelectedWaiver = () => {
    if (!selectedLive) return;
    const idx = waivers?.findIndex((w) => w.name === selectedLive.metadata.name) ?? -1;
    if (idx < 0) return;
    // Same empty-patch guard / failure message as the orphan-removal path.
    removeWaiverByIndex(idx, selectedLive.metadata.name);
  };

  // Waivers whose check has no current result (its rule was removed or its
  // profile unbound): they still exclude from scoring but have no table row, so
  // there is otherwise no way to remove them but hand-editing the CR. Only when
  // results are loaded and not errored, so a loading/failed list does not flag
  // every waiver as orphaned.
  const orphanWaivers = React.useMemo<OrphanedWaiver[]>(() => {
    if (!loaded || resultsError || !waivers?.length) {
      return [];
    }
    const names = new Set((results ?? []).map((r) => r.metadata?.name).filter(Boolean));
    return waivers.flatMap((w, index) =>
      w.name && !names.has(w.name) ? [{ name: w.name, index }] : [],
    );
    // eslint-disable-next-line react-hooks/exhaustive-deps -- content key + stable results identity
  }, [loaded, resultsError, waiversKey, results]);

  const removeWaiverByIndex = (index: number, name: string, successMsg?: string) => {
    const data = removeWaiverPatch(index, name);
    if (!data.length) {
      setWaiveError(t('Failed to remove waiver.'));
      return;
    }
    setRemovingWaiverName(name);
    void patchWaivers(
      data,
      t('Failed to remove waiver.'),
      // An orphaned waiver matches no result, so the live path's "the check
      // counts toward the score again" is false there; the caller passes a
      // message that does not claim a score effect.
      successMsg ?? t('Waiver removed. The check counts toward the score again.'),
    ).finally(() => setRemovingWaiverName(null));
  };

  // Rendered in both the main list and the empty-results early return, so that
  // when every check is gone (the case where orphan waivers matter most) the
  // removal affordance is still reachable.
  const orphanWaiverAlert =
    canWaive && orphanWaivers.length > 0 ? (
      <Alert
        variant="warning"
        isInline
        title={t('Waivers referencing a removed check ({{count}})', {
          count: orphanWaivers.length,
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
                isLoading={busy && removingWaiverName === name}
                title={name}
                onClick={() => removeWaiverByIndex(index, name, t('Waiver removed.'))}
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
            title={<span dir="auto">{waiveError}</span>}
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
        t('Maximum of {{formattedMax}} waivers reached. Remove one before adding another.', {
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
          'Waiver fields are invalid or exceed length limits (name {{formattedName}}, reason {{formattedReason}}, attribution {{formattedAttribution}}).',
          {
            formattedName: formatCount(K8S_NAME_MAX_LEN, i18n.language),
            formattedReason: formatCount(WAIVER_REASON_MAX_LEN, i18n.language),
            formattedAttribution: formatCount(WAIVER_ATTRIBUTION_MAX_LEN, i18n.language),
          },
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
        return (
          data
            // Decorate once so each comparison reads a precomputed key and the
            // final map restores the original rows without re-running keyOf.
            .map((row, index) => ({ key: keyOf(row), index }))
            .sort((a, b) => mul * compareForDisplay(a.key, b.key, i18n.language))
            .map((d) => data[d.index])
        );
      },
    [i18n.language],
  );

  // Rank sort for a column whose values come from a fixed vocabulary (Status,
  // Severity). Alphabetical order there is noise: ascending Status reads
  // Error, Fail, ..., Pass, and ascending Severity reads High, Info, Low,
  // Medium. Position in the facet's own list decides instead, and a token
  // outside it (forward-compat value off a CR) falls past the known ones and
  // then falls back to the collator so the group stays locale-ordered.
  const sortByRanked = React.useCallback(
    (order: readonly string[]) =>
      (keyOf: (r: ComplianceCheckResult) => string) =>
      (data: ComplianceCheckResult[], sortDirection: string): ComplianceCheckResult[] => {
        const mul = sortDirection === 'desc' ? -1 : 1;
        const rank = (key: string): number => {
          const i = order.indexOf(key);
          return i === -1 ? order.length : i;
        };
        return data
          .map((row, index) => ({ key: keyOf(row), index }))
          .sort((a, b) => {
            const byRank = rank(a.key) - rank(b.key);
            if (byRank !== 0) {
              return mul * byRank;
            }
            const byText = compareForDisplay(a.key, b.key, i18n.language);
            return byText !== 0 ? byText : a.index - b.index;
          })
          .map((d) => data[d.index]);
      },
    [i18n.language],
  );

  const sortByStatus = React.useMemo(
    () => sortByRanked(RESULT_FILTER_STATUSES)(rowFilterStatus),
    [sortByRanked, rowFilterStatus],
  );
  const sortBySeverity = React.useMemo(
    () => sortByRanked(RESULT_SEVERITIES)(checkSeverity),
    [sortByRanked],
  );

  const columns: TableColumn<ComplianceCheckResult>[] = React.useMemo(
    () => [
      { title: t('Check'), id: 'title', sort: sortByString(checkTitle) },
      // The same rule appears in several benchmarks, so a Check title can repeat;
      // the Profile column tells otherwise-identical rows apart.
      {
        title: t('Profile'),
        id: 'profile',
        // Display title, not the hidden filter key: the cell renders
        // checkProfileLabel (localized built-in titles, tailored names without
        // the tp- prefix), so sorting on the key orders "ACSC Essential Eight"
        // after "CIS" and puts every tailored row where its own name does not
        // belong. Same rule the profile chips use below. Optional-chain:
        // partial list items must not throw mid-sort.
        sort: sortByString((r) => {
          const key = suiteFilterKey(r.metadata?.labels);
          return key === undefined ? '' : suiteFilterKeyTitle(key);
        }),
      },
      { title: t('Status'), id: 'status', sort: sortByStatus },
      { title: t('Severity'), id: 'severity', sort: sortBySeverity },
    ],
    [t, sortByString, sortByStatus, sortBySeverity],
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
              // Isolate the title inside the label: an attribute has no dir of
              // its own, so an RTL check title (untrusted CO text) is an
              // unisolated run in an LTR sentence and the words around it
              // reorder for anyone reading the label as plain text.
              aria-label={t('View details for {{title}}', { title: bidiIsolate(title) })}
              onClick={(e) => {
                returnFocusRef.current = e.currentTarget;
                setWaiveSuccess(null);
                // A stale error from the orphan-removal flow must not appear
                // inside an unrelated check's waive form.
                setWaiveError(null);
                setSelected(obj);
              }}
              // dir=auto: check titles are untrusted CO text and may be RTL;
              // without it ellipsis sits on the wrong edge and BIDI can
              // reverse surrounding punctuation. Fixed-height virtualized
              // rows: ellipsis + title tooltip so long names do not wrap.
              dir="auto"
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
            {/* Same path as the detail modal and report (checkProfileLabel + t).
                A tailored profile name is CR text, so it is dir=auto like the
                check title in the same row. */}
            <span dir="auto">{t(checkProfileLabel(obj.metadata?.labels))}</span>
          </TableData>
          <TableData id="status" activeColumnIDs={activeColumnIDs}>
            <Label isCompact color={s.color} icon={s.icon}>
              {statusDisplayTitle(status, t)}
            </Label>
            {/* Stale waiver on a non-FAIL (e.g. self-healed PASS): keep a badge
                so the waiver can still be found; FAIL+waiver is already WAIVED. */}
            {status !== 'WAIVED' && name !== '' && activeWaived.has(name) && (
              <Label
                isCompact
                color="grey"
                style={{ marginInlineStart: 'var(--pf-t--global--spacer--sm)' }}
              >
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
  const profilesKey = encodeKeyList(profiles);
  const tailoredKey = encodeKeyList(tailored);
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
    // not the English-ish profile key / tp- prefix. Same cached, numeric-aware
    // order as the Profiles catalog and the results table.
    return [...keys]
      .map((k) => ({
        id: k,
        title: t(suiteFilterKeyTitle(k)),
      }))
      .sort((a, b) => compareForDisplay(a.title, b.title, i18n.language));
    // profiles/tailored read when keys change; ownedResults only when discovering.
    // The collation locale is part of the value, so the key carries the language
    // and not the i18n instance: that object is the same one across a console
    // language switch, so as a dependency it never fires and the chips kept the
    // previous language's titles and order.
    // eslint-disable-next-line react-hooks/exhaustive-deps -- content keys
  }, [profilesKey, tailoredKey, suiteKeysFromBaseline ? null : ownedResults, i18n.language, t]);

  const rowFilters: RowFilter<ComplianceCheckResult>[] = React.useMemo(
    () => [
      {
        filterGroupName: t('Status'),
        type: 'result-status',
        reducer: rowFilterStatus,
        filter: chipFilter(rowFilterStatus),
        items: RESULT_FILTER_STATUSES.map((s) => ({
          id: s,
          title: statusDisplayTitle(s, t),
        })),
      },
      {
        filterGroupName: t('Severity'),
        type: 'result-severity',
        reducer: (r) => checkSeverity(r),
        filter: chipFilter(checkSeverity),
        items: RESULT_SEVERITIES.map((s) => ({
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

  const exportCsvDisabled = !loaded || !!resultsError || filteredData.length === 0;
  let exportCsvDisabledReason: string | undefined;
  if (resultsError) {
    // A failed check-results watch never sets loaded, so without this branch the
    // button stays pinned with a message claiming it is still loading.
    exportCsvDisabledReason = t('Export is unavailable while check results fail to load.');
  } else if (!loaded) {
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
      // A filtered export says how much it wrote: the file is named the same
      // either way, so a subset would otherwise read as the full report.
      setWaiveSuccess(
        filteredData.length === ownedResults.length
          ? t('Results downloaded as compliance-results.csv.')
          : t(
              'Exported {{count}} of {{formattedTotal}} filtered checks as compliance-results.csv.',
              {
                count: filteredData.length,
                formattedCount: formatCount(filteredData.length, i18n.language),
                formattedTotal: formatCount(ownedResults.length, i18n.language),
              },
            ),
      );
    } catch (e) {
      // DOM / serialization failures must not look like a silent no-op click.
      setExportError(errorMessage(e) ?? t('Failed to export results CSV.'));
    }
  }, [filteredData, activeWaived, ownedResults.length, i18n.language, t]);

  // Empty / misconfigured baselines: a bare table with no rows leaves first-time
  // admins without a next step (Overview already explains; Results must too).
  if (loaded && !resultsError && !baseline) {
    return (
      <ListPageBody>
        {baselineError ? <BaselineUnavailable error={baselineError} /> : <BaselineNotConfigured />}
      </ListPageBody>
    );
  }
  if (loaded && !resultsError && ownedResults.length === 0) {
    const noScanning = scanningDisabled(baseline);
    return (
      <ListPageBody>
        {orphanWaiverAlert}
        <EmptyState
          titleText={noScanning ? t('Scanning is disabled') : t('No check results yet')}
          headingLevel="h2"
        >
          <EmptyStateBody>
            {noScanning ? (
              <>
                {t('No profiles are selected. Enable a profile to resume scanning.')}{' '}
                <ConsoleLink href="/baseline-security/profiles">{t('Go to Profiles')}</ConsoleLink>
              </>
            ) : (
              t(
                'Results appear after a scan completes. The first scan starts automatically; use Rescan now above to run one sooner.',
              )
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
          title={<span dir="auto">{exportError}</span>}
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
        <FlexItem grow={{ default: 'grow' }} style={{ minWidth: FILTER_FIELD_MIN_WIDTH }}>
          <ListPageFilter
            data={data}
            loaded={loaded}
            rowFilters={rowFilters}
            onFilterChange={onFilterChange}
          />
          {/* How much of the set the chips are showing. Without it a filter that
              drops 4000 of 5000 rows looks like the whole result set. Same
              wording Remediations uses for its search count. */}
          {loaded && !resultsError && ownedResults.length > 0 && (
            <HelperText>
              <HelperTextItem>
                {filteredData.length === ownedResults.length
                  ? t('{{count}} check', {
                      count: ownedResults.length,
                      formattedCount: formatCount(ownedResults.length, i18n.language),
                    })
                  : t('Showing {{formattedShown}} of {{count}} checks', {
                      count: ownedResults.length,
                      formattedCount: formatCount(ownedResults.length, i18n.language),
                      formattedShown: formatCount(filteredData.length, i18n.language),
                      formattedTotal: formatCount(ownedResults.length, i18n.language),
                    })}
              </HelperTextItem>
            </HelperText>
          )}
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
              <ConsoleLink href="/baseline-security/results">{t('Clear filters')}</ConsoleLink>
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
                        {/* A tailored profile name is CR text, so it is dir=auto
                            for the same reason the check title above is. */}
                        <span dir="auto">{profileText}</span>
                      </Label>
                    </FlexItem>
                  </Flex>
                );
              })()}
              <Content
                component="p"
                dir="auto"
                style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}
              >
                {checkBody(selectedLive) || t('No description provided.')}
              </Content>
              {selectedLive.status === 'INCONSISTENT' &&
                (() => {
                  const { sources, mostCommon } = inconsistentSources(selectedLive);
                  const pool = nodeScanPool(selectedLive);
                  const mostCommonStyle = mostCommon ? statusStyle(mostCommon) : null;
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
                            <ConsoleLink href={machineConfigPoolHref(pool)} dir="auto">
                              {pool}
                            </ConsoleLink>
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
                            // Tokens are already ASCII-uppercased by
                            // inconsistentSources. Do not call toUpperCase:
                            // "paß" would become "PASS" and disagree with
                            // effectiveStatus / the operator.
                            const style = statusStyle(s.status);
                            // Index in the key: hostile data could repeat a node name.
                            return (
                              // oxlint-disable-next-line react/no-array-index-key -- node names can repeat
                              <Tr key={`${s.node}-${i}`}>
                                <Td dir="auto">{s.node}</Td>
                                <Td>
                                  <Label isCompact color={style.color} icon={style.icon}>
                                    {s.status ? statusDisplayTitle(s.status, t) : '—'}
                                  </Label>
                                </Td>
                              </Tr>
                            );
                          })}
                          {mostCommon && mostCommonStyle && (
                            <Tr>
                              <Td>{t('all other nodes')}</Td>
                              <Td>
                                <Label
                                  isCompact
                                  color={mostCommonStyle.color}
                                  icon={mostCommonStyle.icon}
                                >
                                  {statusDisplayTitle(mostCommon, t)}
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
                  <Content
                    component="pre"
                    dir="auto"
                    style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}
                  >
                    {selectedLive.instructions}
                  </Content>
                </>
              )}
              <Content component="p" style={{ marginTop: 'var(--pf-t--global--spacer--md)' }}>
                <ConsoleLink href={checkResultHref(selectedLive.metadata.name)}>
                  {t('View full check details in OpenShift')}
                </ConsoleLink>
              </Content>
              {/* Waivers: accept a failing check as risk so it leaves the score.
                  Only FAIL affects the score, so waiving is offered for FAIL (and
                  any already-waived check, so a stale waiver stays removable). */}
              {showWaiverForm &&
                (() => {
                  const w = findWaiver(selectedLive.metadata.name, waivers);
                  const expired = !!w && waiverExpired(w);
                  const reasonText = stripInvisibleText(w?.reason ?? '');
                  const requestedByText = stripControlAndFormat(w?.requestedBy ?? '');
                  const approvedByText = stripControlAndFormat(w?.approvedBy ?? '');
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
                            {reasonText && (
                              <DescriptionListGroup>
                                <DescriptionListTerm>{t('Reason')}</DescriptionListTerm>
                                <DescriptionListDescription dir="auto">{reasonText}</DescriptionListDescription>
                              </DescriptionListGroup>
                            )}
                            {requestedByText && (
                              <DescriptionListGroup>
                                <DescriptionListTerm>{t('Requested by')}</DescriptionListTerm>
                                <DescriptionListDescription dir="auto">{requestedByText}</DescriptionListDescription>
                              </DescriptionListGroup>
                            )}
                            {approvedByText && (
                              <DescriptionListGroup>
                                <DescriptionListTerm>{t('Approved by')}</DescriptionListTerm>
                                <DescriptionListDescription dir="auto">{approvedByText}</DescriptionListDescription>
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
                              onChange={waiveEdit(setWaiveReason)}
                              // Match ClusterBaseline CRD waiver field MaxLength
                              // (same constant the patch validator enforces).
                              maxLength={WAIVER_REASON_MAX_LEN}
                              // Same reason as the attribution inputs below: the
                              // reason is free text persisted in a cluster-scoped
                              // CR and in every exported report, so browser
                              // autofill must not offer the operator's own name.
                              autoComplete="off"
                              rows={2}
                            />
                          </FormGroup>
                          {/* Wrap on narrow viewports so four fields do not squash. */}
                          <Flex
                            gap={{ default: 'gapMd' }}
                            flexWrap={{ default: 'wrap' }}
                            style={{ marginTop: 'var(--pf-t--global--spacer--sm)' }}
                          >
                            <FlexItem
                              flex={{ default: 'flex_1' }}
                              style={{ minWidth: WAIVER_FIELD_MIN_WIDTH }}
                            >
                              <FormGroup label={t('Requested by (optional)')} fieldId="waive-req">
                                <TextInput
                                  id="waive-req"
                                  value={waiveRequestedBy}
                                  onChange={waiveEdit(setWaiveRequestedBy)}
                                  maxLength={WAIVER_ATTRIBUTION_MAX_LEN}
                                  // Attribution takes a cluster username, not a
                                  // personal one: browser name autofill would
                                  // write the operator's own name into a
                                  // cluster-scoped CR and every exported report.
                                  autoComplete="off"
                                />
                              </FormGroup>
                            </FlexItem>
                            <FlexItem
                              flex={{ default: 'flex_1' }}
                              style={{ minWidth: WAIVER_FIELD_MIN_WIDTH }}
                            >
                              <FormGroup label={t('Approved by (optional)')} fieldId="waive-appr">
                                <TextInput
                                  id="waive-appr"
                                  value={waiveApprovedBy}
                                  onChange={waiveEdit(setWaiveApprovedBy)}
                                  maxLength={WAIVER_ATTRIBUTION_MAX_LEN}
                                  autoComplete="off"
                                />
                              </FormGroup>
                            </FlexItem>
                            <FlexItem
                              flex={{ default: 'flex_1' }}
                              style={{ minWidth: WAIVER_FIELD_MIN_WIDTH }}
                            >
                              <FormGroup label={t('Expires (optional)')} fieldId="waive-exp">
                                <TextInput
                                  id="waive-exp"
                                  type="date"
                                  // Past expiry creates an immediately expired waiver.
                                  // Local calendar day (not UTC) so min matches the date picker.
                                  min={localDateInputValue()}
                                  value={waiveExpiresAt}
                                  onChange={waiveEdit(setWaiveExpiresAt)}
                                  aria-label={t('Expires (optional)')}
                                />
                              </FormGroup>
                            </FlexItem>
                            <FlexItem
                              flex={{ default: 'flex_1' }}
                              style={{ minWidth: WAIVER_FIELD_MIN_WIDTH }}
                            >
                              <FormGroup label={t('Review by (optional)')} fieldId="waive-review">
                                <TextInput
                                  id="waive-review"
                                  type="date"
                                  // A review deadline in the past is not schedulable; match Expires.
                                  min={localDateInputValue()}
                                  value={waiveReviewBy}
                                  onChange={waiveEdit(setWaiveReviewBy)}
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
                          title={<span dir="auto">{waiveError}</span>}
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
                    waiveDisabledReason,
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
                    waiveDisabledReason,
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
