## ADDED Requirements

### Requirement: Batch apply with MachineConfigPool pause

Applying multiple node remediations SHALL pause the affected MachineConfigPool(s)
before applying and resume after, so the pool drains and reboots once for the
batch rather than once per remediation.

#### Scenario: Batch reboots once
- **WHEN** an admin batch-applies several node remediations targeting the worker
  pool
- **THEN** the worker MachineConfigPool is paused, all selected remediations are
  set to apply, then the pool is resumed, so nodes reboot a single time

#### Scenario: Non-node remediations skip pausing
- **WHEN** the batch contains only non-node (non-MachineConfig) remediations
- **THEN** no MachineConfigPool is paused and each is applied directly

#### Scenario: Resume on failure
- **WHEN** applying a remediation in a paused-pool batch fails partway
- **THEN** the pool is resumed (never left paused) and the failure is surfaced

### Requirement: Dependency-aware ordering

Remediations with unmet dependencies (MissingDependencies) SHALL be surfaced and
ordered so prerequisite remediations are applied first.

#### Scenario: Dependency state shown
- **WHEN** a remediation is in MissingDependencies state
- **THEN** the UI marks it as blocked and names the missing dependency rather than
  offering a plain Apply that would fail
