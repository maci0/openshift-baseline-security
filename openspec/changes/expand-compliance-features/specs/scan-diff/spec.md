## ADDED Requirements

### Requirement: Per-check status history

The operator SHALL record, per owned check, the status observed at the two most
recent completed scans, so status transitions can be computed without storing an
unbounded history.

#### Scenario: Status transition captured
- **WHEN** a check was PASS at the previous completed scan and is FAIL at the
  current scan
- **THEN** the status summary records the transition PASS->FAIL for that check

#### Scenario: Bounded storage
- **WHEN** many scans complete over time
- **THEN** only the current and previous status per check are retained (status
  size stays bounded), not one entry per scan

### Requirement: Regression and newly-failing surface

The console SHALL show which checks regressed (were passing, now failing) and
which newly failed since the previous scan, so an admin sees what got worse.

#### Scenario: Regressions listed
- **WHEN** the admin opens the regressions view after a scan where checks changed
- **THEN** checks that went PASS->FAIL are listed, deep-linking to each result

#### Scenario: No previous scan
- **WHEN** only one scan has ever completed
- **THEN** the regressions view shows an empty/first-scan state without error
