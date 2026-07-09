import * as React from 'react';
import { useTranslation } from 'react-i18next';
import { k8sPatch, useAccessReview } from '@openshift-console/dynamic-plugin-sdk';
import {
  Alert,
  Card,
  CardBody,
  CardHeader,
  CardTitle,
  Gallery,
  PageSection,
  Switch,
} from '@patternfly/react-core';
import { ClusterBaseline, ClusterBaselineModel } from '../models';
import { toggledProfiles } from '../utils';

const PROFILE_INFO: Record<string, { title: string; description: string }> = {
  cis: { title: 'CIS', description: 'CIS Red Hat OpenShift Container Platform Benchmark' },
  'pci-dss': { title: 'PCI-DSS', description: 'Payment Card Industry Data Security Standard' },
  'nist-moderate': { title: 'NIST 800-53 Moderate', description: 'FedRAMP Moderate impact baseline' },
  'nist-high': { title: 'NIST 800-53 High', description: 'FedRAMP High impact baseline' },
  stig: {
    title: 'DISA STIG',
    description: 'Defense Information Systems Agency Security Technical Implementation Guide',
  },
  'nerc-cip': {
    title: 'NERC CIP',
    description: 'North American Electric Reliability Corporation Critical Infrastructure Protection',
  },
  e8: { title: 'ACSC Essential Eight', description: 'Australian Cyber Security Centre Essential Eight' },
  bsi: {
    title: 'BSI',
    description: 'German Federal Office for Information Security IT-Grundschutz',
  },
};

const ProfilesTab: React.FC<{ baseline?: ClusterBaseline }> = ({ baseline }) => {
  const { t } = useTranslation('plugin__baseline-security-console-plugin');
  const [pending, setPending] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [canEdit, canEditLoading] = useAccessReview({
    group: 'baselinesecurity.io',
    resource: 'clusterbaselines',
    verb: 'patch',
  });

  const toggle = async (key: string, checked: boolean) => {
    if (!baseline) return;
    const profiles = toggledProfiles(baseline.spec.profiles ?? [], key, checked);
    if (!profiles) return; // CRD requires at least one profile
    setPending(true);
    setError(null);
    try {
      await k8sPatch({
        model: ClusterBaselineModel,
        resource: baseline,
        // test op: reject the patch if another writer changed profiles since
        // this render, instead of silently clobbering their change.
        data: [
          { op: 'test', path: '/spec/profiles', value: baseline.spec.profiles ?? [] },
          { op: 'replace', path: '/spec/profiles', value: profiles },
        ],
      });
    } catch (e) {
      setError(e instanceof Error ? e.message : t('Failed to update profiles.'));
    } finally {
      setPending(false);
    }
  };

  return (
    <PageSection>
      {error && (
        <Alert
          variant="danger"
          isInline
          title={error}
          style={{ marginBottom: 'var(--pf-t--global--spacer--md)' }}
        />
      )}
      <Gallery hasGutter minWidths={{ default: '330px' }}>
        {Object.keys(PROFILE_INFO).map((key) => {
          const profileCount = baseline?.spec.profiles?.length ?? 0;
          const enabled = baseline?.spec.profiles?.includes(key) ?? false;
          return (
            <Card key={key}>
              <CardHeader
                actions={{
                  actions: (
                    <Switch
                      id={`profile-${key}`}
                      aria-label={key}
                      isChecked={enabled}
                      isDisabled={
                        !baseline ||
                        !canEdit ||
                        canEditLoading ||
                        pending ||
                        (enabled && profileCount === 1)
                      }
                      onChange={(_e, checked) => {
                        void toggle(key, checked);
                      }}
                    />
                  ),
                }}
              >
                <CardTitle>{PROFILE_INFO[key].title}</CardTitle>
              </CardHeader>
              <CardBody>{t(PROFILE_INFO[key].description)}</CardBody>
            </Card>
          );
        })}
      </Gallery>
    </PageSection>
  );
};

export default ProfilesTab;
