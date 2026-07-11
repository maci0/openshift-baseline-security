import * as React from 'react';
import { useTranslation } from 'react-i18next';
import {
  k8sPatch,
  useAccessReview,
  useK8sWatchResource,
} from '@openshift-console/dynamic-plugin-sdk';
import {
  Alert,
  Button,
  CodeBlock,
  CodeBlockCode,
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
  errorMessage,
  isNodeRemediation,
  remediationApplyPatch,
  remediationObjectText,
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

  const owned = React.useMemo(
    () =>
      (remediations ?? []).filter((r) =>
        isOwnedByBaseline(r.metadata.labels, baseline?.spec.profiles, baseline?.spec.tailoredProfiles),
      ),
    [remediations, baseline?.spec.profiles, baseline?.spec.tailoredProfiles],
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

  const toggleAutoApply = (checked: boolean) => {
    if (!baseline) return;
    void run(
      () =>
        k8sPatch({
          model: ClusterBaselineModel,
          resource: baseline,
          data: remediationApplyPatch(baseline.spec.remediation != null, checked),
        }),
      t('Failed to update auto-apply setting.'),
    );
  };

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
      {error && (
        <Alert
          variant="danger"
          isInline
          title={error}
          style={{ marginTop: 'var(--pf-t--global--spacer--md)' }}
        />
      )}
      <Split hasGutter style={{ marginTop: 'var(--pf-t--global--spacer--md)' }}>
        <SplitItem isFilled />
        <SplitItem>
          <Switch
            id="auto-apply"
            label={t('Auto-apply remediations after each scan')}
            isChecked={baseline?.spec.remediation?.apply === 'Automatic'}
            isDisabled={!baseline || !canEditBaseline || canEditBaselineLoading || busy}
            onChange={(_e, checked) => toggleAutoApply(checked)}
          />
        </SplitItem>
      </Split>
      {!loaded ? (
        <Spinner />
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
                        onClick={() => void setApply(rem, false)}
                      >
                        {t('Unapply')}
                      </Button>
                    ) : state === 'MissingDependencies' ? (
                      // Blocked: applying now would fail; a prerequisite remediation
                      // must be applied first. Do not offer a plain Apply.
                      <Tooltip content={t('Blocked: apply the prerequisite remediation(s) first.')}>
                        <Button variant="link" isInline isAriaDisabled>
                          {t('Blocked')}
                        </Button>
                      </Tooltip>
                    ) : (
                      <Button
                        variant="link"
                        isInline
                        isDisabled={!canApply || canApplyLoading || busy}
                        onClick={() => setConfirming(rem)}
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
      </Modal>
    </PageSection>
  );
};

export default RemediationsTab;
