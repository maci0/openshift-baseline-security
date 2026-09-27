// History-ring helpers shared by Overview (data prep) and OverviewCharts
// (Victory series). Domain module, so it lives beside the other pure modules
// under src/ rather than in components/: a static import of it must not pull
// the charting library into the page shell.
import { encodeKeyPart } from './contentKey';
import { ScoreSnapshot } from './models';
import { normalizeScore } from './scoring';

// History snapshots to Victory {x: Date, y: score} points.
// Drop points with an unparseable time or a score that is not a finite number:
// a single bad snapshot otherwise makes Victory's time-scale domain NaN and
// silently blanks the whole chart (hand-edited / partial status can carry
// missing scores). A finite score outside [0,100] is clamped by normalizeScore,
// the same bound the operator's clampHistory enforces on write and that
// normalizeScore applies to every status.score read: a restored or hand-edited
// point must not reach the trend's aria label as "5,000", color the sparkline
// from an impossible value, or plot a point the y domain has to clip.
export const toTrendData = (history?: ScoreSnapshot[]): { x: Date; y: number }[] =>
  (history ?? [])
    // status.history is cluster-supplied and not runtime type-checked, so a
    // hand-edited null or non-string entry must be dropped, not throw and blank
    // the Overview page.
    .map((h) => ({ x: new Date(h?.time), y: normalizeScore(h?.score) }))
    .filter(
      (p): p is { x: Date; y: number } =>
        !Number.isNaN(p.x.getTime()) && p.y !== null,
    )
    // Ascending by time, so index 0 is the oldest point and the last index the
    // newest. The operator appends oldest-first (appendHistoryRing) and
    // historyMax caps the ring at 30, but "oldest first" is a write-side
    // convention, not a schema constraint: a restored status or a hand-edited
    // ring can arrive in any order, and the trend's aria label reads
    // chartData[0] / chartData[last] as from/to. Sorting here makes the single
    // shared read point order-independent instead of leaving four call sites to
    // each assume the convention. Array.prototype.sort is stable, so snapshots
    // sharing an instant keep their ring order.
    .sort((a, b) => a.x.getTime() - b.x.getTime());

// Content key for history rings: status-only CR updates reallocate the array
// with the same points; identity deps would rebuild Victory Date/path data on
// every reconcile even when the trend did not change (max 30 snapshots).
//
// Every field goes through encodeKeyPart, which length-prefixes and type-tags
// it, so the encoding is injective. The key is a React memo dependency, so two
// different histories that produced the same key would leave the trend chart
// painting one series over another; time and score are cluster-supplied values
// that may themselves carry the separator bytes a bare paste would use, which
// lets a hand-edited status forge another ring's key and leaves the chart
// painting the previous trend with no way to refresh it. JSON.stringify is not
// an escape either: it maps NaN and null to the same "null".
export const historyContentKey = (history?: ScoreSnapshot[]): string => {
  if (!history?.length) {
    return '';
  }
  let key = `${history.length}\x01`;
  for (let i = 0; i < history.length; i++) {
    const h = history[i];
    key += encodeKeyPart(h?.time) + encodeKeyPart(h?.score);
  }
  return key;
};
