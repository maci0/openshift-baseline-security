## ADDED Requirements

### Requirement: Waiver attribution and dates

A waiver entry SHALL carry an optional expiry date, review date, and the identity
of who requested and who approved it, in addition to the existing reason, so an
accepted risk has an audit trail.

#### Scenario: Waiver records attribution
- **WHEN** an admin waives a failing check with a reason, a requester, an
  approver, and an expiry date
- **THEN** the ClusterBaseline `spec.waivers` entry persists `reason`,
  `requestedBy`, `approvedBy`, `reviewBy`, and `expiresAt`

#### Scenario: Attribution is optional
- **WHEN** a waiver is added with only a name
- **THEN** the entry is accepted and the optional fields are absent, preserving
  backward compatibility with existing waivers

### Requirement: Expiry enforcement in the score

An expired waiver SHALL NOT exclude its check from the score; the check reverts to
its raw status (a FAIL counts against the score again).

#### Scenario: Expired waiver stops excluding
- **WHEN** a waiver's `expiresAt` is in the past and the underlying check is FAIL
- **THEN** the check is counted as FAIL in the score, not moved to the Waived
  bucket, and the profile's Waived count does not include it

#### Scenario: Unexpired waiver still excludes
- **WHEN** a waiver's `expiresAt` is absent or in the future and the check is FAIL
- **THEN** the check is excluded from the pass/fail denominator into the Waived
  bucket, as today

### Requirement: Surfacing expiring and expired waivers

The console SHALL surface waivers that are expired or expiring soon so an admin
can review or renew them.

#### Scenario: Expired waiver is flagged
- **WHEN** the Results view shows a check whose waiver has expired
- **THEN** the check is shown as failing (not waived) and the detail modal
  indicates the waiver expired with its review/attribution metadata

#### Scenario: Expiring-soon waivers are listed
- **WHEN** any owned waiver expires within a configurable window
- **THEN** the Overview surfaces a count of expiring waivers linking to them
