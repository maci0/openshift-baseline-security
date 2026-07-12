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
  Skeleton,
  Split,
  SplitItem,
  Switch,
  TextArea,
  TextInput,
} from '@patternfly/react-core';
import {
  ClusterBaseline,
  ClusterBaselineModel,
  PROFILE_INFO,
  PROFILE_KEYS,
  TAILORED_PROFILE_MAX_ITEMS,
  TailoredProfileModel,
} from '../models';
import { errorMessage, isAlreadyExists } from '../errors';
import { isValidK8sName, isValidTailoredProfileName } from '../names';
import { resourceVersionTest, tailoredProfileBindingPatch } from '../patches';
import { tailoredProfileManifest, toggledProfiles } from '../profiles';
import { withDisabledTip } from './DisabledTip';

const ProfilesTab: React.FC<{ baseline?: ClusterBaseline; loaded?: boolean }> = ({
  baseline,
  loaded = true,
}) => {
  const { t } = useTranslation('plugin__baseline-security-console-plugin');
  const [pending, setPending] = React.useState(false);
  // Which profile switch is mid-patch (for per-control loading feedback).
  const [pendingKey, setPendingKey] = React.useState<string | null>(null);
  // Sync guard: React state alone cannot block a second click before re-render.
  const pendingRef = React.useRef(false);
  const [error, setError] = React.useState<string | null>(null);
  const [success, setSuccess] = React.useState<string | null>(null);
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
  const tpNameRef = React.useRef<HTMLInputElement>(null);

  // Focus the name field when the create modal opens (keyboard users).
  React.useEffect(() => {
    if (creating) {
      tpNameRef.current?.focus();
    }
  }, [creating]);

  const createTailored = async () => {
    const name = tpName.trim();
    if (!baseline || pendingRef.current) return;
    // Enter key can fire while the primary button is disabled; surface validation
    // instead of a silent no-op so the form never looks broken.
    if (!isValidTailoredProfileName(name)) {
      setError(
        t(
          'Use lowercase letters, digits, - and .; must start and end with a letter or digit.',
        ),
      );
      return;
    }
    // Base profile must be a DNS-1123 name (same shape as CO Profile metadata.name).
    const extendsBase = tpExtends.trim() || 'ocp4-cis';
    if (!isValidK8sName(extendsBase)) {
      setError(
        t(
          'Base profile name is invalid. Use lowercase letters, digits, - and .; must start and end with a letter or digit.',
        ),
      );
      return;
    }
    pendingRef.current = true;
    setPending(true);
    setError(null);
    // Two steps: create the TailoredProfile, then bind it into spec. Track which
    // step we reached so a bind failure does not read as "nothing happened" and
    // an AlreadyExists on retry is treated as the create having succeeded.
    let created = false;
    try {
      // Drop non-DNS-1123 rule lines before create (manifest also filters).
      const disable = tpDisable
        .split('\n')
        .map((s) => s.trim())
        .filter((s) => isValidK8sName(s));
      try {
        await k8sCreate({
          model: TailoredProfileModel,
          data: tailoredProfileManifest(name, extendsBase, disable),
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
      } else if (!(baseline.spec.tailoredProfiles ?? []).includes(name)) {
        // Empty patch is MaxItems or validation: profile may exist in CO but is
        // not bound. Do not report success or the orphan is invisible.
        setError(
          t(
            'Tailored profile "{{name}}" was created but could not be bound (limit of {{max}} tailored profiles reached). Remove one, then retry.',
            { name, max: TAILORED_PROFILE_MAX_ITEMS },
          ),
        );
        return;
      }
      setCreating(false);
      setTpName('');
      setTpDisable('');
      setSuccess(t('Tailored profile created and bound.'));
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
      pendingRef.current = false;
      setPending(false);
    }
  };

  const nameValid = tpName.trim() === '' || isValidTailoredProfileName(tpName.trim());
  const extendsValid =
    tpExtends.trim() === '' || isValidK8sName(tpExtends.trim());

  const editDisabled = !canEdit || canEditLoading || pending;
  let editDisabledReason: string | undefined;
  if (!pending) {
    if (canEditLoading) {
      editDisabledReason = t('Checking permissions…');
    } else if (!canEdit) {
      editDisabledReason = t('You do not have permission to edit the baseline.');
    }
  }

  const toggle = async (key: string, checked: boolean) => {
    if (!baseline || pendingRef.current) return;
    // Empty is allowed: clearing every profile disables scanning.
    const profiles = toggledProfiles(baseline.spec.profiles ?? [], key, checked);
    pendingRef.current = true;
    setPending(true);
    setPendingKey(key);
    setError(null);
    setSuccess(null);
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
      const title = t(PROFILE_INFO[key as (typeof PROFILE_KEYS)[number]].title);
      setSuccess(
        checked
          ? t('{{profile}} enabled. Scans will include this profile on the next run.', {
              profile: title,
            })
          : t('{{profile}} disabled.', { profile: title }),
      );
    } catch (e) {
      setError(errorMessage(e) ?? t('Failed to update profiles.'));
    } finally {
      pendingRef.current = false;
      setPending(false);
      setPendingKey(null);
    }
  };

  if (!loaded) {
    return (
      <PageSection>
        <Gallery hasGutter minWidths={{ default: '330px' }}>
          {[0, 1, 2].map((i) => (
            <Card key={i}>
              <CardBody>
                <Skeleton height="80px" screenreaderText={t('Loading compliance data')} />
              </CardBody>
            </Card>
          ))}
        </Gallery>
      </PageSection>
    );
  }
  if (!baseline) {
    return (
      <PageSection>
        <EmptyState titleText={t('Baseline not configured')} headingLevel="h2">
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
          isLiveRegion
          title={error}
          style={{ marginBottom: 'var(--pf-t--global--spacer--md)' }}
          actionClose={
            <AlertActionCloseButton aria-label={t('Close')} onClose={() => setError(null)} />
          }
        />
      )}
      {success && (
        <Alert
          variant="success"
          isInline
          isLiveRegion
          title={success}
          style={{ marginBottom: 'var(--pf-t--global--spacer--md)' }}
          actionClose={
            <AlertActionCloseButton aria-label={t('Close')} onClose={() => setSuccess(null)} />
          }
        />
      )}
      {canAuthor && (
        <Split hasGutter style={{ marginBottom: 'var(--pf-t--global--spacer--md)' }}>
          <SplitItem isFilled />
          <SplitItem>
            {withDisabledTip(
              editDisabled && editDisabledReason ? editDisabledReason : undefined,
              <Button
                variant="secondary"
                isDisabled={editDisabled}
                onClick={() => {
                  setError(null);
                  setSuccess(null);
                  setCreating(true);
                }}
              >
                {t('New tailored profile')}
              </Button>,
            )}
          </SplitItem>
        </Split>
      )}
      <Modal
        variant="medium"
        isOpen={creating}
        onClose={() => {
          // pendingRef: setPending is async; dismiss between pendingRef=true and
          // re-render must not wipe the create form mid-request.
          if (pendingRef.current) return;
          setCreating(false);
        }}
        aria-labelledby="new-tp-title"
      >
        <ModalHeader title={t('New tailored profile')} labelId="new-tp-title" />
        <ModalBody>
          {error && (
            <Alert
              variant="danger"
              isInline
              isLiveRegion
              title={error}
              style={{ marginBottom: 'var(--pf-t--global--spacer--md)' }}
            />
          )}
          <FormGroup label={t('Name')} fieldId="tp-name" isRequired>
            <TextInput
              ref={tpNameRef}
              id="tp-name"
              value={tpName}
              onChange={(_e, v) => setTpName(v)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault();
                  void createTailored();
                }
              }}
              validated={nameValid ? 'default' : 'error'}
              isRequired
              aria-invalid={!nameValid}
              aria-describedby={!nameValid ? 'tp-name-help' : undefined}
            />
            {!nameValid && (
              <FormHelperText>
                <HelperText id="tp-name-help">
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
            <TextInput
              id="tp-extends"
              value={tpExtends}
              onChange={(_e, v) => setTpExtends(v)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault();
                  void createTailored();
                }
              }}
              validated={extendsValid ? 'default' : 'error'}
              aria-invalid={!extendsValid}
              aria-describedby="tp-extends-help"
            />
            <FormHelperText>
              <HelperText id="tp-extends-help">
                <HelperTextItem variant={extendsValid ? 'default' : 'error'}>
                  {extendsValid
                    ? t(
                        'Compliance Operator profile name this tailored profile extends (for example ocp4-cis).',
                      )
                    : t(
                        'Use lowercase letters, digits, - and .; must start and end with a letter or digit.',
                      )}
                </HelperTextItem>
              </HelperText>
            </FormHelperText>
          </FormGroup>
          <FormGroup label={t('Disable rules (one per line)')} fieldId="tp-disable">
            <TextArea
              id="tp-disable"
              value={tpDisable}
              onChange={(_e, v) => setTpDisable(v)}
              rows={4}
              placeholder={t('ocp4-cis-...')}
              aria-label={t('Disable rules (one per line)')}
              aria-describedby="tp-disable-help"
            />
            <FormHelperText>
              <HelperText id="tp-disable-help">
                <HelperTextItem>
                  {t('Optional. One Compliance Operator rule name per line to disable in the base profile.')}
                </HelperTextItem>
              </HelperText>
            </FormHelperText>
          </FormGroup>
        </ModalBody>
        <ModalFooter>
          <Button
            variant="primary"
            isDisabled={
              !isValidTailoredProfileName(tpName.trim()) ||
              !isValidK8sName(tpExtends.trim() || 'ocp4-cis') ||
              pending
            }
            isLoading={pending}
            onClick={() => void createTailored()}
          >
            {t('Create and bind')}
          </Button>
          <Button
            variant="link"
            isDisabled={pending}
            onClick={() => {
              if (pendingRef.current) return;
              setCreating(false);
            }}
          >
            {t('Cancel')}
          </Button>
        </ModalFooter>
      </Modal>
      <Gallery hasGutter minWidths={{ default: '330px' }}>
        {PROFILE_KEYS.map((key) => {
          const info = PROFILE_INFO[key];
          const enabled = baseline.spec.profiles?.includes(key) ?? false;
          return (
            <Card key={key}>
              <CardHeader
                actions={{
                  // Any profile can be toggled off, including the last one, which
                  // disables scanning.
                  actions: withDisabledTip(
                    editDisabled && editDisabledReason ? editDisabledReason : undefined,
                    <Switch
                      id={`profile-${key}`}
                      aria-label={
                        pendingKey === key
                          ? t('Updating {{profile}} profile', { profile: t(info.title) })
                          : t('Enable {{profile}} profile', {
                              profile: t(info.title),
                            })
                      }
                      aria-busy={pendingKey === key || undefined}
                      isChecked={enabled}
                      isDisabled={editDisabled}
                      onChange={(_e, checked) => {
                        void toggle(key, checked);
                      }}
                    />,
                  ),
                }}
              >
                <CardTitle>{t(info.title)}</CardTitle>
              </CardHeader>
              <CardBody>{t(info.description)}</CardBody>
            </Card>
          );
        })}
      </Gallery>
    </PageSection>
  );
};

export default ProfilesTab;
