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
  ClusterBaseline,
  ClusterBaselineGVK,
  COMPLIANCE_NAMESPACE,
  ComplianceCheckResult,
  ComplianceCheckResultGVK,
  ComplianceScanGVK,
  ComplianceScanModel,
  ownedSuiteSelector,
} from '../models';
import { formatCount } from '../dates';
import { downloadBlob } from '../download';
import { errorMessage } from '../errors';
import { rescanPatch } from '../patches';
import { buildReportHtml } from '../report';
import { withDisabledTip } from './DisabledTip';
import { SUCCESS_DISMISS_MS } from './feedback';
import {
  BaselineContext,
  OverviewRoute,
  ProfilesRoute,
  RemediationsRoute,
  ResultsRoute,
} from './BaselineContext';

type Scan = {
  metadata: {
    name: string;
    namespace: string;
    labels?: Record<string, string>;
    annotations?: Record<string, string>;
    resourceVersion?: string;
  };
};

// Stable empties so `?? []` does not allocate a new array every render (hooks deps).
const EMPTY_SCANS: Scan[] = [];
const EMPTY_RESULTS: ComplianceCheckResult[] = [];

const CompliancePage: React.FC = () => {
  const { t, i18n } = useTranslation('plugin__baseline-security-console-plugin');
  const [baselines, loaded, baselineError] = useK8sWatchResource<ClusterBaseline[]>({
    groupVersionKind: ClusterBaselineGVK,
    isList: true,
  });
  // CRD requires metadata.name == "cluster"; prefer that over list order.
  const baseline =
    baselines?.find((b) => b.metadata.name === 'cluster') ?? baselines?.[0];
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
  const [scans, , scansError] = useK8sWatchResource<Scan[]>(scansWatch);
  const [checkResults, checkResultsHookLoaded, checkResultsError] =
    useK8sWatchResource<ComplianceCheckResult[]>(resultsWatch);
  // null watch reports loaded=true immediately; wait for the baseline (and for
  // the suite-scoped list when suites are selected) before treating results ready.
  const checkResultsLoaded = loaded && (!suiteSel || checkResultsHookLoaded);
  const [rescanning, setRescanning] = React.useState(false);
  // Sync guard: React state alone cannot block a second click before re-render.
  const rescanningRef = React.useRef(false);
  const [rescanError, setRescanError] = React.useState<string | null>(null);
  const [rescanStarted, setRescanStarted] = React.useState(false);
  // Success (popup-blocked download) is info; failure must be danger so it is
  // not mistaken for a soft notice.
  const [exportNotice, setExportNotice] = React.useState<{
    message: string;
    variant: 'info' | 'danger';
  } | null>(null);
  // Auto-dismiss non-error banners so rescan/export feedback does not stick.
  React.useEffect(() => {
    if (!rescanStarted || rescanError) return;
    const id = window.setTimeout(() => setRescanStarted(false), SUCCESS_DISMISS_MS);
    return () => window.clearTimeout(id);
  }, [rescanStarted, rescanError]);
  React.useEffect(() => {
    if (!exportNotice || exportNotice.variant === 'danger') return;
    const id = window.setTimeout(() => setExportNotice(null), SUCCESS_DISMISS_MS);
    return () => window.clearTimeout(id);
  }, [exportNotice]);
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

  const rescan = async () => {
    if (rescanningRef.current) return;
    // Button is disabled when there are no scans; still refuse a no-op path so a
    // race (scans unmounted mid-click) does not look like a successful rescan.
    if (!ownedScans.length) {
      setRescanError(t('No owned scans to rescan yet. Enable a profile first.'));
      return;
    }
    rescanningRef.current = true;
    setRescanning(true);
    setRescanError(null);
    setRescanStarted(false);
    // Unique value so a second click still mutates the annotation (CO watches changes).
    const token = String(Date.now());
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
        // Surface the first rejection so a 403/409 is actionable, not just a count.
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
    } finally {
      rescanningRef.current = false;
      setRescanning(false);
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

  const exportDisabled = !checkResultsLoaded || !!checkResultsError;
  const exportDisabledReason = checkResultsError
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
      rescanDisabledReason = t('No owned scans to rescan yet. Enable a profile first.');
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
                    onClick={() => {
                      setExportNotice(null);
                      try {
                        const html = buildReportHtml(baseline, ownedResults, new Date(), t);
                        const blob = new Blob([html], { type: 'text/html;charset=utf-8' });
                        // Prefer a blob URL over document.write: no blank-window
                        // document mutation, and opener is dropped when available.
                        // Revoke on every path: a throw after createObjectURL
                        // must not leak the blob URL for the session.
                        const url = URL.createObjectURL(blob);
                        try {
                          // Do NOT pass noopener/noreferrer in the feature string:
                          // per the HTML spec that forces window.open to return null
                          // even when the tab opens, which would make the block below
                          // always take the "popup was blocked" path (false notice +
                          // redundant download + early blob revoke). Open plainly so a
                          // real block is the only null, then drop opener manually (the
                          // report is our own static, script-free, CSP-locked HTML).
                          const w = window.open(url, '_blank');
                          if (w) {
                            w.opener = null;
                            // Keep the blob alive long enough for the tab to load.
                            window.setTimeout(() => URL.revokeObjectURL(url), 60_000);
                          } else {
                            URL.revokeObjectURL(url);
                            // Popup blockers should not turn export into a silent no-op.
                            downloadBlob(blob, 'compliance-report.html');
                            setExportNotice({
                              variant: 'info',
                              message: t(
                                'Report downloaded as compliance-report.html (popup was blocked).',
                              ),
                            });
                          }
                        } catch (openErr) {
                          URL.revokeObjectURL(url);
                          throw openErr;
                        }
                      } catch (e) {
                        // DOM / serialization failures must not leave a blank click.
                        setExportNotice({
                          variant: 'danger',
                          message:
                            errorMessage(e) ?? t('Failed to export compliance report.'),
                        });
                      }
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
