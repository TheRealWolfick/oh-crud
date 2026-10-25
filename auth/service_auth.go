package auth

import (
	"errors"
	"net/http"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"lotusforge.au/api-server/models"
)

type Auth struct {
	DB        *pgxpool.Pool
	Auth      models.ServiceStore
	TokenURL  string
	Replay    models.ReplayCache
}

// Returns a new authhandler object for middleware authentication handling
func AuthHandler(db *pgxpool.Pool, url string) *Auth {
	return &Auth{
		DB: db,
		TokenURL: url,   // aud
	}
}

func (a *Auth) AuthenticateService(r *http.Request) (*models.Service, error) {
	assertion_type := "urn:ietf:params:oauth:client-assertion-type:jwt-bearer"

	// Ensure that the assertion type is the supported method
	if r.PostFormValue("client_assertion_type") != assertion_type {
		return nil, errors.New("invalid client auth method")
	}

	// Init the service variable and generate a new jwt
	var svc *models.Service
	c := &jwt.RegisteredClaims{}

	// Parse the client assertion
	_, err := jwt.ParseWithClaims(r.PostFormValue("client_assertion"), c, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		iss, _ := t.Claims.GetIssuer()

		// Use the kid and iss to get the service from the DB. False if revoked.
		key, s, err := a.Auth.ServicePublicKey(r.Context(), iss, kid)

	})
}

// Read the client credentials from the database
func (a *Auth) ClientCredentials(
	ks *KeyStore,
) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {

	}
}

