package globals

// Every fixed SQL statement the server runs, grouped by the package that runs it, so
// queries can be reviewed in one place. Parameters are always $n placeholders.
//
// A few statements need a part only known at run time:
//   - names ending in Fmt are fmt templates; their %s verbs only ever receive
//     identifiers sanitised with pgx.Identifier or literals quoted by the caller
//   - names ending in Select are the fixed head of a query whose WHERE, ORDER BY and
//     LIMIT the caller appends from validated filters
//   - SQLListUsers holds a {status} marker the caller replaces with one of the
//     SQLUserStatus* filters
//
// The querybuilder package's templates are not here: they are the generic CRUD query
// builder, not statements in their own right.

// ---------------------------------------------------------------------------------
// admin: audit and logs
// ---------------------------------------------------------------------------------

// SQLAuditSelect reads audit entries; the caller adds filters, ORDER BY id DESC and LIMIT
const SQLAuditSelect = `SELECT id, at, action, target, service, actor_user, via_service, amr, org_id, before, after, ip, request_id
	          FROM ohc_audit_events`

// SQLLogsSelect reads log rows; the caller adds filters, ORDER BY at DESC and LIMIT
const SQLLogsSelect = `SELECT at, level, msg, request_id, service, attrs FROM ohc_logs`

// ---------------------------------------------------------------------------------
// admin: overview
// ---------------------------------------------------------------------------------

// sqlNotAdminChanges is the audit actions that are logins and logouts, not changes an admin made
const sqlNotAdminChanges = `('user.login', 'user.login_failed', 'user.logout', 'user.logout_all')`

// SQLOverviewCounts gives the dashboard's tiles for the UTC day: people, active today,
// sessions ended, token reuse, admin changes, admin changes made with an OTP login
const SQLOverviewCounts = `
		SELECT (SELECT count(*) FROM ohc_users WHERE id NOT LIKE 'svc:%'),
		       (SELECT count(DISTINCT target) FROM ohc_audit_events WHERE action = 'user.login' AND at >= date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
		       (SELECT count(*) FROM ohc_audit_events WHERE action IN ('session.revoke', 'refresh_token.reuse') AND at >= date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
		       (SELECT count(*) FROM ohc_audit_events WHERE action = 'refresh_token.reuse' AND at >= date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
		       (SELECT count(*) FROM ohc_audit_events WHERE actor_user IS NOT NULL AND action NOT IN ` + sqlNotAdminChanges + ` AND at >= date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
		       (SELECT count(*) FROM ohc_audit_events WHERE actor_user IS NOT NULL AND action NOT IN ` + sqlNotAdminChanges + ` AND 'otp' = ANY(amr) AND at >= date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')`

// SQLOverviewFlaggedUsers lists active people with their OTP status and whether their
// sessions were ended by token reuse in the last day
const SQLOverviewFlaggedUsers = `
		SELECT id, username, COALESCE(roles, '{}') AS roles, totp_secret IS NOT NULL AS has_otp,
		       EXISTS (SELECT 1 FROM ohc_audit_events a WHERE a.target = ohc_users.id AND a.action = 'refresh_token.reuse'
		                  AND a.at > now() - interval '1 day') AS reused
		  FROM ohc_users WHERE id NOT LIKE 'svc:%' AND disabled_at IS NULL ORDER BY username`

// ---------------------------------------------------------------------------------
// admin: services
// ---------------------------------------------------------------------------------

// sqlServiceSelect is a service with its key counts and the last time any key signed
const sqlServiceSelect = `
SELECT s.id, s.role, s.entra_client_id, s.disabled_at,
       count(k.kid) FILTER (WHERE k.revoked_at IS NULL)     AS active_keys,
       count(k.kid) FILTER (WHERE k.revoked_at IS NOT NULL) AS revoked_keys,
       max(k.last_used_at)                                  AS last_signed_at
  FROM ohc_services s
  LEFT JOIN ohc_service_keys k ON k.service_id = s.id`

