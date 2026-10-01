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
	URL                string `json:"url"`
	Token              string `json:"token,omitempty"`
	ReadOnlyToken      string `json:"read_only_token,omitempty"`
	Session            string `json:"session,omitempty"`
	ReadOnlySession    string `json:"read_only_session,omitempty"`
	CategoryID         int64  `json:"category_id,omitempty"`
	TicketID           int64  `json:"ticket_id,omitempty"`
	OtherTicketID      int64  `json:"other_ticket_id,omitempty"`
	OtherLabelID       int64  `json:"other_label_id,omitempty"`
	OtherTicketLabelID int64  `json:"other_ticket_label_id,omitempty"`
}

type input struct {
	AccountSession  accountEndpoint  `json:"account_session"`
	ArticleBearer   endpoint         `json:"article_bearer"`
	ArticleSession  endpoint         `json:"article_session"`
	HelpdeskSession endpoint         `json:"helpdesk_session"`
	IdentitySession identityEndpoint `json:"identity_session"`
	IdentityBearer  identityEndpoint `json:"identity_bearer"`
}

type identityEndpoint struct {
	endpoint
	ActorID               int64 `json:"actor_id"`
	TargetID              int64 `json:"target_id"`
	ProtectedUserID       int64 `json:"protected_user_id"`
	ProtectedGroupID      int64 `json:"protected_group_id"`
	ProtectedPermissionID int64 `json:"protected_permission_id"`
	CascadeUserID         int64 `json:"cascade_user_id"`
	CascadeGroupID        int64 `json:"cascade_group_id"`
	CascadePermissionID   int64 `json:"cascade_permission_id"`
}

// The parent independently requires every name. A successful process cannot
// omit a flow, publish partial results, or infer race instrumentation at runtime.
var requiredChecks = [...]string{
	"account_reset_anonymous_csrf", "account_reset_mail_and_hidden_proof", "account_reset_atomic_completion", "generated_reset_unknown_no_retry",
	"account_session_product_login", "account_session_password_csrf", "account_session_rotation_revocation", "account_session_product_logout", "generated_account_unknown_no_retry",
	"article_bearer_slug", "article_bearer_crud",
	"article_bearer_patch_presence",
	"article_bearer_put_defaults",
	"article_bearer_auth_errors",
	"article_session_slug", "article_session_csrf_crud",
	"article_session_invalid_csrf",
	"helpdesk_session_relations",
	"helpdesk_session_create_defaults",
	"helpdesk_session_integer_values", "helpdesk_session_multiline_text", "helpdesk_session_datetime_values", "helpdesk_session_calendar_dates", "helpdesk_session_clock_times", "helpdesk_session_durations", "helpdesk_session_float_values", "helpdesk_session_decimal_values", "helpdesk_session_uuid_values", "helpdesk_session_url_values", "helpdesk_session_binary_digest", "helpdesk_session_uniqueness", "helpdesk_session_json_values", "helpdesk_session_json_search", "helpdesk_service_reports", "helpdesk_category_labels", "helpdesk_ticket_label_links", "helpdesk_ticket_collections",
	"helpdesk_session_read_only_denied",
	"helpdesk_session_choices", "helpdesk_nullable_boolean_presence", "helpdesk_put_patch",
	"generated_choice_response_domain", "generated_nullable_boolean_wire", "generated_calendar_date_wire", "generated_clock_time_wire", "generated_duration_wire", "generated_float_wire", "generated_decimal_wire", "generated_uuid_wire", "generated_url_wire", "generated_binary_wire", "generated_json_wire", "generated_collection_wire",
	"generated_slug_wire", "generated_int64_wire",
	"generated_response_rejections",
	"pre_canceled_request",
	"identity_bearer_crud", "identity_bearer_auth_errors", "identity_bearer_unusable_password", "identity_bearer_password_status", "identity_bearer_last_login", "identity_bearer_email", "identity_stale_bearer_current_authorization",
	"identity_session_crud", "identity_session_conditions_csrf", "identity_session_collection_presence", "identity_session_password_revocation", "identity_session_unusable_password", "identity_session_password_status", "identity_session_last_login", "identity_session_email", "identity_host_deletion",
	"generated_identity_revision_wire", "generated_identity_collection_rejections", "generated_identity_unknown_no_retry", "generated_identity_unusable_password_wire", "generated_identity_password_status_wire",
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
	if config.AccountSession.Username == "" || config.AccountSession.Password == "" || config.AccountSession.NewPassword == "" || config.AccountSession.Email == "" || config.AccountSession.ResetPassword == "" || config.AccountSession.MailProof == "" {
		return config, errors.New("missing account inputs")
	}
	addresses := make(map[string]bool)
	for _, target := range []endpoint{config.ArticleBearer, config.ArticleSession, config.HelpdeskSession, config.IdentitySession.endpoint, config.IdentityBearer.endpoint, endpoint{URL: config.AccountSession.URL}} {
		address, err := url.Parse(target.URL)
		if err != nil || address.Scheme != "http" || address.User != nil || address.RawQuery != "" || address.Fragment != "" || (address.Path != "" && address.Path != "/") {
			return config, errors.New("invalid endpoint")
		}
		ip := net.ParseIP(address.Hostname())
		if ip == nil || !ip.IsLoopback() || address.Port() == "" {
			return config, errors.New("invalid endpoint")
		}
		if addresses[address.Host] {
			return config, errors.New("endpoints must be distinct")
		}
		addresses[address.Host] = true
	}
	if config.ArticleBearer.Token == "" || config.ArticleBearer.ReadOnlyToken == "" || config.ArticleSession.Session == "" || config.HelpdeskSession.Session == "" || config.HelpdeskSession.ReadOnlySession == "" {
		return config, errors.New("missing credentials")
	}
	if config.HelpdeskSession.CategoryID <= 0 || config.HelpdeskSession.TicketID <= 0 || config.HelpdeskSession.OtherTicketID <= 0 || config.HelpdeskSession.OtherLabelID <= 0 || config.HelpdeskSession.OtherTicketLabelID <= 0 || config.HelpdeskSession.TicketID == config.HelpdeskSession.OtherTicketID {
		return config, errors.New("missing fixture identities")
	}
	if config.IdentitySession.Session == "" || config.IdentitySession.ReadOnlySession == "" || config.IdentityBearer.Token == "" || config.IdentityBearer.ReadOnlyToken == "" {
		return config, errors.New("missing identity credentials")
	}
	for _, target := range []identityEndpoint{config.IdentitySession, config.IdentityBearer} {
		if target.ActorID <= 0 || target.TargetID <= 0 || target.ActorID == target.TargetID || target.ProtectedUserID <= 0 || target.ProtectedGroupID <= 0 || target.ProtectedPermissionID <= 0 || target.CascadeUserID <= 0 || target.CascadeGroupID <= 0 || target.CascadePermissionID <= 0 {
			return config, errors.New("missing identity fixture identities")
		}
	}
	return config, nil
}

