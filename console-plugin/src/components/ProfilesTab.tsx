import * as React from 'react';
import { useTranslation } from 'react-i18next';
import { k8sPatch } from '@openshift-console/dynamic-plugin-sdk';
import {
  Card,
  CardBody,
  CardHeader,
  CardTitle,
  Gallery,
  PageSection,
  Switch,
} from '@patternfly/react-core';
import { ClusterBaseline, ClusterBaselineModel, PROFILE_KEYS } from '../models';

const PROFILE_INFO: Record<string, { title: string; description: string }> = {
  cis: { title: 'CIS', description: 'CIS Red Hat OpenShift Container Platform Benchmark' },
  'pci-dss': { title: 'PCI-DSS', description: 'Payment Card Industry Data Security Standard' },
  'nist-moderate': { title: 'NIST 800-53 Moderate', description: 'FedRAMP Moderate impact baseline' },
  'nist-high': { title: 'NIST 800-53 High', description: 'FedRAMP High impact baseline' },
  stig: { title: 'DISA STIG', description: 'Defense Information Systems Agency Security Technical Implementation Guide' },
  'nerc-cip': { title: 'NERC CIP', description: 'North American Electric Reliability Corporation Critical Infrastructure Protection' },
  e8: { title: 'ACSC Essential Eight', description: 'Australian Cyber Security Centre Essential Eight' },
  bsi: { title: 'BSI', description: 'German Federal Office for Information Security IT-Grundschutz' },
};

const ProfilesTab: React.FC<{ baseline?: ClusterBaseline }> = ({ baseline }) => {
  const { t } = useTranslation('plugin__baseline-security-console-plugin');

  const toggle = (key: string, checked: boolean) => {
    if (!baseline) return;
    const profiles = checked
      ? [...baseline.spec.profiles, key]
      : baseline.spec.profiles.filter((p) => p !== key);
    if (!profiles.length) return; // CRD requires at least one profile
    k8sPatch({
      model: ClusterBaselineModel,
      resource: baseline,
      data: [{ op: 'replace', path: '/spec/profiles', value: profiles }],
    });
  };

  return (
    <PageSection>
      <Gallery hasGutter minWidths={{ default: '330px' }}>
        {PROFILE_KEYS.map((key) => {
          const enabled = baseline?.spec.profiles.includes(key) ?? false;
          return (
            <Card key={key}>
              <CardHeader
                actions={{
                  actions: (
                    <Switch
                      id={`profile-${key}`}
                      aria-label={key}
                      isChecked={enabled}
                      isDisabled={!baseline || (enabled && baseline.spec.profiles.length === 1)}
                      onChange={(_e, checked) => toggle(key, checked)}
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