// SQLListServices lists services by id after a cursor, matching q on id or role. $1 q, $2 cursor, $3 limit
const SQLListServices = sqlServiceSelect + `
		WHERE ($1 = '' OR s.id ILIKE '%' || $1 || '%' OR s.role ILIKE '%' || $1 || '%')
		  AND s.id > $2
		GROUP BY s.id ORDER BY s.id LIMIT $3`

// SQLGetService reads one service by id
const SQLGetService = sqlServiceSelect + ` WHERE s.id = $1 GROUP BY s.id`

// SQLServiceKeys lists a service's keys, newest first
const SQLServiceKeys = `SELECT kid, created_at, last_used_at, revoked_at, revoked_reason
		FROM ohc_service_keys WHERE service_id = $1 ORDER BY created_at DESC`

// ---------------------------------------------------------------------------------
// admin: service changes
// ---------------------------------------------------------------------------------

const SQLInsertServiceKey = `INSERT INTO ohc_service_keys (kid, service_id, public_key) VALUES ($1, $2, $3)`

// SQLLockService reads a service's role and disabled state, locking the row for change
const SQLLockService = `SELECT role, disabled_at IS NOT NULL FROM ohc_services WHERE id = $1 FOR UPDATE`

const SQLInsertService = `INSERT INTO ohc_services (id, role, entra_client_id) VALUES ($1, $2, $3)`

const SQLUpdateServiceRole = `UPDATE ohc_services SET role = $2 WHERE id = $1`

// SQLRevokeServiceKey revokes one active key. $1 service, $2 kid, $3 reason
const SQLRevokeServiceKey = `UPDATE ohc_service_keys SET revoked_at = now(), revoked_reason = $3
			WHERE service_id = $1 AND kid = $2 AND revoked_at IS NULL`

const (
	SQLEnableService  = `UPDATE ohc_services SET disabled_at = NULL WHERE id = $1`
	SQLDisableService = `UPDATE ohc_services SET disabled_at = now() WHERE id = $1`
)

// ---------------------------------------------------------------------------------
// admin: users
// ---------------------------------------------------------------------------------

// sqlUsersCTE works out each user's derived fields: last login from the audit log, live
// sessions from refresh tokens. A session is live while it holds an unused, unrevoked,
// unexpired refresh token.
const sqlUsersCTE = `
WITH u AS (
  SELECT id, username, display_name, COALESCE(roles, '{}') AS roles, created_at, disabled_at,
         COALESCE(password_hash, '') <> '' AS has_password,
         totp_secret IS NOT NULL           AS has_otp,
         id LIKE 'svc:%'                   AS is_service,
         COALESCE(needs_new_password, FALSE) AS needs_new_password,
         (SELECT max(a.at) FROM ohc_audit_events a WHERE a.target = ohc_users.id AND a.action = 'user.login') AS last_login,
         (SELECT count(DISTINCT t.family_id) FROM ohc_refresh_tokens t
           WHERE t.user_id = ohc_users.id AND t.revoked_at IS NULL AND t.used_at IS NULL AND t.expires_at > now()) AS live_sessions,
         EXISTS (SELECT 1 FROM ohc_audit_events a WHERE a.target = ohc_users.id
                    AND a.action IN ('refresh_token.reuse', 'session.revoke') AND a.at > now() - interval '7 days') AS sessions_ended
    FROM ohc_users)`

// sqlUserSearch matches $1 against username, id, display name or any role; ” matches all
const sqlUserSearch = `($1 = '' OR username ILIKE '%' || $1 || '%' OR id ILIKE '%' || $1 || '%'
	OR display_name ILIKE '%' || $1 || '%' OR EXISTS (SELECT 1 FROM unnest(roles) r WHERE r ILIKE '%' || $1 || '%'))`

// The user list's tabs, as filters over the users CTE
const (
	SQLUserStatusAll           = `true`
	SQLUserStatusActive        = `NOT is_service AND disabled_at IS NULL`
	SQLUserStatusSessionsEnded = `NOT is_service AND disabled_at IS NULL AND sessions_ended`
	SQLUserStatusDisabled      = `disabled_at IS NOT NULL`
	SQLUserStatusService       = `is_service`
)

