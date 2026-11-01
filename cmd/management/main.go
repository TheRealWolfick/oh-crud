// The management platform: Oh CRUD's admin dashboard, run as its own service.
//
//	management keygen -kid management-1 -out management.pem
//	    makes the service's key pair and prints how to register it with Oh CRUD
//	management
//	    serves the dashboard (settings below, from the environment)
//
// Environment:
//
//	OHC_URL              Oh CRUD's base URL, e.g. https://api.example.com (required)
//	OHC_TOKEN_AUDIENCE   Oh CRUD's token-url from its server.yaml. Default OHC_URL + /oauth/token
//	MGMT_SERVICE_ID      This service's id at Oh CRUD. Default management
//	MGMT_KID             The key id registered for MGMT_KEY_FILE (required)
//	MGMT_KEY_FILE        The private key, as written by keygen (required)
//	ADDR                 Address to listen on. Default :8090
//	MGMT_INSECURE_COOKIE Set to true only for local development over plain http
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"lotusforge.au/api-server/management"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "keygen" {
		if err := keygen(os.Args[2:]); err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
		return
	}
	lg := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := serve(lg); err != nil { lg.Error("management platform stopped", "error", err); os.Exit(1) }
}

func env(name, def string) string {
	if v := os.Getenv(name); v != "" { return v }
	return def
}

func serve(lg *slog.Logger) error {
	base := os.Getenv("OHC_URL")
	kid := os.Getenv("MGMT_KID")
	keyFile := os.Getenv("MGMT_KEY_FILE")
	if base == "" || kid == "" || keyFile == "" { return errors.New("OHC_URL, MGMT_KID and MGMT_KEY_FILE are required") }
	key, err := readKey(keyFile)
	if err != nil { return err }

	client := management.NewClient(base, env("OHC_TOKEN_AUDIENCE", strings.TrimRight(base, "/")+"/oauth/token"), env("MGMT_SERVICE_ID", "management"), kid, key)
	srv, err := management.NewServer(client, lg, os.Getenv("MGMT_INSECURE_COOKIE") != "true")
	if err != nil { return err }

	addr := env("ADDR", ":8090")
	lg.Info("management platform listening", "addr", addr, "oh_crud", base)
	return (&http.Server{Addr: addr, Handler: srv.Handler(), ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second}).ListenAndServe()
}

func readKey(path string) (ed25519.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil { return nil, err }
	block, _ := pem.Decode(b)
	if block == nil { return nil, fmt.Errorf("%s holds no PEM key", path) }
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil { return nil, err }
	ed, ok := k.(ed25519.PrivateKey)
	if !ok { return nil, fmt.Errorf("%s is not an Ed25519 key", path) }
	return ed, nil
}

func keygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	kid := fs.String("kid", "management-1", "key id to register")
	out := fs.String("out", "management.pem", "where to write the private key")
	svc := fs.String("service", "management", "the service id")
	if err := fs.Parse(args); err != nil { return err }

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil { return err }
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil { return err }
	f, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil { return err }
	defer f.Close()
	if err := pem.Encode(f, &pem.Block{Type: "PRIVATE KEY", Bytes: der}); err != nil { return err }

	pk := base64.RawURLEncoding.EncodeToString(pub)
	fmt.Printf(`Private key written to %s (keep it secret; only this service needs it).

Register the service with Oh CRUD. Through the admin API, as a platform admin:
  POST /admin/v1/services
  {"id": %q, "role": "management-service", "key": {"kid": %q, "public_key": %q}}

or, for the very first one, directly in the database:
  INSERT INTO ohc_services (id, role) VALUES ('%s', 'management-service');
  INSERT INTO ohc_service_keys (kid, service_id, public_key) VALUES ('%s', '%s', decode('%s', 'base64'));

Then list it in Oh CRUD's server.yaml:
  admin:
    services: [%s]

and run the platform with MGMT_KID=%s MGMT_KEY_FILE=%s
`, *out, *svc, *kid, pk, *svc, *kid, *svc, base64.StdEncoding.EncodeToString(pub), *svc, *kid, *out)
	return nil
}
