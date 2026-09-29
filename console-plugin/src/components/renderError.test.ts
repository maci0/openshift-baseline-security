import { reportRenderError } from './renderError';

describe('reportRenderError', () => {
  // The browser console is the only sink a dynamic plugin has: a render throw
  // otherwise leaves a blank tab with no record. The report is what makes a
  // support request answerable, so it is pinned here.
  let errorSpy: jest.SpyInstance;

  beforeEach(() => {
    errorSpy = jest.spyOn(console, 'error').mockImplementation(() => undefined);
  });

  afterEach(() => {
    errorSpy.mockRestore();
  });

  it('reports the component and the error object so the stack survives', () => {
    const thrown = new Error('not a function');
    expect(reportRenderError('Results', thrown)).toBe('Results: not a function');
    expect(errorSpy).toHaveBeenCalledTimes(1);
    expect(errorSpy).toHaveBeenCalledWith('Results: not a function', thrown);
  });

  it('reports a throw with no reason rather than swallowing it', () => {
    reportRenderError('Remediations', undefined);
    expect(errorSpy).toHaveBeenCalledWith(
      'Remediations: render failed with no message',
      undefined,
    );
  });

  it('uses a thrown string as the reason', () => {
    expect(reportRenderError('Overview', 'bad status shape')).toBe(
      'Overview: bad status shape',
    );
  });
});
