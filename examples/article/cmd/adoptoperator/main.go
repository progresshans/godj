// Command adoptoperator explicitly transfers an existing Article operator to
// the identity app after godj migrate. Startup never performs this operation.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/progresshans/godj/examples/article/databaseconfig"
	"github.com/progresshans/godj/examples/article/internal/operatorconfig"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/systemstate"
)

type outcome struct {
	Status        string `json:"status"`
	Code          string `json:"code,omitempty"`
	Kind          string `json:"kind,omitempty"`
	UserID        int64  `json:"user_id,omitempty"`
	CleanupFailed bool   `json:"cleanup_failed,omitempty"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.LookupEnv, os.Stdout)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, lookup databaseconfig.LookupEnvFunc, output io.Writer) int {
	result, code := execute(ctx, args, lookup)
	if err := json.NewEncoder(output).Encode(result); err != nil {
		// A failed output is not permission to replay an ownership transfer.
		return 3
	}
	return code
}

func execute(ctx context.Context, args []string, lookup databaseconfig.LookupEnvFunc) (result outcome, code int) {
	result = outcome{Status: "failed", Code: "invalid_arguments"}
	flags := flag.NewFlagSet("adoptoperator", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	staff := flags.Bool("staff", false, "explicit staff role")
	superuser := flags.Bool("superuser", false, "explicit superuser role")
	inspect := flags.Bool("inspect", false, "read the durable transition without retrying")
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return result, 2
	}
	seen := make(map[string]bool)
	flags.Visit(func(f *flag.Flag) { seen[f.Name] = true })
	if *inspect {
		if seen["staff"] || seen["superuser"] {
			return result, 2
		}
	} else if !seen["staff"] || !seen["superuser"] {
		return result, 2
	}
	if ctx == nil || ctx.Err() != nil {
		return outcome{Status: "failed", Code: "canceled"}, 3
	}
	selected, err := databaseconfig.FromEnvironment(lookup)
	if err != nil {
		return outcome{Status: "failed", Code: "database_config"}, 2
	}
	backend, err := databaseconfig.Open(ctx, selected)
	if err != nil {
		return outcome{Status: "failed", Code: "backend_open"}, 3
	}
	defer func() {
		if backend.Close() != nil {
			result.CleanupFailed = true
			code = 3
		}
	}()
	var receipt systemstate.IdentityTransition
	if *inspect {
		receipt, err = systemstate.InspectIdentityTransition(ctx, backend)
	} else {
		var expected systemstate.RuntimeConfig
		expected, err = operatorconfig.RuntimeConfig()
		if err == nil {
			receipt, err = systemstate.AdoptOperator(ctx, backend, systemstate.AdoptOperatorConfig{Expected: expected, Staff: *staff, Superuser: *superuser})
		}
	}
	if err != nil {
		if errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) || errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) {
			return outcome{Status: "uncertain", Code: "inspect_required"}, 3
		}
		var stateError *systemstate.Error
		if errors.As(err, &stateError) && stateError != nil {
			return outcome{Status: "failed", Code: string(stateError.Code)}, 1
		}
		return outcome{Status: "failed", Code: "operation_failed"}, 3
	}
	status := "adopted"
	if *inspect {
		status = "recorded"
	}
	return outcome{Status: status, Kind: receipt.Kind(), UserID: receipt.UserID()}, 0
}
