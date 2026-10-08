-- name: CreatePlan :one
INSERT INTO plans (name, type, billing, price, validity, validity_unit, time_limit, time_unit,
                   data_limit, data_unit, shared_users, bandwidth_id, router_id, pool_id, enabled,
                   limited, limit_type, expired_plan_id, billing_day, on_login, on_logout, device)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetPlan :one
SELECT * FROM plans WHERE id = ?;

-- name: ListPlans :many
SELECT * FROM plans ORDER BY name LIMIT ? OFFSET ?;

-- name: ListEnabledPlansByType :many
SELECT * FROM plans WHERE enabled = 1 AND type = ? ORDER BY price;

-- name: SearchPlans :many
SELECT * FROM plans WHERE name LIKE '%' || CAST(sqlc.arg(q) AS TEXT) || '%' ORDER BY name LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: UpdatePlan :exec
UPDATE plans SET name = ?, type = ?, billing = ?, price = ?, validity = ?, validity_unit = ?,
    time_limit = ?, time_unit = ?, data_limit = ?, data_unit = ?, shared_users = ?,
    bandwidth_id = ?, router_id = ?, pool_id = ?, enabled = ?,
    limited = ?, limit_type = ?, expired_plan_id = ?, billing_day = ?, on_login = ?, on_logout = ?, device = ?
WHERE id = ?;

-- name: DeletePlan :exec
DELETE FROM plans WHERE id = ?;
