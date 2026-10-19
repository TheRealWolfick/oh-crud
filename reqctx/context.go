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

func SetRoles(ctx context.Context, user_roles []string) context.Context {
	return context.WithValue(ctx, userRolesKey, user_roles)
}

func CheckUserHasAllowedRole(ctx context.Context, allowed_roles []string, svr_cfg *models.ServerConfig) bool {
	user_roles, ok := ctx.Value(userRolesKey).([]string)
	if ok {
		// Check if user is an admin
		if svr_cfg.RBAC != nil && slices.Contains(user_roles, svr_cfg.RBAC.Admin_role) { return true }
		// Check if user can access this end point
		for _, role := range allowed_roles {
			if slices.Contains(user_roles, role) { return true }
		}
  }
	return false
}