// SQLUserStatusMarker is replaced in SQLListUsers with one of the SQLUserStatus* filters
const SQLUserStatusMarker = "{status}"

// SQLListUsers lists users by username after a cursor. $1 search, $2 cursor, $3 limit
const SQLListUsers = sqlUsersCTE + `
		SELECT * FROM u WHERE ` + SQLUserStatusMarker + ` AND ` + sqlUserSearch + ` AND username > $2 ORDER BY username LIMIT $3`

// SQLCountUsers counts the users matching search $1 on each tab: all, active,
// sessions ended, disabled, service
const SQLCountUsers = sqlUsersCTE + `
		SELECT count(*),
		       count(*) FILTER (WHERE ` + SQLUserStatusActive + `),
		       count(*) FILTER (WHERE ` + SQLUserStatusSessionsEnded + `),
		       count(*) FILTER (WHERE ` + SQLUserStatusDisabled + `),
		       count(*) FILTER (WHERE ` + SQLUserStatusService + `)
		  FROM u WHERE ` + sqlUserSearch

// SQLGetUserView reads one user with their derived fields
const SQLGetUserView = sqlUsersCTE + ` SELECT * FROM u WHERE id = $1`

// SQLUserSessions lists a user's login sessions (refresh token families), latest first
const SQLUserSessions = `
		SELECT family_id, client_id, COALESCE(min(amr), '{}') AS amr, -- one amr per session; min returns it as an array
		       min(auth_time) AS started_at, max(expires_at) AS expires_at,
		       bool_or(revoked_at IS NULL AND used_at IS NULL AND expires_at > now()) AS live,
		       bool_or(revoked_at IS NOT NULL) AS revoked
		  FROM ohc_refresh_tokens WHERE user_id = $1
		 GROUP BY family_id, client_id ORDER BY max(expires_at) DESC LIMIT 50`

// SQLUserCount counts the people who can sign in. Service rows (svc:<id>), which
// record a service's own writes, aren't people and don't count.
const SQLUserCount = `SELECT count(*) FROM ohc_users WHERE id NOT LIKE 'svc:%'`

// SQLFirstAdminLock serialises creating the first admin across instances starting
// together. It is held until the transaction ends.
const SQLFirstAdminLock = `SELECT pg_advisory_xact_lock(hashtext('ohc_first_admin'))`

// ---------------------------------------------------------------------------------
// admin: user changes
// ---------------------------------------------------------------------------------

// SQLCountOtherRoleHolders counts active users other than $2 holding role $1
const SQLCountOtherRoleHolders = `SELECT count(*) FROM ohc_users WHERE $1 = ANY(roles) AND disabled_at IS NULL AND id <> $2`

// SQLLockUser reads a user's roles and disabled state, locking the row for change
const SQLLockUser = `SELECT COALESCE(roles, '{}'), disabled_at IS NOT NULL FROM ohc_users WHERE id = $1 FOR UPDATE`

const SQLUpdateUserRoles = `UPDATE ohc_users SET roles = $2 WHERE id = $1`

const (
	SQLEnableUser  = `UPDATE ohc_users SET disabled_at = NULL WHERE id = $1`
	SQLDisableUser = `UPDATE ohc_users SET disabled_at = now() WHERE id = $1`
)

// SQLSessionLive reports whether user $1's session $2 is live; no row if it doesn't exist
const SQLSessionLive = `SELECT bool_or(revoked_at IS NULL AND used_at IS NULL AND expires_at > now())
			FROM ohc_refresh_tokens WHERE user_id = $1 AND family_id = $2 HAVING count(*) > 0`

// SQLResetPassword sets temporary password hash $1 on user $2, who must replace it at
// their next login. Users without a password (Entra, say) are left alone: no row is
// updated. Resetting again replaces the temporary password.
const SQLResetPassword = `UPDATE ohc_users SET needs_new_password = TRUE, password_hash = $1 WHERE id = $2 AND password_hash IS NOT NULL`

