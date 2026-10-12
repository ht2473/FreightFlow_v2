-- name: UpsertRole :exec
INSERT INTO core.role (code, name, description, builtin)
VALUES ($1, $2, $3, $4)
ON CONFLICT (code) DO UPDATE SET name = EXCLUDED.name, description = EXCLUDED.description, builtin = EXCLUDED.builtin;

-- name: ListRoles :many
SELECT r.*,
       coalesce(array_agg(rp.permission ORDER BY rp.permission) FILTER (WHERE rp.permission IS NOT NULL),
                '{}')::text[] AS permissions
FROM core.role r
LEFT JOIN core.role_permission rp ON rp.role_code = r.code
GROUP BY r.code
ORDER BY r.builtin DESC, r.name;

-- name: DeleteRolePermissions :exec
DELETE FROM core.role_permission WHERE role_code = $1;

-- name: InsertRolePermission :exec
INSERT INTO core.role_permission (role_code, permission) VALUES ($1, $2) ON CONFLICT DO NOTHING;

-- name: ListUserRoles :many
SELECT role_code FROM core.user_role WHERE user_id = $1 ORDER BY role_code;

-- name: DeleteUserRoles :exec
DELETE FROM core.user_role WHERE user_id = $1;

-- name: AddUserRole :exec
INSERT INTO core.user_role (user_id, role_code) VALUES ($1, $2) ON CONFLICT DO NOTHING;

-- name: ListUserPermissions :many
SELECT DISTINCT rp.permission
FROM core.user_role ur
JOIN core.role_permission rp ON rp.role_code = ur.role_code
WHERE ur.user_id = $1
ORDER BY rp.permission;

-- name: CountActiveUsersWithRole :one
SELECT count(*)
FROM core.user_role ur
JOIN core.app_user u ON u.id = ur.user_id
WHERE ur.role_code = $1 AND u.status = 'active';

-- name: RoleExists :one
SELECT EXISTS (SELECT 1 FROM core.role WHERE code = $1);
