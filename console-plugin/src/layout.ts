// The measurements PatternFly tokens do not cover: how wide a card may get
// before its gallery wraps, how tall a reserved chart slot is, how narrow a
// form field may get before its flex row wraps. Each was a literal in the view
// that used it, so one idea carried a different number in two files (the
// reserved sparkline slot has to match the chart that fills it, and the
// loading gallery has to match the cards it stands in for) and the grid could
// drift between tabs.
//
// A value a PatternFly prop takes as a CSS length is a string; a value read by
// a `style` prop or a chart prop is a number. Spacing, color, and type stay on
// `--pf-t--global--*` tokens in the views that use them.

/** Gallery card width for the Overview score, details, trend, and changes cards. */
export const DASHBOARD_CARD_MIN_WIDTH = '300px';
/** Same gallery, per-profile score cards: narrower, so more fit before wrapping. */
export const SCORE_CARD_MIN_WIDTH = '260px';
/** Gallery card width for the built-in and tailored profile cards. */
export const PROFILE_CARD_MIN_WIDTH = '330px';

/** Reserved height for the composition donut, and for Overview's loading gallery. */
export const DONUT_SKELETON_HEIGHT = '180px';
/** Reserved height for the score trend chart, in Overview and in its error state. */
export const TREND_SKELETON_HEIGHT = '200px';
/** Height of the profile-card loading placeholders. */
export const PROFILE_SKELETON_HEIGHT = '80px';
/** Row height of the remediations loading placeholders. */
export const LOADING_ROW_HEIGHT = '48px';

/** Sparkline slot reserved per profile card so the cards stay bottom-aligned. */
export const SPARKLINE_HEIGHT = 40;
/** Keeps the donut readable and centered in a card stretched to the tallest row. */
export const DONUT_CARD_MIN_HEIGHT = 260;
/** Cap on the focusable Recent-changes scroll region. */
export const CHANGES_MAX_HEIGHT = 260;

/** Below this a filter or header field wraps out of its flex row. */
export const FILTER_FIELD_MIN_WIDTH = 200;
/** Four waiver fields share one row, so each wraps sooner than a lone field. */
export const WAIVER_FIELD_MIN_WIDTH = 140;
/** The schedule input and its Save button share a row. */
export const SCHEDULE_FIELD_MIN_WIDTH = 160;
