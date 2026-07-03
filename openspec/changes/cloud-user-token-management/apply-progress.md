# Apply Progress: Cloud User Token Management

## Current branch

`feat/cloud-user-token-management-storage-auth`

## Chain context

- Tracker branch: `feat/cloud-user-token-management`
- Chain strategy: `feature-branch-chain`
- Current slice: PR 1B — `internal/cloud/cloudstore` storage/cloudstore foundation
- Prior slice on this branch: PR 1A — `internal/cloud/auth` principal and token foundation, committed as `d4c3b38 feat(cloud): add auth principal foundation`
- Out of scope for this slice: cloudserver, dashboard, CLI bootstrap, docs beyond this progress file and task checkboxes

## Structured status consumed / produced before apply

```yaml
schemaName: spec-driven
changeName: cloud-user-token-management
artifactStore: openspec
planningHome:
  root: /Users/alanbuscaglia/work/engram/openspec
  changesDir: /Users/alanbuscaglia/work/engram/openspec/changes
changeRoot: /Users/alanbuscaglia/work/engram/openspec/changes/cloud-user-token-management
artifactPaths:
  proposal: [openspec/changes/cloud-user-token-management/proposal.md]
  specs: [openspec/changes/cloud-user-token-management/spec.md]
  design: [openspec/changes/cloud-user-token-management/design.md]
  tasks: [openspec/changes/cloud-user-token-management/tasks.md]
  applyProgress: [openspec/changes/cloud-user-token-management/apply-progress.md]
contextFiles:
  proposal: [openspec/changes/cloud-user-token-management/proposal.md]
  specs: [openspec/changes/cloud-user-token-management/spec.md]
  design: [openspec/changes/cloud-user-token-management/design.md]
  tasks: [openspec/changes/cloud-user-token-management/tasks.md]
  applyProgress: [openspec/changes/cloud-user-token-management/apply-progress.md]
artifacts:
  proposal: done
  specs: done
  design: done
  tasks: done
  applyProgress: done
applyState: ready
dependencies:
  apply: ready
  verify: ready
  sync: blocked
  archive: blocked
actionContext:
  mode: repo-local
  workspaceRoot: /Users/alanbuscaglia/work/engram
  allowedEditRoots: [/Users/alanbuscaglia/work/engram]
  warnings: []
nextRecommended: apply PR2 server middleware/sync grant enforcement after PR1B review
isNonAuthoritative: false
```

## Progress

### Previously completed: PR 1A auth foundation

- Added `internal/cloud/auth/foundation_test.go` covering:
  - principal kind, role, and source validation/string values;
  - managed token format and entropy shape;
  - dedicated token pepper requirement;
  - domain-separated HMAC token verifier behavior;
  - no raw token material in token verifiers;
  - resolver rejection for revoked tokens and disabled principals;
  - legacy env sync/admin principal resolution.
- Added `internal/cloud/auth/foundation.go` with:
  - principal domain types;
  - MVP roles and principal sources;
  - managed token generation;
  - dedicated pepper HMAC token hasher/verifier;
  - storage-agnostic managed token lookup interface;
  - principal resolver with managed-token and legacy-env resolution.
- Updated `tasks.md` to mark the auth RED/GREEN tasks complete.

### Completed in PR 1B storage/cloudstore foundation

- Added `internal/cloud/cloudstore/identity_storage_test.go` with Postgres-gated integration tests for:
  - additive migration creation of `cloud_principals`, `cloud_human_users`, `cloud_principal_tokens`, `cloud_project_grants`, and `cloud_auth_audit_log`;
  - preservation of existing `cloud_chunks` and `cloud_mutations` rows across a second migration run;
  - principal create/get/update lifecycle;
  - human user create/list/disable lifecycle;
  - token metadata create/list/revoke with list responses omitting token hash/raw token material and database persistence using hash-only verifier values;
  - project grant create/list/revoke with normalized duplicate handling;
  - active-admin existence checks and last-active-admin guard helper;
  - auth audit insert/list with non-secret metadata;
  - error paths for invalid principal kind/role, duplicate usernames/emails, duplicate token hashes, and empty projects.
- Extended `internal/cloud/cloudstore/cloudstore.go` migrations additively with the five auth foundation tables and related indexes.
- Added `internal/cloud/cloudstore/identity.go` with storage-only cloudstore types and methods for principals, managed human users, token metadata, project grants, admin checks, and auth audit events.
- Kept the slice storage-only: no cloudserver, dashboard, CLI bootstrap, docs, or legacy env-token behavior changes.
- Removed the local `.codegraph/` index generated during structural inspection so no generated/local files remain.
- Updated persisted `tasks.md` checkboxes for completed PR 1 / PR1B tasks.

