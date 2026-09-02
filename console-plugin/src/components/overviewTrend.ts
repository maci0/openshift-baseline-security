// History-ring helpers shared by Overview (data prep) and OverviewCharts
// (Victory series). Kept out of OverviewCharts so a static import of the
// helpers cannot pull the charting library into the page shell.
import { ScoreSnapshot } from '../models';
import { isFiniteNumber } from '../parse';

// History snapshots to Victory {x: Date, y: score} points.
// Drop points with an unparseable time or non-finite score: a single bad
// snapshot otherwise makes Victory's time-scale domain NaN and silently blanks
// the whole chart (hand-edited / partial status can carry missing scores).
export const toTrendData = (history?: ScoreSnapshot[]): { x: Date; y: number }[] =>
  (history ?? [])
    .map((h) => ({ x: new Date(h.time), y: h.score }))
    .filter((p) => !Number.isNaN(p.x.getTime()) && isFiniteNumber(p.y));

// Content key for history rings: status-only CR updates reallocate the array
// with the same points; identity deps would rebuild Victory Date/path data on
// every reconcile even when the trend did not change (max 30 snapshots).
export const historyContentKey = (history?: ScoreSnapshot[]): string => {
  if (!history?.length) {
    return '';
  }
  let key = '';
  for (let i = 0; i < history.length; i++) {
    const h = history[i];
    if (i > 0) {
      key += '\x01';
    }
    key += `${h.time ?? ''}\0${h.score ?? ''}`;
  }
  return key;
};
