import * as React from 'react';
import { useTranslation } from 'react-i18next';
import {
  HorizontalNav,
  k8sPatch,
  useAccessReview,
  useK8sWatchResource,
} from '@openshift-console/dynamic-plugin-sdk';
import {
  Alert,
  AlertActionCloseButton,
  Button,
  PageSection,
  Content,
  Split,
  SplitItem,
  Title,
  Tooltip,
} from '@patternfly/react-core';
import {
  ClusterBaseline,
  ClusterBaselineGVK,
  ComplianceCheckResult,
  ComplianceCheckResultGVK,
  ComplianceScanGVK,
  ComplianceScanModel,
  isOwnedByBaseline,
} from '../models';
import { buildReportHtml, errorMessage, rescanPatch } from '../utils';
import {
  BaselineContext,
  OverviewRoute,
  ProfilesRoute,
  RemediationsRoute,
  ResultsRoute,
} from '../baselineContext';

type Scan = {
  metadata: {
    name: string;
    namespace: string;
    labels?: Record<string, string>;
    annotations?: Record<string, string>;
    resourceVersion?: string;
  };
};

const CompliancePage: React.FC = () => {
  const { t } = useTranslation('plugin__baseline-security-console-plugin');
  const [baselines, loaded, baselineError] = useK8sWatchResource<ClusterBaseline[]>({
    groupVersionKind: ClusterBaselineGVK,
    isList: true,
  });
  const [scans, , scansError] = useK8sWatchResource<Scan[]>({
    groupVersionKind: ComplianceScanGVK,
    isList: true,
    namespace: 'openshift-compliance',
  });
  const [checkResults, checkResultsLoaded, checkResultsError] = useK8sWatchResource<ComplianceCheckResult[]>({
    groupVersionKind: ComplianceCheckResultGVK,
    isList: true,
    namespace: 'openshift-compliance',
  });
  // CRD requires metadata.name == "cluster"; prefer that over list order.
  const baseline =
    baselines?.find((b) => b.metadata.name === 'cluster') ?? baselines?.[0];
  const [rescanning, setRescanning] = React.useState(false);
  const [rescanError, setRescanError] = React.useState<string | null>(null);
  const [rescanStarted, setRescanStarted] = React.useState(false);
  const [canRescan, canRescanLoading] = useAccessReview({
    group: 'compliance.openshift.io',
    resource: 'compliancescans',
    verb: 'patch',
    namespace: 'openshift-compliance',
  });
  const rescanWatchError = errorMessage(baselineError) ?? errorMessage(scansError);
  const watchError = rescanWatchError ?? errorMessage(checkResultsError);

  const profiles = baseline?.spec.profiles;
  const tailored = baseline?.spec.tailoredProfiles;
  const ownedScans = React.useMemo(
    () => (scans ?? []).filter((s) => isOwnedByBaseline(s.metadata.labels, profiles, tailored)),
    [scans, profiles, tailored],
  );
  const ownedResults = React.useMemo(
    () =>
      (checkResults ?? []).filter((r) =>
        isOwnedByBaseline(r.metadata.labels, profiles, tailored),
      ),
    [checkResults, profiles, tailored],
  );

  const rescan = async () => {
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
        setRescanError(
          detail
            ? t('Failed to rescan {{count}} of {{total}} scans: {{detail}}', {
                count: failed.length,
                total: results.length,
                detail,
              })
            : t('Failed to rescan {{count}} of {{total}} scans. Check permissions and try again.', {
                count: failed.length,
                total: results.length,
              }),
        );
      }
    } finally {
      setRescanning(false);
    }
  };

  // One watch of ComplianceCheckResults for the whole page tree: Export report,
  // Overview (recent changes / weighted scores), and Results share it instead of
  // each tab opening a parallel list watch of the same large CR set.
  const ctx = React.useMemo(
    () => ({
      baseline,
      loaded,
      checkResults,
      checkResultsLoaded,
      checkResultsError,
    }),
    [baseline, loaded, checkResults, checkResultsLoaded, checkResultsError],
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
    rescanning || !ownedScans.length || !canRescan || canRescanLoading || !!rescanWatchError;
  let rescanDisabledReason: string | undefined;
  if (!rescanning) {
    if (rescanWatchError) {
      rescanDisabledReason = t('Rescan is unavailable while compliance data fails to load.');
    } else if (canRescanLoading) {
      rescanDisabledReason = t('Checking permissions…');
    } else if (!canRescan) {
      rescanDisabledReason = t('You do not have permission to rescan.');
    } else if (!ownedScans.length) {
      rescanDisabledReason = t('No owned scans to rescan yet. Enable a profile first.');
    }
  }

  // Wrap disabled controls so tooltips still receive pointer events.
  const withDisabledTip = (tip: string | undefined, child: React.ReactElement) =>
    tip ? (
      <Tooltip content={tip}>
        <span style={{ display: 'inline-block' }}>{child}</span>
      </Tooltip>
    ) : (
      child
    );

  return (
    <BaselineContext.Provider value={ctx}>
      <PageSection hasBodyWrapper={false}>
        <Split hasGutter>
          <SplitItem isFilled>
            <Title headingLevel="h1">{t('Compliance')}</Title>
            <Content component="p">
              {t('Cluster benchmark compliance, scanned by the Compliance Operator.')}
            </Content>
          </SplitItem>
          <SplitItem>
            {baseline &&
              withDisabledTip(
                exportDisabled ? exportDisabledReason : undefined,
                <Button
                  variant="secondary"
                  style={{ marginRight: 'var(--pf-t--global--spacer--sm)' }}
                  isDisabled={exportDisabled}
                  onClick={() => {
                    const html = buildReportHtml(baseline, ownedResults, new Date(), t);
                    const w = window.open('', '_blank');
                    if (w) {
                      w.opener = null;
                      w.document.write(html);
                      w.document.close();
                    } else {
                      // Popup blockers should not turn export into a silent no-op.
                      const url = URL.createObjectURL(
                        new Blob([html], { type: 'text/html;charset=utf-8' }),
                      );
                      const a = document.createElement('a');
                      a.href = url;
                      a.download = 'compliance-report.html';
                      a.style.display = 'none';
                      document.body.appendChild(a);
                      a.click();
                      a.remove();
                      window.setTimeout(() => URL.revokeObjectURL(url), 0);
                    }
                  }}
                >
                  {t('Export report')}
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
          </SplitItem>
        </Split>
        {watchError && (
          <Alert
            variant="danger"
            isInline
            title={t('Failed to load compliance data.')}
            style={{ marginTop: 'var(--pf-t--global--spacer--md)' }}
          >
            {watchError}
          </Alert>
        )}
        {rescanError && (
          <Alert
            variant={rescanStarted ? 'warning' : 'danger'}
            isInline
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
