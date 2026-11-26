package auth

import (
	"context"
	"encoding/json"
)

// Paging is applied after role filtering so hidden rows
// cannot truncate pages or cause visible records to be skipped.
type pagedStore interface {
	ListUsersPage(context.Context, string, string, int, int) ([]User, error)
	QueryAuditPage(context.Context, string, int, int) ([]AuditLogEntry, error)
}

func (s *PGStore) QueryAuditPage(ctx context.Context, role string, limit, offset int) ([]AuditLogEntry, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,user_id,actor_role,action,resource,resource_id,ip_address,metadata,created_at FROM public.admin_audit_log WHERE ($1='owner' OR actor_role IN ('employee','customer')) ORDER BY created_at DESC,id DESC LIMIT $2 OFFSET $3`, role, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := []AuditLogEntry{}

	for rows.Next() {
		var e AuditLogEntry
		var raw []byte

		if err := rows.Scan(&e.ID, &e.UserID, &e.ActorRole, &e.Action, &e.Resource, &e.ResourceID, &e.IPAddress, &raw, &e.CreatedAt); err != nil {
			return nil, err
		}

		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &e.Metadata)
		}

		entries = append(entries, e)
	}

	return entries, rows.Err()
}

func (s *Service) ListUsersPage(ctx context.Context, actor *User, filter string, limit, offset int) ([]User, error) {
	if actor == nil || !IsStaff(actor.Role) {
		return nil, ErrPermissionDenied
	}

	if actor.Role == RoleEmployee {
		if filter != "" && filter != RoleCustomer {
			return nil, ErrPermissionDenied
		}
		filter = RoleCustomer
	}

	if p, ok := s.store.(pagedStore); ok {
		return p.ListUsersPage(ctx, actor.Role, filter, limit, offset)
	}

	users, err := s.ListUsers(ctx, actor, filter)
	if err != nil {
		return nil, err
	}

	if offset >= len(users) {
		return []User{}, nil
	}

	return users[offset:min(len(users), offset+limit)], nil
}

func (s *PGStore) ListUsersPage(ctx context.Context, role, filter string, limit, offset int) ([]User, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, google_sub, email, name, picture_url,
		       role, is_active, can_manage_inventory,
		       created_at, updated_at
		FROM public.app_user
		WHERE (
			$1 = 'owner'
			OR ($1 = 'employee' AND role = 'customer')
		)
		AND ($2 = '' OR role = $2)
		ORDER BY created_at DESC, id DESC
		LIMIT $3 OFFSET $4
	`, role, filter, limit, offset)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := []User{}

	for rows.Next() {
		var u User

		if err := rows.Scan(
			&u.ID,
			&u.GoogleSub,
			&u.Email,
			&u.Name,
			&u.PictureURL,
			&u.Role,
			&u.IsActive,
			&u.CanManageInventory,
			&u.CreatedAt,
			&u.UpdatedAt,
		); err != nil {
			return nil, err
		}

		users = append(users, u)
	}

	return users, rows.Err()
}
