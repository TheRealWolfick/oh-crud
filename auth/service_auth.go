package auth

import (
	"errors"
	"net/http"

	"github.com/golang-jwt/jwt/v5"
	"lotusforge.au/api-server/models"
)

func AuthHandler()

func AuthenticateService(r *http.Request) (*models.Service, error) {
	assertion_type := "urn:ietf:params:oauth:client-assertion-type:jwt-bearer"

	// Ensure that the assertion type is the supported method
	if r.PostFormValue("client_assertion_type") != assertion_type {
		return nil, errors.New("invalid client auth method")
	}

	// Init the service variable and generate a new jwt
	var svc *models.Service
	c := &jwt.RegisteredClaims{}

	// Parse the client assertion
	_, err := jwt.ParseWithClaims(r.FormValue("client_assertion"), c, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header("kid").(string)
		iss, _ := t.Claims.GetIssuer()

		// Use the kid and iss to get the service from the DB. False if revoked.
	})
}

func ClientCredentials(
	ks *KeyStore,
) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {

	}
}
