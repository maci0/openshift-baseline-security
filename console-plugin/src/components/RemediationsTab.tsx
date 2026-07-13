import * as React from 'react';
import { useTranslation } from 'react-i18next';
import {
  k8sPatch,
  useAccessReview,
  useK8sWatchResource,
  WatchK8sResource,
} from '@openshift-console/dynamic-plugin-sdk';
import {
  Alert,
  AlertActionCloseButton,
  Bullseye,
  Button,
  ClipboardCopyButton,
  CodeBlock,
  CodeBlockAction,
  CodeBlockCode,
  EmptyState,
  EmptyStateBody,
  Flex,
  FlexItem,
  HelperText,
  HelperTextItem,
  Label,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  PageSection,
  Spinner,
  Switch,
  Tooltip,
} from '@patternfly/react-core';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';
import {
  CheckCircleIcon,
  ExclamationCircleIcon,
  ExclamationTriangleIcon,
  InProgressIcon,
  MinusCircleIcon,
} from '@patternfly/react-icons';
import {
  ClusterBaseline,
  ClusterBaselineModel,
  COMPLIANCE_NAMESPACE,
  ComplianceRemediation,
  ComplianceRemediationGVK,
  ComplianceRemediationModel,
  ownedSuiteSelector,
} from '../models';
import { formatCount } from '../dates';
import { errorMessage } from '../errors';
import {
  batchApplyPatch,
  batchApplyRequested,
  remediationApplyPatch,
  resourceVersionTest,
} from '../patches';
import {
  compareRemediationsForApplyOrder,
  isNodeRemediation,
  missingDependencySummary,
  remediationObjectText,
} from '../remediation';
import BaselineNotConfigured from './BaselineNotConfigured';
import { regionFocusProps, restoreFocus, withDisabledTip } from './DisabledTip';
import { SUCCESS_DISMISS_MS } from './feedback';

// Stable empty list when the suite-scoped watch is inactive.
const EMPTY_REMEDIATIONS: ComplianceRemediation[] = [];

// Sub-row detail text (MissingDependencies summary / Error detail).
const detailStyle: React.CSSProperties = {
  marginTop: 2,
  fontSize: 'var(--pf-t--global--font--size--sm)',
  color: 'var(--pf-t--global--text--color--subtle)',
  overflowWrap: 'anywhere',
};

// Color + icon so state is not color-only (matches Results status labels).
const stateStyle: Record<
  string,
  { color: React.ComponentProps<typeof Label>['color']; icon: React.ReactElement }
> = {
  Applied: { color: 'green', icon: <CheckCircleIcon /> },
  NotApplied: { color: 'grey', icon: <MinusCircleIcon /> },
  Error: { color: 'red', icon: <ExclamationCircleIcon /> },
  Outdated: { color: 'orange', icon: <ExclamationTriangleIcon /> },
  MissingDependencies: { color: 'orange', icon: <ExclamationTriangleIcon /> },
};
const defaultStateStyle = { color: 'grey' as const, icon: <MinusCircleIcon /> };

// CR applicationState enums stay English for logic; only the Label text is localized.
const stateDisplayTitle = (state: string, t: (k: string) => string): string => {
  switch (state) {
    case 'Applied':
      return t('Applied');
    case 'NotApplied':
      return t('Not applied');
    case 'Error':
      return t('Error');
    case 'Outdated':
      return t('Outdated');
    case 'MissingDependencies':
      return t('Missing dependencies');
    default:
      return state;
  }
};

