DELETE FROM role_permissions
WHERE permission_key IN (SELECT key FROM permissions WHERE module = 'services' OR key = 'reports.services')
  AND role_id IN (SELECT id FROM roles WHERE slug = 'owner');