## Persisted task checkbox updates

The following task lines are now visibly checked in `openspec/changes/cloud-user-token-management/tasks.md`:

- [x] RED: Add cloudstore migration tests in `internal/cloud/cloudstore/` proving additive creation of `cloud_principals`, `cloud_human_users`, `cloud_principal_tokens`, `cloud_project_grants`, and `cloud_auth_audit_log` without altering existing sync tables.
- [x] GREEN: Extend `internal/cloud/cloudstore/cloudstore.go` migrations and add focused store methods for principal CRUD, human user create/list/enable/disable, token metadata create/list/revoke, project grant create/list/revoke, admin existence checks, and auth audit insertion.
- [x] TRIANGULATE: Add error-path tests for duplicate usernames/emails, invalid roles/kinds, duplicate grants, revoked tokens, missing pepper, and hash-only persistence.
- [x] REFACTOR: Keep storage interfaces small so `internal/cloud/cloudserver` can depend on auth/store contracts without importing dashboard rendering logic.
- [x] Verify: `go test ./internal/cloud/auth ./internal/cloud/cloudstore` and `go test ./...`.
- [x] Rollback boundary: revert new migrations and auth foundation only; legacy env-token sync remains untouched.

## TDD Cycle Evidence

| Task | Test File | Layer | Safety Net | RED | GREEN | TRIANGULATE | REFACTOR |
|------|-----------|-------|------------|-----|-------|-------------|----------|
| PR1B migration foundation | `internal/cloud/cloudstore/identity_storage_test.go` | Integration, Postgres-gated | ✅ `go test ./internal/cloud/cloudstore ./internal/cloud/auth` passed before production edits | ✅ Compile failed on missing storage API/migrations after tests were written | ✅ `go test ./internal/cloud/cloudstore -run 'TestAuthFoundationMigrationsAreAdditive|TestCloudstorePrincipalHumanTokenGrantAndAuditLifecycle|TestCloudstoreIdentityGuardsAndErrorPaths'` passed/skipped cleanly without DSN | ✅ Additive migration preservation test included existing sync rows and second migration run | ✅ Migration statements are additive `CREATE ... IF NOT EXISTS` / indexes only |
| PR1B storage methods | `internal/cloud/cloudstore/identity_storage_test.go` | Integration, Postgres-gated | ✅ Existing cloudstore/auth packages green | ✅ Compile failed on missing `CreatePrincipal`, `CreateHumanUser`, token/grant/admin/audit APIs | ✅ Focused cloudstore test target passed/skipped cleanly without DSN | ✅ Error-path coverage for invalid kind/role, duplicate username/email, duplicate token hash, duplicate grants, empty project, revocation metadata, and hash-only persistence | ✅ Storage-only implementation isolated in `identity.go`; no server/dashboard imports |

### Test Summary

- Total tests written: 3 Postgres-gated integration tests.
- Total tests passing: targeted package and full suite pass in this environment; the new Postgres-gated tests skip at runtime because `CLOUDSTORE_TEST_DSN` is not set.
- Layers used: Integration (3), Unit (0), E2E (0).
- Approval tests: None — this was additive storage behavior, not a behavior-preserving refactor.
- Pure functions created: small validation/normalization helpers in `identity.go`.

## Validation run

```bash
go test ./internal/cloud/cloudstore ./internal/cloud/auth
go test ./...
git diff --check
```

Results:

- `go test ./internal/cloud/cloudstore ./internal/cloud/auth`: PASS.
- `go test ./...`: PASS.
- `git diff --check`: PASS.

Review remediation after PR1B:

- Project grant normalization now canonicalizes whitespace and punctuation to stable grant slugs, so `Alpha Project`, `alpha project`, and `alpha-project` map to the same project grant key.
- Last-active-admin protection moved into storage mutation paths for principal update and human enable/disable, with transaction-level checks returning `ErrLastActiveAdmin`; the guard now uses a transaction-scoped advisory lock so concurrent admin removals serialize.
- Auth audit metadata now rejects sensitive keys such as raw tokens, authorization headers, cookies, token hashes, passwords, and bearer values while still allowing safe `token_prefix` metadata; nested maps, typed maps, arrays, and slices are inspected.
- Added non-Postgres pure helper tests for project grant normalization and sensitive audit metadata classification so important storage-adjacent contracts execute even when `CLOUDSTORE_TEST_DSN` is unset.
- Added a Postgres-gated concurrent last-admin removal regression test for DSN-backed runs.

Additional RED/GREEN detail:

