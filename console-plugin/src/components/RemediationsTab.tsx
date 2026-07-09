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
} from '@patternfly/react-core';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';
import {
  ClusterBaseline,
  ClusterBaselineModel,
  ComplianceRemediation,
  ComplianceRemediationGVK,
  ComplianceRemediationModel,
} from '../models';

const stateColor: Record<string, React.ComponentProps<typeof Label>['color']> = {
  Applied: 'green',
  NotApplied: 'grey',
  Error: 'red',
  Outdated: 'orange',
  MissingDependencies: 'orange',
};

const RemediationsTab: React.FC<{ baseline?: ClusterBaseline }> = ({ baseline }) => {
  const { t } = useTranslation('plugin__baseline-security-console-plugin');
  const [remediations, loaded] = useK8sWatchResource<ComplianceRemediation[]>({
    groupVersionKind: ComplianceRemediationGVK,
    isList: true,
    namespace: 'openshift-compliance',
  });
  const [confirming, setConfirming] = React.useState<ComplianceRemediation | null>(null);
  const [canApply] = useAccessReview({
    group: 'compliance.openshift.io',
    resource: 'complianceremediations',
    verb: 'patch',
    namespace: 'openshift-compliance',
  });
  const [canEditBaseline] = useAccessReview({
    group: 'baselinesecurity.io',
    resource: 'clusterbaselines',
    verb: 'patch',
  });

  const setApply = (rem: ComplianceRemediation, apply: boolean) =>
    k8sPatch({
      model: ComplianceRemediationModel,
      resource: rem,
      data: [{ op: 'replace', path: '/spec/apply', value: apply }],
    });

  const toggleAutoApply = (checked: boolean) => {
    if (!baseline) return;
    k8sPatch({
      model: ClusterBaselineModel,
      resource: baseline,
      data: [{ op: 'add', path: '/spec/remediation', value: { autoApply: checked } }],
    });
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
      <Split hasGutter style={{ marginTop: 'var(--pf-t--global--spacer--md)' }}>
        <SplitItem isFilled />
        <SplitItem>
          <Switch
            id="auto-apply"
            label={t('Auto-apply remediations after each scan')}
            isChecked={baseline?.spec.remediation?.autoApply ?? false}
            isDisabled={!baseline || !canEditBaseline}
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
              <Th screenReaderText={t('Actions')} />
            </Tr>
          </Thead>
          <Tbody>
            {(remediations ?? []).map((rem) => {
              const state = rem.status?.applicationState ?? 'NotApplied';
              return (
                <Tr key={rem.metadata.name}>
                  <Td modifier="breakWord">{rem.metadata.name}</Td>
                  <Td>{rem.spec.current?.object?.kind ?? '—'}</Td>
                  <Td>
                    <Label isCompact color={stateColor[state] ?? 'grey'}>
                      {state}
                    </Label>
                  </Td>
                  <Td>
                    {rem.spec.apply ? (
                      <Button
                        variant="link"
                        isInline
                        isDisabled={!canApply}
                        onClick={() => setApply(rem, false)}
                      >
                        {t('Unapply')}
                      </Button>
                    ) : (
                      <Button
                        variant="link"
                        isInline
                        isDisabled={!canApply}
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
            '{{name}} will be applied to the cluster. If it renders into a MachineConfig, affected nodes reboot one by one. A rescan is required afterwards for results to reflect the change.',
            { name: confirming?.metadata.name },
          )}
        </ModalBody>
        <ModalFooter>
          <Button
            variant="danger"
            onClick={() => {
              if (confirming) setApply(confirming, true);
              setConfirming(null);
            }}
          >
            {t('Apply')}
          </Button>
          <Button variant="link" onClick={() => setConfirming(null)}>
            {t('Cancel')}
          </Button>
        </ModalFooter>
      </Modal>
    </PageSection>
  );
};

export default RemediationsTab;
