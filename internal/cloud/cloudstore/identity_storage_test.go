package cloudstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gentleman-Programming/engram/internal/cloud"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func openIsolatedCloudStore(t *testing.T) *CloudStore {
	t.Helper()
	dsn := os.Getenv("CLOUDSTORE_TEST_DSN")
	if dsn == "" {
		t.Skip("CLOUDSTORE_TEST_DSN not set — skipping integration test (requires Postgres)")
	}
	if !strings.HasPrefix(dsn, "postgres://") && !strings.HasPrefix(dsn, "postgresql://") {
		t.Skip("test requires URL-style CLOUDSTORE_TEST_DSN so a per-test search_path can be attached")
	}

	schema := fmt.Sprintf("cloudstore_identity_%d", time.Now().UnixNano())
	adminDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open admin db: %v", err)
	}
	t.Cleanup(func() { _ = adminDB.Close() })
	if _, err := adminDB.ExecContext(context.Background(), `CREATE SCHEMA `+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { _, _ = adminDB.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS `+schema+` CASCADE`) })

	testDSN := dsn + "?search_path=" + schema
	if strings.Contains(dsn, "?") {
		testDSN = dsn + "&search_path=" + schema
	}
	cs, err := New(cloud.Config{DSN: testDSN})
	if err != nil {
		t.Fatalf("New isolated cloudstore: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func TestAuthFoundationMigrationsAreAdditive(t *testing.T) {
	ctx := context.Background()
	cs := openIsolatedCloudStore(t)

	for _, table := range []string{"cloud_principals", "cloud_human_users", "cloud_principal_tokens", "cloud_project_grants", "cloud_auth_audit_log"} {
		if !tableExists(t, cs.db, table) {
			t.Fatalf("expected migration to create %s", table)
		}
	}

	if _, err := cs.db.ExecContext(ctx, `INSERT INTO cloud_chunks (project_name, chunk_id, created_by, payload) VALUES ('identity-migration', 'chunk-1', 'tester', '{}'::jsonb)`); err != nil {
		t.Fatalf("insert existing sync chunk after migration: %v", err)
	}
	if _, err := cs.db.ExecContext(ctx, `INSERT INTO cloud_mutations (project, entity, entity_key, op, payload) VALUES ('identity-migration', 'session', 'session-1', 'upsert', '{}'::jsonb)`); err != nil {
		t.Fatalf("insert existing sync mutation after migration: %v", err)
	}
	if err := cs.migrate(ctx); err != nil {
		t.Fatalf("second migrate should be additive: %v", err)
	}

	var chunkCount, mutationCount int
	if err := cs.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cloud_chunks WHERE project_name = 'identity-migration' AND chunk_id = 'chunk-1'`).Scan(&chunkCount); err != nil {
		t.Fatalf("count preserved chunk: %v", err)
	}
	if err := cs.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cloud_mutations WHERE project = 'identity-migration' AND entity_key = 'session-1'`).Scan(&mutationCount); err != nil {
		t.Fatalf("count preserved mutation: %v", err)
	}
	if chunkCount != 1 || mutationCount != 1 {
		t.Fatalf("migration must preserve existing sync rows, chunks=%d mutations=%d", chunkCount, mutationCount)
	}
}

func TestCloudstorePrincipalHumanTokenGrantAndAuditLifecycle(t *testing.T) {
	ctx := context.Background()
	cs := openIsolatedCloudStore(t)

	principal, err := cs.CreatePrincipal(ctx, CreatePrincipalParams{Kind: PrincipalKindHuman, DisplayName: "Alice Admin", Role: PrincipalRoleAdmin})
	if err != nil {
		t.Fatalf("CreatePrincipal: %v", err)
	}
	gotPrincipal, err := cs.GetPrincipal(ctx, principal.ID)
	if err != nil {
		t.Fatalf("GetPrincipal: %v", err)
	}
	if gotPrincipal.ID != principal.ID || gotPrincipal.Kind != PrincipalKindHuman || gotPrincipal.Role != PrincipalRoleAdmin || !gotPrincipal.Enabled {
		t.Fatalf("stored principal mismatch: %+v", gotPrincipal)
	}
	if err := cs.UpdatePrincipal(ctx, principal.ID, UpdatePrincipalParams{Role: PrincipalRoleMember, Enabled: false}); err != nil {
		t.Fatalf("UpdatePrincipal: %v", err)
	}
	updated, err := cs.GetPrincipal(ctx, principal.ID)
	if err != nil {
		t.Fatalf("GetPrincipal updated: %v", err)
	}
	if updated.Role != PrincipalRoleMember || updated.Enabled {
		t.Fatalf("principal update did not persist role/enabled: %+v", updated)
	}

	human, err := cs.CreateHumanUser(ctx, CreateHumanUserParams{Username: "alice", Email: "alice@example.test", DisplayName: "Alice Human", Role: PrincipalRoleAdmin})
	if err != nil {
		t.Fatalf("CreateHumanUser: %v", err)
	}
	users, err := cs.ListHumanUsers(ctx)
	if err != nil {
		t.Fatalf("ListHumanUsers: %v", err)
	}
	if len(users) != 1 || users[0].PrincipalID != human.PrincipalID || users[0].Username != "alice" || !users[0].Enabled || users[0].Role != PrincipalRoleAdmin {
		t.Fatalf("human user listing mismatch: %+v", users)
	}
	if _, err := cs.CreateHumanUser(ctx, CreateHumanUserParams{Username: "backup", Email: "backup@example.test", DisplayName: "Backup", Role: PrincipalRoleAdmin}); err != nil {
		t.Fatalf("CreateHumanUser backup admin: %v", err)
	}
	if err := cs.SetHumanUserEnabled(ctx, human.PrincipalID, false); err != nil {
		t.Fatalf("SetHumanUserEnabled(false): %v", err)
	}
	users, err = cs.ListHumanUsers(ctx)
	if err != nil {
		t.Fatalf("ListHumanUsers after disable: %v", err)
	}
	if users[0].Enabled {
		t.Fatalf("disabled human user should list as disabled: %+v", users[0])
	}

	token, err := cs.CreatePrincipalToken(ctx, CreatePrincipalTokenParams{PrincipalID: human.PrincipalID, TokenPrefix: "egc_live_ab12cd34", TokenHash: "hmac-sha256:v1:hash-only", Name: "laptop", CreatedByPrincipalID: principal.ID})
	if err != nil {
		t.Fatalf("CreatePrincipalToken: %v", err)
	}
	tokens, err := cs.ListPrincipalTokens(ctx, human.PrincipalID)
	if err != nil {
		t.Fatalf("ListPrincipalTokens: %v", err)
	}
	if len(tokens) != 1 || tokens[0].ID != token.ID || tokens[0].TokenPrefix != "egc_live_ab12cd34" || tokens[0].TokenHash != "" || tokens[0].RevokedAt != nil {
		t.Fatalf("token metadata listing must expose prefix only and no hash/raw token: %+v", tokens)
	}
	storedHash, err := rawStoredTokenHash(ctx, cs.db, token.ID)
	if err != nil {
		t.Fatalf("raw stored token hash: %v", err)
	}
	if storedHash != "hmac-sha256:v1:hash-only" || strings.Contains(storedHash, "raw") {
		t.Fatalf("database must persist hash-only verifier, got %q", storedHash)
	}
	if err := cs.RevokePrincipalToken(ctx, token.ID, principal.ID, "rotated"); err != nil {
		t.Fatalf("RevokePrincipalToken: %v", err)
	}
	tokens, err = cs.ListPrincipalTokens(ctx, human.PrincipalID)
	if err != nil {
		t.Fatalf("ListPrincipalTokens after revoke: %v", err)
	}
	if tokens[0].RevokedAt == nil || tokens[0].RevokedByPrincipalID != principal.ID || tokens[0].RevocationReason != "rotated" {
		t.Fatalf("token revocation metadata mismatch: %+v", tokens[0])
	}

	grant, err := cs.CreateProjectGrant(ctx, CreateProjectGrantParams{PrincipalID: human.PrincipalID, Project: "Alpha Project", GrantedByPrincipalID: principal.ID})
	if err != nil {
		t.Fatalf("CreateProjectGrant: %v", err)
	}
	if _, err := cs.CreateProjectGrant(ctx, CreateProjectGrantParams{PrincipalID: human.PrincipalID, Project: "alpha-project", GrantedByPrincipalID: principal.ID}); err != nil {
		t.Fatalf("duplicate CreateProjectGrant should be idempotent: %v", err)
	}
	grants, err := cs.ListProjectGrants(ctx, human.PrincipalID)
	if err != nil {
		t.Fatalf("ListProjectGrants: %v", err)
	}
	if len(grants) != 1 || grants[0].Project != "alpha-project" || grant.Project != "alpha-project" {
		t.Fatalf("expected one normalized project grant after duplicate insert, got initial=%+v list=%+v", grant, grants)
	}
	if err := cs.RevokeProjectGrant(ctx, human.PrincipalID, "alpha-project"); err != nil {
		t.Fatalf("RevokeProjectGrant: %v", err)
	}
	grants, err = cs.ListProjectGrants(ctx, human.PrincipalID)
	if err != nil {
		t.Fatalf("ListProjectGrants after revoke: %v", err)
	}
	if len(grants) != 0 {
		t.Fatalf("revoked grant should not list, got %+v", grants)
	}

	if err := cs.InsertAuthAuditEvent(ctx, AuthAuditEvent{ActorPrincipalID: principal.ID, ActorSource: "managed", TargetPrincipalID: human.PrincipalID, Project: "alpha-project", Action: "grant.revoke", Outcome: "success", ReasonCode: "operator_request", Metadata: map[string]any{"token_prefix": "egc_live_ab12cd34"}}); err != nil {
		t.Fatalf("InsertAuthAuditEvent: %v", err)
	}
	events, err := cs.ListAuthAuditEvents(ctx, AuthAuditQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListAuthAuditEvents: %v", err)
	}
	if len(events) != 1 || events[0].Action != "grant.revoke" || events[0].Metadata["token_prefix"] != "egc_live_ab12cd34" {
		t.Fatalf("auth audit event mismatch: %+v", events)
	}
}

func TestCloudstoreIdentityGuardsAndErrorPaths(t *testing.T) {
	ctx := context.Background()
	cs := openIsolatedCloudStore(t)

	if _, err := cs.CreatePrincipal(ctx, CreatePrincipalParams{Kind: "robot", DisplayName: "Bad", Role: PrincipalRoleMember}); err == nil {
		t.Fatal("invalid principal kind must be rejected")
	}
	if _, err := cs.CreateHumanUser(ctx, CreateHumanUserParams{Username: "owner", DisplayName: "Owner", Role: "owner"}); err == nil {
		t.Fatal("invalid human role must be rejected")
	}

	admin, err := cs.CreateHumanUser(ctx, CreateHumanUserParams{Username: "admin", Email: "admin@example.test", DisplayName: "Admin", Role: PrincipalRoleAdmin})
	if err != nil {
		t.Fatalf("CreateHumanUser admin: %v", err)
	}
	if _, err := cs.CreateHumanUser(ctx, CreateHumanUserParams{Username: "admin", Email: "other@example.test", DisplayName: "Duplicate Username", Role: PrincipalRoleMember}); err == nil {
		t.Fatal("duplicate username must be rejected")
	}
	if _, err := cs.CreateHumanUser(ctx, CreateHumanUserParams{Username: "admin2", Email: "admin@example.test", DisplayName: "Duplicate Email", Role: PrincipalRoleMember}); err == nil {
		t.Fatal("duplicate email must be rejected")
	}

	hasAdmin, err := cs.HasActiveAdmin(ctx)
	if err != nil {
		t.Fatalf("HasActiveAdmin: %v", err)
	}
	if !hasAdmin {
		t.Fatal("active admin should exist after creating enabled admin human")
	}
	wouldRemove, err := cs.WouldRemoveLastActiveAdmin(ctx, admin.PrincipalID)
	if err != nil {
		t.Fatalf("WouldRemoveLastActiveAdmin: %v", err)
	}
	if !wouldRemove {
		t.Fatal("single enabled admin should trigger last-admin guard")
	}
	if err := cs.SetHumanUserEnabled(ctx, admin.PrincipalID, false); !errors.Is(err, ErrLastActiveAdmin) {
		t.Fatalf("disabling last active admin must fail with ErrLastActiveAdmin, got %v", err)
	}
	if err := cs.UpdatePrincipal(ctx, admin.PrincipalID, UpdatePrincipalParams{Role: PrincipalRoleMember, Enabled: true}); !errors.Is(err, ErrLastActiveAdmin) {
		t.Fatalf("demoting last active admin must fail with ErrLastActiveAdmin, got %v", err)
	}
	if _, err := cs.CreateHumanUser(ctx, CreateHumanUserParams{Username: "backup", Email: "backup@example.test", DisplayName: "Backup", Role: PrincipalRoleAdmin}); err != nil {
		t.Fatalf("CreateHumanUser backup admin: %v", err)
	}
	wouldRemove, err = cs.WouldRemoveLastActiveAdmin(ctx, admin.PrincipalID)
	if err != nil {
		t.Fatalf("WouldRemoveLastActiveAdmin with backup: %v", err)
	}
	if wouldRemove {
		t.Fatal("two enabled admins should not trigger last-admin guard for one admin")
	}

	if _, err := cs.CreatePrincipalToken(ctx, CreatePrincipalTokenParams{PrincipalID: admin.PrincipalID, TokenPrefix: "egc_live_dup", TokenHash: "hmac-sha256:v1:dup", Name: "one"}); err != nil {
		t.Fatalf("CreatePrincipalToken one: %v", err)
	}
	if _, err := cs.CreatePrincipalToken(ctx, CreatePrincipalTokenParams{PrincipalID: admin.PrincipalID, TokenPrefix: "egc_live_dup2", TokenHash: "hmac-sha256:v1:dup", Name: "two"}); err == nil {
		t.Fatal("duplicate token hash must be rejected")
	}

	if _, err := cs.CreateProjectGrant(ctx, CreateProjectGrantParams{PrincipalID: admin.PrincipalID, Project: ""}); err == nil {
		t.Fatal("empty project grant must be rejected")
	}
	if err := cs.InsertAuthAuditEvent(ctx, AuthAuditEvent{ActorSource: "managed", Action: "token.create", Outcome: "success", Metadata: map[string]any{"raw_token": "egc_live_secret"}}); !errors.Is(err, ErrSensitiveAuditMetadata) {
		t.Fatalf("raw token audit metadata must be rejected, got %v", err)
	}
	if err := cs.InsertAuthAuditEvent(ctx, AuthAuditEvent{ActorSource: "managed", Action: "token.create", Outcome: "success", Metadata: map[string]any{"token_hash": "hmac-sha256:v1:secret"}}); !errors.Is(err, ErrSensitiveAuditMetadata) {
		t.Fatalf("token hash audit metadata must be rejected, got %v", err)
	}
	if err := cs.InsertAuthAuditEvent(ctx, AuthAuditEvent{ActorSource: "managed", Action: "token.create", Outcome: "success", Metadata: map[string]any{"events": []any{map[string]any{"raw_token": "secret"}}}}); !errors.Is(err, ErrSensitiveAuditMetadata) {
		t.Fatalf("nested array token audit metadata must be rejected, got %v", err)
	}
}

func TestCloudstoreLastActiveAdminGuardSerializesConcurrentRemoval(t *testing.T) {
	ctx := context.Background()
	cs := openIsolatedCloudStore(t)

	first, err := cs.CreateHumanUser(ctx, CreateHumanUserParams{Username: "first", Email: "first@example.test", DisplayName: "First", Role: PrincipalRoleAdmin})
	if err != nil {
		t.Fatalf("CreateHumanUser first: %v", err)
	}
	second, err := cs.CreateHumanUser(ctx, CreateHumanUserParams{Username: "second", Email: "second@example.test", DisplayName: "Second", Role: PrincipalRoleAdmin})
	if err != nil {
		t.Fatalf("CreateHumanUser second: %v", err)
	}

	errs := make(chan error, 2)
	go func() { errs <- cs.SetHumanUserEnabled(ctx, first.PrincipalID, false) }()
	go func() { errs <- cs.SetHumanUserEnabled(ctx, second.PrincipalID, false) }()

	firstErr := <-errs
	secondErr := <-errs
	if firstErr == nil && secondErr == nil {
		t.Fatal("concurrent disable of both admins must not both succeed")
	}
	if firstErr != nil && secondErr != nil {
		t.Fatalf("exactly one concurrent disable should succeed, got %v and %v", firstErr, secondErr)
	}
	hasAdmin, err := cs.HasActiveAdmin(ctx)
	if err != nil {
		t.Fatalf("HasActiveAdmin after concurrent disable: %v", err)
	}
	if !hasAdmin {
		t.Fatal("last-admin guard must leave at least one active admin")
	}
}

func TestCloudstoreIdentityPureHelpers(t *testing.T) {
	cases := map[string]string{
		"Alpha Project":     "alpha-project",
		" alpha   project ": "alpha-project",
		"alpha_project":     "alpha_project",
		"alpha.project":     "alpha.project",
		"!!!":               "",
	}
	for input, want := range cases {
		if got := normalizeCloudProjectGrant(input); got != want {
			t.Fatalf("normalizeCloudProjectGrant(%q) = %q, want %q", input, got, want)
		}
	}
	if sensitiveAuthAuditKey("token_prefix") {
		t.Fatal("token_prefix is safe metadata and should not be rejected")
	}
	for _, key := range []string{"raw_token", "authorization_header", "session_cookie", "token_hash", "password"} {
		if !sensitiveAuthAuditKey(key) {
			t.Fatalf("%s should be classified as sensitive audit metadata", key)
		}
	}
	if err := rejectSensitiveAuthAuditMetadata(map[string]any{"nested": map[string]any{"raw_token": "secret"}}); !errors.Is(err, ErrSensitiveAuditMetadata) {
		t.Fatalf("nested sensitive audit metadata must be rejected, got %v", err)
	}
	if err := rejectSensitiveAuthAuditMetadata(map[string]any{"nested": map[string]string{"raw_token": "secret"}}); !errors.Is(err, ErrSensitiveAuditMetadata) {
		t.Fatalf("typed nested map sensitive audit metadata must be rejected, got %v", err)
	}
	if err := rejectSensitiveAuthAuditMetadata(map[string]any{"events": []map[string]any{{"raw_token": "secret"}}}); !errors.Is(err, ErrSensitiveAuditMetadata) {
		t.Fatalf("typed nested slice sensitive audit metadata must be rejected, got %v", err)
	}
}

func tableExists(t *testing.T, db *sql.DB, table string) bool {
	t.Helper()
	var exists bool
	if err := db.QueryRowContext(context.Background(), `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = $1)`, table).Scan(&exists); err != nil {
		t.Fatalf("table exists %s: %v", table, err)
	}
	return exists
}

func rawStoredTokenHash(ctx context.Context, db *sql.DB, tokenID string) (string, error) {
	var hash string
	if err := db.QueryRowContext(ctx, `SELECT token_hash FROM cloud_principal_tokens WHERE id::text = $1`, tokenID).Scan(&hash); err != nil {
		return "", err
	}
	return hash, nil
}

func assertProjects(t *testing.T, got []ProjectGrant, want []string) {
	t.Helper()
	projects := make([]string, 0, len(got))
	for _, grant := range got {
		projects = append(projects, grant.Project)
	}
	if !slices.Equal(projects, want) {
		t.Fatalf("projects mismatch: got=%v want=%v", projects, want)
	}
}
