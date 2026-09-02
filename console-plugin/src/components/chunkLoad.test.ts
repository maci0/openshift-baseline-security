import { ChunkState, watchChunk } from './chunkLoad';

const flush = (): Promise<void> => Promise.resolve();

describe('watchChunk', () => {
  it('delivers ready when the import resolves', async () => {
    const seen: ChunkState<string>[] = [];
    watchChunk(Promise.resolve('mod'), (next) => {
      seen.push(next);
    });
    await flush();
    expect(seen).toEqual([{ status: 'ready', module: 'mod' }]);
  });

  it('delivers failed when the import rejects', async () => {
    const seen: ChunkState<string>[] = [];
    const pending = Promise.reject(new Error('chunk 404'));
    // Attach a no-op so the rejection is handled even before watchChunk's then.
    pending.catch(() => undefined);
    watchChunk(pending, (next) => {
      seen.push(next);
    });
    await flush();
    expect(seen).toEqual([{ status: 'failed' }]);
  });

  it('ignores a settle after cancel (unmount or retry)', async () => {
    const seen: ChunkState<string>[] = [];
    const cancel = watchChunk(Promise.resolve('mod'), (next) => {
      seen.push(next);
    });
    cancel();
    await flush();
    expect(seen).toEqual([]);
  });

  it('ignores a rejection after cancel', async () => {
    const seen: ChunkState<string>[] = [];
    const pending = Promise.reject(new Error('chunk 404'));
    pending.catch(() => undefined);
    const cancel = watchChunk(pending, (next) => {
      seen.push(next);
    });
    cancel();
    await flush();
    expect(seen).toEqual([]);
  });
});
