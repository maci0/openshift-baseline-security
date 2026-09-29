import * as React from 'react';
import { useTranslation } from 'react-i18next';
import ConsoleLink from './ConsoleLink';

/**
 * The body of the "scanning is disabled" notice. Three views render it: the
 * Overview page and the Compliance tabs, each as an info alert or as an empty
 * state. One definition keeps the wording and the way out of the dead end from
 * drifting between them. ProfilesTab spells the sentence without the link,
 * because it is the page the link points at.
 */
const ScanningDisabledHint: React.FC = () => {
  const { t } = useTranslation('plugin__baseline-security-console-plugin');
  return (
    <>
      {t('No profiles are selected. Enable a profile to resume scanning.')}{' '}
      <ConsoleLink href="/baseline-security/profiles">{t('Go to Profiles')}</ConsoleLink>
    </>
  );
};
ScanningDisabledHint.displayName = 'ScanningDisabledHint';

export default ScanningDisabledHint;
