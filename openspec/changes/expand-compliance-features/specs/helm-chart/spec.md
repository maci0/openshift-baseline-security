## ADDED Requirements

### Requirement: Non-OLM install via Helm

A Helm chart SHALL install the operator, its CRD, RBAC, and the console plugin
without OLM, for clusters or workflows that do not use OperatorHub.

#### Scenario: Chart installs the operator
- **WHEN** the chart is installed with default values
- **THEN** the CRD, operator Deployment, RBAC, and (when the console is present)
  the console plugin are created, and a default ClusterBaseline is produced unless
  disabled

#### Scenario: Image and defaults are configurable
- **WHEN** the chart is installed with overridden image references and profile set
- **THEN** those values are used instead of the defaults

#### Scenario: Clean uninstall
- **WHEN** the chart is uninstalled
- **THEN** operator-owned resources are removed and the Compliance Operator (a
  shared component) is not uninstalled
