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
	Topic string        // The event topic the task publishes on
}

var (
	userContextKey   = ctxKey{"user"}
	taskContextKey   = ctxKey{"task"}
	loggerContextKey = ctxKey{"logger"}
	userRolesKey     = ctxKey{"roles"}
)

// SetActor records who the request acts as, which writes are recorded against, along with
// the roles of every party to the request. They are set together so neither is missing.
func SetActor(ctx context.Context, actor *models.User, roles CallerRoles) context.Context {
	ctx = context.WithValue(ctx, userContextKey, actor)
	return context.WithValue(ctx, userRolesKey, roles)
}

// SetUserActor sets a user acting alone as the actor, taking the roles from the user
func SetUserActor(ctx context.Context, user *models.User) context.Context {
	var roles []string
	if user != nil { roles = user.Roles }
	return SetActor(ctx, user, CallerRoles{User: roles, HasUser: true})
}

func GetActor(ctx context.Context) (*models.User, bool) {
	user, ok := ctx.Value(userContextKey).(*models.User)
	return user, ok
}

func Actor(ctx context.Context) string {
	user, ok := GetActor(ctx)
	if !ok || user == nil { return "" }
	return user.ID
}

// Username returns the username of the user in the context, or "" if there is none
func Username(ctx context.Context) string {
	user, ok := GetActor(ctx)
	if !ok || user == nil { return "" }
	return user.Username
}

// Start a task with a valid task type. Valid types are based off event actions: create, read, update, delete
// The task's events publish on the model's table topic
func StartTask(ctx context.Context, action string, cfg *models.DataModel) context.Context {
	return StartTaskOnTopic(ctx, action, cfg, cfg.TableTopic())
}

// Start a task whose events publish on topic instead of the model's table topic
func StartTaskOnTopic(ctx context.Context, action string, cfg *models.DataModel, topic string) context.Context {
	id, _ := secrets.RandomString(32)
	task := TaskContext{Id: id, Type: action, Cfg: cfg, Topic: topic}
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

// Set the logger within the context
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

// GetCallerRoles returns the roles of every party to the request, set by SetActor
func GetCallerRoles(ctx context.Context) (CallerRoles, bool) {
	c, ok := ctx.Value(userRolesKey).(CallerRoles)
	return c, ok
}

// CheckActorHasAllowedRole reports whether every party to the request holds an allowed
// role or the admin role. A service acting for a user needs both to pass, so neither
// can reach further than the other is allowed to.
func CheckActorHasAllowedRole(ctx context.Context, allowed_roles []string, svr_cfg *models.ServerConfig) bool {
	c, ok := GetCallerRoles(ctx)
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
