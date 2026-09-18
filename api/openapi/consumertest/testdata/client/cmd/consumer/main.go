package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"time"
)

const maxInputBytes = 64 << 10

type endpoint struct {
	URL             string `json:"url"`
	Token           string `json:"token,omitempty"`
	ReadOnlyToken   string `json:"read_only_token,omitempty"`
	Session         string `json:"session,omitempty"`
	ReadOnlySession string `json:"read_only_session,omitempty"`
	CategoryID      int64  `json:"category_id,omitempty"`
	TicketID        int64  `json:"ticket_id,omitempty"`
	OtherTicketID   int64  `json:"other_ticket_id,omitempty"`
}

type input struct {
	ArticleBearer   endpoint `json:"article_bearer"`
	ArticleSession  endpoint `json:"article_session"`
	HelpdeskSession endpoint `json:"helpdesk_session"`
}

// The parent independently requires every name. A successful process cannot
// omit a flow, publish partial results, or infer race instrumentation at runtime.
var requiredChecks = [...]string{
	"article_bearer_crud",
	"article_bearer_patch_presence",
	"article_bearer_put_defaults",
	"article_bearer_auth_errors",
	"article_session_csrf_crud",
	"article_session_invalid_csrf",
	"helpdesk_session_relations",
	"helpdesk_session_create_defaults",
	"helpdesk_session_integer_values", "helpdesk_session_multiline_text",
	"helpdesk_session_read_only_denied",
	"generated_int64_wire",
	"generated_response_rejections",
	"pre_canceled_request",
}

type report struct {
	Checks []string `json:"checks"`
	Race   bool     `json:"race"`
}

func main() { os.Exit(entrypoint()) }

func entrypoint() (status int) {
	status = 1
	// Generated transport errors and panic values can contain request URLs or
	// response bodies. Only fixed, caller-owned stage labels leave this process.
	defer func() {
		if recover() != nil {
			fmt.Fprintln(os.Stderr, "consumer internal failure")
		}
	}()
	config, err := readInput(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "consumer input rejected")
		return status
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	checks, err := run(ctx, config)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return status
	}
	if err := json.NewEncoder(os.Stdout).Encode(report{Checks: checks, Race: raceEnabled}); err != nil {
		fmt.Fprintln(os.Stderr, "consumer report write failed")
		return status
	}
	return 0
}

func readInput(reader io.Reader) (input, error) {
	var config input
	data, err := io.ReadAll(io.LimitReader(reader, maxInputBytes+1))
	if err != nil || len(data) > maxInputBytes {
		return config, errors.New("invalid input")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return config, errors.New("invalid input")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return config, errors.New("invalid input")
	}
	for _, target := range []endpoint{config.ArticleBearer, config.ArticleSession, config.HelpdeskSession} {
		address, err := url.Parse(target.URL)
		if err != nil || address.Scheme != "http" || address.User != nil || address.RawQuery != "" || address.Fragment != "" || (address.Path != "" && address.Path != "/") {
			return config, errors.New("invalid endpoint")
		}
		ip := net.ParseIP(address.Hostname())
		if ip == nil || !ip.IsLoopback() || address.Port() == "" {
			return config, errors.New("invalid endpoint")
		}
	}
	if config.ArticleBearer.URL == config.ArticleSession.URL || config.ArticleBearer.URL == config.HelpdeskSession.URL || config.ArticleSession.URL == config.HelpdeskSession.URL {
		return config, errors.New("endpoints must be distinct")
	}
	if config.ArticleBearer.Token == "" || config.ArticleBearer.ReadOnlyToken == "" || config.ArticleSession.Session == "" || config.HelpdeskSession.Session == "" || config.HelpdeskSession.ReadOnlySession == "" {
		return config, errors.New("missing credentials")
	}
	if config.HelpdeskSession.CategoryID <= 0 || config.HelpdeskSession.TicketID <= 0 || config.HelpdeskSession.OtherTicketID <= 0 || config.HelpdeskSession.TicketID == config.HelpdeskSession.OtherTicketID {
		return config, errors.New("missing fixture identities")
	}
	return config, nil
}

func run(ctx context.Context, config input) ([]string, error) {
	completed := make(map[string]bool, len(requiredChecks))
	flows := []struct {
		run    func() error
		checks []string
	}{
		{func() error { return checkArticleBearer(ctx, config.ArticleBearer) }, []string{
			"article_bearer_crud", "article_bearer_patch_presence", "article_bearer_put_defaults", "article_bearer_auth_errors", "pre_canceled_request",
		}},
		{func() error { return checkArticleSession(ctx, config.ArticleSession) }, []string{
			"article_session_csrf_crud", "article_session_invalid_csrf",
		}},
		{func() error { return checkHelpdeskSession(ctx, config.HelpdeskSession) }, []string{
			"helpdesk_session_relations", "helpdesk_session_create_defaults", "helpdesk_session_integer_values", "helpdesk_session_multiline_text", "helpdesk_session_read_only_denied",
		}},
		{func() error { return checkGeneratedWire(ctx) }, []string{
			"generated_int64_wire", "generated_response_rejections",
		}},
	}
	for _, flow := range flows {
		if err := flow.run(); err != nil {
			return nil, err
		}
		for _, check := range flow.checks {
			if completed[check] {
				return nil, fail("duplicate check")
			}
			completed[check] = true
		}
	}
	if len(completed) != len(requiredChecks) {
		return nil, fail("incomplete report")
	}
	checks := make([]string, 0, len(requiredChecks))
	for _, check := range requiredChecks {
		if !completed[check] {
			return nil, fail("missing required check")
		}
		checks = append(checks, check)
	}
	return checks, nil
}

func fail(stage string) error { return errors.New("consumer check failed: " + stage) }
