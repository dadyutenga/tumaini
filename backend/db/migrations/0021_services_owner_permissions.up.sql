-- Owners hold every permission as an explicit row (the role editor and the
-- permission map read them). Migration 0020 added the services permissions for
-- managers and staff only, so existing Owners need theirs too.
INSERT INTO role_permissions (role_id, business_id, permission_key, scope)
SELECT r.id, r.business_id, p.key, 'all'
FROM roles r CROSS JOIN permissions p
WHERE r.slug = 'owner' AND (p.module = 'services' OR p.key = 'reports.services')
ON CONFLICT DO NOTHING;

UPDATE roles SET perm_version = perm_version + 1 WHERE slug IN ('owner', 'manager', 'staff');
