package validation

import (
	"net/netip"
	"strings"
	"unicode/utf8"
)

// ValidEmail applies the pinned Django 6.1 EmailValidator's external grammar, including
// quoted local parts, literal IPs, case-sensitive localhost and Unicode BMP
// domain labels. It does not use net/mail's broader address/display-name parser.
func ValidEmail(text string) bool {
	if !utf8.ValidString(text) || utf8.RuneCountInString(text) > 320 {
		return false
	}
	at := strings.LastIndexByte(text, '@')
	if at < 1 || at == len(text)-1 {
		return false
	}
	local, domain := text[:at], text[at+1:]
	if !validEmailLocal(local) {
		return false
	}
	if domain == "localhost" {
		return true
	}
	if strings.HasPrefix(domain, "[") && strings.HasSuffix(domain, "]") {
		literal := domain[1 : len(domain)-1]
		for _, r := range literal {
			if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F' || r == ':' || r == '.') {
				return false
			}
		}
		_, err := netip.ParseAddr(literal)
		return err == nil
	}
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		runes := []rune(label)
		if len(runes) < 1 || len(runes) > 63 || runes[0] == '-' || runes[len(runes)-1] == '-' {
			return false
		}
		for _, r := range runes {
			if !(emailDomainLetter(r) || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	tld := labels[len(labels)-1]
	runes := []rune(tld)
	if len(runes) >= 5 && strings.EqualFold(tld[:4], "xn--") {
		valid := true
		for _, r := range runes[4:] {
			valid = valid && (emailASCIILetter(r) || r >= '0' && r <= '9')
		}
		if valid {
			return true
		}
	}
	if len(runes) < 2 {
		return false
	}
	for _, r := range runes {
		if !emailDomainLetter(r) && r != '-' {
			return false
		}
	}
	return true
}

func emailDomainLetter(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= 0xa1 && r <= 0xffff
}
func emailASCIILetter(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == 'İ' || r == 'ı' || r == 'ſ' || r == 'K'
}

func validEmailLocal(value string) bool {
	if strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) && len(value) >= 2 {
		runes := []rune(value[1 : len(value)-1])
		for i := 0; i < len(runes); i++ {
			r := runes[i]
			if r == '\\' {
				i++
				if i == len(runes) {
					return false
				}
				r = runes[i]
				if !(r >= 1 && r <= 9 || r == 11 || r == 12 || r >= 14 && r <= 127) {
					return false
				}
			} else if !(r >= 1 && r <= 8 || r == 11 || r == 12 || r >= 14 && r <= 31 || r == '!' || r >= '#' && r <= '[' || r >= ']' && r <= 127) {
				return false
			}
		}
		return true
	}
	for _, atom := range strings.Split(value, ".") {
		if atom == "" {
			return false
		}
		for _, r := range atom {
			if !(emailASCIILetter(r) || r >= '0' && r <= '9' || strings.ContainsRune("-!#$%&'*+/=?^_`{}|~", r)) {
				return false
			}
		}
	}
	return true
}
