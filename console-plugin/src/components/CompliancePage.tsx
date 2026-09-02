import * as React from 'react';
import { useTranslation } from 'react-i18next';
import {
  HorizontalNav,
  k8sPatch,
  useAccessReview,
  useK8sWatchResource,
  WatchK8sResource,
} from '@openshift-console/dynamic-plugin-sdk';
import {
  Alert,
  AlertActionCloseButton,
  Button,
  Flex,
  FlexItem,
  PageSection,
  Content,
  Title,
} from '@patternfly/react-core';
import { DownloadIcon } from '@patternfly/react-icons';
import {
  CLUSTER_BASELINE_NAME,
  ClusterBaseline,
  ClusterBaselineGVK,
  COMPLIANCE_NAMESPACE,
  ComplianceCheckResult,
  ComplianceCheckResultGVK,
  ComplianceScan,
  ComplianceScanGVK,
  ComplianceScanModel,
  ownedSuiteSelector,
  scanningDisabled,
} from '../models';
import { formatCount } from '../dates';
import { downloadBlob, openBlobInTab } from '../download';
import { errorMessage } from '../errors';
import { rescanPatch } from '../patches';
import { withDisabledTip } from './DisabledTip';
import { useAutoDismiss } from './feedback';
import {
  BaselineContext,
  OverviewRoute,
  ProfilesRoute,
  RemediationsRoute,
  ResultsRoute,
} from './BaselineContext';

// Stable empties so `?? []` does not allocate a new array every render (hooks deps).
const EMPTY_SCANS: ComplianceScan[] = [];
const EMPTY_RESULTS: ComplianceCheckResult[] = [];

