import * as React from 'react';
import { useTranslation } from 'react-i18next';
import {
  k8sCreate,
  k8sGet,
  k8sPatch,
  k8sUpdate,
  useAccessReview,
  useK8sWatchResource,
} from '@openshift-console/dynamic-plugin-sdk';
import {
  Alert,
  AlertActionCloseButton,
  Button,
  Card,
  CardBody,
  CardHeader,
  CardTitle,
  Checkbox,
  Content,
  Flex,
  FlexItem,
  FormGroup,
  FormHelperText,
  FormSelect,
  FormSelectOption,
  Gallery,
  HelperText,
  HelperTextItem,
  Label,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  PageSection,
  SearchInput,
  Skeleton,
  Spinner,
  Split,
  SplitItem,
  Switch,
  TextInput,
  Title,
} from '@patternfly/react-core';
import {
  ClusterBaseline,
  ClusterBaselineModel,
  COMPLIANCE_NAMESPACE,
  ComplianceProfile,
  ComplianceRule,
  PROFILE_INFO,
  PROFILE_KEYS,
  ProfileGVK,
  profileTitle,
  RuleGVK,
  TAILORED_PROFILE_MAX_ITEMS,
  TailoredProfileModel,
  TailoredProfileResource,
} from '../models';
import { formatCount } from '../dates';
import { errorMessage, isAlreadyExists } from '../errors';
import { isValidK8sName, isValidTailoredProfileName } from '../names';
import { resourceVersionTest, tailoredProfileBindingPatch } from '../patches';
import {
  tailoredProfileManifest,
  tailoredProfileSpecMatches,
  toggledProfiles,
} from '../profiles';
import BaselineNotConfigured from './BaselineNotConfigured';
import { withDisabledTip } from './DisabledTip';
import { restoreFocus } from './focus';
import { SUCCESS_DISMISS_MS } from './feedback';

