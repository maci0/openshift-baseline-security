import * as React from 'react';
import { useTranslation } from 'react-i18next';
import {
  k8sPatch,
  useAccessReview,
  useK8sWatchResource,
} from '@openshift-console/dynamic-plugin-sdk';
import {
  Alert,
  AlertActionCloseButton,
  Bullseye,
  Button,
  CodeBlock,
  CodeBlockCode,
  EmptyState,
  EmptyStateBody,
  Label,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  PageSection,
  Spinner,
  Split,
  SplitItem,
  Switch,
  Tooltip,
} from '@patternfly/react-core';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';
import {
  ClusterBaseline,
  ClusterBaselineModel,
  ComplianceRemediation,
  ComplianceRemediationGVK,
  ComplianceRemediationModel,
  isOwnedByBaseline,
} from '../models';
import {
  batchApplyPatch,
  errorMessage,
  isNodeRemediation,
  remediationApplyPatch,
  remediationObjectText,
  resourceVersionTest,
} from '../utils';

const stateColor: Record<string, React.ComponentProps<typeof Label>['color']> = {
  Applied: 'green',
  NotApplied: 'grey',
  Error: 'red',
  Outdated: 'orange',
  MissingDependencies: 'orange',
};

const RemediationsTab: React.FC<{ baseline?: ClusterBaseline }> = ({ baseline }) => {
  const { t } = useTranslation('plugin__baseline-security-console-plugin');
  const [remediations, loaded, loadError] = useK8sWatchResource<ComplianceRemediation[]>({
    groupVersionKind: ComplianceRemediationGVK,
    isList: true,
    namespace: 'openshift-compliance',
  });
  const [confirming, setConfirming] = React.useState<ComplianceRemediation | null>(null);
  const [unapplying, setUnapplying] = React.useState<ComplianceRemediation | null>(null);
  const [autoApplyConfirming, setAutoApplyConfirming] = React.useState(false);
  const [batchConfirming, setBatchConfirming] = React.useState(false);
  const [viewing, setViewing] = React.useState<ComplianceRemediation | null>(null);
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [canApply, canApplyLoading] = useAccessReview({
    group: 'compliance.openshift.io',
    resource: 'complianceremediations',
    verb: 'patch',
    namespace: 'openshift-compliance',
  });
  const [canEditBaseline, canEditBaselineLoading] = useAccessReview({
    group: 'baselinesecurity.io',
    resource: 'clusterbaselines',
    verb: 'patch',
  });
  const watchError = errorMessage(loadError);
  const batchInProgress =
    baseline?.status?.remediationBatch != null ||
    Object.prototype.hasOwnProperty.call(
      baseline?.metadata.annotations ?? {},
      'baselinesecurity.io/batch-apply',
    );

  const profiles = baseline?.spec.profiles;
  const tailoredProfiles = baseline?.spec.tailoredProfiles;
  const owned = React.useMemo(
    () =>
      (remediations ?? []).filter((r) =>
        isOwnedByBaseline(r.metadata.labels, profiles, tailoredProfiles),
      ),
    [remediations, profiles, tailoredProfiles],
  );

  const run = async (fn: () => Promise<unknown>, failMsg: string): Promise<boolean> => {
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
      setBusy(false);
    }
  };

  // Node remediations that can be batch-applied: owned, not yet applied, not
  // blocked on dependencies. Batching them pauses the pool so nodes reboot once.
  const batchable = React.useMemo(
    () =>
      owned.filter(
        (r) =>
          !r.spec.apply &&
          r.status?.applicationState !== 'MissingDependencies' &&
          isNodeRemediation(r),
      ),
    [owned],
  );

  const doBatchApply = () => {
    if (!baseline || batchInProgress || batchable.length === 0) return;
    void run(
      () =>
        k8sPatch({
          model: ClusterBaselineModel,
          resource: baseline,
          data: [
            ...resourceVersionTest(baseline.metadata.resourceVersion),
            ...batchApplyPatch(
              !!baseline.metadata.annotations,
              batchable.map((r) => r.metadata.name),
            ),
          ],
        }),
      t('Failed to start batch apply.'),
    ).then((ok) => ok && setBatchConfirming(false));
  };

  const setApply = (rem: ComplianceRemediation, apply: boolean) =>
    run(
      () =>
        k8sPatch({
          model: ComplianceRemediationModel,
          resource: rem,
          data: [{ op: 'add', path: '/spec/apply', value: apply }],
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
      setAutoApplyConfirming(true);
      return;
    }
    void toggleAutoApply(false);
  };

  const anyModalOpen = !!confirming || !!unapplying || batchConfirming || autoApplyConfirming;

  return (
    <PageSection>
      <Alert
        variant="warning"
        isInline
        title={t(
          'Node remediations render into MachineConfigs. Applying them triggers rolling node reboots.',
        )}
      />
      {watchError && (
        <Alert
          variant="danger"
          isInline
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
      <Split hasGutter style={{ marginTop: 'var(--pf-t--global--spacer--md)' }}>
        <SplitItem isFilled>
          {batchInProgress ? (
            <Label color="blue">{t('Batch apply in progress')}</Label>
          ) : batchable.length > 0 ? (
            <Button
              variant="secondary"
              isDisabled={!baseline || !canEditBaseline || canEditBaselineLoading || busy}
              onClick={() => {
                setError(null);
                setBatchConfirming(true);
              }}
            >
              {t('Batch apply {{count}} node remediation', { count: batchable.length })}
            </Button>
          ) : null}
        </SplitItem>
        <SplitItem>
          <Switch
            id="auto-apply"
            label={t('Auto-apply remediations after each scan')}
            isChecked={
              autoApplyConfirming || baseline?.spec.remediation?.apply === 'Automatic'
            }
            isDisabled={!baseline || !canEditBaseline || canEditBaselineLoading || busy}
            onChange={onAutoApplyChange}
          />
        </SplitItem>
      </Split>
      <Modal
        variant="small"
        isOpen={autoApplyConfirming}
        onClose={() => setAutoApplyConfirming(false)}
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
              void toggleAutoApply(true).then((ok) => ok && setAutoApplyConfirming(false));
            }}
          >
            {t('Enable auto-apply')}
          </Button>
          <Button
            variant="link"
            isDisabled={busy}
            onClick={() => setAutoApplyConfirming(false)}
          >
            {t('Cancel')}
          </Button>
        </ModalFooter>
      </Modal>
      <Modal
        variant="small"
        isOpen={batchConfirming}
        onClose={() => setBatchConfirming(false)}
        aria-labelledby="batch-apply-title"
      >
        <ModalHeader title={t('Batch apply node remediations?')} labelId="batch-apply-title" />
        <ModalBody>
          {t(
            'The affected MachineConfigPools are paused, all {{count}} node remediations are applied, then the pools resume so nodes reboot once instead of per remediation. A rescan is required afterwards.',
            { count: batchable.length },
          )}
          {error && (
            <Alert
              variant="danger"
              isInline
              title={error}
              style={{ marginTop: 'var(--pf-t--global--spacer--md)' }}
            />
          )}
        </ModalBody>
        <ModalFooter>
          <Button variant="danger" isDisabled={busy} isLoading={busy} onClick={doBatchApply}>
            {t('Batch apply')}
          </Button>
          <Button variant="link" isDisabled={busy} onClick={() => setBatchConfirming(false)}>
            {t('Cancel')}
          </Button>
        </ModalFooter>
      </Modal>
      {!loaded ? (
        <Bullseye style={{ padding: 'var(--pf-t--global--spacer--xl)' }}>
          <Spinner aria-label={t('Loading remediations')} />
        </Bullseye>
      ) : owned.length === 0 ? (
        <EmptyState
          titleText={t('No remediations')}
          headingLevel="h4"
          style={{ marginTop: 'var(--pf-t--global--spacer--md)' }}
        >
          <EmptyStateBody>
            {t('The Compliance Operator generates remediations for failing checks that can be auto-fixed. None are available yet; rescan after new failures appear.')}
          </EmptyStateBody>
        </EmptyState>
      ) : (
        <Table variant="compact">
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
            {owned.map((rem) => {
              const state = rem.status?.applicationState ?? 'NotApplied';
              return (
                <Tr key={rem.metadata.name}>
                  <Td modifier="breakWord">{rem.metadata.name}</Td>
                  <Td>
                    {rem.spec.current?.object?.kind ?? '—'}
                    {isNodeRemediation(rem) && (
                      <Label isCompact color="orange" style={{ marginLeft: 8 }}>
                        {t('reboots nodes')}
                      </Label>
                    )}
                  </Td>
                  <Td>
                    <Label isCompact color={stateColor[state] ?? 'grey'}>
                      {state}
                    </Label>
                  </Td>
                  <Td>
                    <Button variant="link" isInline onClick={() => setViewing(rem)}>
                      {t('View')}
                    </Button>
                  </Td>
                  <Td>
                    {rem.spec.apply ? (
                      <Button
                        variant="link"
                        isInline
                        isDisabled={!canApply || canApplyLoading || busy}
                        onClick={() => {
                          setError(null);
                          setUnapplying(rem);
                        }}
                      >
                        {t('Unapply')}
                      </Button>
                    ) : state === 'MissingDependencies' ? (
                      // Blocked: applying now would fail; a prerequisite remediation
                      // must be applied first. Do not offer a plain Apply.
                      <Tooltip content={t('Blocked: apply the prerequisite remediations first.')}>
                        <Button variant="link" isInline isAriaDisabled>
                          {t('Blocked')}
                        </Button>
                      </Tooltip>
                    ) : (
                      <Button
                        variant="link"
                        isInline
                        isDisabled={!canApply || canApplyLoading || busy}
                        onClick={() => {
                          setError(null);
                          setConfirming(rem);
                        }}
                      >
                        {t('Apply')}
                      </Button>
                    )}
                  </Td>
                </Tr>
              );
            })}
          </Tbody>
        </Table>
      )}
      <Modal
        variant="small"
        isOpen={!!confirming}
        onClose={() => setConfirming(null)}
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
              title={error}
              style={{ marginTop: 'var(--pf-t--global--spacer--md)' }}
            />
          )}
        </ModalBody>
        <ModalFooter>
          <Button
            variant="danger"
            isDisabled={busy || !canApply || canApplyLoading}
            isLoading={busy}
            onClick={() => {
              if (!confirming) return;
              const rem = confirming;
              void (async () => {
                if (await setApply(rem, true)) {
                  setConfirming(null);
                }
              })();
            }}
          >
            {t('Apply')}
          </Button>
          <Button variant="link" isDisabled={busy} onClick={() => setConfirming(null)}>
            {t('Cancel')}
          </Button>
        </ModalFooter>
      </Modal>
      <Modal
        variant="small"
        isOpen={!!unapplying}
        onClose={() => setUnapplying(null)}
        aria-labelledby="unapply-remediation-title"
      >
        <ModalHeader title={t('Unapply remediation?')} labelId="unapply-remediation-title" />
        <ModalBody>
          {t(
            '{{name}} will stop being applied. A rescan is required afterwards for results to reflect the change.',
            { name: unapplying?.metadata.name },
          )}
          {error && (
            <Alert
              variant="danger"
              isInline
              title={error}
              style={{ marginTop: 'var(--pf-t--global--spacer--md)' }}
            />
          )}
        </ModalBody>
        <ModalFooter>
          <Button
            variant="secondary"
            isDisabled={busy || !canApply || canApplyLoading}
            isLoading={busy}
            onClick={() => {
              if (!unapplying) return;
              const rem = unapplying;
              void (async () => {
                if (await setApply(rem, false)) {
                  setUnapplying(null);
                }
              })();
            }}
          >
            {t('Unapply')}
          </Button>
          <Button variant="link" isDisabled={busy} onClick={() => setUnapplying(null)}>
            {t('Cancel')}
          </Button>
        </ModalFooter>
      </Modal>
      <Modal
        variant="medium"
        isOpen={!!viewing}
        onClose={() => setViewing(null)}
        aria-labelledby="remediation-object-title"
      >
        <ModalHeader
          title={t('Rendered object')}
          labelId="remediation-object-title"
          description={viewing?.metadata.name}
        />
        <ModalBody>
          <CodeBlock>
            <CodeBlockCode>
              {viewing ? remediationObjectText(viewing) || t('No rendered object.') : ''}
            </CodeBlockCode>
          </CodeBlock>
        </ModalBody>
        <ModalFooter>
          <Button variant="link" onClick={() => setViewing(null)}>
            {t('Close')}
          </Button>
        </ModalFooter>
      </Modal>
    </PageSection>
  );
};

export default RemediationsTab;
