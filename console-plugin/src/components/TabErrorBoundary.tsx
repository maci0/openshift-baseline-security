import * as React from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, PageSection } from '@patternfly/react-core';
import { errorMessage } from '../errors';
import { reportRenderError } from './renderError';

type TabErrorBoundaryProps = {
  // Shown in the alert and in the reported line, so the throw is attributable
  // to a tab without a stack trace.
  name: string;
  children: React.ReactNode;
};

type TabErrorBoundaryState = {
  // A flag, not the error's own presence: a component may throw `undefined`,
  // and a state keyed on the value would clear itself and remount the same
  // throwing subtree forever.
  failed: boolean;
  error: unknown;
};

// The console renders a dynamic plugin page with no error boundary of its own,
// so a throw anywhere in a tab body unmounts the whole page and leaves a blank
// tab with no record of what failed. This keeps the nav and the other tabs
// usable, names the throw, and reports it once per occurrence.
//
// The failure that reaches here comes from rendering an untrusted value off a
// cluster object, so it is often a bad object the operator can fix: Retry
// clears the captured error and remounts the subtree, and the same data throws
// again if it is still bad. No reset key is needed; React already unmounted the
// failed subtree, so clearing the state mounts it fresh.
export class TabErrorBoundary extends React.Component<
  TabErrorBoundaryProps,
  TabErrorBoundaryState
> {
  override state: TabErrorBoundaryState = { failed: false, error: undefined };

  static getDerivedStateFromError(error: unknown): TabErrorBoundaryState {
    return { failed: true, error };
  }

  override componentDidCatch(error: unknown): void {
    reportRenderError(this.props.name, error);
  }

  override render(): React.ReactNode {
    if (!this.state.failed) {
      return this.props.children;
    }
    return <TabError name={this.props.name} error={this.state.error} onRetry={this.retry} />;
  }

  private readonly retry = (): void => {
    this.setState({ failed: false, error: undefined });
  };
}

const TabError: React.FC<{ name: string; error: unknown; onRetry: () => void }> = ({
  name,
  error,
  onRetry,
}) => {
  const { t } = useTranslation('plugin__baseline-security-console-plugin');
  const detail = errorMessage(error) ?? t('No reason was reported.');
  return (
    <PageSection>
      <Alert
        variant="danger"
        isInline
        isLiveRegion
        title={t('This view failed to render.')}
        <p>
          {/* The tab name is one of four literals we pass in, so it needs no
              dir. The reason comes from a render over untrusted cluster text,
              so its direction and script are unknown. */}
          <b>{name}</b>
          {' '}
          <span dir="auto">{t('Reason: {{detail}}', { detail })}</span>
        </p>
        <Button variant="link" isInline onClick={onRetry}>
          {t('Retry')}
        </Button>
      </Alert>
    </PageSection>
  );
};