// ---------------------------------------------------------------------------------
// audit
// ---------------------------------------------------------------------------------

const SQLInsertAuditEvent = `INSERT INTO ` + AuditTable + `
	(action, target, service, actor_user, via_service, amr, org_id, before, after, ip, request_id)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`

// ---------------------------------------------------------------------------------
// auth: users
// ---------------------------------------------------------------------------------

// SQLEnsureServiceUser gives a service its user row. $1 fills both a text and a varchar
// column, so it is typed explicitly: PostgreSQL won't infer one type for a parameter
// used against two
const SQLEnsureServiceUser = `INSERT INTO ohc_users (id, username, display_name, roles, needs_new_password) VALUES ($1::text, $1::text, $2, '{}', FALSE) ON CONFLICT DO NOTHING`

// sqlUserColumns coalesces the nullable text columns to fit the plain string fields
const sqlUserColumns = `
	id, COALESCE(username, '') AS username, COALESCE(display_name, '') AS display_name,
	COALESCE(password_hash, '') AS password_hash, totp_secret, created_at, disabled_at,
	COALESCE(roles, '{}') AS roles, needs_new_password`

const (
	SQLGetUserByUsername = `SELECT ` + sqlUserColumns + ` FROM ohc_users WHERE username = $1 LIMIT 1`
	SQLGetUserByID       = `SELECT ` + sqlUserColumns + ` FROM ohc_users WHERE id = $1 LIMIT 1`
)

// SQLSwapPasswordHash stores a stronger hash of the same password (a rehash at login).
// It replaces the hash only if it is still $3, so a concurrent password change is
// never overwritten with the old password
const SQLSwapPasswordHash = `UPDATE ohc_users SET password_hash = $1 WHERE id = $2 AND password_hash = $3`

// SQLUpdatePasswordHash sets the password a user chose in place of a temporary one. It
// only matches while the temporary hash $3 is still stored and still flagged, so a
// replayed or concurrent change can't apply twice
const SQLUpdatePasswordHash = `UPDATE ohc_users SET password_hash = $1, needs_new_password = FALSE WHERE id = $2 AND password_hash = $3 AND needs_new_password`

// SQLUserUpdatePasswordHash sets a password the user chose to replace their own. It
// only matches while hash $3, the one the login just checked, is still stored, so a
// concurrent change or admin reset is never overwritten
const SQLUserUpdatePasswordHash = `UPDATE ohc_users SET password_hash = $1 WHERE id = $2 AND password_hash = $3`

const SQLInsertUser = `INSERT INTO ohc_users (id, username, display_name, password_hash, roles, needs_new_password) VALUES ($1, $2, $3, $4, $5, $6)`

// ---------------------------------------------------------------------------------
// auth: revocations
// ---------------------------------------------------------------------------------

const SQLInsertRevocation = `INSERT INTO ` + RevocationsTable + ` (key, reason) VALUES ($1, $2) RETURNING created_at`

// SQLRecentRevocations gives each key's latest revocation within the last $1 seconds
const SQLRecentRevocations = `SELECT key, max(created_at) FROM ` + RevocationsTable + `
		WHERE created_at > now() - make_interval(secs => $1) GROUP BY key`

// ---------------------------------------------------------------------------------
// auth: signing keys
// ---------------------------------------------------------------------------------

// SQLInsertSigningKey stamps activated_at only when the key goes straight in as active
const SQLInsertSigningKey = `INSERT INTO ohc_signing_keys(kid, public_key, private_key, status, activated_at)
	VALUES ( $1, $2, $3, $4::varchar, CASE WHEN $4::varchar = 'active' THEN now() END )`

const SQLRetireSigningKey = `UPDATE ohc_signing_keys SET status = 'retiring', retired_at = now() WHERE kid = $1`

const SQLActivateSigningKey = `UPDATE ohc_signing_keys SET status = 'active', activated_at = now() WHERE kid = $1`

