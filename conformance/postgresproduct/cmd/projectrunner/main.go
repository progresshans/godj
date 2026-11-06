package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

const (
	postgresURLEnv       = "GODJ_TEST_POSTGRES_URL"
	postgresSchemaEnv    = "GODJ_TEST_POSTGRES_SCHEMA"
	postgresSchemaPrefix = "godj_postgresproduct_"
	minimumSchemaSuffix  = 8
	maximumSchemaSuffix  = 40
	maximumIdentifier    = 63
	runnerTimeout        = 2 * time.Minute
)

var (
	errInvalidArguments   = errors.New("project runner requires exactly one supported mode")
	errMissingDatabaseURL = errors.New("GODJ_TEST_POSTGRES_URL is required")
	errMissingSchema      = errors.New("GODJ_TEST_POSTGRES_SCHEMA is required")
)

type runnerConfig struct {
	databaseURL string
	schema      string
}

type modeResult struct {
	Mode    string `json:"mode"`
	Status  string `json:"status"`
	History int    `json:"history,omitempty"`
	Rows    int    `json:"rows,omitempty"`
}

type failureResult struct {
	Mode   string `json:"mode,omitempty"`
	Status string `json:"status"`
	Error  string `json:"error"`
}

func main() {
	signalContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalContext, runnerTimeout)
	defer cancel()

	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		_ = writeFailure(os.Stderr, modeFromArguments(os.Args[1:]), err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string, output io.Writer) error {
	if ctx == nil {
		return errors.New("context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if output == nil {
		return errors.New("output is nil")
	}
	mode := modeFromArguments(arguments)
	if mode == "" {
		return errInvalidArguments
	}
	config, err := configFromEnvironment()
	if err != nil {
		return err
	}
	result, err := executeMode(ctx, config, mode)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(result)
}

func modeFromArguments(arguments []string) string {
	if len(arguments) != 1 {
		return ""
	}
	switch arguments[0] {
	case "prepare", "probe", "resume", "verify", "cleanup":
		return arguments[0]
	default:
		return ""
	}
}

func configFromEnvironment() (runnerConfig, error) {
	databaseURL := os.Getenv(postgresURLEnv)
	if strings.TrimSpace(databaseURL) == "" {
		return runnerConfig{}, errMissingDatabaseURL
	}
	schema := os.Getenv(postgresSchemaEnv)
	if strings.TrimSpace(schema) == "" {
		return runnerConfig{}, errMissingSchema
	}
	if err := validateRunnerSchema(schema); err != nil {
		return runnerConfig{}, err
	}
	return runnerConfig{databaseURL: databaseURL, schema: schema}, nil
}

func validateRunnerSchema(schema string) error {
	if len(schema) > maximumIdentifier {
		return errors.New("PostgreSQL product schema exceeds 63 bytes")
	}
	if !strings.HasPrefix(schema, postgresSchemaPrefix) {
		return errors.New("PostgreSQL product schema has an invalid prefix")
	}
	suffix := strings.TrimPrefix(schema, postgresSchemaPrefix)
	if len(suffix) < minimumSchemaSuffix || len(suffix) > maximumSchemaSuffix {
		return errors.New("PostgreSQL product schema suffix length is invalid")
	}
	for _, character := range []byte(suffix) {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') {
			return errors.New("PostgreSQL product schema suffix contains an invalid character")
		}
	}
	return nil
}

func writeFailure(output io.Writer, mode string, err error) error {
	if output == nil {
		return errors.New("failure output is nil")
	}
	return json.NewEncoder(output).Encode(failureResult{
		Mode:   mode,
		Status: "error",
		Error:  publicErrorCode(err),
	})
}

func publicErrorCode(err error) string {
	switch {
	case errors.Is(err, errInvalidArguments):
		return "invalid_arguments"
	case errors.Is(err, errMissingDatabaseURL), errors.Is(err, errMissingSchema):
		return "invalid_environment"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	default:
		return "operation_failed"
	}
}