- RED command: `go test ./internal/cloud/cloudstore -run 'TestAuthFoundationMigrationsAreAdditive|TestCloudstorePrincipalHumanTokenGrantAndAuditLifecycle|TestCloudstoreIdentityGuardsAndErrorPaths'`
- RED result: compile failure because `CloudStore` did not yet expose the PR1B storage methods/types.
- GREEN result after implementation: PASS, with the new integration tests skipped because `CLOUDSTORE_TEST_DSN` is not configured locally.

## Files changed

- `internal/cloud/cloudstore/cloudstore.go`
- `internal/cloud/cloudstore/identity.go`
- `internal/cloud/cloudstore/identity_storage_test.go`
- `openspec/changes/cloud-user-token-management/tasks.md`
- `openspec/changes/cloud-user-token-management/apply-progress.md`

## Changed-line estimate

- Code/test slice estimate before this progress update: ~911 added lines plus 6 task-line checkbox edits.
- This exceeds the preferred ~700-line review target, but remains storage-only and does not expand into server wiring. Treat PR1B as a size-risk review item or split storage API/tests if the maintainer wants a tighter diff before commit/PR.

## Deviations from design

- No cloudserver/auth wiring was added in PR1B, by design for this slice.
- `internal/cloud/cloudstore` cannot directly import `internal/cloud/auth` types because existing `internal/cloud/auth` already imports `cloudstore`; storage types are therefore local storage DTOs for now. A later server/auth wiring slice should add adapters without introducing an import cycle.
- Postgres integration assertions are present but skipped locally without `CLOUDSTORE_TEST_DSN`; a Postgres-backed CI/local run should execute them before PR merge.

## Remaining tasks

Exact unchecked task lines remaining in `tasks.md`:

