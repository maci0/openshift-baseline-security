import * as React from 'react';
import { useTranslation } from 'react-i18next';
import { k8sPatch, useK8sWatchResource } from '@openshift-console/dynamic-plugin-sdk';
import { Checkbox, PageSection, Spinner, Title } from '@patternfly/react-core';
import { ClusterBaseline, ClusterBaselineGVK, PROFILE_KEYS } from '../models';

const ProfilesPage: React.FC = () => {
  const { t } = useTranslation('plugin__baseline-security-console-plugin');
  const [baselines, loaded] = useK8sWatchResource<ClusterBaseline[]>({
    groupVersionKind: ClusterBaselineGVK,
    isList: true,
  });
  const baseline = baselines?.[0];

  const toggle = (key: string, checked: boolean) => {
    if (!baseline) return;
    const profiles = checked
      ? [...baseline.spec.profiles, key]
      : baseline.spec.profiles.filter((p) => p !== key);
    // ponytail: no optimistic update; the watch refreshes the checkbox state
    k8sPatch({
      model: {
        apiGroup: ClusterBaselineGVK.group,
        apiVersion: ClusterBaselineGVK.version,
        kind: ClusterBaselineGVK.kind,
        plural: 'clusterbaselines',
        abbr: 'cb',
        label: 'ClusterBaseline',
        labelPlural: 'ClusterBaselines',
        id: '',
        namespaced: false,
      },
      resource: baseline,
      data: [{ op: 'replace', path: '/spec/profiles', value: profiles }],
    });
  };

  return (
    <PageSection>
      <Title headingLevel="h1">{t('Profiles')}</Title>
      {!loaded ? (
        <Spinner />
      ) : (
        PROFILE_KEYS.map((key) => (
          <Checkbox
            key={key}
            id={`profile-${key}`}
            label={key}
            isChecked={baseline?.spec.profiles.includes(key) ?? false}
            isDisabled={!baseline}
            onChange={(_e, checked) => toggle(key, checked)}
          />
        ))
      )}
    </PageSection>
  );
};

export default ProfilesPage;
