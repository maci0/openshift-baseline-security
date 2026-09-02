import { historyContentKey, toTrendData } from './overviewTrend';

describe('toTrendData', () => {
  it('returns empty for missing or empty history', () => {
    expect(toTrendData(undefined)).toEqual([]);
    expect(toTrendData([])).toEqual([]);
  });

  it('drops unparseable times and non-finite scores', () => {
    expect(
      toTrendData([
        { time: 'not-a-date', score: 10 },
        { time: '2026-01-01T00:00:00Z', score: Number.NaN },
        { time: '2026-01-01T00:00:00Z', score: Number.POSITIVE_INFINITY },
      ]),
    ).toEqual([]);
  });

  it('keeps finite scores with parseable times', () => {
    const points = toTrendData([{ time: '2026-01-01T00:00:00.000Z', score: 80 }]);
    expect(points).toHaveLength(1);
    expect(points[0].y).toBe(80);
    expect(points[0].x.getTime()).toBe(Date.parse('2026-01-01T00:00:00.000Z'));
  });
});

describe('historyContentKey', () => {
  it('is empty for missing or empty history', () => {
    expect(historyContentKey(undefined)).toBe('');
    expect(historyContentKey([])).toBe('');
  });

  it('is stable across reallocations of the same points', () => {
    const a = [
      { time: '2026-01-01T00:00:00Z', score: 80 },
      { time: '2026-01-02T00:00:00Z', score: 82 },
    ];
    const b = a.map((h) => ({ time: h.time, score: h.score }));
    expect(historyContentKey(a)).toBe(historyContentKey(b));
    expect(historyContentKey(a)).not.toBe(historyContentKey(a.slice(0, 1)));
  });
});
