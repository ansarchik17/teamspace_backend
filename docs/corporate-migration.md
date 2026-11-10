# Corporate product boundary

Workspace is now scoped for customer companies rather than public-sector
hierarchies.

## Canonical structure

The product structure is:

```text
organization (customer/tenant)
└── team (optionally nested)
    └── user
        └── manager
```

The canonical identifiers are `organization_id`, `team_id`, and `manager_id`.
They are created by migration
`20260912000000_create_corporate_structure.sql` and are available on the user
model and profile responses.

## Structure builder API

The structure is intentionally an arbitrary tree. The `teams` table is used as
the storage layer for customer-defined organization units; its `parent_team_id`
can point to any active unit in the same organization. `unit_type` is a label,
not a hard-coded level, so one company can use `branch`, `division`, `project`,
or any other name while another company uses a completely different model.

Authenticated endpoints:

- `POST /api/v1/organizations` — create a company (global administrator only);
- `GET /api/v1/organizations/current` — get the current company;
- `GET/POST /api/v1/organizations/current/units` — list or create units;
- `PATCH/DELETE /api/v1/organizations/current/units/:unit_id` — edit or archive
  an empty unit;
- `PUT /api/v1/organizations/current/users/:user_id/assignment` — place an
  employee in a unit and set a manager.

Only the organization owner or a global administrator can change the tree.
Parent units, managers and assigned employees are always checked against the
same organization. Units with children or assigned users cannot be archived.

## Removed from the public product boundary

The API no longer exposes or routes:

- government login and callback endpoints;
- HR-directory registration;
- ministry user export;
- ministry administrator assignment;
- ministry/state-body analytics access and rating endpoints;
- `ministry_ids` on public applications.

The old hierarchy fields are marked non-serializable on the backend response
models. This prevents accidental leakage while existing installations are
migrated.

## Safe database rollout

The corporate migration is additive. Apply it before assigning users to a
customer organization or team. Existing users remain unassigned until an
administrator maps them. Do not drop legacy columns in a live installation
until a data export has been verified and all custom consumers have moved to
the corporate fields; the old columns are no longer part of the API contract,
but may still exist in an older database created outside this repository.

## Identity and access

Keycloak remains the enterprise identity provider. It is suitable for company
SSO and does not imply a government integration. Organization/team assignment
must be controlled by customer administrators, and all list/search/storage
queries must be scoped by the authenticated organization before enabling
multi-company production use.

## Remaining migration work

Legacy publication, contact, and analytics code still contains historical
storage adapters. They are not reachable through the removed government
routes, but should be migrated to organization/team repositories before
physically deleting legacy columns from an existing production database.
