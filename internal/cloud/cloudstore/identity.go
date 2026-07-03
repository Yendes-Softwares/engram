package cloudstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/Gentleman-Programming/engram/internal/store"
)

const (
	PrincipalKindHuman          = "human"
	PrincipalKindServiceAccount = "service_account"

	PrincipalRoleAdmin  = "admin"
	PrincipalRoleMember = "member"
)

var (
	ErrInvalidPrincipalKind   = errors.New("cloudstore: invalid principal kind")
	ErrInvalidPrincipalRole   = errors.New("cloudstore: invalid principal role")
	ErrLastActiveAdmin        = errors.New("cloudstore: cannot remove last active admin")
	ErrSensitiveAuditMetadata = errors.New("cloudstore: sensitive auth audit metadata is not allowed")
)

type Principal struct {
	ID          string
	Kind        string
	DisplayName string
	Role        string
	Enabled     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type CreatePrincipalParams struct {
	Kind        string
	DisplayName string
	Role        string
	Enabled     *bool
}

type UpdatePrincipalParams struct {
	Role    string
	Enabled bool
}

type HumanUser struct {
	PrincipalID string
	Username    string
	Email       string
	DisplayName string
	Role        string
	Enabled     bool
	CreatedAt   time.Time
}

type CreateHumanUserParams struct {
	Username    string
	Email       string
	DisplayName string
	Role        string
}

type PrincipalToken struct {
	ID                   string
	PrincipalID          string
	TokenPrefix          string
	TokenHash            string
	Name                 string
	CreatedByPrincipalID string
	CreatedAt            time.Time
	LastUsedAt           *time.Time
	RevokedAt            *time.Time
	RevokedByPrincipalID string
	RevocationReason     string
}

type CreatePrincipalTokenParams struct {
	PrincipalID          string
	TokenPrefix          string
	TokenHash            string
	Name                 string
	CreatedByPrincipalID string
}

type ProjectGrant struct {
	PrincipalID          string
	Project              string
	GrantedByPrincipalID string
	CreatedAt            time.Time
}

type CreateProjectGrantParams struct {
	PrincipalID          string
	Project              string
	GrantedByPrincipalID string
}

type AuthAuditEvent struct {
	ID                string
	OccurredAt        time.Time
	ActorPrincipalID  string
	ActorSource       string
	TargetPrincipalID string
	Project           string
	Action            string
	Outcome           string
	ReasonCode        string
	Metadata          map[string]any
}

type AuthAuditQuery struct {
	Limit int
}

func (cs *CloudStore) CreatePrincipal(ctx context.Context, params CreatePrincipalParams) (Principal, error) {
	if cs == nil || cs.db == nil {
		return Principal{}, fmt.Errorf("cloudstore: not initialized")
	}
	kind := strings.TrimSpace(params.Kind)
	if !validPrincipalKind(kind) {
		return Principal{}, ErrInvalidPrincipalKind
	}
	role := strings.TrimSpace(params.Role)
	if !validPrincipalRole(role) {
		return Principal{}, ErrInvalidPrincipalRole
	}
	displayName := strings.TrimSpace(params.DisplayName)
	if displayName == "" {
		return Principal{}, fmt.Errorf("cloudstore: display name is required")
	}
	enabled := true
	if params.Enabled != nil {
		enabled = *params.Enabled
	}

	const q = `
		INSERT INTO cloud_principals (kind, display_name, role, enabled)
		VALUES ($1, $2, $3, $4)
		RETURNING id::text, kind, display_name, role, enabled, created_at, updated_at`
	return scanPrincipal(cs.db.QueryRowContext(ctx, q, kind, displayName, role, enabled))
}

func (cs *CloudStore) GetPrincipal(ctx context.Context, id string) (Principal, error) {
	if cs == nil || cs.db == nil {
		return Principal{}, fmt.Errorf("cloudstore: not initialized")
	}
	const q = `SELECT id::text, kind, display_name, role, enabled, created_at, updated_at FROM cloud_principals WHERE id = $1`
	principal, err := scanPrincipal(cs.db.QueryRowContext(ctx, q, strings.TrimSpace(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return Principal{}, fmt.Errorf("cloudstore: principal not found")
	}
	return principal, err
}

func (cs *CloudStore) ListPrincipals(ctx context.Context) ([]Principal, error) {
	if cs == nil || cs.db == nil {
		return nil, fmt.Errorf("cloudstore: not initialized")
	}
	rows, err := cs.db.QueryContext(ctx, `SELECT id::text, kind, display_name, role, enabled, created_at, updated_at FROM cloud_principals ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("cloudstore: list principals: %w", err)
	}
	defer rows.Close()
	principals := make([]Principal, 0)
	for rows.Next() {
		principal, err := scanPrincipal(rows)
		if err != nil {
			return nil, fmt.Errorf("cloudstore: scan principal: %w", err)
		}
		principals = append(principals, principal)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cloudstore: iterate principals: %w", err)
	}
	return principals, nil
}

func (cs *CloudStore) UpdatePrincipal(ctx context.Context, id string, params UpdatePrincipalParams) error {
	if cs == nil || cs.db == nil {
		return fmt.Errorf("cloudstore: not initialized")
	}
	role := strings.TrimSpace(params.Role)
	if !validPrincipalRole(role) {
		return ErrInvalidPrincipalRole
	}
	tx, err := cs.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("cloudstore: begin update principal tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := guardLastActiveAdminTx(ctx, tx, strings.TrimSpace(id), role, params.Enabled); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE cloud_principals SET role = $2, enabled = $3, updated_at = NOW() WHERE id = $1`, strings.TrimSpace(id), role, params.Enabled)
	if err != nil {
		return fmt.Errorf("cloudstore: update principal: %w", err)
	}
	if err := requireAffected(res, "principal"); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("cloudstore: commit update principal: %w", err)
	}
	return nil
}

func (cs *CloudStore) CreateHumanUser(ctx context.Context, params CreateHumanUserParams) (HumanUser, error) {
	if cs == nil || cs.db == nil {
		return HumanUser{}, fmt.Errorf("cloudstore: not initialized")
	}
	username := strings.TrimSpace(params.Username)
	if username == "" {
		return HumanUser{}, fmt.Errorf("cloudstore: username is required")
	}
	role := strings.TrimSpace(params.Role)
	if !validPrincipalRole(role) {
		return HumanUser{}, ErrInvalidPrincipalRole
	}
	displayName := strings.TrimSpace(params.DisplayName)
	if displayName == "" {
		displayName = username
	}

	tx, err := cs.db.BeginTx(ctx, nil)
	if err != nil {
		return HumanUser{}, fmt.Errorf("cloudstore: begin human user tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var principal Principal
	const principalQ = `
		INSERT INTO cloud_principals (kind, display_name, role, enabled)
		VALUES ($1, $2, $3, TRUE)
		RETURNING id::text, kind, display_name, role, enabled, created_at, updated_at`
	if err := tx.QueryRowContext(ctx, principalQ, PrincipalKindHuman, displayName, role).Scan(&principal.ID, &principal.Kind, &principal.DisplayName, &principal.Role, &principal.Enabled, &principal.CreatedAt, &principal.UpdatedAt); err != nil {
		return HumanUser{}, fmt.Errorf("cloudstore: create human principal: %w", err)
	}

	const humanQ = `
		INSERT INTO cloud_human_users (principal_id, username, email)
		VALUES ($1, $2, $3)
		RETURNING principal_id::text, username, COALESCE(email, ''), created_at`
	var human HumanUser
	if err := tx.QueryRowContext(ctx, humanQ, principal.ID, username, nullableText(params.Email)).Scan(&human.PrincipalID, &human.Username, &human.Email, &human.CreatedAt); err != nil {
		return HumanUser{}, fmt.Errorf("cloudstore: create human user: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return HumanUser{}, fmt.Errorf("cloudstore: commit human user: %w", err)
	}
	human.DisplayName = principal.DisplayName
	human.Role = principal.Role
	human.Enabled = principal.Enabled
	return human, nil
}

func (cs *CloudStore) ListHumanUsers(ctx context.Context) ([]HumanUser, error) {
	if cs == nil || cs.db == nil {
		return nil, fmt.Errorf("cloudstore: not initialized")
	}
	const q = `
		SELECT h.principal_id::text, h.username, COALESCE(h.email, ''), p.display_name, p.role, p.enabled, h.created_at
		FROM cloud_human_users h
		JOIN cloud_principals p ON p.id = h.principal_id
		ORDER BY h.username ASC`
	rows, err := cs.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("cloudstore: list human users: %w", err)
	}
	defer rows.Close()
	users := make([]HumanUser, 0)
	for rows.Next() {
		var user HumanUser
		if err := rows.Scan(&user.PrincipalID, &user.Username, &user.Email, &user.DisplayName, &user.Role, &user.Enabled, &user.CreatedAt); err != nil {
			return nil, fmt.Errorf("cloudstore: scan human user: %w", err)
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cloudstore: iterate human users: %w", err)
	}
	return users, nil
}

func (cs *CloudStore) SetHumanUserEnabled(ctx context.Context, principalID string, enabled bool) error {
	if cs == nil || cs.db == nil {
		return fmt.Errorf("cloudstore: not initialized")
	}
	tx, err := cs.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("cloudstore: begin set human enabled tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var currentRole string
	if err := tx.QueryRowContext(ctx, `SELECT role FROM cloud_principals WHERE id = $1 AND kind = $2 FOR UPDATE`, strings.TrimSpace(principalID), PrincipalKindHuman).Scan(&currentRole); errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("cloudstore: human user not found")
	} else if err != nil {
		return fmt.Errorf("cloudstore: read human user: %w", err)
	}
	if err := guardLastActiveAdminTx(ctx, tx, strings.TrimSpace(principalID), currentRole, enabled); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE cloud_principals SET enabled = $2, updated_at = NOW() WHERE id = $1 AND kind = $3`, strings.TrimSpace(principalID), enabled, PrincipalKindHuman)
	if err != nil {
		return fmt.Errorf("cloudstore: set human enabled: %w", err)
	}
	if err := requireAffected(res, "human user"); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("cloudstore: commit set human enabled: %w", err)
	}
	return nil
}

func (cs *CloudStore) CreatePrincipalToken(ctx context.Context, params CreatePrincipalTokenParams) (PrincipalToken, error) {
	if cs == nil || cs.db == nil {
		return PrincipalToken{}, fmt.Errorf("cloudstore: not initialized")
	}
	principalID := strings.TrimSpace(params.PrincipalID)
	if principalID == "" {
		return PrincipalToken{}, fmt.Errorf("cloudstore: principal id is required")
	}
	tokenPrefix := strings.TrimSpace(params.TokenPrefix)
	if tokenPrefix == "" {
		return PrincipalToken{}, fmt.Errorf("cloudstore: token prefix is required")
	}
	tokenHash := strings.TrimSpace(params.TokenHash)
	if tokenHash == "" {
		return PrincipalToken{}, fmt.Errorf("cloudstore: token hash is required")
	}
	const q = `
		INSERT INTO cloud_principal_tokens (principal_id, token_prefix, token_hash, name, created_by_principal_id)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id::text, principal_id::text, token_prefix, '' AS token_hash, name, COALESCE(created_by_principal_id::text, ''), created_at, last_used_at, revoked_at, COALESCE(revoked_by_principal_id::text, ''), COALESCE(revocation_reason, '')`
	return scanPrincipalToken(cs.db.QueryRowContext(ctx, q, principalID, tokenPrefix, tokenHash, strings.TrimSpace(params.Name), nullableID(params.CreatedByPrincipalID)))
}

func (cs *CloudStore) ListPrincipalTokens(ctx context.Context, principalID string) ([]PrincipalToken, error) {
	if cs == nil || cs.db == nil {
		return nil, fmt.Errorf("cloudstore: not initialized")
	}
	const q = `
		SELECT id::text, principal_id::text, token_prefix, '' AS token_hash, name, COALESCE(created_by_principal_id::text, ''), created_at, last_used_at, revoked_at, COALESCE(revoked_by_principal_id::text, ''), COALESCE(revocation_reason, '')
		FROM cloud_principal_tokens
		WHERE principal_id = $1
		ORDER BY created_at ASC, id ASC`
	rows, err := cs.db.QueryContext(ctx, q, strings.TrimSpace(principalID))
	if err != nil {
		return nil, fmt.Errorf("cloudstore: list principal tokens: %w", err)
	}
	defer rows.Close()
	tokens := make([]PrincipalToken, 0)
	for rows.Next() {
		token, err := scanPrincipalToken(rows)
		if err != nil {
			return nil, fmt.Errorf("cloudstore: scan principal token: %w", err)
		}
		tokens = append(tokens, token)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cloudstore: iterate principal tokens: %w", err)
	}
	return tokens, nil
}

func (cs *CloudStore) RevokePrincipalToken(ctx context.Context, tokenID, revokedByPrincipalID, reason string) error {
	if cs == nil || cs.db == nil {
		return fmt.Errorf("cloudstore: not initialized")
	}
	res, err := cs.db.ExecContext(ctx, `
		UPDATE cloud_principal_tokens
		SET revoked_at = COALESCE(revoked_at, NOW()), revoked_by_principal_id = $2, revocation_reason = $3
		WHERE id = $1`, strings.TrimSpace(tokenID), nullableID(revokedByPrincipalID), nullableText(reason))
	if err != nil {
		return fmt.Errorf("cloudstore: revoke principal token: %w", err)
	}
	return requireAffected(res, "principal token")
}

func (cs *CloudStore) CreateProjectGrant(ctx context.Context, params CreateProjectGrantParams) (ProjectGrant, error) {
	if cs == nil || cs.db == nil {
		return ProjectGrant{}, fmt.Errorf("cloudstore: not initialized")
	}
	principalID := strings.TrimSpace(params.PrincipalID)
	if principalID == "" {
		return ProjectGrant{}, fmt.Errorf("cloudstore: principal id is required")
	}
	project := normalizeCloudProjectGrant(params.Project)
	if project == "" {
		return ProjectGrant{}, fmt.Errorf("cloudstore: project is required")
	}
	const q = `
		INSERT INTO cloud_project_grants (principal_id, project, granted_by_principal_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (principal_id, project) DO UPDATE SET project = EXCLUDED.project
		RETURNING principal_id::text, project, COALESCE(granted_by_principal_id::text, ''), created_at`
	return scanProjectGrant(cs.db.QueryRowContext(ctx, q, principalID, project, nullableID(params.GrantedByPrincipalID)))
}

func (cs *CloudStore) ListProjectGrants(ctx context.Context, principalID string) ([]ProjectGrant, error) {
	if cs == nil || cs.db == nil {
		return nil, fmt.Errorf("cloudstore: not initialized")
	}
	rows, err := cs.db.QueryContext(ctx, `SELECT principal_id::text, project, COALESCE(granted_by_principal_id::text, ''), created_at FROM cloud_project_grants WHERE principal_id = $1 ORDER BY project ASC`, strings.TrimSpace(principalID))
	if err != nil {
		return nil, fmt.Errorf("cloudstore: list project grants: %w", err)
	}
	defer rows.Close()
	grants := make([]ProjectGrant, 0)
	for rows.Next() {
		grant, err := scanProjectGrant(rows)
		if err != nil {
			return nil, fmt.Errorf("cloudstore: scan project grant: %w", err)
		}
		grants = append(grants, grant)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cloudstore: iterate project grants: %w", err)
	}
	return grants, nil
}

func (cs *CloudStore) RevokeProjectGrant(ctx context.Context, principalID, project string) error {
	if cs == nil || cs.db == nil {
		return fmt.Errorf("cloudstore: not initialized")
	}
	normalized := normalizeCloudProjectGrant(project)
	res, err := cs.db.ExecContext(ctx, `DELETE FROM cloud_project_grants WHERE principal_id = $1 AND project = $2`, strings.TrimSpace(principalID), normalized)
	if err != nil {
		return fmt.Errorf("cloudstore: revoke project grant: %w", err)
	}
	_, _ = res.RowsAffected()
	return nil
}

func (cs *CloudStore) HasActiveAdmin(ctx context.Context) (bool, error) {
	if cs == nil || cs.db == nil {
		return false, fmt.Errorf("cloudstore: not initialized")
	}
	var exists bool
	if err := cs.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM cloud_principals WHERE role = $1 AND enabled = TRUE)`, PrincipalRoleAdmin).Scan(&exists); err != nil {
		return false, fmt.Errorf("cloudstore: has active admin: %w", err)
	}
	return exists, nil
}

func (cs *CloudStore) WouldRemoveLastActiveAdmin(ctx context.Context, principalID string) (bool, error) {
	if cs == nil || cs.db == nil {
		return false, fmt.Errorf("cloudstore: not initialized")
	}
	var role string
	var enabled bool
	err := cs.db.QueryRowContext(ctx, `SELECT role, enabled FROM cloud_principals WHERE id = $1`, strings.TrimSpace(principalID)).Scan(&role, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("cloudstore: read admin candidate: %w", err)
	}
	if role != PrincipalRoleAdmin || !enabled {
		return false, nil
	}
	var count int
	if err := cs.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cloud_principals WHERE role = $1 AND enabled = TRUE`, PrincipalRoleAdmin).Scan(&count); err != nil {
		return false, fmt.Errorf("cloudstore: count active admins: %w", err)
	}
	return count <= 1, nil
}

func (cs *CloudStore) InsertAuthAuditEvent(ctx context.Context, event AuthAuditEvent) error {
	if cs == nil || cs.db == nil {
		return fmt.Errorf("cloudstore: not initialized")
	}
	actorSource := strings.TrimSpace(event.ActorSource)
	if actorSource == "" {
		return fmt.Errorf("cloudstore: actor source is required")
	}
	action := strings.TrimSpace(event.Action)
	if action == "" {
		return fmt.Errorf("cloudstore: action is required")
	}
	outcome := strings.TrimSpace(event.Outcome)
	if outcome == "" {
		return fmt.Errorf("cloudstore: outcome is required")
	}
	metadata, err := nullableJSON(event.Metadata)
	if err != nil {
		return err
	}
	_, err = cs.db.ExecContext(ctx, `
		INSERT INTO cloud_auth_audit_log (actor_principal_id, actor_source, target_principal_id, project, action, outcome, reason_code, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, nullableID(event.ActorPrincipalID), actorSource, nullableID(event.TargetPrincipalID), nullableText(event.Project), action, outcome, nullableText(event.ReasonCode), metadata)
	if err != nil {
		return fmt.Errorf("cloudstore: insert auth audit event: %w", err)
	}
	return nil
}

func (cs *CloudStore) ListAuthAuditEvents(ctx context.Context, query AuthAuditQuery) ([]AuthAuditEvent, error) {
	if cs == nil || cs.db == nil {
		return nil, fmt.Errorf("cloudstore: not initialized")
	}
	limit := query.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := cs.db.QueryContext(ctx, `
		SELECT id::text, occurred_at, COALESCE(actor_principal_id::text, ''), actor_source, COALESCE(target_principal_id::text, ''), COALESCE(project, ''), action, outcome, COALESCE(reason_code, ''), metadata
		FROM cloud_auth_audit_log
		ORDER BY occurred_at DESC, id DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("cloudstore: list auth audit events: %w", err)
	}
	defer rows.Close()
	events := make([]AuthAuditEvent, 0)
	for rows.Next() {
		var event AuthAuditEvent
		var metadata []byte
		if err := rows.Scan(&event.ID, &event.OccurredAt, &event.ActorPrincipalID, &event.ActorSource, &event.TargetPrincipalID, &event.Project, &event.Action, &event.Outcome, &event.ReasonCode, &metadata); err != nil {
			return nil, fmt.Errorf("cloudstore: scan auth audit event: %w", err)
		}
		if len(metadata) > 0 {
			if err := json.Unmarshal(metadata, &event.Metadata); err != nil {
				return nil, fmt.Errorf("cloudstore: decode auth audit metadata: %w", err)
			}
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cloudstore: iterate auth audit events: %w", err)
	}
	return events, nil
}

func scanPrincipal(scanner interface{ Scan(dest ...any) error }) (Principal, error) {
	var principal Principal
	if err := scanner.Scan(&principal.ID, &principal.Kind, &principal.DisplayName, &principal.Role, &principal.Enabled, &principal.CreatedAt, &principal.UpdatedAt); err != nil {
		return Principal{}, err
	}
	return principal, nil
}

func scanPrincipalToken(scanner interface{ Scan(dest ...any) error }) (PrincipalToken, error) {
	var token PrincipalToken
	if err := scanner.Scan(&token.ID, &token.PrincipalID, &token.TokenPrefix, &token.TokenHash, &token.Name, &token.CreatedByPrincipalID, &token.CreatedAt, &token.LastUsedAt, &token.RevokedAt, &token.RevokedByPrincipalID, &token.RevocationReason); err != nil {
		return PrincipalToken{}, err
	}
	return token, nil
}

func scanProjectGrant(scanner interface{ Scan(dest ...any) error }) (ProjectGrant, error) {
	var grant ProjectGrant
	if err := scanner.Scan(&grant.PrincipalID, &grant.Project, &grant.GrantedByPrincipalID, &grant.CreatedAt); err != nil {
		return ProjectGrant{}, err
	}
	return grant, nil
}

func requireAffected(res sql.Result, label string) error {
	rows, err := res.RowsAffected()
	if err != nil {
		return nil
	}
	if rows == 0 {
		return fmt.Errorf("cloudstore: %s not found", label)
	}
	return nil
}

func validPrincipalKind(kind string) bool {
	return kind == PrincipalKindHuman || kind == PrincipalKindServiceAccount
}

func validPrincipalRole(role string) bool {
	return role == PrincipalRoleAdmin || role == PrincipalRoleMember
}

func nullableID(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

func nullableText(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

func nullableJSON(metadata map[string]any) (any, error) {
	if len(metadata) == 0 {
		return nil, nil
	}
	if err := rejectSensitiveAuthAuditMetadata(metadata); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("cloudstore: encode auth audit metadata: %w", err)
	}
	return encoded, nil
}

func normalizeCloudProjectGrant(project string) string {
	normalized, _ := store.NormalizeProject(project)
	normalized = strings.TrimSpace(normalized)
	if normalized == "" {
		return ""
	}
	var builder strings.Builder
	previousHyphen := false
	for _, r := range normalized {
		allowed := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_'
		if allowed {
			builder.WriteRune(r)
			previousHyphen = false
			continue
		}
		if !previousHyphen {
			builder.WriteByte('-')
			previousHyphen = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

func guardLastActiveAdminTx(ctx context.Context, tx *sql.Tx, principalID string, nextRole string, nextEnabled bool) error {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('engram_cloud_active_admin_guard'))`); err != nil {
		return fmt.Errorf("cloudstore: lock active admin guard: %w", err)
	}
	var currentRole string
	var currentEnabled bool
	err := tx.QueryRowContext(ctx, `SELECT role, enabled FROM cloud_principals WHERE id = $1 FOR UPDATE`, principalID).Scan(&currentRole, &currentEnabled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cloudstore: read admin guard candidate: %w", err)
	}
	if currentRole != PrincipalRoleAdmin || !currentEnabled {
		return nil
	}
	if nextRole == PrincipalRoleAdmin && nextEnabled {
		return nil
	}
	var activeAdmins int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM cloud_principals WHERE role = $1 AND enabled = TRUE`, PrincipalRoleAdmin).Scan(&activeAdmins); err != nil {
		return fmt.Errorf("cloudstore: count active admins: %w", err)
	}
	if activeAdmins <= 1 {
		return ErrLastActiveAdmin
	}
	return nil
}

func rejectSensitiveAuthAuditMetadata(metadata map[string]any) error {
	for key, value := range metadata {
		if sensitiveAuthAuditKey(key) {
			return fmt.Errorf("%w: %s", ErrSensitiveAuditMetadata, key)
		}
		if err := rejectSensitiveAuthAuditValue(value); err != nil {
			return err
		}
	}
	return nil
}

func rejectSensitiveAuthAuditValue(value any) error {
	if value == nil {
		return nil
	}
	reflected := reflect.ValueOf(value)
	for reflected.Kind() == reflect.Pointer || reflected.Kind() == reflect.Interface {
		if reflected.IsNil() {
			return nil
		}
		reflected = reflected.Elem()
	}
	switch reflected.Kind() {
	case reflect.Map:
		if reflected.Type().Key().Kind() != reflect.String {
			return nil
		}
		for _, key := range reflected.MapKeys() {
			keyText := key.String()
			if sensitiveAuthAuditKey(keyText) {
				return fmt.Errorf("%w: %s", ErrSensitiveAuditMetadata, keyText)
			}
			if err := rejectSensitiveAuthAuditValue(reflected.MapIndex(key).Interface()); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < reflected.Len(); i++ {
			if err := rejectSensitiveAuthAuditValue(reflected.Index(i).Interface()); err != nil {
				return err
			}
		}
	}
	return nil
}

func sensitiveAuthAuditKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "token_prefix" {
		return false
	}
	for _, fragment := range []string{"token", "authorization", "cookie", "secret", "hash", "password", "bearer"} {
		if strings.Contains(key, fragment) {
			return true
		}
	}
	return false
}
