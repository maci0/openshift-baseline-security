## ADDED Requirements

### Requirement: Author a TailoredProfile from the console

The console SHALL let an admin create and edit a Compliance Operator
TailoredProfile: choose a base profile, enable or disable specific rules, and set
variable values, then bind it into the baseline scan.

#### Scenario: Create and bind
- **WHEN** an admin creates a TailoredProfile from a base profile, disables a rule
  and sets a variable, and saves
- **THEN** a TailoredProfile CR is created in openshift-compliance and its name is
  added to `spec.tailoredProfiles` so it is scanned and scored

#### Scenario: Edit an existing tailored profile
- **WHEN** an admin edits the enabled/disabled rules of a bound TailoredProfile
- **THEN** the TailoredProfile CR is updated and the next scan reflects the change

#### Scenario: Gated by RBAC
- **WHEN** the user cannot create/patch TailoredProfiles or patch the
  ClusterBaseline
- **THEN** the authoring controls are disabled

### Requirement: Only when the operator supports tailoring

Authoring SHALL be offered only when the TailoredProfile CRD is present.

#### Scenario: CRD absent
- **WHEN** the Compliance Operator does not provide the TailoredProfile CRD
- **THEN** the authoring UI is hidden and no create is attempted
