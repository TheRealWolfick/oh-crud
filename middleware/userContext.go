package middleware

import (
	"context"

	"lotusforge.au/api-server/models"
)

type Contextkey string

const userContextKey Contextkey = "user"

func SetUser(ctx context.Context, user *models.User) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

func GetUser(ctx context.Context) (*models.User, bool) {
	user, ok := ctx.Value(userContextKey).(*models.User)
	return user, ok
}
