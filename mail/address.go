package mail

import (
	"fmt"
	stdmail "net/mail"
	"strings"

	"golang.org/x/net/idna"
)

// Address is one validated mailbox. Display names are not envelope recipients.
// Unicode domains use the pinned IDNA lookup profile with transitional mapping;
// non-ASCII local parts require the SMTPUTF8 transport capability. Header parsing
// does not replace a consumer's email form or stored identity validation policy.
type Address struct{ state *address }
type address struct {
	name, mailbox string
	smtpUTF8      bool
}

var domainProfile = idna.New(idna.MapForLookup(), idna.Transitional(true), idna.BidiRule(), idna.VerifyDNSLength(true))

func ParseAddress(value string) (Address, error) {
	if len(value) > 2048 || !headerText(value) {
		return Address{}, failure(CodeInvalidInput, "address", nil)
	}
	parsed, err := stdmail.ParseAddress(value)
	if err != nil {
		return Address{}, failure(CodeInvalidInput, "address", err)
	}
	at := strings.LastIndexByte(parsed.Address, '@')
	if at < 1 || at == len(parsed.Address)-1 || !headerText(parsed.Address) || !headerText(parsed.Name) {
		return Address{}, failure(CodeInvalidInput, "address", nil)
	}
	local, domain := parsed.Address[:at], parsed.Address[at+1:]
	if !strings.HasPrefix(domain, "[") {
		domain, err = domainProfile.ToASCII(domain)
		if err != nil {
			return Address{}, failure(CodeInvalidInput, "address", err)
		}
	}
	// net/mail owns RFC quoting of the already parsed, unquoted local part.
	// Store that result once; passing a quoted mailbox back to Address.String
	// would quote its quote characters for a second time.
	canonical := (&stdmail.Address{Address: local + "@" + domain}).String()
	mailbox := strings.TrimSuffix(strings.TrimPrefix(canonical, "<"), ">")
	if len(mailbox) > 254 || len(local) > 64 {
		return Address{}, failure(CodeInvalidInput, "address", nil)
	}
	nonASCII := false
	for _, char := range local {
		nonASCII = nonASCII || char > 127
	}
	return Address{&address{parsed.Name, mailbox, nonASCII}}, nil
}

// Mailbox returns the explicit envelope address, without a display name.
func (a Address) Mailbox() string {
	if a.state == nil {
		return ""
	}
	return a.state.mailbox
}

// Header returns an encoded RFC header value. Any folds are renderer-owned.
func (a Address) Header() string {
	if a.state == nil {
		return ""
	}
	mailbox := "<" + a.state.mailbox + ">"
	if a.state.name == "" {
		return mailbox
	}
	return encodedWords(a.state.name) + " " + mailbox
}

func (Address) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("mail.Address{redacted}"))
}
func (Address) MarshalJSON() ([]byte, error) { return []byte(`"mail.Address{redacted}"`), nil }
