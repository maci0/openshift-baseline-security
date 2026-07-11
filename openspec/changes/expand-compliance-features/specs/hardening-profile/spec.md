## ADDED Requirements

### Requirement: Curated NSA/CISA hardening profile

The addon SHALL ship a curated NSA/CISA Kubernetes-hardening profile as a
Compliance Operator TailoredProfile manifest that an admin can apply and bind,
mapping the guidance to the rules the operator content provides.

#### Scenario: Ship as a bindable TailoredProfile
- **WHEN** an admin applies the shipped hardening TailoredProfile and adds its name
  to `spec.tailoredProfiles`
- **THEN** it is scanned and scored like any other tailored profile

#### Scenario: Documented rule mapping
- **WHEN** a maintainer reviews the shipped profile
- **THEN** it documents which operator rules it selects and notes any guidance
  items with no corresponding rule (not silently dropped)
