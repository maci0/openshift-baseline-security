## ADDED Requirements

### Requirement: Export a point-in-time compliance report

The console SHALL export a human-readable compliance report (PDF or printable
HTML) capturing the score, per-profile breakdown, failing checks, and active
waivers with their attribution, for a point-in-time snapshot suitable for an
auditor.

#### Scenario: Report reflects current state
- **WHEN** an admin exports a report
- **THEN** the output includes the overall score, each profile's score and counts,
  the failing checks, and the active waivers with reason/attribution/expiry, as of
  the last completed scan

#### Scenario: Client-side generation, no server dependency
- **WHEN** the report is generated
- **THEN** it is produced in-browser from already-watched data without a new
  backend service, and untrusted text (rule titles) is rendered as text, not HTML
