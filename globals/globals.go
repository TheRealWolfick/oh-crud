// Package globals holds the project's constants in one place. It is a leaf: it imports
// nothing from this module, so every other package can import it. SQL lives in sql.go.
package globals

import "time"

// ---------------------------------------------------------------------------------
// datamodel
// ---------------------------------------------------------------------------------

// FieldKind identifies a model field's config-declared type ("int", "json", "time", ...).
// It exists so WHERE-clause building (setWhere) and value validation (ValidateFieldValue)
// can distinguish types like "time" and "json" that don't map onto distinct reflect.Kind
// values (both could otherwise only be represented as reflect.Struct/reflect.Map).
type FieldKind string

const (
	FieldInt        FieldKind = "int"
	FieldFloat      FieldKind = "float"
	FieldString     FieldKind = "string"
	FieldBool       FieldKind = "bool"
	FieldTime       FieldKind = "time"
	FieldJSON       FieldKind = "json"
	FieldUUID       FieldKind = "uuid"
	FieldByte       FieldKind = "byte"
	FieldStringList FieldKind = "[]string"
)

// MaxIdentifier is PostgreSQL's identifier limit. Longer names are silently truncated,
// which would make the table Oh CRUD creates differ from the one it queries.
const MaxIdentifier = 63

// SystemService owns Oh CRUD's own tables. Only models in SystemModelsDir may use it.
const SystemService = "ohc"

// SystemModelsDir holds Oh CRUD's own models
const SystemModelsDir = "config/default"

// ---------------------------------------------------------------------------------
// models (server config)
// ---------------------------------------------------------------------------------

// Defaults for the server.yaml settings when it leaves them out
const (
	DefaultLogRetentionDays   = 14
	DefaultAuditRetentionDays = 365
)

// ---------------------------------------------------------------------------------
// schematools
// ---------------------------------------------------------------------------------

// HCLDir is where generated HCL files (and their .version sidecars) are written.
const HCLDir = "./config/database"

// ---------------------------------------------------------------------------------
// cluster
// ---------------------------------------------------------------------------------

// ClusterPath is where an instance receives messages from its peers
const ClusterPath = "/mesh/v1/peer/events"

// ClusterSignatureHeader carries the hex HMAC-SHA256 of the request body
const ClusterSignatureHeader = "X-OhCrud-Peer-Signature"

const (
	ClusterMaxAge      = time.Minute // a message older than this, or this far in the future, is refused
	ClusterMaxBody     = 64 << 10
	ClusterSendTimeout = 2 * time.Second
)

// ---------------------------------------------------------------------------------
// monitors
// ---------------------------------------------------------------------------------

// SettleFor is how long a changed file must stay unchanged before it is read. Editors
// often save in several writes, and a model is checked, synced and logged only once
// its file has settled, so a single save gives a single reload of the final contents.
const SettleFor = 10 * time.Second

// ---------------------------------------------------------------------------------
// secrets
// ---------------------------------------------------------------------------------

// RandomChars is the alphabet RandomString draws from
const RandomChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890"

// ---------------------------------------------------------------------------------
// audit
// ---------------------------------------------------------------------------------

// AuditTable is where entries are written
const AuditTable = "ohc_audit_events"

// AuditSystem is the service that owns Oh CRUD's own targets: users, services and keys
const AuditSystem = "ohc"

// Actions recorded today. Keep the <object>.<verb> shape for new ones.
const (
	ActionUserLogin           = "user.login"
	ActionUserLoginFailed     = "user.login_failed"
	ActionRefreshTokenReuse   = "refresh_token.reuse"
	ActionRefreshTokenMisused = "refresh_token.wrong_client"
	ActionSessionRevoke       = "session.revoke"
	ActionUserLogout          = "user.logout"
	ActionUserLogoutAll       = "user.logout_all"
	ActionUserPasswordChange  = "user.password_change" // the user replaced a temporary password at login

	// Admin changes
	ActionServiceCreate      = "service.create"
	ActionServiceUpdate      = "service.update"
	ActionServiceDisable     = "service.disable"
	ActionServiceEnable      = "service.enable"
	ActionServiceKeyAdd      = "service_key.add"
	ActionServiceKeyRevoke   = "service_key.revoke"
	ActionUserCreate         = "user.create"
	ActionUserPasswordReset  = "user.password_reset" // an admin set a temporary password
	ActionUserRolesUpdate    = "user.roles.update"
	ActionUserDisable        = "user.disable"
	ActionUserEnable         = "user.enable"
	ActionUserSessionsRevoke = "user.sessions_revoke"
	ActionUserSessionRevoke  = "user.session_revoke"
	ActionSchemaApprove      = "schema.approve"
	ActionSchemaDeny         = "schema.deny"
)