const RemediationsTab: React.FC<{
  baseline?: ClusterBaseline;
  // Baseline watch from CompliancePage; remediations list has its own loaded flag.
  baselineLoaded?: boolean;
}> = ({ baseline, baselineLoaded = true }) => {
  const { t, i18n } = useTranslation('plugin__baseline-security-console-plugin');
  const profiles = baseline?.spec.profiles;
  const tailoredProfiles = baseline?.spec.tailoredProfiles;
  // Content keys: status-only CR updates reallocate spec arrays with the same
  // membership; identity deps would rebuild the remediation watch every reconcile.
  const profilesKey = (profiles ?? []).join('\0');
  const tailoredKey = (tailoredProfiles ?? []).join('\0');
  // Suite selector scopes the watch to this baseline; skip full-namespace list
  // when no suites are selected (avoids foreign remediations in the browser).
  // profiles/tailoredProfiles are read when keys change (content-stable deps).
  const remediationsWatch = React.useMemo((): WatchK8sResource | null => {
    const selector = ownedSuiteSelector(profiles, tailoredProfiles);
    if (!selector) {
      return null;
    }
    return {
      groupVersionKind: ComplianceRemediationGVK,
      isList: true,
      namespace: COMPLIANCE_NAMESPACE,
      selector,
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- content keys
  }, [profilesKey, tailoredKey]);
  const [remediations, loaded, loadError] =
    useK8sWatchResource<ComplianceRemediation[]>(remediationsWatch);
  const [confirming, setConfirming] = React.useState<ComplianceRemediation | null>(null);
  const [unapplying, setUnapplying] = React.useState<ComplianceRemediation | null>(null);
  const [autoApplyConfirming, setAutoApplyConfirming] = React.useState(false);
  const [batchConfirming, setBatchConfirming] = React.useState(false);
  const [viewing, setViewing] = React.useState<ComplianceRemediation | null>(null);
  // Copy-to-clipboard feedback for the rendered-object modal.
  const [copied, setCopied] = React.useState(false);
  const [busy, setBusy] = React.useState(false);
  // Sync guard: React state alone cannot block a second click before re-render.
  const busyRef = React.useRef(false);
  // Return focus to the control that opened a confirm/view modal (WCAG 2.4.3).
  const returnFocusRef = React.useRef<HTMLElement | null>(null);
  // Focus fallback when a modal's trigger unmounts on success (e.g. the batch
  // button is replaced by an in-progress label): restore to the tab region.
  const regionRef = React.useRef<HTMLDivElement>(null);
  const modalWasOpen = React.useRef(false);
  const [error, setError] = React.useState<string | null>(null);
  // Success feedback after modal close so apply/unapply/batch is not a silent no-op.
  const [success, setSuccess] = React.useState<string | null>(null);
  // Auto-dismiss success so the banner does not stick after the user moves on.
  React.useEffect(() => {
    if (!success) return;
    const id = window.setTimeout(() => setSuccess(null), SUCCESS_DISMISS_MS);
    return () => window.clearTimeout(id);
  }, [success]);
  const [canApply, canApplyLoading] = useAccessReview({
    group: 'compliance.openshift.io',
    resource: 'complianceremediations',
    verb: 'patch',
    namespace: COMPLIANCE_NAMESPACE,
  });
  const [canEditBaseline, canEditBaselineLoading] = useAccessReview({
    group: 'baselinesecurity.openshift.io',
    resource: 'clusterbaselines',
    verb: 'patch',
  });
  const watchError = errorMessage(loadError);
  // status.remediationBatch is the live batch; the annotation is the one-shot
  // request (may exist before status persists). Empty/comma-only values do not
  // start a batch on the operator and must not lock the UI as "in progress".
  const batchInProgress =
    baseline?.status?.remediationBatch != null ||
    batchApplyRequested(baseline?.metadata.annotations);

  // Selector already scopes to owned suites.
  const owned = remediations ?? EMPTY_REMEDIATIONS;

  const run = async (fn: () => Promise<unknown>, failMsg: string): Promise<boolean> => {
    if (busyRef.current) return false;
    busyRef.current = true;
    setBusy(true);
    setError(null);
    try {
      await fn();
      return true;
    } catch (e) {
      // k8sPatch often rejects with { message } Status objects, not Error.
      setError(errorMessage(e) ?? failMsg);
      return false;
    } finally {
      busyRef.current = false;
      setBusy(false);
    }
  };

  // Apply-order: prerequisites (applyable) before MissingDependencies so the
  // table guides the admin to fix blockers first (openspec guided-remediation).
  const ordered = React.useMemo(
    () => [...owned].sort(compareRemediationsForApplyOrder),
    [owned],
  );

  // Node-remediation membership computed once (isNodeRemediation parses the
  // scan-name label and runs a name regex). Reused by the reboot-warning gate,
  // the batchable filter, and the per-row Kind badge so the list is not walked
  // through isNodeRemediation 3-4x per render.
  const nodeNames = React.useMemo(
    () => new Set(owned.filter(isNodeRemediation).map((r) => r.metadata.name)),
    [owned],
  );

  // Node remediations that can be batch-applied: owned, not yet applied, not
  // blocked on dependencies, and not already Applied (re-batching an Applied
  // row during an unapply lag would re-set apply=true). Batching pauses the
  // pool so nodes reboot once.
  const batchable = React.useMemo(
    () =>
      ordered.filter((r) => {
        if (r.spec.apply || !nodeNames.has(r.metadata.name)) {
          return false;
        }
        const state = r.status?.applicationState;
        return state !== 'MissingDependencies' && state !== 'Applied';
      }),
    [ordered, nodeNames],
  );

  const doBatchApply = () => {
    if (!baseline || batchInProgress || batchable.length === 0) return;
    // Empty patch (all names invalid/filtered) would succeed as a no-op RV-only
    // patch and look like the batch started when nothing was annotated.
    const batchPatch = batchApplyPatch(
      !!baseline.metadata.annotations,
      batchable.map((r) => r.metadata.name),
    );
    if (!batchPatch.length) {
      setError(t('No valid remediations to batch-apply.'));
      return;
    }
    void run(
      () =>
        k8sPatch({
          model: ClusterBaselineModel,
          resource: baseline,
          data: [...resourceVersionTest(baseline.metadata.resourceVersion), ...batchPatch],
        }),
      t('Failed to start batch apply.'),
    ).then((ok) => {
      if (!ok) return;
      setBatchConfirming(false);
      setSuccess(
        t('Batch apply started. Nodes will reboot once when the pools resume.'),
      );
    });
  };

  const setApply = (rem: ComplianceRemediation, apply: boolean) =>
    run(
      () =>
        k8sPatch({
          model: ComplianceRemediationModel,
          resource: rem,
          data: [
            ...resourceVersionTest(rem.metadata.resourceVersion),
            { op: 'add', path: '/spec/apply', value: apply },
          ],
        }),
      t('Failed to update remediation.'),
    );

  const toggleAutoApply = async (checked: boolean): Promise<boolean> => {
    if (!baseline) return false;
    return run(
      () =>
        k8sPatch({
          model: ClusterBaselineModel,
          resource: baseline,
          data: [
            ...resourceVersionTest(baseline.metadata.resourceVersion),
            ...remediationApplyPatch(baseline.spec.remediation != null, checked),
          ],
        }),
      t('Failed to update auto-apply setting.'),
    );
  };

  // Turning auto-apply on can reboot nodes after every scan; confirm first.
  // Turning it off is safe and applies immediately.
  const onAutoApplyChange = (_e: unknown, checked: boolean) => {
    if (checked) {
      setError(null);
      setSuccess(null);
      setAutoApplyConfirming(true);
      return;
    }
    void toggleAutoApply(false).then((ok) => {
      if (ok) {
        setSuccess(t('Auto-apply remediations disabled.'));
      }
    });
  };

  // Include viewing so page-top alerts stay behind the object modal and errors
  // (clipboard, etc.) render inside the open modal instead of under the backdrop.
  const anyModalOpen =
    !!confirming || !!unapplying || batchConfirming || autoApplyConfirming || !!viewing;

  // Restore focus to the trigger when every remediations modal has closed.
  React.useEffect(() => {
    if (anyModalOpen) {
      modalWasOpen.current = true;
      return;
    }
    if (!modalWasOpen.current) return;
    modalWasOpen.current = false;
    const el = returnFocusRef.current;
    returnFocusRef.current = null;
    restoreFocus(el, regionRef);
  }, [anyModalOpen]);

  const baselineEditDisabled =
    !baselineLoaded || !baseline || !canEditBaseline || canEditBaselineLoading || busy;
  let baselineEditDisabledReason: string | undefined;
  if (!busy) {
    if (!baselineLoaded) {
      baselineEditDisabledReason = t('Waiting for compliance data to load.');
    } else if (canEditBaselineLoading) {
      baselineEditDisabledReason = t('Checking permissions…');
    } else if (!canEditBaseline) {
      baselineEditDisabledReason = t('You do not have permission to edit the baseline.');
    } else if (!baseline) {
      baselineEditDisabledReason = t('Baseline not configured');
    }
  }

  const applyDisabled = !canApply || canApplyLoading || busy;
  let applyDisabledReason: string | undefined;
  if (!busy) {
    if (canApplyLoading) {
      applyDisabledReason = t('Checking permissions…');
    } else if (!canApply) {
      applyDisabledReason = t('You do not have permission to apply remediations.');
    }
  }

  return (
    <PageSection ref={regionRef} tabIndex={-1}>
      {/* Only when node remediations exist: during loading / empty / platform-only
          the reboot warning is irrelevant (or misleading) noise. */}
      {nodeNames.size > 0 && (
        <Alert
          variant="warning"
          isInline
          isLiveRegion
          title={t(
            'Node remediations render into MachineConfigs. Applying them triggers rolling node reboots.',
          )}
        />
      )}
      {watchError && (
        <Alert
          variant="danger"
          isInline
          isLiveRegion
          title={t('Failed to load remediations.')}
          style={{ marginTop: 'var(--pf-t--global--spacer--md)' }}
        >
          {watchError}
        </Alert>
      )}
      {/* Shown page-top only when no modal is open; the modals render their own
          copy of this error so a failed apply is not hidden behind the backdrop. */}
      {error && !anyModalOpen && (
        <Alert
          variant="danger"
          isInline
          isLiveRegion
          title={error}
          style={{ marginTop: 'var(--pf-t--global--spacer--md)' }}
          actionClose={
            <AlertActionCloseButton
              aria-label={t('Close')}
              onClose={() => setError(null)}
            />
          }
        />
      )}
      {success && !anyModalOpen && (
        <Alert
          variant="success"
          isInline
          isLiveRegion
          title={success}
          style={{ marginTop: 'var(--pf-t--global--spacer--md)' }}
          actionClose={
            <AlertActionCloseButton
              aria-label={t('Close')}
              onClose={() => setSuccess(null)}
            />
          }
        />
      )}
      <Flex
        justifyContent={{ default: 'justifyContentSpaceBetween' }}
        alignItems={{ default: 'alignItemsCenter' }}
        flexWrap={{ default: 'wrap' }}
        gap={{ default: 'gapMd' }}
        style={{ marginTop: 'var(--pf-t--global--spacer--md)' }}
      >
        <FlexItem>
          {batchInProgress ? (
            // Label alone left admins unsure whether the batch was stuck or
            // still running; explain the pause/resume behavior next to it.
            // Icon + text so status is not color-only (matches Results labels).
            <div>
              <Label color="blue" icon={<InProgressIcon />}>
                {t('Batch apply in progress')}
              </Label>
              <HelperText style={{ marginTop: 'var(--pf-t--global--spacer--xs)' }}>
                <HelperTextItem>
                  {t(
                    'MachineConfigPools are paused while node remediations apply. This clears when the batch finishes.',
                  )}
                </HelperTextItem>
              </HelperText>
            </div>
          ) : batchable.length > 0 ? (
            withDisabledTip(
              baselineEditDisabledReason,
              <Button
                variant="secondary"
                isDisabled={baselineEditDisabled}
                onClick={(e) => {
                  returnFocusRef.current = e.currentTarget;
                  setError(null);
                  setSuccess(null);
                  setBatchConfirming(true);
                }}
              >
                {t('Batch apply {{count}} node remediation', {
                  count: batchable.length,
                  formattedCount: formatCount(batchable.length, i18n.language),
                })}
              </Button>,
            )
          ) : null}
        </FlexItem>
        <FlexItem>
          {withDisabledTip(
            baselineEditDisabledReason,
            <Switch
              id="auto-apply"
              label={t('Auto-apply remediations after each scan')}
              isChecked={
                autoApplyConfirming || baseline?.spec.remediation?.apply === 'Automatic'
              }
              isDisabled={baselineEditDisabled}
              onChange={(e, checked) => {
                // Capture the switch before the confirm modal steals focus.
                if (checked) {
                  returnFocusRef.current = e.currentTarget;
                }
                onAutoApplyChange(e, checked);
              }}
            />,
          )}
        </FlexItem>
      </Flex>
      <Modal
        variant="small"
        isOpen={autoApplyConfirming}
        onClose={() => {
          // busyRef: setBusy is async; dismiss between busyRef=true and re-render
          // must not drop the confirm modal mid-request.
          if (busyRef.current) return;
          setAutoApplyConfirming(false);
        }}
        aria-labelledby="auto-apply-title"
      >
        <ModalHeader
          title={t('Enable auto-apply remediations?')}
          labelId="auto-apply-title"
        />
        <ModalBody>
          {t(
            'After each scan, available remediations will apply automatically. Node remediations render into MachineConfigs and trigger rolling node reboots.',
          )}
          {error && (
            <Alert
              variant="danger"
              isInline
              isLiveRegion
              title={error}
              style={{ marginTop: 'var(--pf-t--global--spacer--md)' }}
            />
          )}
        </ModalBody>
        <ModalFooter>
          <Button
            variant="danger"
            isDisabled={busy || !canEditBaseline || canEditBaselineLoading}
            isLoading={busy}
            onClick={() => {
              void toggleAutoApply(true).then((ok) => {
                if (!ok) return;
                setAutoApplyConfirming(false);
                setSuccess(t('Auto-apply remediations enabled.'));
              });
            }}
          >
            {t('Enable auto-apply')}
          </Button>
          <Button
            variant="link"
            isDisabled={busy}
            onClick={() => {
              if (busyRef.current) return;
              setAutoApplyConfirming(false);
            }}
          >
            {t('Cancel')}
          </Button>
        </ModalFooter>
      </Modal>
      <Modal
        variant="small"
        isOpen={batchConfirming}
        onClose={() => {
          if (busyRef.current) return;
          setBatchConfirming(false);
        }}
        aria-labelledby="batch-apply-title"
      >
        <ModalHeader title={t('Batch apply node remediations?')} labelId="batch-apply-title" />
        <ModalBody>
          {t(
            'The affected MachineConfigPools are paused, all {{count}} node remediations are applied, then the pools resume so nodes reboot once instead of per remediation. A rescan is required afterwards.',
            {
              count: batchable.length,
              formattedCount: formatCount(batchable.length, i18n.language),
            },
          )}
          {error && (
            <Alert
              variant="danger"
              isInline
              isLiveRegion
              title={error}
              style={{ marginTop: 'var(--pf-t--global--spacer--md)' }}
            />
          )}
        </ModalBody>
        <ModalFooter>
          <Button
            variant="danger"
            isDisabled={busy || !canEditBaseline || canEditBaselineLoading}
            isLoading={busy}
            onClick={doBatchApply}
          >
            {t('Batch apply')}
          </Button>
          <Button
            variant="link"
            isDisabled={busy}
            onClick={() => {
              if (busyRef.current) return;
              setBatchConfirming(false);
            }}
          >
            {t('Cancel')}
          </Button>
        </ModalFooter>
      </Modal>
      {!baselineLoaded || !loaded ? (
        <Bullseye style={{ padding: 'var(--pf-t--global--spacer--xl)' }}>
          <Spinner
            aria-label={
              !baselineLoaded ? t('Loading compliance data') : t('Loading remediations')
            }
          />
        </Bullseye>
      ) : !baseline ? (
        <BaselineNotConfigured style={{ marginTop: 'var(--pf-t--global--spacer--md)' }} />
      ) : owned.length === 0 ? (
        (() => {
          // Same dead-end as Results: "rescan after failures" is wrong when no
          // profile is selected and scans will never run.
          const scanningDisabled =
            (baseline.spec.profiles?.length ?? 0) === 0 &&
            (baseline.spec.tailoredProfiles?.length ?? 0) === 0;
          return (
            <EmptyState
              titleText={
                scanningDisabled ? t('Scanning is disabled') : t('No remediations')
              }
              headingLevel="h2"
              style={{ marginTop: 'var(--pf-t--global--spacer--md)' }}
            >
              <EmptyStateBody>
                {scanningDisabled ? (
                  <>
                    {t('No profiles are selected. Enable a profile to resume scanning.')}{' '}
                    <a href="/baseline-security/profiles">{t('Go to Profiles')}</a>
                  </>
                ) : (
                  <>
                    {t(
                      'The Compliance Operator generates remediations for failing checks that can be auto-fixed. None are available yet; rescan after new failures appear.',
                    )}{' '}
                    <a href="/baseline-security/results">{t('Review check results')}</a>
                    {' · '}
                    <a href="/baseline-security/profiles">{t('Go to Profiles')}</a>
                  </>
                )}
              </EmptyStateBody>
            </EmptyState>
          );
        })()
      ) : (
        <div
          style={{ overflowX: 'auto' }}
          tabIndex={0}
          role="region"
          aria-label={t('Remediations')}
          {...regionFocusProps}
        >
        <Table variant="compact" aria-label={t('Remediations')}>
          <Thead>
            <Tr>
              <Th>{t('Remediation')}</Th>
              <Th>{t('Kind')}</Th>
              <Th>{t('State')}</Th>
              <Th screenReaderText={t('Object')} />
              <Th screenReaderText={t('Actions')} />
            </Tr>
          </Thead>
          <Tbody>
            {ordered.map((rem) => {
              const state = rem.status?.applicationState ?? 'NotApplied';
              const style = stateStyle[state] ?? defaultStateStyle;
              // Only blocked rows read the dependency annotations; skip the
              // split / JSON.parse (missingDependencySummary) and the Blocked
              // tip interpolation on every other row on every render.
              const isBlocked = state === 'MissingDependencies';
              const depsSummary = isBlocked ? missingDependencySummary(rem) : '';
              const errorDetail = rem.status?.errorMessage?.trim();
              return (
                <Tr key={rem.metadata.name}>
                  <Td dataLabel={t('Remediation')} modifier="breakWord">
                    {rem.metadata.name}
                  </Td>
                  <Td dataLabel={t('Kind')}>
                    {rem.spec.current?.object?.kind ?? '—'}
                    {nodeNames.has(rem.metadata.name) && (
                      <Label isCompact color="orange" style={{ marginInlineStart: 8 }}>
                        {t('reboots nodes')}
                      </Label>
                    )}
                  </Td>
                  <Td dataLabel={t('State')}>
                    <Label
                      isCompact
                      color={style.color}
                      icon={style.icon}
                    >
                      {stateDisplayTitle(state, t)}
                    </Label>
                    {state === 'MissingDependencies' && depsSummary && (
                      <div
                        style={detailStyle}
                      >
                        {depsSummary}
                      </div>
                    )}
                    {state === 'Error' && errorDetail && (
                      <div
                        style={detailStyle}
                      >
                        {errorDetail}
                      </div>
                    )}
                  </Td>
                  <Td dataLabel={t('Object')}>
                    <Button
                      variant="link"
                      isInline
                      aria-label={t('View object for {{name}}', { name: rem.metadata.name })}
                      onClick={(e) => {
                        returnFocusRef.current = e.currentTarget;
                        setError(null);
                        setCopied(false);
                        setViewing(rem);
                      }}
                    >
                      {t('View')}
                    </Button>
                  </Td>
                  <Td dataLabel={t('Actions')}>
                    {rem.spec.apply ? (
                      withDisabledTip(
                        applyDisabledReason,
                        <Button
                          variant="link"
                          isInline
                          isDisabled={applyDisabled}
                          aria-label={t('Unapply {{name}}', { name: rem.metadata.name })}
                          onClick={(e) => {
                            returnFocusRef.current = e.currentTarget;
                            setError(null);
                            setSuccess(null);
                            setUnapplying(rem);
                          }}
                        >
                          {t('Unapply')}
                        </Button>,
                      )
                    ) : state === 'MissingDependencies' ? (
                      // Blocked: applying now would fail; name the dependency so
                      // the admin knows which prerequisite to apply first.
                      <Tooltip
                        content={
                          depsSummary
                            ? t(
                                'Blocked: missing dependency {{deps}}. Apply its remediations first.',
                                { deps: depsSummary },
                              )
                            : t('Blocked: apply the prerequisite remediations first.')
                        }
                      >
                        <Button
                          variant="link"
                          isInline
                          isAriaDisabled
                          aria-label={t('Blocked: {{name}}', { name: rem.metadata.name })}
                        >
                          {t('Blocked')}
                        </Button>
                      </Tooltip>
                    ) : (
                      withDisabledTip(
                        applyDisabledReason,
                        <Button
                          variant="link"
                          isInline
                          isDisabled={applyDisabled}
                          aria-label={t('Apply {{name}}', { name: rem.metadata.name })}
                          onClick={(e) => {
                            returnFocusRef.current = e.currentTarget;
                            setError(null);
                            setSuccess(null);
                            setConfirming(rem);
                          }}
                        >
                          {t('Apply')}
                        </Button>,
                      )
                    )}
                  </Td>
                </Tr>
              );
            })}
          </Tbody>
        </Table>
        </div>
      )}
      <Modal
        variant="small"
        isOpen={!!confirming}
        onClose={() => {
          if (busyRef.current) return;
          setConfirming(null);
        }}
        aria-labelledby="apply-remediation-title"
      >
        <ModalHeader title={t('Apply remediation?')} labelId="apply-remediation-title" />
        <ModalBody>
          {t(
            '{{name}} will be applied to the cluster. A rescan is required afterwards for results to reflect the change.',
            { name: confirming?.metadata.name },
          )}
          {confirming && isNodeRemediation(confirming) && (
            <Alert
              variant="warning"
              isInline
              title={t('This is a node remediation')}
              style={{ marginTop: 'var(--pf-t--global--spacer--md)' }}
            >
              {t(
                'It renders into a MachineConfig; applying it reboots the affected nodes one by one. To batch changes, pause the target MachineConfigPool first (Compute -> MachineConfigPools) and resume it when done.',
              )}
            </Alert>
          )}
          {error && (
            <Alert
              variant="danger"
              isInline
              isLiveRegion
              title={error}
              style={{ marginTop: 'var(--pf-t--global--spacer--md)' }}
            />
          )}
        </ModalBody>
        <ModalFooter>
          <Button
            // Danger only when apply reboots nodes; platform remediations are not destructive.
            variant={
              confirming && isNodeRemediation(confirming) ? 'danger' : 'primary'
            }
            isDisabled={busy || !canApply || canApplyLoading}
            isLoading={busy}
            onClick={() => {
              if (!confirming) return;
              const rem = confirming;
              void (async () => {
                if (await setApply(rem, true)) {
                  setConfirming(null);
                  setSuccess(
                    t('Remediation applied. Use Rescan now above to refresh results.'),
                  );
                }
              })();
            }}
          >
            {t('Apply')}
          </Button>
          <Button
            variant="link"
            isDisabled={busy}
            onClick={() => {
              if (busyRef.current) return;
              setConfirming(null);
            }}
          >
            {t('Cancel')}
          </Button>
        </ModalFooter>
      </Modal>
      <Modal
        variant="small"
        isOpen={!!unapplying}
        onClose={() => {
          if (busyRef.current) return;
          setUnapplying(null);
        }}
        aria-labelledby="unapply-remediation-title"
      >
        <ModalHeader title={t('Unapply remediation?')} labelId="unapply-remediation-title" />
        <ModalBody>
          {t(
            '{{name}} will stop being applied. A rescan is required afterwards for results to reflect the change.',
            { name: unapplying?.metadata.name },
          )}
          {unapplying && isNodeRemediation(unapplying) && (
            <Alert
              variant="warning"
              isInline
              title={t('This is a node remediation')}
              style={{ marginTop: 'var(--pf-t--global--spacer--md)' }}
            >
              {t(
                'It renders into a MachineConfig; unapplying it reboots the affected nodes one by one.',
              )}
            </Alert>
          )}
          {error && (
            <Alert
              variant="danger"
              isInline
              isLiveRegion
              title={error}
              style={{ marginTop: 'var(--pf-t--global--spacer--md)' }}
            />
          )}
        </ModalBody>
        <ModalFooter>
          <Button
            // Node unapply can reboot; match apply severity so the confirm is not understated.
            variant={
              unapplying && isNodeRemediation(unapplying) ? 'danger' : 'secondary'
            }
            isDisabled={busy || !canApply || canApplyLoading}
            isLoading={busy}
            onClick={() => {
              if (!unapplying) return;
              const rem = unapplying;
              void (async () => {
                if (await setApply(rem, false)) {
                  setUnapplying(null);
                  setSuccess(
                    t('Remediation unapplied. Use Rescan now above to refresh results.'),
                  );
                }
              })();
            }}
          >
            {t('Unapply')}
          </Button>
          <Button
            variant="link"
            isDisabled={busy}
            onClick={() => {
              if (busyRef.current) return;
              setUnapplying(null);
            }}
          >
            {t('Cancel')}
          </Button>
        </ModalFooter>
      </Modal>
      <Modal
        variant="medium"
        isOpen={!!viewing}
        onClose={() => {
          setViewing(null);
          setCopied(false);
          // Clipboard errors already shown inline; do not leave them on the page.
          setError(null);
        }}
        aria-labelledby="remediation-object-title"
      >
        <ModalHeader
          title={t('Rendered object')}
          labelId="remediation-object-title"
          description={viewing?.metadata.name}
        />
        <ModalBody>
          {error && (
            <Alert
              variant="danger"
              isInline
              isLiveRegion
              title={error}
              style={{ marginBottom: 'var(--pf-t--global--spacer--md)' }}
              actionClose={
                <AlertActionCloseButton
                  aria-label={t('Close')}
                  onClose={() => setError(null)}
                />
              }
            />
          )}
          {(() => {
            const objectText = viewing ? remediationObjectText(viewing) : '';
            return (
              <CodeBlock
                actions={
                  <CodeBlockAction>
                    <ClipboardCopyButton
                      id="remediation-object-copy"
                      aria-label={copied ? t('Copied') : t('Copy to clipboard')}
                      variant="plain"
                      disabled={!objectText}
                      exitDelay={copied ? 1500 : 600}
                      onTooltipHidden={() => setCopied(false)}
                      onClick={() => {
                        // Only report "Copied" when the write actually succeeds:
                        // clipboard is undefined on insecure origins and writeText
                        // rejects when the document lacks focus / permission. The
                        // object stays on-screen for manual selection either way.
                        const write = navigator.clipboard?.writeText(objectText);
                        if (!write) {
                          setCopied(false);
                          setError(t('Copy to clipboard is unavailable in this browser.'));
                          return;
                        }
                        write.then(
                          () => {
                            setError(null);
                            setCopied(true);
                          },
                          () => {
                            setCopied(false);
                            setError(t('Failed to copy to clipboard.'));
                          },
                        );
                      }}
                    >
                      {copied ? t('Copied') : t('Copy to clipboard')}
                    </ClipboardCopyButton>
                  </CodeBlockAction>
                }
              >
                <CodeBlockCode id="remediation-object-code">
                  {objectText || t('No rendered object.')}
                </CodeBlockCode>
              </CodeBlock>
            );
          })()}
        </ModalBody>
        <ModalFooter>
          <Button
            variant="link"
            onClick={() => {
              setViewing(null);
              setCopied(false);
              setError(null);
            }}
          >
            {t('Close')}
          </Button>
        </ModalFooter>
      </Modal>
    </PageSection>
  );
};

export default RemediationsTab;
