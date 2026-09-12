// Command export writes current consumer schemas into an explicitly selected
// empty directory. It configures real API adapters without serving requests.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/bearerauth"
	apisessionauth "github.com/progresshans/godj/api/sessionauth"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/apiapp"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/sessions"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "export OpenAPI:", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	flags := flag.NewFlagSet("export", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	output := flags.String("out", "", "required output directory; must be absent or empty and must not be a symlink")
	if err := flags.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *output == "" || flags.NArg() != 0 {
		return errors.New("usage: go run ./api/openapi/consumertest/testdata/export -out NEW_EMPTY_DIRECTORY")
	}
	files, err := documents(context.Background())
	if err != nil {
		return err
	}
	// Construct all documents, including closing the temporary backend, before
	// creating an output directory or publishing any file.
	return writeDocuments(*output, files)
}

type schemaFile struct {
	name string
	data []byte
}

func documents(ctx context.Context) (files []schemaFile, err error) {
	backend, err := sqlite.OpenMemory(ctx, "godj_openapi_consumer_export")
	if err != nil {
		return nil, fmt.Errorf("open isolated schema backend: %w", err)
	}
	defer func() { err = errors.Join(err, backend.Close()) }()
	store, err := sessions.NewMemoryStore(1)
	if err != nil {
		return nil, err
	}
	manager, err := sessions.NewManager(store, sessions.Config{})
	if err != nil {
		return nil, err
	}
	guard := &authenticationGuard{}
	webRuntime, err := websessionauth.New(websessionauth.Config{
		Sessions: manager, Authenticator: guard, Authorizer: guard,
	})
	if err != nil {
		return nil, err
	}
	session, err := apisessionauth.New(webRuntime)
	if err != nil {
		return nil, err
	}
	bearer, err := bearerauth.New(bearerauth.Config{Verifier: guard, Authorizer: guard})
	if err != nil {
		return nil, err
	}
	for _, profile := range []struct {
		name           string
		authentication api.Authentication
	}{{"articlebearer", bearer}, {"articlesession", session}} {
		application, err := apiapp.New(backend, profile.authentication)
		if err != nil {
			return nil, fmt.Errorf("construct %s API: %w", profile.name, err)
		}
		document, err := application.OpenAPI()
		if err != nil {
			return nil, fmt.Errorf("describe %s API: %w", profile.name, err)
		}
		files = append(files, schemaFile{name: profile.name + ".json", data: document.Bytes()})
	}
	application, err := helpdesk.New(backend, 1)
	if err != nil {
		return nil, fmt.Errorf("construct Helpdesk application: %w", err)
	}
	adapter, err := application.API(session)
	if err != nil {
		return nil, fmt.Errorf("construct Helpdesk API: %w", err)
	}
	document, err := adapter.OpenAPI()
	if err != nil {
		return nil, fmt.Errorf("describe Helpdesk API: %w", err)
	}
	files = append(files, schemaFile{name: "helpdesksession.json", data: document.Bytes()})
	if guard.called {
		return nil, errors.New("document construction unexpectedly invoked authentication")
	}
	return files, nil
}

func writeDocuments(directory string, files []schemaFile) error {
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(directory, 0o700); err != nil {
			return fmt.Errorf("create output directory: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("inspect output directory: %w", err)
	} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("output must be a directory, not a file or symlink")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read output directory: %w", err)
	}
	if len(entries) != 0 {
		return errors.New("output directory must be empty; existing files are never overwritten")
	}
	for _, schema := range files {
		file, err := os.OpenFile(filepath.Join(directory, schema.name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return fmt.Errorf("create %s without replacing an existing file: %w", schema.name, err)
		}
		_, writeErr := file.Write(schema.data)
		if err := errors.Join(writeErr, file.Close()); err != nil {
			return fmt.Errorf("write %s: %w", schema.name, err)
		}
	}
	return nil
}

// Real authentication profiles describe their transport without resolving a
// credential. Reject any accidental runtime call and remember it even if a
// caller incorrectly ignores the returned error. No input is logged or stored.
type authenticationGuard struct{ called bool }

func (guard *authenticationGuard) reject(operation string) error {
	guard.called = true
	return fmt.Errorf("OpenAPI export must not execute %s", operation)
}

func (guard *authenticationGuard) Authenticate(context.Context, string, string) (auth.Principal, error) {
	return auth.Principal{}, guard.reject("credential authentication")
}

func (guard *authenticationGuard) Resolve(context.Context, string) (auth.Principal, error) {
	return auth.Principal{}, guard.reject("principal resolution")
}

func (guard *authenticationGuard) Verify(context.Context, bearerauth.Token) (auth.Principal, error) {
	return auth.Principal{}, guard.reject("Bearer verification")
}

func (guard *authenticationGuard) Allowed(context.Context, auth.Principal, auth.Permission) (bool, error) {
	return false, guard.reject("permission evaluation")
}