// ---------------------------------------------------------------------------------
// admin
// ---------------------------------------------------------------------------------

// MinPassword is the shortest password an admin may set
const MinPassword = 12

// The first admin, created when an instance starts with no users (auth.EnsureFirstAdmin).
// Their password comes from FirstAdminPasswordEnv, or is FirstAdminPasswordLength
// random characters shown once on stderr.
const (
	FirstAdminUsername       = "admin"
	FirstAdminPasswordEnv    = "OH_CRUD_ADMIN_PASSWORD"
	FirstAdminPasswordLength = 24
)

// AuditCSVLimit caps an audit export; narrow the filters for more
const AuditCSVLimit = 10000

// Cluster channels for schema decisions
const (
	SchemaAppliedChannel = "schema.applied"
	SchemaDeniedChannel  = "schema.denied"
)

// ---------------------------------------------------------------------------------
// logging
// ---------------------------------------------------------------------------------

// Sizes and timings for writing logs. A batch goes out when it is full or a second
// has passed, whichever is first.
const (
	LogQueueSize    = 10000
	LogBatchSize    = 500
	LogFlushEvery   = time.Second
	LogWriteTimeout = 5 * time.Second
)

// LogTable holds the application log, partitioned by day (UTC), so old days are
// dropped whole: instant, and leaving no dead rows for VACUUM. It isn't a YAML model:
// the model format can't declare partitioning, and the schema sync only manages the
// tables it is given, so the two never meet.
const LogTable = "ohc_logs"

// LogPartitionPrefix starts the name of each day's partition of LogTable
const LogPartitionPrefix = LogTable + "_p"

// How far ahead log partitions are made, and how often maintenance runs
const (
	LogPartitionDaysAhead = 3
	LogMaintainEvery      = time.Hour
)

// ---------------------------------------------------------------------------------
// events
// ---------------------------------------------------------------------------------

// CloseCodeRevoked is the websocket close code sent when the token a socket was opened
// with is revoked. It mirrors HTTP 401: reconnecting with the same token is refused.
const CloseCodeRevoked = 4401

// ---------------------------------------------------------------------------------
// auth
// ---------------------------------------------------------------------------------

// UserTokenHeader carries the user's access token when a service calls on their behalf
const UserTokenHeader = "X-OhCrud-User"

// ServiceUserPrefix starts the subject of service tokens, and the id and username of
// the user row each service writes as
const ServiceUserPrefix = "svc:"

// UserIDLength is the length of a generated user id
const UserIDLength = 32

// OTPReplayWindow is how long a used OTP code is remembered. Longer than the 90 s a
// code can be accepted for.
const OTPReplayWindow = 2 * time.Minute

// LimiterSweepEvery is how many Allow calls pass between sweeps of idle requesters
const LimiterSweepEvery = 500

const (
	// AMRFederated marks a login made through an external IdP such as Entra
	AMRFederated = "fed"

	// Password sessions slide: each rotation extends them by the full lifetime
	RefreshLifetime = 14 * 24 * time.Hour
	// External IdP sessions are capped from login and never extended, so the user goes
	// back to the IdP daily and an account disabled there is noticed within a day
	FederatedRefreshLifetime = 24 * time.Hour
)

const (
	RevocationsTable = "ohc_revocations"

	// A revocation only matters while a token issued before it could still be live
	UserTokenLifetime = 15 * time.Minute
	RevocationMargin  = 5 * time.Minute
)

// RevocationChannel is the cluster channel revocations travel on
const RevocationChannel = "auth.revocation"

// ---------------------------------------------------------------------------------
// management
// ---------------------------------------------------------------------------------

// MgmtCookieName holds the session id; it is all the browser gets
const MgmtCookieName = "ohc_mgmt"

// MgmtIdleTimeout signs a person out after this long without a request
const MgmtIdleTimeout = 12 * time.Hour

// MgmtRefreshEarly renews the access token this long before it expires, so a request
// never carries one that dies in flight
const MgmtRefreshEarly = 30 * time.Second