func run(ctx context.Context, config input) ([]string, error) {
	completed := make(map[string]bool, len(requiredChecks))
	flows := []struct {
		run    func() error
		checks []string
	}{
		{func() error { return checkAccountSession(ctx, config.AccountSession) }, []string{"account_session_product_login", "account_session_password_csrf", "account_session_rotation_revocation", "account_session_product_logout", "generated_account_unknown_no_retry"}},
		{func() error { return checkAccountReset(ctx, config.AccountSession) }, []string{"account_reset_anonymous_csrf", "account_reset_mail_and_hidden_proof", "account_reset_atomic_completion", "generated_reset_unknown_no_retry"}},
		{func() error { return checkArticleBearer(ctx, config.ArticleBearer) }, []string{
			"article_bearer_slug", "article_bearer_crud", "article_bearer_patch_presence", "article_bearer_put_defaults", "article_bearer_auth_errors", "pre_canceled_request",
		}},
		{func() error { return checkArticleSession(ctx, config.ArticleSession) }, []string{
			"article_session_slug", "article_session_csrf_crud", "article_session_invalid_csrf",
		}},
		{func() error { return checkHelpdeskSession(ctx, config.HelpdeskSession) }, []string{
			"helpdesk_session_relations", "helpdesk_session_create_defaults", "helpdesk_session_integer_values", "helpdesk_session_multiline_text", "helpdesk_session_datetime_values", "helpdesk_session_calendar_dates", "helpdesk_session_clock_times", "helpdesk_session_durations", "helpdesk_session_float_values", "helpdesk_session_decimal_values", "helpdesk_session_uuid_values", "helpdesk_session_url_values", "helpdesk_session_binary_digest", "helpdesk_session_uniqueness", "helpdesk_session_json_values", "helpdesk_session_json_search", "helpdesk_service_reports", "helpdesk_category_labels", "helpdesk_ticket_label_links", "helpdesk_ticket_collections", "helpdesk_session_read_only_denied",
			"helpdesk_session_choices", "helpdesk_nullable_boolean_presence", "helpdesk_put_patch",
		}},
		{func() error { return checkGeneratedWire(ctx) }, []string{
			"generated_slug_wire", "generated_int64_wire", "generated_response_rejections",
			"generated_choice_response_domain", "generated_nullable_boolean_wire", "generated_calendar_date_wire", "generated_clock_time_wire", "generated_duration_wire", "generated_float_wire", "generated_decimal_wire", "generated_uuid_wire", "generated_url_wire", "generated_binary_wire", "generated_json_wire", "generated_collection_wire",
		}},
		{func() error { return checkIdentityBearer(ctx, config.IdentityBearer) }, []string{"identity_bearer_crud", "identity_bearer_auth_errors", "identity_bearer_unusable_password", "identity_bearer_password_status", "identity_bearer_last_login", "identity_bearer_email"}},
		{func() error { return checkIdentitySession(ctx, config.IdentitySession) }, []string{
			"identity_session_crud", "identity_session_conditions_csrf", "identity_session_collection_presence", "identity_session_password_revocation", "identity_session_unusable_password", "identity_session_password_status", "identity_session_last_login", "identity_session_email", "identity_host_deletion",
		}},
		{func() error { return checkIdentityStaleBearer(ctx, config.IdentityBearer) }, []string{"identity_stale_bearer_current_authorization"}},
		{func() error { return checkGeneratedIdentityWire(ctx) }, []string{"generated_identity_revision_wire", "generated_identity_collection_rejections", "generated_identity_unknown_no_retry", "generated_identity_unusable_password_wire", "generated_identity_password_status_wire"}},
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
