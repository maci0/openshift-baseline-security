import { messageForRenderError, reportRenderError } from './renderError';

describe('messageForRenderError', () => {
  it('names the component and the thrown reason', () => {
    expect(messageForRenderError('Results', new Error('not a function'))).toBe(
      'Results: not a function',
    );
  });

  it('still names the component when the throw carries no message', () => {
    expect(messageForRenderError('Profiles', undefined)).toBe(
      'Profiles: render failed with no message',
    );
  });

  it('uses a thrown string as the reason', () => {
    expect(messageForRenderError('Overview', 'bad status shape')).toBe(
      'Overview: bad status shape',
    );
  });
});

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
});