const CompliancePage: React.FC = () => {
  const { t, i18n } = useTranslation('plugin__baseline-security-console-plugin');
  const [baselines, loaded, baselineError] = useK8sWatchResource<ClusterBaseline[]>({
    groupVersionKind: ClusterBaselineGVK,
    isList: true,
  });
  // CRD requires metadata.name == "cluster". Do not fall back to list order:
  // a foreign-named object must not drive patches or suite-scoped watches.
  const baseline = baselines?.find((b) => b.metadata.name === CLUSTER_BASELINE_NAME);
  const profiles = baseline?.spec.profiles;
  const tailored = baseline?.spec.tailoredProfiles;
  // Content keys: status-only CR updates reallocate spec arrays with the same
  // membership. Identity deps would rebuild suiteSel (and re-open CCR/scan
  // watches) on every reconcile even when owned suites did not change.
  const profilesKey = (profiles ?? []).join('\0');
  const tailoredKey = (tailored ?? []).join('\0');
  // Suite selector depends on the baseline; wait for baseline load so we do not
  // briefly open an unfiltered full-namespace CCR watch.
  const suiteSel = React.useMemo(
    () => (loaded ? ownedSuiteSelector(profiles, tailored) : undefined),
    // profiles/tailored read from the latest render when keys change.
    // eslint-disable-next-line react-hooks/exhaustive-deps -- content keys
    [loaded, profilesKey, tailoredKey],
  );
  // No owned suites (or baseline still loading): skip list watches entirely.
  // useK8sWatchResource(null) returns empty/loaded without a namespace list.
  // One shared builder so the two suite-scoped list watches cannot drift.
  const listWatch = React.useCallback(
    (groupVersionKind: WatchK8sResource['groupVersionKind']): WatchK8sResource | null =>
      loaded && suiteSel
        ? { groupVersionKind, isList: true, namespace: COMPLIANCE_NAMESPACE, selector: suiteSel }
        : null,
    [loaded, suiteSel],
  );
  const scansWatch = React.useMemo(() => listWatch(ComplianceScanGVK), [listWatch]);
  const resultsWatch = React.useMemo(() => listWatch(ComplianceCheckResultGVK), [listWatch]);
  const [scans, , scansError] = useK8sWatchResource<ComplianceScan[]>(scansWatch);
  const [checkResults, checkResultsHookLoaded, checkResultsError] =
    useK8sWatchResource<ComplianceCheckResult[]>(resultsWatch);
  // null watch reports loaded=true immediately; wait for the baseline (and for
  // the suite-scoped list when suites are selected) before treating results ready.
  const checkResultsLoaded = loaded && (!suiteSel || checkResultsHookLoaded);
  const [rescanning, setRescanning] = React.useState(false);
  // Sync guard: React state alone cannot block a second click before re-render.
  const rescanningRef = React.useRef(false);
  // Monotonic token so a second click still mutates the annotation (CO watches
  // changes) even when both clicks land in the same millisecond.
  const rescanSeq = React.useRef(0);
  const [rescanError, setRescanError] = React.useState<string | null>(null);
  const [rescanStarted, setRescanStarted] = React.useState(false);
  // Success (popup-blocked download) is info; failure must be danger so it is
  // not mistaken for a soft notice.
  const [exportNotice, setExportNotice] = React.useState<{
    message: string;
    variant: 'info' | 'danger';
  } | null>(null);
  const [exporting, setExporting] = React.useState(false);
  // Sync guard: React state alone cannot block a second click before re-render,
  // and each click would otherwise pin another HTML-report blob URL for 60s.
  const exportingRef = React.useRef(false);
  // Auto-dismiss non-error banners so rescan/export feedback does not stick.
  useAutoDismiss(rescanStarted, !!rescanError, () => setRescanStarted(false));
  useAutoDismiss(exportNotice, exportNotice?.variant === 'danger', () => setExportNotice(null));
  const [canRescan, canRescanLoading] = useAccessReview({
    group: 'compliance.openshift.io',
    resource: 'compliancescans',
    verb: 'patch',
    namespace: COMPLIANCE_NAMESPACE,
  });
  const rescanWatchError = errorMessage(baselineError) ?? errorMessage(scansError);
  const watchError = rescanWatchError ?? errorMessage(checkResultsError);

  // Selector already scopes to owned suites; keep stable aliases for rescan/export.
  const ownedScans = scans ?? EMPTY_SCANS;
  const ownedResults = checkResults ?? EMPTY_RESULTS;

  const noOwnedScansReason = (): string => {
    if (!baseline) {
      return t('Baseline not configured');
    }
    if (scanningDisabled(baseline)) {
      return t('No owned scans to rescan yet. Enable a profile first.');
    }
    return t('No owned scans yet. The first scan starts once profiles are bound.');
  };

  const rescan = async () => {
    if (rescanningRef.current) return;
    // Button is disabled when there are no scans; still refuse a no-op path so a
    // race (scans unmounted mid-click) does not look like a successful rescan.
    if (!ownedScans.length) {
      setRescanError(noOwnedScansReason());
      return;
    }
    rescanningRef.current = true;
    setRescanning(true);
    setRescanError(null);
    setRescanStarted(false);
    // Unique value so a second click still mutates the annotation (CO watches changes).
    rescanSeq.current += 1;
    const token = String(rescanSeq.current);
    // allSettled never rejects; rejections land in the results array.
    try {
      const results = await Promise.allSettled(
        ownedScans.map((s) =>
          k8sPatch({
            model: ComplianceScanModel,
            resource: s,
            data: rescanPatch(s.metadata.annotations != null, token, s.metadata.resourceVersion),
          }),
        ),
      );
      const failed = results.filter((r): r is PromiseRejectedResult => r.status === 'rejected');
      const succeeded = results.length - failed.length;
      // Partial success is real for multi-scan suites (platform + node): some
      // patches land while others 403/409. Surface both signals so the admin
      // knows rescans that did start are running, not that nothing happened.
      if (succeeded > 0) {
        setRescanStarted(true);
      }
      if (failed.length) {
        // Surface the first rejection so a 403/409 says what failed, not just a count.
        const detail = errorMessage(failed[0].reason);
        const counts = {
          formattedCount: formatCount(failed.length, i18n.language),
          formattedTotal: formatCount(results.length, i18n.language),
        };
        setRescanError(
          detail
            ? t('Failed to rescan {{formattedCount}} of {{formattedTotal}} scans: {{detail}}', {
                ...counts,
                detail,
              })
            : t('Failed to rescan {{formattedCount}} of {{formattedTotal}} scans. Check permissions and try again.', counts),
        );
      }
    } catch (e) {
      // allSettled covers per-scan rejections; this catches a synchronous throw
      // while building the patch calls, which must not become an unhandled
      // rejection with no banner (finally alone would only reset the spinner).
      setRescanError(errorMessage(e) ?? t('Failed to start rescan.'));
    } finally {
      rescanningRef.current = false;
      setRescanning(false);
    }
  };

  const exportReport = async () => {
    if (exportingRef.current || !baseline) return;
    exportingRef.current = true;
    setExporting(true);
    setExportNotice(null);
    try {
      let buildReportHtml: typeof import('../report').buildReportHtml;
      try {
        ({ buildReportHtml } = await import(
          /* webpackChunkName: "report" */ '../report'
        ));
      } catch {
        setExportNotice({
          variant: 'danger',
          message: t('Failed to load the report exporter.'),
        });
        return;
      }
      try {
        const html = buildReportHtml(
          baseline,
          ownedResults,
          new Date(),
          t,
          i18n.language,
        );
        const blob = new Blob([html], { type: 'text/html;charset=utf-8' });
        // Prefer a blob URL over document.write: no blank-window document
        // mutation, and opener is dropped when available. openBlobInTab
        // revokes on every path (blocked popup, throw, grace timeout).
        const tab = openBlobInTab(blob);
        if (!tab.opened) {
          // Popup blockers should not turn export into a silent no-op.
          downloadBlob(blob, 'compliance-report.html');
          setExportNotice({
            variant: 'info',
            message: t(
              'Report downloaded as compliance-report.html (popup was blocked).',
            ),
          });
        }
      } catch (e) {
        // DOM / serialization failures must not leave a blank click.
        setExportNotice({
          variant: 'danger',
          message: errorMessage(e) ?? t('Failed to export compliance report.'),
        });
      }
    } finally {
      exportingRef.current = false;
      setExporting(false);
    }
  };

  // One watch of ComplianceCheckResults for the whole page tree: Export report,
  // Overview (recent changes / weighted scores), and Results share it instead of
  // each tab opening a parallel list watch of the same large CR set.
  // Pass ownedResults so tabs skip a second full-namespace ownership scan.
  const ctx = React.useMemo(
    () => ({
      baseline,
      // Treat the baseline as resolved once its watch loads OR errors, so a
      // failed baseline watch (RBAC-denied, CRD absent) that leaves loaded=false
      // does not perpetually skeleton the tab bodies. The error itself is shown
      // in the page banner; the tabs then fall to their empty/error state.
      loaded: loaded || !!baselineError,
      checkResults: ownedResults,
      checkResultsLoaded,
      checkResultsError,
    }),
    // Watch payloads are replaced on update, not mutated in place.
    // eslint-disable-next-line react-hooks/preserve-manual-memoization -- watch objects are replaced
    [baseline, loaded, baselineError, ownedResults, checkResultsLoaded, checkResultsError],
  );

  // Page component types are module-level (stable). Only labels depend on t.
  const pages = React.useMemo(
    () => [
      { href: '', name: t('Overview'), component: OverviewRoute },
      { href: 'results', name: t('Results'), component: ResultsRoute },
      { href: 'remediations', name: t('Remediations'), component: RemediationsRoute },
      { href: 'profiles', name: t('Profiles'), component: ProfilesRoute },
    ],
    [t],
  );

  const exportDisabled = !checkResultsLoaded || !!checkResultsError || exporting;
  const exportDisabledReason = exporting
    ? undefined
    : checkResultsError
      ? t('Export is unavailable while check results fail to load.')
      : !checkResultsLoaded
        ? t('Waiting for check results to load.')
        : undefined;

  const rescanDisabled =
    rescanning ||
    !loaded ||
    !ownedScans.length ||
    !canRescan ||
    canRescanLoading ||
    !!rescanWatchError;
  let rescanDisabledReason: string | undefined;
  if (!rescanning) {
    if (rescanWatchError) {
      rescanDisabledReason = t('Rescan is unavailable while compliance data fails to load.');
    } else if (!loaded) {
      rescanDisabledReason = t('Waiting for compliance data to load.');
    } else if (canRescanLoading) {
      rescanDisabledReason = t('Checking permissions…');
    } else if (!canRescan) {
      rescanDisabledReason = t('You do not have permission to rescan.');
    } else if (!ownedScans.length) {
      rescanDisabledReason = noOwnedScansReason();
    }
  }

  return (
    <BaselineContext.Provider value={ctx}>
      <PageSection hasBodyWrapper={false}>
        <Flex
          justifyContent={{ default: 'justifyContentSpaceBetween' }}
          alignItems={{ default: 'alignItemsFlexStart' }}
          flexWrap={{ default: 'wrap' }}
          gap={{ default: 'gapMd' }}
        >
          <FlexItem flex={{ default: 'flex_1' }} style={{ minWidth: 200 }}>
            <Title headingLevel="h1">{t('Compliance')}</Title>
            <Content component="p">
              {t('Cluster benchmark compliance, scanned by the Compliance Operator.')}
            </Content>
          </FlexItem>
          <FlexItem>
            <Flex gap={{ default: 'gapSm' }} flexWrap={{ default: 'wrap' }}>
              {baseline &&
                withDisabledTip(
                  exportDisabled ? exportDisabledReason : undefined,
                  <Button
                    variant="secondary"
                    icon={<DownloadIcon />}
                    isDisabled={exportDisabled}
                    isLoading={exporting}
                    onClick={() => {
                      void exportReport();
                    }}
                  >
                    {t('Export HTML report')}
                  </Button>,
                )}
              {withDisabledTip(
                rescanDisabled && rescanDisabledReason ? rescanDisabledReason : undefined,
                <Button
                  variant="secondary"
                  onClick={() => {
                    void rescan();
                  }}
                  isDisabled={rescanDisabled}
                  isLoading={rescanning}
                >
                  {t('Rescan now')}
                </Button>,
              )}
            </Flex>
          </FlexItem>
        </Flex>
        {watchError && (
          <Alert
            variant="danger"
            isInline
            isLiveRegion
            title={t('Failed to load compliance data.')}
            style={{ marginTop: 'var(--pf-t--global--spacer--md)' }}
          >
            {watchError}
          </Alert>
        )}
        {exportNotice && (
          <Alert
            variant={exportNotice.variant}
            isInline
            isLiveRegion
            title={exportNotice.message}
            style={{ marginTop: 'var(--pf-t--global--spacer--md)' }}
            actionClose={
              <AlertActionCloseButton
                aria-label={t('Close')}
                onClose={() => setExportNotice(null)}
              />
            }
          />
        )}
        {rescanError && (
          <Alert
            variant={rescanStarted ? 'warning' : 'danger'}
            isInline
            isLiveRegion
            title={rescanError}
            style={{ marginTop: 'var(--pf-t--global--spacer--md)' }}
            actionClose={
              <AlertActionCloseButton
                aria-label={t('Close')}
                onClose={() => {
                  setRescanError(null);
                  setRescanStarted(false);
                }}
              />
            }
          >
            {rescanStarted
              ? t('Some scans did start. Results will update when those scans complete.')
              : undefined}
          </Alert>
        )}
        {rescanStarted && !rescanError && (
          <Alert
            variant="success"
            isInline
            isLiveRegion
            title={t('Rescan started. Results will update when the scan completes.')}
            style={{ marginTop: 'var(--pf-t--global--spacer--md)' }}
            actionClose={
              <AlertActionCloseButton
                aria-label={t('Close')}
                onClose={() => setRescanStarted(false)}
              />
            }
          />
        )}
      </PageSection>
      <HorizontalNav pages={pages} />
    </BaselineContext.Provider>
  );
};

export default CompliancePage;
