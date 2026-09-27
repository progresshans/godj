package identityadmin

import (
	"net/netip"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/internal/unicode16"
	"github.com/progresshans/godj/validation"
)

func passwordFields(required bool) ([]forms.Field, error) {
	first, err := forms.CharField("password1", forms.WithLabel("Password"), forms.WithWidget(forms.PasswordInput), forms.WithTrimWhitespace(false), forms.WithRequired(required))
	if err != nil {
		return nil, err
	}
	second, err := forms.CharField("password2", forms.WithLabel("Password confirmation"), forms.WithWidget(forms.PasswordInput), forms.WithTrimWhitespace(false), forms.WithRequired(required))
	if err != nil {
		return nil, err
	}
	return []forms.Field{first, second}, nil
}

func passwordConfirmation() forms.CrossValidator {
	return forms.CrossValidatorFunc(func(values forms.Values) validation.Errors {
		first, a := values.String("password1")
		second, b := values.String("password2")
		if a && b && first != "" && second != "" && first != second {
			return validation.NewErrors(validation.New("password2", "password_mismatch"))
		}
		return validation.Errors{}
	})
}

// The creation choice owns whether password fields are required. A deliberate
// unusable selection ignores their contents, matching AdminUserCreationForm;
// transport/resource bounds and private rendering still apply to both fields.
func creationPasswords() forms.CrossValidator {
	return forms.CrossValidatorFunc(func(values forms.Values) validation.Errors {
		unusable, _ := values.Boolean("unusable_password")
		if unusable {
			return validation.NewErrors()
		}
		var required []validation.Violation
		for _, name := range []string{"password1", "password2"} {
			value, present := values.String(name)
			if present && value == "" {
				required = append(required, validation.New(validation.Field(name), "required"))
			}
		}
		return validation.Join(validation.NewErrors(required...), passwordConfirmation().ValidateForm(values))
	})
}

func trimPythonSpace(value string) string {
	return unicode16.TrimSpace(value)
}

// UsernameField normalizes after stripping and avoids expensive normalization
// of an already overlong input. Length validation still observes the result.
func usernameNormalizer(limit int) func(string) string {
	return func(value string) string {
		value = trimPythonSpace(value)
		if utf8.RuneCountInString(value) > limit {
			return value
		}
		return unicode16.NFKC(value)
	}
}

func usernameValidator(limit int) forms.FieldValidator {
	return forms.FieldValidatorFunc(func(value forms.Value) validation.Errors {
		text, ok := value.AsString()
		if !ok || text == "" {
			return validation.Errors{}
		}
		// Django's model username validator runs only after the form field
		// succeeds. Preserve that boundary instead of adding a second error
		// to a rejected length, NUL or malformed encoding.
		if !utf8.ValidString(text) || strings.ContainsRune(text, 0) || utf8.RuneCountInString(text) > limit {
			return validation.Errors{}
		}
		valid := true
		for _, r := range text {
			valid = valid && (unicode16.IsAlphanumeric(r) || strings.ContainsRune("_.@+-", r))
		}
		if !valid {
			return validation.NewErrors(validation.New("username", "invalid"))
		}
		return validation.Errors{}
	})
}

func emailValidator() forms.FieldValidator {
	return forms.FieldValidatorFunc(func(value forms.Value) validation.Errors {
		text, ok := value.AsString()
		if !ok || text == "" || validEmail(text) {
			return validation.Errors{}
		}
		return validation.NewErrors(validation.New("email", "invalid"))
	})
}

// This is the pinned Django 6.1 EmailValidator's external grammar, including
// quoted local parts, literal IPs, case-sensitive localhost and Unicode BMP
// domain labels. It does not use net/mail's broader address/display-name parser.
func validEmail(text string) bool {
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