const ProfilesTab: React.FC<{ baseline?: ClusterBaseline; loaded?: boolean }> = ({
  baseline,
  loaded = true,
}) => {
  const { t, i18n } = useTranslation('plugin__baseline-security-console-plugin');
  const [pending, setPending] = React.useState(false);
  // Which profile switch is mid-patch (for per-control loading feedback).
  const [pendingKey, setPendingKey] = React.useState<string | null>(null);
  // Sync guard: React state alone cannot block a second click before re-render.
  const pendingRef = React.useRef(false);
  const [error, setError] = React.useState<string | null>(null);
  const [success, setSuccess] = React.useState<string | null>(null);
  const [canEdit, canEditLoading] = useAccessReview({
    group: 'baselinesecurity.openshift.io',
    resource: 'clusterbaselines',
    verb: 'patch',
  });
  const [canAuthor, canAuthorLoading] = useAccessReview({
    group: 'compliance.openshift.io',
    resource: 'tailoredprofiles',
    verb: 'create',
    namespace: COMPLIANCE_NAMESPACE,
  });
  const [creating, setCreating] = React.useState(false);
  // The existing TailoredProfile being edited (fetched object, for the update),
  // or null when the form is in create mode. Reuses the create modal.
  const [editing, setEditing] = React.useState<{ name: string; obj: TailoredProfileResource } | null>(
    null,
  );
  // Name of the tailored profile pending unbind confirmation (null when closed).
  const [unbinding, setUnbinding] = React.useState<string | null>(null);
  // Built-in profile key pending "disable last / stop scanning" confirmation.
  const [disablingLast, setDisablingLast] = React.useState<string | null>(null);
  const [tpName, setTpName] = React.useState('');
  const [tpExtends, setTpExtends] = React.useState('ocp4-cis');
  // Selected rule names to disable (was a free-text list; now a selection).
  const [tpDisable, setTpDisable] = React.useState<string[]>([]);
  const [ruleFilter, setRuleFilter] = React.useState('');
  // Rules to enable on top of the base profile (from the full Rule catalog).
  const [tpEnable, setTpEnable] = React.useState<string[]>([]);
  const [enableFilter, setEnableFilter] = React.useState('');

  // Compliance Operator Profiles: the base-profile options and their rule lists.
  const [profiles] = useK8sWatchResource<ComplianceProfile[]>({
    groupVersionKind: ProfileGVK,
    isList: true,
    namespaced: true,
    namespace: COMPLIANCE_NAMESPACE,
  });
  // Base-profile names, sorted, deduped. Fallback to ocp4-cis so the form is
  // usable before the watch resolves (or if Profiles are not readable).
  const baseProfileNames = React.useMemo(() => {
    const names = (Array.isArray(profiles) ? profiles : [])
      .map((p) => p?.metadata?.name)
      .filter((n): n is string => typeof n === 'string' && n.length > 0);
    return names.length > 0 ? [...new Set(names)].sort() : ['ocp4-cis'];
  }, [profiles]);
  // Rule names in the selected base profile (for the disable selection).
  const baseRules = React.useMemo(() => {
    const p = (Array.isArray(profiles) ? profiles : []).find(
      (x) => x?.metadata?.name === tpExtends,
    );
    const rules = (p?.rules ?? []).filter(
      (r): r is string => typeof r === 'string' && r.length > 0,
    );
    return [...new Set(rules)].sort();
  }, [profiles, tpExtends]);
  const filteredRules = React.useMemo(() => {
    const q = ruleFilter.trim().toLowerCase();
    return q ? baseRules.filter((r) => r.toLowerCase().includes(q)) : baseRules;
  }, [baseRules, ruleFilter]);

  // Full Rule catalog: candidates for enableRules are the rules NOT already in
  // the base profile (those are active anyway). Large list; filtered below.
  const [allRules] = useK8sWatchResource<ComplianceRule[]>({
    groupVersionKind: RuleGVK,
    isList: true,
    namespaced: true,
    namespace: COMPLIANCE_NAMESPACE,
  });
  const enableCandidates = React.useMemo(() => {
    const inBase = new Set(baseRules);
    const names = (Array.isArray(allRules) ? allRules : [])
      .map((r) => r?.metadata?.name)
      .filter((n): n is string => typeof n === 'string' && n.length > 0 && !inBase.has(n));
    return [...new Set(names)].sort();
  }, [allRules, baseRules]);
  // Only render the (long) candidate list once the user filters, so the modal
  // does not paint thousands of checkboxes; always show current selections.
  const filteredEnable = React.useMemo(() => {
    const q = enableFilter.trim().toLowerCase();
    if (!q) return tpEnable;
    return enableCandidates.filter((r) => r.toLowerCase().includes(q)).slice(0, 200);
  }, [enableCandidates, enableFilter, tpEnable]);
  const tpNameRef = React.useRef<HTMLInputElement>(null);
  const createButtonRef = React.useRef<HTMLButtonElement>(null);
  // Track create sessions so Cancel/close can restore focus to the trigger (WCAG 2.4.3).
  const wasCreating = React.useRef(false);
  // Return focus to Unbind / profile switch when those confirm modals close.
  const returnFocusRef = React.useRef<HTMLElement | null>(null);
  // Focus fallback when a confirm modal's trigger (e.g. the Unbind button on a
  // now-removed tailored row) unmounts on success: restore to the tab region.
  const regionRef = React.useRef<HTMLDivElement>(null);
  const confirmModalWasOpen = React.useRef(false);
  const anyConfirmModalOpen = !!unbinding || !!disablingLast;

  // Focus the name field when the create modal opens; return it to the trigger when closing.
  React.useEffect(() => {
    if (creating) {
      tpNameRef.current?.focus();
      wasCreating.current = true;
    } else if (wasCreating.current) {
      createButtonRef.current?.focus();
      wasCreating.current = false;
    }
  }, [creating]);

  // Open the shared modal in edit mode: fetch the TailoredProfile and pre-fill
  // the base profile and disabled rules from its spec.
  const openEdit = async (name: string, trigger: HTMLElement | null) => {
    if (pendingRef.current) return;
    setError(null);
    setSuccess(null);
    try {
      const obj = (await k8sGet({
        model: TailoredProfileModel,
        name,
        ns: COMPLIANCE_NAMESPACE,
      })) as TailoredProfileResource;
      setEditing({ name, obj });
      setTpName(name);
      setTpExtends(obj.spec?.extends || 'ocp4-cis');
      const ruleNames = (list: { name?: string }[] | undefined) =>
        (list ?? [])
          .map((r) => r?.name)
          .filter((n): n is string => typeof n === 'string' && n.length > 0);
      setTpDisable(ruleNames(obj.spec?.disableRules));
      setTpEnable(ruleNames(obj.spec?.enableRules));
      setRuleFilter('');
      setEnableFilter('');
      returnFocusRef.current = trigger;
      setCreating(true);
    } catch (e) {
      setError(errorMessage(e) ?? t('Failed to load tailored profile.'));
    }
  };

  // Restore focus to the control that opened unbind / disable-last confirms.
  React.useEffect(() => {
    if (anyConfirmModalOpen) {
      confirmModalWasOpen.current = true;
      return;
    }
    if (!confirmModalWasOpen.current) return;
    confirmModalWasOpen.current = false;
    const el = returnFocusRef.current;
    returnFocusRef.current = null;
    restoreFocus(el, regionRef);
  }, [anyConfirmModalOpen]);

  // Auto-dismiss success so enable/disable/create feedback does not stick forever.
  React.useEffect(() => {
    if (!success) return;
    const id = window.setTimeout(() => setSuccess(null), SUCCESS_DISMISS_MS);
    return () => window.clearTimeout(id);
  }, [success]);

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
    // Selected rule names; keep only valid ones (manifest also filters).
    // A rule in both lists is contradictory; disable wins (manifest also does
    // this) so we never build a self-conflicting enable+disable set.
    const disable = tpDisable.filter((s) => isValidK8sName(s));
    const disableSet = new Set(disable);
    const enable = tpEnable.filter((s) => isValidK8sName(s) && !disableSet.has(s));

    // Edit mode: the profile is already created and bound, so just update its
    // spec (base + rule lists) on the fetched object (preserves rv via
    // k8sUpdate). No re-bind needed.
    if (editing) {
      try {
        const rule = (n: string) => ({ name: n, rationale: 'set via console' });
        const next: TailoredProfileResource = {
          ...editing.obj,
          spec: {
            ...(editing.obj.spec ?? {}),
            extends: extendsBase,
            disableRules: disable.length ? disable.map(rule) : undefined,
            enableRules: enable.length ? enable.map(rule) : undefined,
          },
        };
        await k8sUpdate({ model: TailoredProfileModel, data: next });
        setCreating(false);
        setEditing(null);
        setTpName('');
        setTpDisable([]);
        setTpEnable([]);
        setRuleFilter('');
        setEnableFilter('');
        setTpExtends('ocp4-cis');
        setSuccess(t('Tailored profile updated.'));
      } catch (e) {
        setError(errorMessage(e) ?? t('Failed to update tailored profile.'));
      } finally {
        pendingRef.current = false;
        setPending(false);
      }
      return;
    }

    // Two steps: create the TailoredProfile, then bind it into spec. Track which
    // step we reached so a bind failure does not read as "nothing happened" and
    // an AlreadyExists on retry is treated as the create having succeeded.
    let created = false;
    try {
      try {
        await k8sCreate({
          model: TailoredProfileModel,
          data: tailoredProfileManifest(name, extendsBase, disable, enable),
        });
      } catch (e) {
        if (!isAlreadyExists(e)) throw e;
        // The name is taken. Adopt it only if its content matches what we would
        // have created (a genuine retry, e.g. after a prior bind failure). A
        // collision with an unrelated profile must not be bound as if it were
        // ours, or the user's rule edits are silently discarded and a different
        // profile is scanned under a false "created and bound" success.
        const existing = (await k8sGet({
          model: TailoredProfileModel,
          name,
          ns: COMPLIANCE_NAMESPACE,
        })) as Record<string, unknown>;
        if (!tailoredProfileSpecMatches(existing, extendsBase, disable, enable)) {
          setError(
            t(
              'A tailored profile named "{{name}}" already exists with different settings. Choose another name.',
              { name },
            ),
          );
          return;
        }
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
            {
              name,
              max: TAILORED_PROFILE_MAX_ITEMS,
              formattedMax: formatCount(TAILORED_PROFILE_MAX_ITEMS, i18n.language),
            },
          ),
        );
        return;
      }
      setCreating(false);
      setTpName('');
      setTpDisable([]);
      setTpEnable([]);
      setRuleFilter('');
      setEnableFilter('');
      // Match closeCreateModal: the next open must be a clean form, not
      // pre-filled with the previous base profile.
      setTpExtends('ocp4-cis');
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

  // Cancel / backdrop close: drop draft fields so the next open is a clean form.
  // Keep error: page-top alert can still show bind/create failures after close
  // (e.g. profile created but not bound).
  const closeCreateModal = () => {
    if (pendingRef.current) return;
    setCreating(false);
    setEditing(null);
    setTpName('');
    setTpDisable([]);
    setTpEnable([]);
    setRuleFilter('');
    setEnableFilter('');
    setTpExtends('ocp4-cis');
  };

  const editDisabled = !canEdit || canEditLoading || pending;
  let editDisabledReason: string | undefined;
  if (!pending) {
    if (canEditLoading) {
      editDisabledReason = t('Checking permissions…');
    } else if (!canEdit) {
      editDisabledReason = t('You do not have permission to edit the baseline.');
    }
  }

  // True when turning off `key` would leave zero built-ins and zero tailored
  // suites, which stops all compliance scanning.
  const wouldStopScanning = (key: string, checked: boolean): boolean => {
    if (checked || !baseline) return false;
    const remaining = toggledProfiles(baseline.spec.profiles ?? [], key, false);
    return remaining.length === 0 && (baseline.spec.tailoredProfiles?.length ?? 0) === 0;
  };

  const toggle = async (key: string, checked: boolean) => {
    if (!baseline || pendingRef.current) return;
    // Empty is allowed: clearing every profile disables scanning.
    const current = baseline.spec.profiles;
    const profiles = toggledProfiles(current ?? [], key, checked);
    pendingRef.current = true;
    setPending(true);
    setPendingKey(key);
    setError(null);
    setSuccess(null);
    try {
      // When profiles is absent (hand-edit / pre-default CR), use add rather
      // than test+replace against [] (test on a missing path always 422s).
      const profileOps =
        current != null
          ? [
              { op: 'test' as const, path: '/spec/profiles', value: current },
              { op: 'replace' as const, path: '/spec/profiles', value: profiles },
            ]
          : [{ op: 'add' as const, path: '/spec/profiles', value: profiles }];
      await k8sPatch({
        model: ClusterBaselineModel,
        resource: baseline,
        // test op (when present): reject if another writer changed profiles.
        data: [...resourceVersionTest(baseline.metadata.resourceVersion), ...profileOps],
      });
      setDisablingLast(null);
      // profileTitle falls back safely for unexpected keys (no PROFILE_INFO throw).
      const title = t(profileTitle(key));
      setSuccess(
        checked
          ? t('{{profile}} enabled. Scans will include this profile on the next run.', {
              profile: title,
            })
          : profiles.length === 0 && (baseline.spec.tailoredProfiles?.length ?? 0) === 0
            ? t(
                '{{profile}} disabled. Scanning is off until you enable a profile.',
                { profile: title },
              )
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

  // Drop a name from spec.tailoredProfiles so scans stop including it. The
  // TailoredProfile CR in openshift-compliance is left in place (unbind ≠ delete).
  const unbindTailored = async (name: string) => {
    if (!baseline || pendingRef.current) return;
    const current = baseline.spec.tailoredProfiles;
    if (!current?.includes(name)) {
      setUnbinding(null);
      return;
    }
    const next = current.filter((n) => n !== name);
    pendingRef.current = true;
    setPending(true);
    setError(null);
    setSuccess(null);
    try {
      await k8sPatch({
        model: ClusterBaselineModel,
        resource: baseline,
        data: [
          ...resourceVersionTest(baseline.metadata.resourceVersion),
          { op: 'test' as const, path: '/spec/tailoredProfiles', value: current },
          { op: 'replace' as const, path: '/spec/tailoredProfiles', value: next },
        ],
      });
      setUnbinding(null);
      const scanningOff =
        (baseline.spec.profiles?.length ?? 0) === 0 && next.length === 0;
      setSuccess(
        scanningOff
          ? t(
              'Tailored profile "{{name}}" unbound. Scanning is off until you enable a profile.',
              { name },
            )
          : t('Tailored profile "{{name}}" unbound. It is no longer included in scans.', {
              name,
            }),
      );
    } catch (e) {
      setError(errorMessage(e) ?? t('Failed to unbind tailored profile.'));
    } finally {
      pendingRef.current = false;
      setPending(false);
    }
  };

  const boundTailored = baseline?.spec.tailoredProfiles ?? [];

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
        <BaselineNotConfigured />
      </PageSection>
    );
  }

  return (
    <PageSection>
      {/* Real DOM focus fallback: PatternFly PageSection is not forwardRef, so a
          ref on it is dropped. restoreFocus targets this sentinel when a modal
          trigger unmounts on success, recovering focus to the tab top. */}
      <div ref={regionRef} tabIndex={-1} />
      {/* Hide page-top error while a modal owns the same message. */}
      {error && !unbinding && !disablingLast && !creating && (
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
      {/* Wait for SAR: other write gates use loading so the button does not
          flash for viewers while useAccessReview is still resolving. */}
      {canAuthor && !canAuthorLoading && (
        <Split hasGutter style={{ marginBottom: 'var(--pf-t--global--spacer--md)' }}>
          <SplitItem isFilled />
          <SplitItem>
            {withDisabledTip(
              editDisabledReason,
              <Button
                ref={createButtonRef}
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
        onClose={closeCreateModal}
        aria-labelledby="new-tp-title"
      >
        <ModalHeader
          title={editing ? t('Edit tailored profile') : t('New tailored profile')}
          labelId="new-tp-title"
        />
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
              // A TailoredProfile cannot be renamed; lock the name when editing.
              readOnlyVariant={editing ? 'default' : undefined}
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
              // Resource names are not prose: suppress spellcheck / managers.
              spellCheck={false}
              autoComplete="off"
              autoCorrect="off"
              autoCapitalize="off"
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
          <FormGroup label={t('Base profile')} fieldId="tp-extends" isRequired>
            <FormSelect
              id="tp-extends"
              value={tpExtends}
              aria-describedby="tp-extends-help"
              // Rules differ per base profile; reset both rule selections.
              onChange={(_e, v) => {
                setTpExtends(v);
                setTpDisable([]);
                setTpEnable([]);
                setRuleFilter('');
                setEnableFilter('');
              }}
            >
              {/* Keep the current value selectable even if the Profile watch has
                  not resolved it (offline / not-yet-loaded). */}
              {(baseProfileNames.includes(tpExtends)
                ? baseProfileNames
                : [tpExtends, ...baseProfileNames]
              ).map((name) => (
                <FormSelectOption key={name} value={name} label={name} />
              ))}
            </FormSelect>
            <FormHelperText>
              <HelperText id="tp-extends-help">
                <HelperTextItem>
                  {t('The Compliance Operator profile this tailored profile extends.')}
                </HelperTextItem>
              </HelperText>
            </FormHelperText>
          </FormGroup>
          <FormGroup
            label={t('Disable rules')}
            fieldId="tp-disable"
            role="group"
            aria-label={t('Disable rules')}
          >
            {baseRules.length === 0 ? (
              <HelperText>
                <HelperTextItem>
                  {t('No rules found for this base profile (or Profiles are still loading).')}
                </HelperTextItem>
              </HelperText>
            ) : (
              <>
                <SearchInput
                  value={ruleFilter}
                  onChange={(_e, v) => setRuleFilter(v)}
                  onClear={() => setRuleFilter('')}
                  placeholder={t('Filter rules')}
                  aria-label={t('Filter rules')}
                />
                <div
                  style={{
                    maxHeight: 220,
                    overflow: 'auto',
                    marginTop: 'var(--pf-t--global--spacer--sm)',
                    border: '1px solid var(--pf-t--global--border--color--default)',
                    borderRadius: 'var(--pf-t--global--border--radius--small)',
                    padding: 'var(--pf-t--global--spacer--sm)',
                  }}
                >
                  {filteredRules.length === 0 ? (
                    <HelperText>
                      <HelperTextItem>{t('No rules match the filter.')}</HelperTextItem>
                    </HelperText>
                  ) : (
                    filteredRules.map((rule) => (
                      <Checkbox
                        key={rule}
                        id={`tp-rule-${rule}`}
                        label={rule}
                        isChecked={tpDisable.includes(rule)}
                        onChange={(_e, checked) =>
                          setTpDisable((prev) =>
                            checked ? [...prev, rule] : prev.filter((r) => r !== rule),
                          )
                        }
                      />
                    ))
                  )}
                </div>
                <FormHelperText>
                  <HelperText id="tp-disable-help">
                    <HelperTextItem>
                      {t('Optional. {{count}} rule selected to disable in the base profile.', {
                        count: tpDisable.length,
                        formattedCount: formatCount(tpDisable.length, i18n.language),
                      })}
                    </HelperTextItem>
                  </HelperText>
                </FormHelperText>
              </>
            )}
          </FormGroup>
          <FormGroup
            label={t('Enable extra rules')}
            fieldId="tp-enable"
            role="group"
            aria-label={t('Enable extra rules')}
          >
            <SearchInput
              value={enableFilter}
              onChange={(_e, v) => setEnableFilter(v)}
              onClear={() => setEnableFilter('')}
              placeholder={t('Search the rule catalog to add rules')}
              aria-label={t('Search the rule catalog to add rules')}
            />
            <div
              style={{
                maxHeight: 220,
                overflow: 'auto',
                marginTop: 'var(--pf-t--global--spacer--sm)',
                border: '1px solid var(--pf-t--global--border--color--default)',
                borderRadius: 'var(--pf-t--global--border--radius--small)',
                padding: 'var(--pf-t--global--spacer--sm)',
              }}
            >
              {filteredEnable.length === 0 ? (
                <HelperText>
                  <HelperTextItem>
                    {enableFilter.trim()
                      ? t('No rules match the filter.')
                      : t('Type to search the rule catalog; selected rules stay listed here.')}
                  </HelperTextItem>
                </HelperText>
              ) : (
                filteredEnable.map((rule) => (
                  <Checkbox
                    key={rule}
                    id={`tp-enable-${rule}`}
                    label={rule}
                    isChecked={tpEnable.includes(rule)}
                    onChange={(_e, checked) =>
                      setTpEnable((prev) =>
                        checked ? [...prev, rule] : prev.filter((r) => r !== rule),
                      )
                    }
                  />
                ))
              )}
            </div>
            <FormHelperText>
              <HelperText id="tp-enable-help">
                <HelperTextItem>
                  {t('Optional. {{count}} extra rule enabled on top of the base profile.', {
                    count: tpEnable.length,
                    formattedCount: formatCount(tpEnable.length, i18n.language),
                  })}
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
            {editing ? t('Save') : t('Create and bind')}
          </Button>
          <Button variant="link" isDisabled={pending} onClick={closeCreateModal}>
            {t('Cancel')}
          </Button>
        </ModalFooter>
      </Modal>
      <Gallery hasGutter minWidths={{ default: '330px' }}>
        {PROFILE_KEYS.map((key) => {
          const info = PROFILE_INFO[key];
          const enabled = baseline.spec.profiles?.includes(key) ?? false;
          const updating = pendingKey === key;
          return (
            <Card key={key}>
              <CardHeader
                actions={{
                  // Any profile can be toggled off, including the last one, which
                  // disables scanning. Spinner next to the switch so the busy
                  // state is visible (not only aria-busy for assistive tech).
                  actions: (
                    <Flex
                      gap={{ default: 'gapSm' }}
                      alignItems={{ default: 'alignItemsCenter' }}
                    >
                      {updating && (
                        <FlexItem>
                          <Spinner
                            size="md"
                            aria-label={t('Updating {{profile}} profile', {
                              profile: t(info.title),
                            })}
                          />
                        </FlexItem>
                      )}
                      <FlexItem>
                        {withDisabledTip(
                          editDisabledReason,
                          <Switch
                            id={`profile-${key}`}
                            aria-label={
                              updating
                                ? t('Updating {{profile}} profile', { profile: t(info.title) })
                                : enabled
                                ? t('Disable {{profile}} profile', { profile: t(info.title) })
                                : t('Enable {{profile}} profile', { profile: t(info.title) })
                            }
                            aria-busy={updating || undefined}
                            isChecked={enabled}
                            isDisabled={editDisabled}
                            onChange={(e, checked) => {
                              // Accidental off of the last suite stops scanning;
                              // confirm before patching so the switch is not a trap.
                              if (wouldStopScanning(key, checked)) {
                                returnFocusRef.current = e.currentTarget;
                                setError(null);
                                setSuccess(null);
                                setDisablingLast(key);
                                return;
                              }
                              void toggle(key, checked);
                            }}
                          />,
                        )}
                      </FlexItem>
                    </Flex>
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
      <Modal
        variant="small"
        isOpen={!!disablingLast}
        onClose={() => {
          if (pendingRef.current) return;
          setDisablingLast(null);
        }}
        aria-labelledby="disable-last-profile-title"
      >
        <ModalHeader
          title={t('Turn off compliance scanning?')}
          labelId="disable-last-profile-title"
        />
        <ModalBody>
          {t(
            'Disabling {{profile}} removes the last selected profile. Scheduled scans and rescan stop until you enable a profile again.',
            {
              profile: disablingLast ? t(profileTitle(disablingLast)) : '',
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
            isDisabled={editDisabled}
            isLoading={pending}
            onClick={() => {
              if (disablingLast) void toggle(disablingLast, false);
            }}
          >
            {t('Turn off scanning')}
          </Button>
          <Button
            variant="link"
            isDisabled={pending}
            onClick={() => {
              if (pendingRef.current) return;
              setDisablingLast(null);
            }}
          >
            {t('Cancel')}
          </Button>
        </ModalFooter>
      </Modal>
      {/* Bound tailored profiles: create alone was a dead end (no list, no
          unbind). Surface membership and let admins stop scanning a suite
          without deleting the TailoredProfile CR. */}
      {boundTailored.length > 0 && (
        <div style={{ marginTop: 'var(--pf-t--global--spacer--lg)' }}>
          <Title headingLevel="h2" size="lg">
            {t('Bound tailored profiles')}
          </Title>
          <Content component="p" style={{ marginBottom: 'var(--pf-t--global--spacer--md)' }}>
            {t(
              'These TailoredProfiles are included in scans. Unbind to stop scanning them; the resource in openshift-compliance is kept.',
            )}
          </Content>
          <Gallery hasGutter minWidths={{ default: '330px' }}>
            {boundTailored.map((name) => (
              <Card key={name}>
                <CardHeader
                  actions={{
                    actions: withDisabledTip(
                      editDisabledReason,
                      <Split hasGutter>
                        <SplitItem>
                          <Button
                            variant="link"
                            isInline
                            isDisabled={editDisabled}
                            aria-label={t('Edit tailored profile {{name}}', { name })}
                            onClick={(e) => void openEdit(name, e.currentTarget)}
                          >
                            {t('Edit')}
                          </Button>
                        </SplitItem>
                        <SplitItem>
                          <Button
                            variant="link"
                            isInline
                            isDisabled={editDisabled}
                            aria-label={t('Unbind tailored profile {{name}}', { name })}
                            onClick={(e) => {
                              returnFocusRef.current = e.currentTarget;
                              setError(null);
                              setSuccess(null);
                              setUnbinding(name);
                            }}
                          >
                            {t('Unbind')}
                          </Button>
                        </SplitItem>
                      </Split>,
                    ),
                    hasNoOffset: true,
                  }}
                >
                  <CardTitle>
                    {name}{' '}
                    <Label isCompact color="blue">
                      {t('Tailored')}
                    </Label>
                  </CardTitle>
                </CardHeader>
              </Card>
            ))}
          </Gallery>
        </div>
      )}
      <Modal
        variant="small"
        isOpen={!!unbinding}
        onClose={() => {
          if (pendingRef.current) return;
          setUnbinding(null);
        }}
        aria-labelledby="unbind-tp-title"
      >
        <ModalHeader title={t('Unbind tailored profile?')} labelId="unbind-tp-title" />
        <ModalBody>
          {t(
            '"{{name}}" will no longer be included in compliance scans. The TailoredProfile resource is not deleted.',
            { name: unbinding ?? '' },
          )}
          {baseline &&
            (baseline.spec.profiles?.length ?? 0) === 0 &&
            (baseline.spec.tailoredProfiles ?? []).filter((n) => n !== unbinding).length ===
              0 && (
              <Alert
                variant="warning"
                isInline
                isLiveRegion
                title={t('This is the last selected profile')}
                style={{ marginTop: 'var(--pf-t--global--spacer--md)' }}
              >
                {t('Scheduled scans and rescan will stop until you enable a profile again.')}
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
            variant="danger"
            isDisabled={editDisabled}
            isLoading={pending}
            onClick={() => {
              if (unbinding) void unbindTailored(unbinding);
            }}
          >
            {t('Unbind')}
          </Button>
          <Button
            variant="link"
            isDisabled={pending}
            onClick={() => {
              if (pendingRef.current) return;
              setUnbinding(null);
            }}
          >
            {t('Cancel')}
          </Button>
        </ModalFooter>
      </Modal>
    </PageSection>
  );
};

export default ProfilesTab;
