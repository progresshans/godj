package postgres

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/query"
)

func poisonPostgresEnvironment(t *testing.T) map[string]string {
	t.Helper()
	directory := t.TempDir()
	passfile, servicefile := filepath.Join(directory, "passwords"), filepath.Join(directory, "services")
	if err := os.WriteFile(passfile, []byte("*:*:*:*:ambient-password-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(servicefile, []byte("[ambient-service-secret]\nhost=wrong.invalid\nuser=wrong_user\npassword=service-password-secret\ndbname=wrong_database\nsslmode=disable\n"), 0600); err != nil {
		t.Fatal(err)
	}
	environment := map[string]string{
		"PGHOST": "wrong.invalid", "PGPORT": "not-a-port", "PGDATABASE": "wrong_database", "PGUSER": "wrong_user",
		"PGPASSWORD": "ambient-password-secret", "PGPASSFILE": passfile, "PGAPPNAME": "wrong_application",
		"PGCONNECT_TIMEOUT": "invalid", "PGSSLMODE": "invalid", "PGSSLKEY": "wrong-key-secret",
		"PGSSLCERT": "wrong-certificate-secret", "PGSSLSNI": "0", "PGSSLROOTCERT": "wrong-root-secret",
		"PGSSLPASSWORD": "wrong-tls-password-secret", "PGSSLNEGOTIATION": "direct", "PGTARGETSESSIONATTRS": "invalid",
		"PGSERVICE": "", "PGSERVICEFILE": servicefile, "PGTZ": "wrong-timezone", "PGOPTIONS": "-c session_replication_role=replica",
		"PGMINPROTOCOLVERSION": "invalid", "PGMAXPROTOCOLVERSION": "invalid", "PGCHANNELBINDING": "disable", "PGREQUIREAUTH": "invalid",
	}
	for name, value := range environment {
		t.Setenv(name, value)
	}
	return environment
}

func TestPostgresConnectionInputsAreExplicitAndIsolated(t *testing.T) {
	environment := poisonPostgresEnvironment(t)
	var readers sync.WaitGroup
	for index := range 8 {
		readers.Go(func() {
			for range 10 {
				name := fmt.Sprintf("project_%d", index)
				configuration, err := currentConnectionConfig("postgres://" + name + "@localhost/" + name)
				if err != nil {
					t.Errorf("explicit connection rejected under unrelated environment: %v", err)
					return
				}
				if configuration.Host != "localhost" || configuration.Port != 5432 || configuration.User != name ||
					configuration.Database != name || configuration.Password != "" || configuration.ConnectTimeout != 0 ||
					configuration.ValidateConnect != nil || configuration.SSLNegotiation != "" || configuration.RequireAuth != "" ||
					configuration.ChannelBinding != "prefer" || configuration.MinProtocolVersion != "3.0" || configuration.MaxProtocolVersion != "3.0" {
					t.Error("connection inherited a credential, identity or protocol option")
				}
				if configuration.TLSConfig == nil || configuration.TLSConfig.InsecureSkipVerify ||
					configuration.TLSConfig.ServerName != "localhost" || len(configuration.TLSConfig.Certificates) != 0 || len(configuration.Fallbacks) != 0 {
					t.Error("default connection did not require verified TLS without plaintext fallback or ambient client credentials")
				}
				if configuration.RuntimeParams["application_name"] != "" || configuration.RuntimeParams["timezone"] != "UTC" {
					t.Error("runtime profile inherited another project's settings")
				}
				configuration.RuntimeParams["application_name"] = name
			}
		})
	}
	readers.Wait()
	for name, value := range environment {
		if os.Getenv(name) != value {
			t.Fatalf("opening configuration changed process environment key %s", name)
		}
	}
}

func TestPostgresExplicitIdentityPortsAndAuthenticationOptions(t *testing.T) {
	t.Parallel()
	configuration, err := currentConnectionConfig("postgres://first:p%40ss%3Aword@one.invalid,two.invalid:5544/database?sslmode=disable&application_name=first&connect_timeout=7&channel_binding=require&require_auth=scram-sha-256")
	if err != nil {
		t.Fatal(err)
	}
	if configuration.User != "first" || configuration.Password != "p@ss:word" || configuration.Host != "one.invalid" ||
		configuration.Port != 5432 || configuration.Database != "database" || configuration.TLSConfig != nil ||
		configuration.ConnectTimeout != 7*time.Second || configuration.ChannelBinding != "require" || configuration.RequireAuth != "scram-sha-256" ||
		configuration.RuntimeParams["application_name"] != "first" || len(configuration.Fallbacks) != 1 {
		t.Fatal("explicit connection identity or options changed")
	}
	if configuration.Fallbacks[0].Host != "two.invalid" || configuration.Fallbacks[0].Port != 5544 {
		t.Fatal("multiple-host ports were shifted or inherited")
	}
	ipv6, err := currentConnectionConfig("postgres://user@[::1]/database?sslmode=disable")
	if err != nil || ipv6.Host != "::1" || ipv6.Port != 5432 {
		t.Fatalf("IPv6 default port was not explicit: %v", err)
	}
}

func TestPostgresRejectsAmbiguousAndImplicitConnectionInputs(t *testing.T) {
	t.Parallel()
	for _, rawURL := range []string{
		"postgres://localhost/database", "postgres://user@localhost/", "postgres://user@localhost:0/database",
		"postgres://user@localhost:/database", "postgres://user@localhost,,other/database", "postgres://user@localhost/database#fragment",
		"postgres://user@localhost/database?host=other", "postgres://user@localhost/database?port=5433",
		"postgres://user@localhost/database?user=other", "postgres://user@localhost/database?password=private-secret",
		"postgres://user@localhost/database?database=other", "postgres://user@localhost/database?dbname=other",
		"postgres://user@localhost/database?service=private-secret", "postgres://user@localhost/database?servicefile=private-secret",
		"postgres://user@localhost/database?passfile=private-secret", "postgres://user@localhost/database?sslmode=disable&sslmode=verify-full",
		"postgres://user@localhost/database?sslmode=",
		"postgres://user@localhost/database?options=private-secret", "postgres://user@localhost/database?unknown-private-secret=value",
		"postgres://user@localhost/database?sslmode=%ZZ", "postgres://user@localhost/database?application_name=x;y=z",
		"postgres://user%00name@localhost/database", "postgres://user:pass%00word@localhost/database", "postgres://user@localhost/db%00name",
		"postgres://user@localhost/database?application_name=" + strings.Repeat("s", 64*1024),
	} {
		err := ValidateConfig(Config{URL: rawURL, Schema: "application"})
		if !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Field: "url"}) ||
			strings.Contains(fmt.Sprintf("%v %#v", err, err), "private-secret") {
			t.Fatalf("invalid connection accepted or diagnostic retained input: %v", err)
		}
	}
}