```markdown
- [ ] RED: Add handler tests in `internal/cloud/cloudserver/` proving existing `/sync/*` routes still accept valid legacy tokens and reject invalid/malformed/revoked managed tokens with current auth error style.
- [ ] GREEN: Replace `withAuth` internals in `internal/cloud/cloudserver/cloudserver.go` with principal resolution, request context helpers, and compatibility adapters for existing auth callers.
- [ ] RED: Add push/pull authorization tests for managed principals: granted project succeeds, ungranted project returns `403`, mutation batch with any ungranted project rejects all-or-nothing, mutation pull leaks no ungranted projects.
- [ ] GREEN: Wire principal-aware project authorization through sync chunk and mutation handlers, including `internal/cloud/cloudserver/mutations.go`, while preserving legacy `ENGRAM_CLOUD_ALLOWED_PROJECTS` wildcard/list/empty semantics.
- [ ] TRIANGULATE: Add regression tests for legacy sync principal behavior under `ENGRAM_CLOUD_ALLOWED_PROJECTS=*`, explicit lists, normalized projects, and empty/missing allowlist.
- [ ] REFACTOR: Keep sync payload structs and route registration unchanged; auth changes must be internal only.
- [ ] Verify: targeted cloudserver sync tests and `go test ./...`.
- [ ] Rollback boundary: disable managed-token resolver wiring and retain legacy env-token authorization path.
- [ ] RED: Add admin authorization tests in `internal/cloud/cloudserver/` proving only managed admin principals can create users, issue/revoke tokens, and create/revoke grants; members receive forbidden responses and no state changes.
- [ ] GREEN: Add admin form/API handlers under `internal/cloud/cloudserver/` for human user create/list/enable/disable, token create/list/revoke, and grant create/list/revoke, backed by cloudstore methods.
- [ ] RED: Add dashboard-session tests proving managed admin login succeeds, member admin access fails, disabled/demoted users lose access on the next protected request, and secure cookie attributes are set correctly.
- [ ] GREEN: Update dashboard auth/session handling in `internal/cloud/cloudserver` and `internal/cloud/dashboard` so signed cookies carry principal claims but every protected request revalidates enabled state and role from storage.
- [ ] RED: Add bootstrap tests for legacy dashboard/admin credential creating the first managed admin, rejecting duplicate first-admin bootstrap, and preventing accidental removal of the last usable managed admin path.
- [ ] GREEN: Implement dashboard bootstrap route/handler and last-admin protections, treating `ENGRAM_CLOUD_ADMIN` as explicit bootstrap/recovery access after managed admins exist.
- [ ] RED: Add audit tests for token create/revoke, user create/enable/disable, grant create/revoke, admin login, dashboard bootstrap, accepted/rejected legacy recovery actions, and redaction of raw tokens.
- [ ] GREEN: Emit synchronous `cloud_auth_audit_log` events for admin/security mutations; fail authoritative admin mutations if audit insertion fails.
- [ ] Verify: targeted admin/dashboard/bootstrap tests and `go test ./...`.
- [ ] Rollback boundary: remove admin/bootstrap routes while retaining storage/auth foundation and legacy auth behavior.
- [ ] RED: Add dashboard rendering/handler tests for `/dashboard/admin/users`, `/dashboard/admin/users/list`, token partials, grant partials, and contributor/managed-user separation.
- [ ] GREEN: Update `internal/cloud/dashboard/dashboard.go` and related templ/templates/assets to show `Managed Users` separately from contributor analytics.
- [ ] GREEN: Add server-rendered forms and HTMX-compatible partials for user create, enable/disable, token create/show-once, token revoke, grant create, and grant revoke.
- [ ] TRIANGULATE: Test non-HTMX form POST/redirect behavior and HTMX partial responses; partials must be meaningful HTML without hidden client-side policy logic.
- [ ] TRIANGULATE: Test empty states explaining deny-by-default project grants and token show-once warnings.
- [ ] REFACTOR: Keep policy checks in server/auth/store layers; dashboard code must render outcomes, not make authorization decisions.
- [ ] Verify: dashboard package tests plus `go test ./...`.
- [ ] Rollback boundary: remove dashboard UI routes/templates without affecting already-tested admin handlers.
- [ ] RED: Add CLI tests in `cmd/engram/` for `engram cloud bootstrap admin --username ...`, duplicate bootstrap refusal, optional token issuance printed once, optional project grants, invalid input, and audit event creation.
- [ ] GREEN: Implement `engram cloud bootstrap admin` in `cmd/engram/cloud.go`, using cloud runtime DB configuration by default and an existing DSN override convention only if already present.
- [ ] TRIANGULATE: Test that raw managed tokens are never persisted, logged, audited, rendered in token metadata lists, or printed except the creation/bootstrap response.
- [ ] GREEN: Update docs discovery targets affected by cloud setup and sync auth, starting with `README.md`, `docs/`, `CONTRIBUTING.md`, and any cloud deployment docs found by `rg "ENGRAM_CLOUD_TOKEN|ENGRAM_CLOUD_ADMIN|ENGRAM_CLOUD_ALLOWED_PROJECTS|cloud bootstrap"`.
- [ ] GREEN: Document managed users/tokens, dedicated token pepper, first-admin dashboard bootstrap, CLI bootstrap, project grants, deny-by-default managed principals, legacy env-token migration, and rollback to legacy sync credentials.
- [ ] RED: Add regression tests that `/sync/*` route methods, paths, request schemas, and response schemas remain unchanged for existing clients.
- [ ] GREEN: Fix any contract drift found by regression tests without changing MVP payloads.
- [ ] REFACTOR: Run `gofmt` on touched Go files and remove any temporary test seams not needed by production behavior.
- [ ] Verify: `go test ./...`, targeted cloud tests (`go test ./internal/cloud/... ./cmd/engram`), and `go test -cover ./...`.
- [ ] Rollback boundary: revert CLI/docs/audit hardening slice while keeping prior reviewed server behavior intact.
- [ ] Managed human users are distinct from contributor analytics.
- [ ] Managed tokens authenticate principals; authorization uses principal role and project grants.
- [ ] Token hashes use a dedicated cloud token pepper, not the dashboard/session signing secret.
- [ ] Raw token values are shown once and never stored or audited.
- [ ] Disabled users, revoked tokens, and revoked grants stop future access immediately.
- [ ] Legacy `ENGRAM_CLOUD_TOKEN`, `ENGRAM_CLOUD_ADMIN`, and `ENGRAM_CLOUD_ALLOWED_PROJECTS` behavior remains compatible during migration.
- [ ] Managed principals are deny-by-default for project sync.
- [ ] Dashboard cookies are `HttpOnly`, `SameSite=Lax` or stronger, and `Secure` under HTTPS/production rules.
- [ ] CLI and dashboard can create the first managed admin safely.
- [ ] Audit events cover all required MVP identity/security actions without secret leakage.
- [ ] Documentation matches real routes, commands, environment variables, and rollback behavior.
```

## Risks

- PR1B code/test diff is above the preferred ~700-line target. It remains bounded to storage-only files, but reviewers may still prefer a split before commit/PR.
- New cloudstore integration tests require `CLOUDSTORE_TEST_DSN` to execute against Postgres; in this environment they compile and skip.
- Storage DTOs currently duplicate some auth-domain string values to avoid the existing `auth -> cloudstore` import direction. The next auth/server wiring slice should be careful not to create a cycle.
