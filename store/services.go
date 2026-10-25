package store

import (
	"context"
	"errors"
	"time"

	"lotusforge.au/api-server/models"
)

type Services struct {
	db models.DBExecQuery
}

func NewServices(db models.DBExecQuery) *Services { return &Services{ db: db } }

// Uses the database to get the service key, the public key is still encrypted at this point
func (s *Services) ServicePublicKey(ctx context.Context, iss string, kid string) ([]byte, *models.Service, error) {
	qry := `
	SELECT k.public_key, s.id, s.role, s.entra_client_id, s.disabled_at
	FROM service_keys k
	JOIN services s ON s.id = k.service_id
	WHERE kid = $1 AND k.service_id = $2
	AND k.revoked_at IS NULL
	AND s.disabled_at IS NULL
	`

	var public_key []byte
	var svc models.Service
	row := s.db.QueryRow(ctx, qry, kid, iss)
	err := row.Scan(&public_key, &svc.ID, &svc.Entra_client_id, &svc.Disabled_at)

	if err != nil { return nil, nil, err }
	return public_key, &svc, nil
}

func (s *Services) TouchServiceKey(ctx context.Context, kid string) error {
	qry := `
	UPDATE service_keys SET last_used_at = $1 WHERE kid = $2
	`
	now := time.Now()

	cmd, err := s.db.Exec(ctx, qry, now, kid)
	if err != nil { return err }
	if cmd.RowsAffected() == 0 { return errors.New("Failed to update service key last_used_at") }
	return nil
}
