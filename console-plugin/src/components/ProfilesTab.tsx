import * as React from 'react';
import { useTranslation } from 'react-i18next';
import { k8sCreate, k8sPatch, useAccessReview } from '@openshift-console/dynamic-plugin-sdk';
import {
  Alert,
  AlertActionCloseButton,
  Button,
  Card,
  CardBody,
  CardHeader,
  CardTitle,
  EmptyState,
  EmptyStateBody,
  FormGroup,
  FormHelperText,
  Gallery,
  HelperText,
  HelperTextItem,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  PageSection,
  Split,
  SplitItem,
  Switch,
  TextArea,
  TextInput,
} from '@patternfly/react-core';
import { ClusterBaseline, ClusterBaselineModel, TailoredProfileModel } from '../models';
import {
  errorMessage,
  isAlreadyExists,
  isValidTailoredProfileName,
  resourceVersionTest,
  tailoredProfileManifest,
  tailoredProfileBindingPatch,
  toggledProfiles,
} from '../utils';

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
  const [canAuthor] = useAccessReview({
    group: 'compliance.openshift.io',
    resource: 'tailoredprofiles',
    verb: 'create',
    namespace: 'openshift-compliance',
  });
  const [creating, setCreating] = React.useState(false);
  const [tpName, setTpName] = React.useState('');
  const [tpExtends, setTpExtends] = React.useState('ocp4-cis');
  const [tpDisable, setTpDisable] = React.useState('');

  const createTailored = async () => {
    const name = tpName.trim();
    if (!baseline || !isValidTailoredProfileName(name)) return;
    setPending(true);
    setError(null);
    // Two steps: create the TailoredProfile, then bind it into spec. Track which
    // step we reached so a bind failure does not read as "nothing happened" and
    // an AlreadyExists on retry is treated as the create having succeeded.
    let created = false;
    try {
      const disable = tpDisable
        .split('\n')
        .map((s) => s.trim())
        .filter(Boolean);
      try {
        await k8sCreate({
          model: TailoredProfileModel,
          data: tailoredProfileManifest(name, tpExtends.trim() || 'ocp4-cis', disable),
        });
      } catch (e) {
        if (!isAlreadyExists(e)) throw e;
      }
      created = true;
      const bindPatch = tailoredProfileBindingPatch(
        baseline.spec.tailoredProfiles,
        name,
        baseline.metadata.resourceVersion,
      );
      if (bindPatch.length) {
        await k8sPatch({ model: ClusterBaselineModel, resource: baseline, data: bindPatch });
      }
      setCreating(false);
      setTpName('');
      setTpDisable('');
    } catch (e) {
      const detail = errorMessage(e);
      setError(
        created
          ? t(
              'Tailored profile "{{name}}" was created but could not be bound: {{detail}}. Retry to bind it.',
              { name, detail: detail ?? t('unknown error') },
            )
          : detail ?? t('Failed to create tailored profile.'),
      );
    } finally {
      setPending(false);
    }
  };

  const nameValid = tpName.trim() === '' || isValidTailoredProfileName(tpName.trim());

  const toggle = async (key: string, checked: boolean) => {
    if (!baseline) return;
    // Empty is allowed: clearing every profile disables scanning.
    const profiles = toggledProfiles(baseline.spec.profiles ?? [], key, checked);
    setPending(true);
    setError(null);
    try {
      await k8sPatch({
        model: ClusterBaselineModel,
        resource: baseline,
        // test op: reject the patch if another writer changed profiles since
        // this render, instead of silently clobbering their change.
        data: [
          ...resourceVersionTest(baseline.metadata.resourceVersion),
          { op: 'test', path: '/spec/profiles', value: baseline.spec.profiles ?? [] },
          { op: 'replace', path: '/spec/profiles', value: profiles },
        ],
      });
    } catch (e) {
      setError(errorMessage(e) ?? t('Failed to update profiles.'));
    } finally {
      setPending(false);
    }
  };

  if (!baseline) {
    return (
      <PageSection>
        <EmptyState titleText={t('Baseline not configured')} headingLevel="h4">
          <EmptyStateBody>
            {t(
              'No ClusterBaseline resource found. Install the baseline-security operator and create a ClusterBaseline to start scanning.',
            )}
          </EmptyStateBody>
        </EmptyState>
      </PageSection>
    );
  }

  return (
    <PageSection>
      {error && (
        <Alert
          variant="danger"
          isInline
          title={error}
          style={{ marginBottom: 'var(--pf-t--global--spacer--md)' }}
          actionClose={
            <AlertActionCloseButton aria-label={t('Close')} onClose={() => setError(null)} />
          }
        />
      )}
      {canAuthor && (
        <Split hasGutter style={{ marginBottom: 'var(--pf-t--global--spacer--md)' }}>
          <SplitItem isFilled />
          <SplitItem>
            <Button
              variant="secondary"
              isDisabled={!canEdit || canEditLoading || pending}
              onClick={() => {
                setError(null);
                setCreating(true);
              }}
            >
              {t('New tailored profile')}
            </Button>
          </SplitItem>
        </Split>
      )}
      <Modal
        variant="medium"
        isOpen={creating}
        onClose={() => setCreating(false)}
        aria-labelledby="new-tp-title"
      >
        <ModalHeader title={t('New tailored profile')} labelId="new-tp-title" />
        <ModalBody>
          {error && (
            <Alert
              variant="danger"
              isInline
              title={error}
              style={{ marginBottom: 'var(--pf-t--global--spacer--md)' }}
            />
          )}
          <FormGroup label={t('Name')} fieldId="tp-name" isRequired>
            <TextInput
              id="tp-name"
              value={tpName}
              onChange={(_e, v) => setTpName(v)}
              validated={nameValid ? 'default' : 'error'}
            />
            {!nameValid && (
              <FormHelperText>
                <HelperText>
                  <HelperTextItem variant="error">
                    {t(
                      'Use lowercase letters, digits, - and .; must start and end with a letter or digit.',
                    )}
                  </HelperTextItem>
                </HelperText>
              </FormHelperText>
            )}
          </FormGroup>
          <FormGroup label={t('Extends (base profile)')} fieldId="tp-extends">
            <TextInput id="tp-extends" value={tpExtends} onChange={(_e, v) => setTpExtends(v)} />
          </FormGroup>
          <FormGroup label={t('Disable rules (one per line)')} fieldId="tp-disable">
            <TextArea
              id="tp-disable"
              value={tpDisable}
              onChange={(_e, v) => setTpDisable(v)}
              rows={4}
              placeholder="ocp4-cis-..."
            />
          </FormGroup>
        </ModalBody>
        <ModalFooter>
          <Button
            variant="primary"
            isDisabled={!isValidTailoredProfileName(tpName.trim()) || pending}
            isLoading={pending}
            onClick={() => void createTailored()}
          >
            {t('Create and bind')}
          </Button>
          <Button variant="link" isDisabled={pending} onClick={() => setCreating(false)}>
            {t('Cancel')}
          </Button>
        </ModalFooter>
      </Modal>
      <Gallery hasGutter minWidths={{ default: '330px' }}>
        {Object.keys(PROFILE_INFO).map((key) => {
          const enabled = baseline.spec.profiles?.includes(key) ?? false;
          return (
            <Card key={key}>
              <CardHeader
                actions={{
                  // Any profile can be toggled off, including the last one, which
                  // disables scanning.
                  actions: (
                    <Switch
                      id={`profile-${key}`}
                      aria-label={t('Enable {{profile}} profile', {
                        profile: PROFILE_INFO[key].title,
                      })}
                      isChecked={enabled}
                      isDisabled={!canEdit || canEditLoading || pending}
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
