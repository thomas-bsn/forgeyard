-- Instances set up with Discord before the password_login setting existed get the Discord-setup default:
-- password sign-in off.
INSERT INTO settings (key, value)
SELECT 'password_login', '0'
WHERE EXISTS (SELECT 1 FROM users WHERE role = 'superadmin' AND discord_id IS NOT NULL)
  AND NOT EXISTS (SELECT 1 FROM settings WHERE key = 'password_login');
