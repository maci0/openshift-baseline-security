import * as React from 'react';
import { useTranslation } from 'react-i18next';
import { useK8sWatchResource } from '@openshift-console/dynamic-plugin-sdk';
import { Label, PageSection, Spinner, Title } from '@patternfly/react-core';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';
import { CheckStatus, ComplianceCheckResult, ComplianceCheckResultGVK } from '../models';

const statusColor: Record<CheckStatus, React.ComponentProps<typeof Label>['color']> = {
  PASS: 'green',
  FAIL: 'red',
  ERROR: 'red',
  MANUAL: 'orange',
  INFO: 'blue',
  'NOT-APPLICABLE': 'grey',
};

const ResultsPage: React.FC = () => {
  const { t } = useTranslation('plugin__baseline-security-console-plugin');
  const [results, loaded] = useK8sWatchResource<ComplianceCheckResult[]>({
    groupVersionKind: ComplianceCheckResultGVK,
    isList: true,
    namespace: 'openshift-compliance',
  });

  return (
    <PageSection>
      <Title headingLevel="h1">{t('Check results')}</Title>
      {!loaded ? (
        <Spinner />
      ) : (
        <Table variant="compact">
          <Thead>
            <Tr>
              <Th>{t('Check')}</Th>
              <Th>{t('Status')}</Th>
              <Th>{t('Severity')}</Th>
            </Tr>
          </Thead>
          <Tbody>
            {(results ?? []).map((r) => (
              <Tr key={r.metadata.name}>
                <Td>{r.metadata.name}</Td>
                <Td>
                  <Label color={statusColor[r.status] ?? 'grey'}>{r.status}</Label>
                </Td>
                <Td>{r.severity}</Td>
              </Tr>
            ))}
          </Tbody>
        </Table>
      )}
    </PageSection>
  );
};

export default ResultsPage;
