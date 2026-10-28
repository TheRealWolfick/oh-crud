package reqctx

import (
	"context"
	"log/slog"
	"slices"

	"lotusforge.au/api-server/models"
	"lotusforge.au/api-server/secrets"
)

// Unexported so values can only be set and read through this package's accessors
type ctxKey struct{ name string }

type TaskContext struct {
	Id string
	Type string
	Cfg *models.DataModel
}

var (
	userContextKey   = ctxKey{"user"}
	taskContextKey   = ctxKey{"task"}
	loggerContextKey = ctxKey{"logger"}
	userRolesKey     = ctxKey{"roles"}
)

func SetUser(ctx context.Context, user *models.User) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

func GetUser(ctx context.Context) (*models.User, bool) {
	user, ok := ctx.Value(userContextKey).(*models.User)
	return user, ok
}

// Username returns the username of the user in the context, or "" if there is none
func Username(ctx context.Context) string {
	user, ok := GetUser(ctx)
	if !ok || user == nil { return "" }
	return user.Username
}

func StartTask(ctx context.Context, task_type string, cfg *models.DataModel) context.Context {
	id, _ := secrets.RandomString(32)
	task := TaskContext{Id: id, Type: task_type, Cfg: cfg}
	return context.WithValue(ctx, taskContextKey, task)
}

func GetTask(ctx context.Context) (TaskContext, bool) {
	val := ctx.Value(taskContextKey)
	if val == nil {
		return TaskContext{}, false
	}
	task, ok := val.(TaskContext)
	if !ok {
		return TaskContext{}, false
	}
	if len(task.Id) == 32 {
		return task, true
	}
	return task, false
}

func SetLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerContextKey, logger)
} 

func GetLogger(ctx context.Context) (*slog.Logger, bool) {
	logger, ok := ctx.Value(loggerContextKey).(*slog.Logger)
	return logger, ok
}

// CallerRoles holds the roles of each party to a request. A request can come from a
// service, a user, or a service acting for a user.
type CallerRoles struct {
	Service    []string
	User       []string
	HasService bool
	HasUser    bool
}

func SetCallerRoles(ctx context.Context, c CallerRoles) context.Context {
	return context.WithValue(ctx, userRolesKey, c)
}

// SetRoles sets the roles of a request made by a user alone
func SetRoles(ctx context.Context, user_roles []string) context.Context {
	return SetCallerRoles(ctx, CallerRoles{User: user_roles, HasUser: true})
}

// CheckUserHasAllowedRole reports whether every party to the request holds an allowed
// role or the admin role. A service acting for a user needs both to pass, so neither
// can reach further than the other is allowed to.
func CheckUserHasAllowedRole(ctx context.Context, allowed_roles []string, svr_cfg *models.ServerConfig) bool {
	c, ok := ctx.Value(userRolesKey).(CallerRoles)
	if !ok || (!c.HasService && !c.HasUser) { return false }

	admin_role := ""
	if svr_cfg.RBAC != nil { admin_role = svr_cfg.RBAC.Admin_role }

	if c.HasService && !roleAllowed(c.Service, allowed_roles, admin_role) { return false }
	if c.HasUser && !roleAllowed(c.User, allowed_roles, admin_role) { return false }
	return true
}

func roleAllowed(have []string, allowed_roles []string, admin_role string) bool {
	if admin_role != "" && slices.Contains(have, admin_role) { return true }
	for _, role := range allowed_roles {
		if slices.Contains(have, role) { return true }
	}
	return false
}
