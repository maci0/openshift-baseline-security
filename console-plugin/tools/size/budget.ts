// Transferred-bytes ceilings for the built plugin, in gzip bytes: what a
// browser downloads from the assets nginx.conf already gzips on the way out.
//
// These are tripwires, not targets. They sit well above the shipped size, so
// ordinary growth does not fail the gate but a change that pulls a heavy
// library back into the entry bundle (the charting library OverviewCharts
// holds, for one) trips it. `yarn size` prints the actual figures; set a
// ceiling from those, not from memory. Raising one is a deliberate edit
// carrying the reason; see console-plugin/AGENTS.md.
export interface SizeBudget {
	// Every `*-bundle-*.min.js`: what the browser must download before the
	// CompliancePage route can paint. This is the critical path.
	readonly initialJsGzipBytes: number;
	// Any one `*-chunk-*.min.js`: an async chunk. The tab bodies and the
	// charts load after the first paint, so they get their own ceiling instead
	// of counting against the critical path.
	readonly asyncChunkGzipBytes: number;
	// Everything in dist/, compressed: the whole download a cold cache pays
	// across every tab of the page.
	readonly distGzipBytes: number;
}

export const SIZE_BUDGET: SizeBudget = {
	initialJsGzipBytes: 500_000,
	asyncChunkGzipBytes: 300_000,
	distGzipBytes: 1_000_000,
};
