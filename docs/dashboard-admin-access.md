# Dashboard admin access

`POST /api/v1/dashboard/auth` mints a dashboard admin token only for an `admin` or
`super_admin` who supplies **email, password and a TOTP code** (or an unused backup
code) from the account's enrolled authenticator. An email address alone is refused,
as is an account with no password, no enrolled second factor, or a deployment where
the 2FA service is not wired (the route then answers 503 rather than skipping the
factor). Every mint and every refusal is logged (`dashboard admin token minted` /
`refused`) with the actor, `source_ip`, `request_id` and `trace_id`; tokens are never
logged.

```json
{"email": "admin@example.com", "password": "...", "totp_code": "123456"}
```

## Break-glass: no admin has a usable password or authenticator

From a shell that already holds the deployment's `DATABASE_URL`, `JWT_SECRET` and
`ENCRYPTION_KEY` (the same environment the service runs with):

```sh
rail_service mint-admin-token -email admin@example.com            # 15 minute token
rail_service mint-admin-token -email admin@example.com -ttl 30m   # at most 1h
```

The command refuses unless the email belongs to an existing, active `admin` or
`super_admin`. The token is printed on stdout; an `AUDIT` line (actor, role, expiry,
host, OS user, no token) goes to stderr. It never runs over HTTP.

## Which admin accounts exist (read-only)

```sql
SELECT u.id, u.email, u.role, u.is_active,
       (u.password_hash IS NOT NULL AND u.password_hash <> '') AS has_password,
       COALESCE(t.is_enabled, FALSE)                           AS totp_enabled,
       u.last_login_at
FROM users u
LEFT JOIN user_2fa t ON t.user_id = u.id
WHERE u.role IN ('admin', 'super_admin')
ORDER BY u.role DESC, u.created_at;
```

An admin with `has_password = false` or `totp_enabled = false` cannot use
`/dashboard/auth`; use the break-glass command above, then enrol the second factor.