func TestPostgresServiceSelectionFailsClosedWithoutReadingServiceContents(t *testing.T) {
	poisonPostgresEnvironment(t)
	configuration, err := currentConnectionConfig("postgres://user@localhost/database?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PGSERVICE", "ambient-service-secret")
	if configuration, err := currentConnectionConfig("postgres://user@localhost/database?sslmode=disable"); configuration != nil ||
		!errors.Is(err, &query.Error{Field: "environment"}) || strings.Contains(fmt.Sprintf("%v %#v", err, err), "ambient-service-secret") {
		t.Fatalf("ambient service did not return a closed field diagnostic: %v", err)
	}
	// Simulate a service appearing after the adapter's check. The actual
	// driver must reject the prepared string instead of reading the available
	// service file, even though the service and its contents are valid.
	if parsed, err := pgx.ParseConfig(configuration.ConnString()); parsed != nil || err == nil {
		t.Fatal("late service selection was allowed to consume service contents")
	}
	if backend, err := Open(t.Context(), Config{URL: "postgres://user@localhost/database?sslmode=disable", Schema: "application"}); backend != nil || !errors.Is(err, &query.Error{Field: "environment"}) {
		t.Fatalf("Open did not reject service selection before network I/O: %v", err)
	}
}

func TestPostgresExplicitTLSFilesAreOwnedByEachOpenedConfiguration(t *testing.T) {
	t.Parallel()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "configuration-test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, public, private)
	if err != nil {
		t.Fatal(err)
	}
	key, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	certificatePath, keyPath := filepath.Join(directory, "client.pem"), filepath.Join(directory, "private.pem")
	if err := os.WriteFile(certificatePath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0600); err != nil {
		t.Fatal(err)
	}
	options := url.Values{"sslmode": {"verify-full"}, "sslrootcert": {certificatePath}, "sslcert": {certificatePath}, "sslkey": {keyPath}}
	rawURL := "postgres://user:connection-private-secret@localhost/database?" + options.Encode()
	configuration, err := currentConnectionConfig(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	if configuration.TLSConfig == nil || configuration.TLSConfig.InsecureSkipVerify || len(configuration.TLSConfig.Certificates) != 1 ||
		configuration.TLSConfig.RootCAs == nil || len(configuration.Fallbacks) != 0 {
		t.Fatal("explicit TLS material or verification policy was not loaded")
	}
	if err := os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(certificatePath); err != nil {
		t.Fatal(err)
	}
	if len(configuration.TLSConfig.Certificates[0].Certificate) != 1 ||
		string(configuration.TLSConfig.Certificates[0].Certificate[0]) != string(der) {
		t.Fatal("opened configuration lost its owned certificate bytes")
	}
	if next, err := currentConnectionConfig(rawURL); next != nil || !errors.Is(err, &query.Error{Field: "url"}) ||
		strings.Contains(fmt.Sprintf("%v %#v", err, err), "connection-private-secret") || strings.Contains(fmt.Sprint(err), directory) {
		t.Fatalf("new configuration reused old TLS files or exposed their cause: %v", err)
	}
}

func TestPostgresIndependentProjectConnectionsIgnoreAmbientSettings(t *testing.T) {
	rawURL := postgresIntegrationURL(t)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	first := postgresMigrationIntegrationSchema(t, ctx, rawURL)
	second := postgresMigrationIntegrationSchema(t, ctx, rawURL)
	admin, err := pgx.Connect(ctx, rawURL)
	if err != nil {
		t.Fatal(redactConnectionError(err))
	}
	var wantDatabase, wantUser string
	if err := admin.QueryRow(ctx, "SELECT current_database(), current_user").Scan(&wantDatabase, &wantUser); err != nil {
		_ = admin.Close(ctx)
		t.Fatal(redactConnectionError(err))
	}
	if err := admin.Close(ctx); err != nil {
		t.Fatal(redactConnectionError(err))
	}
	poisonPostgresEnvironment(t)
	backends := make([]*Backend, 0, 2)
	for _, schema := range []string{first, second} {
		selected := postgresIntegrationURLWithParameter(t, rawURL, "application_name", schema)
		backend := openPostgresMigrationIntegrationBackend(t, ctx, selected, schema)
		// Every read below creates a new physical connection from this pool's
		// already-owned configuration, after the process environment changed.
		backend.database.SetMaxIdleConns(0)
		quoted, err := quoteIdentifier(schema)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := backend.database.ExecContext(ctx, "CREATE TABLE "+quoted+".project_value (value TEXT NOT NULL)"); err != nil {
			t.Fatal(redactConnectionError(err))
		}
		if _, err := backend.database.ExecContext(ctx, "INSERT INTO "+quoted+".project_value VALUES ($1)", schema); err != nil {
			t.Fatal(redactConnectionError(err))
		}
		backends = append(backends, backend)
	}
	t.Setenv("PGPASSWORD", "changed-after-opening-secret")
	var readers sync.WaitGroup
	for _, backend := range backends {
		readers.Go(func() {
			quoted, _ := quoteIdentifier(backend.schema)
			for range 4 {
				var database, user, application, value, timezone, searchPath string
				err := backend.database.QueryRowContext(ctx,
					"SELECT current_database(), current_user, current_setting('application_name'), value, current_setting('TimeZone'), current_setting('search_path') FROM "+quoted+".project_value").
					Scan(&database, &user, &application, &value, &timezone, &searchPath)
				if err != nil {
					t.Error(redactConnectionError(err))
					return
				}
				if database != wantDatabase || user != wantUser || application != backend.schema || value != backend.schema || timezone != "UTC" || searchPath != "pg_catalog" {
					t.Error("independent project connection inherited another identity, namespace or physical profile")
				}
			}
		})
	}
	readers.Wait()
}