const SQLUsableSigningKeys = `
	SELECT kid, public_key, private_key, status
	FROM ohc_signing_keys
	WHERE status IN ('next', 'active', 'retiring')`

// ---------------------------------------------------------------------------------
// auth: refresh tokens
// ---------------------------------------------------------------------------------

// SQLInsertRefreshToken refuses the insert if the family was revoked, so a rotation
// racing a reuse revocation can't leave a live token behind
const SQLInsertRefreshToken = `INSERT INTO ohc_refresh_tokens (token_hash, user_id, client_id, family_id, amr, expires_at, auth_time)
	SELECT $1::text, $2::text, $3::text, $4::text, $5::text[], $6::timestamptz, $7::timestamptz
	WHERE NOT EXISTS (SELECT 1 FROM ohc_refresh_tokens WHERE family_id = $4::text AND revoked_at IS NOT NULL)`

const SQLRefreshTokenByHash = `SELECT id, token_hash, user_id, client_id, family_id, COALESCE(amr, '{}') AS amr,
		expires_at, auth_time, used_at, revoked_at
	FROM ohc_refresh_tokens WHERE token_hash = $1 LIMIT 1`

// SQLUseRefreshToken marks a token used. Only one caller can win it for a given token
const SQLUseRefreshToken = `UPDATE ohc_refresh_tokens SET used_at = now()
	WHERE id = $1 AND used_at IS NULL AND revoked_at IS NULL AND expires_at > now()`

const SQLRevokeUserRefreshTokens = `UPDATE ohc_refresh_tokens SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`

const SQLRevokeRefreshFamily = `UPDATE ohc_refresh_tokens SET revoked_at = now() WHERE family_id = $1 AND revoked_at IS NULL`

// ---------------------------------------------------------------------------------
// events
// ---------------------------------------------------------------------------------

const SQLInsertEvent = `INSERT INTO ohc_events(action, status, service, topic, log, time, "user") VALUES ($1, $2, $3, $4, $5, $6, $7)`

// ---------------------------------------------------------------------------------
// handlers: diffs
// ---------------------------------------------------------------------------------

const SQLGetDiff = `SELECT * FROM ohc_diffs WHERE diff_type = $1 AND checksum = $2 LIMIT 1;`

// SQLClaimDiffBatch batches a diff once: it only matches while batched is false, so the
// row lock serialises concurrent claims. $1 table, $2 user, $3 checksum, $4 service
const SQLClaimDiffBatch = `
			UPDATE ohc_diffs
			SET batched = true, batch_number = generate_batch_number($1, $4, $2), batched_date = now()
			WHERE diff_type = $1 AND checksum = $3 AND batched = false
			RETURNING batch_number`

const SQLDiffBatchNumber = `SELECT batch_number FROM ohc_diffs WHERE diff_type = $1 AND checksum = $2`

// ---------------------------------------------------------------------------------
// logging
// ---------------------------------------------------------------------------------

const SQLSetupLogs = `
CREATE TABLE IF NOT EXISTS ohc_logs (
    at         timestamptz NOT NULL,
    level      text        NOT NULL,
    severity   smallint    NOT NULL,
    msg        text        NOT NULL,
    request_id text,
    service    text        NOT NULL DEFAULT 'ohc',
    attrs      jsonb       NOT NULL DEFAULT '{}'
) PARTITION BY RANGE (at);
CREATE INDEX IF NOT EXISTS ohc_logs_at ON ohc_logs (at);
CREATE INDEX IF NOT EXISTS ohc_logs_severity_at ON ohc_logs (severity, at);
CREATE INDEX IF NOT EXISTS ohc_logs_service_at ON ohc_logs (service, at);
CREATE INDEX IF NOT EXISTS ohc_logs_request_id ON ohc_logs (request_id) WHERE request_id IS NOT NULL;`

// SQLLogMaintenanceLock lets one instance at a time maintain the log partitions
const SQLLogMaintenanceLock = `SELECT pg_try_advisory_xact_lock(hashtext('ohc_logs_maintenance'))`

