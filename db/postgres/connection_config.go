package postgres

import (
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

func connectionConfigInvalid(field, detail string) error {
	failure := backendInvalid(detail)
	failure.Field = field
	return failure
}

// explicitConnectionURL supplies every pgx v5.10.0 environment/default input
// from the URL or this profile. It performs no environment lookup or file I/O.
// The driver still owns parsing TLS, authentication and protocol options.
func explicitConnectionURL(rawURL string) (string, error) {
	invalid := func() (string, error) {
		return "", connectionConfigInvalid("url", "PostgreSQL URL must explicitly identify a user, host and database with supported, unambiguous options")
	}
	if len(rawURL) > 64*1024 || strings.ContainsRune(rawURL, 0) {
		return invalid()
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") ||
		parsed.Host == "" || parsed.User == nil || parsed.User.Username() == "" ||
		strings.TrimLeft(parsed.Path, "/") == "" || parsed.Fragment != "" {
		return invalid()
	}
	password, _ := parsed.User.Password()
	for _, value := range []string{parsed.User.Username(), password, parsed.Host, parsed.Path} {
		if strings.ContainsRune(value, 0) {
			return invalid()
		}
	}
	options, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return invalid()
	}
	for key, values := range options {
		if len(values) != 1 || strings.ContainsRune(values[0], 0) || !supportedConnectionOption(key) {
			return invalid()
		}
	}
	if options.Has("sslmode") && options.Get("sslmode") == "" {
		return invalid()
	}
	// Authority, userinfo and path are the sole identity source. In particular,
	// query host/user/dbname aliases must not silently override that identity.
	hosts := make([]string, 0)
	for _, host := range strings.Split(parsed.Host, ",") {
		if host == "" {
			return invalid()
		}
		port := "5432"
		if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
			host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
			if net.ParseIP(host) == nil {
				return invalid()
			}
		} else if strings.Contains(host, ":") {
			host, port, err = net.SplitHostPort(host)
			if err != nil || host == "" {
				return invalid()
			}
		}
		if number, err := strconv.ParseUint(port, 10, 16); err != nil || number == 0 {
			return invalid()
		}
		hosts = append(hosts, net.JoinHostPort(host, port))
	}
	parsed.Host = strings.Join(hosts, ",")
	if _, present := parsed.User.Password(); !present {
		options.Set("password", "")
	}
	// Empty paths prevent pgx from loading default credential/certificate
	// files. TLS files explicitly named in the URL are read by the driver
	// during Open and retained in that invocation's connection configuration.
	defaults := map[string]string{
		"passfile": "", "servicefile": "",
		"sslmode": "verify-full", "sslcert": "", "sslkey": "", "sslrootcert": "", "sslpassword": "",
		"sslsni": "1", "sslnegotiation": "", "connect_timeout": "0", "target_session_attrs": "any",
		"application_name": "", "options": "", "timezone": "UTC",
		"min_protocol_version": "3.0", "max_protocol_version": "", "channel_binding": "prefer", "require_auth": "",
	}
	for key, value := range defaults {
		if !options.Has(key) {
			options.Set(key, value)
		}
	}
	parsed.RawQuery = options.Encode()
	return parsed.String(), nil
}

func supportedConnectionOption(key string) bool {
	switch key {
	case "sslmode", "sslcert", "sslkey", "sslrootcert", "sslpassword", "sslsni", "sslnegotiation",
		"connect_timeout", "target_session_attrs", "application_name", "min_protocol_version", "max_protocol_version",
		"channel_binding", "require_auth", "statement_cache_capacity", "description_cache_capacity", "default_query_exec_mode",
		"client_encoding", "intervalstyle", "default_transaction_deferrable", "default_transaction_isolation",
		"default_transaction_read_only", "search_path", "standard_conforming_strings", "synchronous_commit", "timezone":
		return true
	default:
		return false
	}
}

// pgx has no injected environment lookup and treats the mere presence of a
// service key as a request to load a service. Reject it explicitly. The forced
// empty servicefile above also makes a concurrent PGSERVICE change fail closed
// inside pgx, without reading a service file or selecting another database.
func rejectAmbientPostgresService() error {
	if os.Getenv("PGSERVICE") != "" {
		return connectionConfigInvalid("environment", "PGSERVICE is unsupported; configure the project PostgreSQL URL explicitly")
	}
	return nil
}
