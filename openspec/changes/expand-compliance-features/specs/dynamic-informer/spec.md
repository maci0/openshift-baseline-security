## ADDED Requirements

### Requirement: Informer-based watches for Compliance CRs

Once the Compliance Operator CRDs exist, the controller SHALL watch the relevant
CRs (ComplianceCheckResult, ComplianceScan, ComplianceRemediation) via informers
and reconcile on their changes, instead of relying on fixed-interval requeue
polling, so status updates promptly after a scan and steady-state API load drops.

#### Scenario: React to scan completion
- **WHEN** a scan completes and its ComplianceCheckResults change
- **THEN** the controller reconciles from the watch event and refreshes the score
  without waiting for the next poll interval

#### Scenario: Tolerate CRDs absent at startup
- **WHEN** the Compliance CRDs are not present when the manager starts
- **THEN** the manager still starts, does not crash, and begins watching once the
  CRDs are installed (falling back to the existing poll until then)

#### Scenario: Only owned suites trigger work
- **WHEN** a foreign (non-owned) ComplianceCheckResult changes
- **THEN** it does not cause a wasteful full reconcile beyond the existing
  ownership filtering
