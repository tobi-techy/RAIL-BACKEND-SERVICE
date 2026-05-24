-- omotadetobiloba@gmail.com is already super_admin from migration 148
-- This is a no-op but ensures the role is set
UPDATE users SET role = 'super_admin' WHERE LOWER(email) = 'omotadetobiloba@gmail.com';