// SQLLogPartitions lists the partitions of table $1
const SQLLogPartitions = `
		SELECT c.relname FROM pg_inherits i
		  JOIN pg_class c ON c.oid = i.inhrelid
		  JOIN pg_class p ON p.oid = i.inhparent
		 WHERE p.relname = $1`

// SQLCreateLogPartitionFmt: partition identifier, parent table, from, to
const SQLCreateLogPartitionFmt = `CREATE TABLE IF NOT EXISTS %s PARTITION OF %s FOR VALUES FROM ('%s') TO ('%s')`

// SQLDropTableFmt: table identifier
const SQLDropTableFmt = `DROP TABLE IF EXISTS %s`

// ---------------------------------------------------------------------------------
// schematools: qualified-name migration
// ---------------------------------------------------------------------------------

// SQLCatalog lists the tables, indexes, owned sequences and foreign keys in public
const SQLCatalog = `
SELECT 'r' AS kind, c.relname AS name, '' AS tbl
  FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
 WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p')
UNION ALL
SELECT 'i', ic.relname, tc.relname
  FROM pg_index x
  JOIN pg_class ic ON ic.oid = x.indexrelid
  JOIN pg_class tc ON tc.oid = x.indrelid
  JOIN pg_namespace n ON n.oid = tc.relnamespace
 WHERE n.nspname = 'public'
UNION ALL
SELECT 'S', s.relname, t.relname
  FROM pg_class s
  JOIN pg_depend d ON d.objid = s.oid AND d.classid = 'pg_class'::regclass AND d.deptype IN ('a', 'i')
  JOIN pg_class t ON t.oid = d.refobjid
  JOIN pg_namespace n ON n.oid = s.relnamespace
 WHERE s.relkind = 'S' AND n.nspname = 'public'
UNION ALL
SELECT 'f', con.conname, t.relname
  FROM pg_constraint con
  JOIN pg_class t ON t.oid = con.conrelid
  JOIN pg_namespace n ON n.oid = t.relnamespace
 WHERE con.contype = 'f' AND n.nspname = 'public'`

// SQLRenameRelationFmt: kind (TABLE, INDEX or SEQUENCE), old identifier, new identifier
const SQLRenameRelationFmt = "ALTER %s %s RENAME TO %s"

// SQLRenameConstraintFmt: table identifier, old constraint, new constraint
const SQLRenameConstraintFmt = "ALTER TABLE %s RENAME CONSTRAINT %s TO %s"

// Widen the columns that hold table names to fit qualified names
const (
	SQLWidenDiffType       = "ALTER TABLE ohc_diffs ALTER COLUMN diff_type TYPE character varying(63)"
	SQLWidenBatchTableName = "ALTER TABLE ohc_batch_numbers ALTER COLUMN table_name TYPE character varying(63)"
)

// SQLRenameDiffTypeFmt: new name literal, old name literal
const SQLRenameDiffTypeFmt = "UPDATE ohc_diffs SET diff_type = %s WHERE diff_type = %s"

// SQLRenameBatchTableFmt: new name literal, service literal, old name literal
const SQLRenameBatchTableFmt = "UPDATE ohc_batch_numbers SET table_name = %s, service = %s WHERE table_name = %s"

// ---------------------------------------------------------------------------------
// store: services
// ---------------------------------------------------------------------------------

// SQLServicePublicKey reads an active key of an enabled service. $1 kid, $2 service
const SQLServicePublicKey = `
	SELECT k.public_key, s.id, s.role, s.entra_client_id, s.disabled_at
	FROM ohc_service_keys k
	JOIN ohc_services s ON s.id = k.service_id
	WHERE k.kid = $1 AND k.service_id = $2
	AND k.revoked_at IS NULL
	AND s.disabled_at IS NULL
	`

const SQLTouchServiceKey = `
	UPDATE ohc_service_keys SET last_used_at = now() WHERE kid = $1
	`
