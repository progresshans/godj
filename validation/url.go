package validation

import (
	"net/netip"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/internal/unicode16"
)

// ValidURL applies the pinned Django 6.1 URLValidator's external grammar.
// It validates a complete http/https/ftp/ftps URL without resolving hosts,
// following redirects, changing case, percent-decoding, or rewriting IDNs.
// This is syntax validation, not permission to fetch or publish a URL.
// Source authority: django/core/validators.py, BSD-3-Clause (LICENSE.django).
func ValidURL(text string) bool {
	if !utf8.ValidString(text) || utf8.RuneCountInString(text) > 2048 || strings.ContainsAny(text, "\t\r\n") {
		return false
	}
	scheme, rest, found := strings.Cut(text, "://")
	if !found {
		return false
	}
	switch strings.ToLower(scheme) {
	case "http", "https", "ftp", "ftps":
	default:
		return false
	}
	authority := rest
	if index := strings.IndexAny(rest, "/?#"); index >= 0 {
		authority = rest[:index]
		if strings.IndexFunc(rest[index:], unicode16.IsSpace) >= 0 {
			return false
		}
	}
	// Python's URL parser rejects authority characters whose NFKC form
	// introduces delimiters. Use the same pinned Unicode tables, not the host
	// toolchain's Unicode version, while retaining the original stored text.
	check := strings.NewReplacer("@", "", ":", "", "#", "", "?", "").Replace(authority)
	if normalized := unicode16.NFKC(check); normalized != check && strings.ContainsAny(normalized, "/?#@:") {
		return false
	}
	hostPort := authority
	if user, host, hasUser := strings.Cut(authority, "@"); hasUser {
		if user == "" || strings.ContainsRune(host, '@') || strings.IndexFunc(user, unicode16.IsSpace) >= 0 {
			return false
		}
		name, password, hasPassword := strings.Cut(user, ":")
		if name == "" || hasPassword && strings.ContainsRune(password, ':') {
			return false
		}
		hostPort = host
	}
	host, port := hostPort, ""
	hasPort := false
	if strings.HasPrefix(hostPort, "[") {
		end := strings.IndexByte(hostPort, ']')
		if end < 0 {
			return false
		}
		host = hostPort[1:end]
		if len(hostPort) > end+1 {
			if hostPort[end+1] != ':' {
				return false
			}
			port, hasPort = hostPort[end+2:], true
		}
		for _, char := range host {
			if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f' || char >= 'A' && char <= 'F' || char == ':' || char == '.') {
				return false
			}
		}
		address, err := netip.ParseAddr(host)
		if err != nil || !address.Is6() {
			return false
		}
	} else {
		host, port, hasPort = strings.Cut(hostPort, ":")
		// urlsplit also treats brackets in userinfo as an IPv6 parse request;
		// ordinary hostnames in such an authority are not accepted.
		if strings.ContainsAny(authority, "[]") || !validURLHost(host) {
			return false
		}
	}
	if utf8.RuneCountInString(unicode16.Lower(host)) > 253 {
		return false
	}
	if hasPort {
		if len(port) < 1 || len(port) > 5 {
			return false
		}
		for _, char := range port {
			if char < '0' || char > '9' {
				return false
			}
		}
	}
	return true
}

func validURLHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if address, err := netip.ParseAddr(host); err == nil && address.Is4() {
		return true
	}
	labels := strings.Split(strings.TrimSuffix(host, "."), ".")
	if len(labels) < 2 {
		return false
	}
	for index, label := range labels {
		runes := []rune(label)
		if len(runes) == 0 || len(runes) > 63 || runes[0] == '-' || runes[len(runes)-1] == '-' {
			return false
		}
		for _, char := range runes {
			if !(emailDomainLetter(char) || char >= '0' && char <= '9' || char == '-') {
				return false
			}
		}
		if index != len(labels)-1 {
			continue
		}
		if len(label) >= 5 && strings.EqualFold(label[:4], "xn--") {
			punycode := true
			for _, char := range runes[4:] {
				punycode = punycode && (emailASCIILetter(char) || char >= '0' && char <= '9')
			}
			if punycode {
				continue
			}
		}
		if len(runes) < 2 {
			return false
		}
		for _, char := range runes {
			if !emailDomainLetter(char) && char != '-' {
				return false
			}
		}
	}
	return true
}
