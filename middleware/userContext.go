package middleware

import (
	"context"
	"log/slog"
	"slices"

	"lotusforge.au/api-server/models"
)

type Contextkey string

type TaskContext struct {
	Id string
	Type string
}

const userContextKey Contextkey = "user"
const taskContextKey Contextkey = "task"
const loggerContextKey Contextkey = "logger"
const userRolesKey Contextkey = "roles"

func SetUser(ctx context.Context, user *models.User) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

func GetUser(ctx context.Context) (*models.User, bool) {
	user, ok := ctx.Value(userContextKey).(*models.User)
	return user, ok
}

func StartTask(ctx context.Context, task_type string) context.Context {
	id, _ := generateRandomString(32)
	task := TaskContext{Id: id, Type: task_type}
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
	user, ok := ctx.Value(loggerContextKey).(*slog.Logger)
	return user, ok
}

func SetRoles(ctx context.Context, user_roles []string) context.Context {
	return context.WithValue(ctx, userRolesKey, user_roles)
}

func CheckUserHasAllowedRole(ctx context.Context, allowed_roles []string) bool {
	user_roles, ok := ctx.Value(userRolesKey).([]string)
	if ok {
		for _, role := range allowed_roles {
			return slices.Contains(user_roles, role)
		}
	}
	return false
}
