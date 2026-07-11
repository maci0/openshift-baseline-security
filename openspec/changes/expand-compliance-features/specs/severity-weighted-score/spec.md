## ADDED Requirements

### Requirement: Opt-in severity-weighted scoring

The ClusterBaseline SHALL support an opt-in scoring mode that weights each FAIL by
the check's severity, so a high-severity failure lowers the score more than a
low-severity one. The default mode remains the flat pass/fail ratio.

#### Scenario: Flat mode is the default
- **WHEN** `spec.scoring.mode` is unset or "Flat"
- **THEN** the score is the existing pooled PASS/(PASS+FAIL) ratio

#### Scenario: Weighted mode changes the score
- **WHEN** `spec.scoring.mode` is "SeverityWeighted"
- **THEN** the score is computed from severity-weighted PASS and FAIL totals, so
  the same counts with more high-severity FAILs yield a lower score than in flat
  mode

#### Scenario: Excluded statuses unaffected
- **WHEN** scoring in weighted mode
- **THEN** MANUAL/INFO/ERROR/INCONSISTENT/NOT-APPLICABLE and waived checks remain
  excluded from the denominator exactly as in flat mode

#### Scenario: Score stays in range
- **WHEN** any severity mix is scored in weighted mode
- **THEN** the resulting score is an integer in 0..100 (or nil when nothing is
  scorable)
