//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/internal/firstaccountwire"
	"github.com/progresshans/godj/project/firstaccount"
	"golang.org/x/term"
)

// FirstAccountTerminalReport contains counts only. Neither model input nor
// password bytes, prompt replies or a terminal transcript are retained here.
type FirstAccountTerminalReport struct {
	Checks, FieldPrompts, OmissionPrompts, PasswordPrompts, ConfirmationPrompts, RestoreAttempts int
	FileReads, FileBytes                                                                         int
}

// The terminal transports declared scalars and explicit local file bytes. The
// compiled ModelForm owns cleaning, defaults and image/model constraints.
// An omission question preserves an intentionally empty scalar or file.
func readFirstAccountTerminal(ctx context.Context, interrupt <-chan struct{}, stdin *os.File, stderr io.Writer,
	description firstaccount.Description, report *FirstAccountTerminalReport, directory string,
) (request firstaccountwire.Request, code string) {
	if ctx == nil || report == nil || firstaccount.ValidateDescription(description) != nil {
		return request, "invalid_config"
	}
	report.Checks++
	if stdin == nil || !term.IsTerminal(int(stdin.Fd())) {
		return request, "input_not_terminal"
	}
	if code = commandInterruption(ctx, interrupt); code != "" {
		return request, code
	}
	fd := int(stdin.Fd())
	state, err := enterCreatesuperuserTerminalPrivateInput(fd)
	if err != nil || state == nil {
		if state != nil {
			report.RestoreAttempts++
			_ = restoreCreatesuperuserTerminalAndDiscardInput(fd, state)
		}
		return request, "terminal_state_failed"
	}
	defer func() {
		report.RestoreAttempts++
		if err := restoreCreatesuperuserTerminalAndDiscardInput(fd, state); err != nil {
			code = "terminal_state_failed"
		}
		if code != "" {
			request.Clear()
		}
	}()
	request.Fingerprint = description.Fingerprint
	request.Values = make([]firstaccount.Value, len(description.Fields))
	visible := createsuperuserTerminalVisibleEcho(stdin, stderr)
	read := func(prompt string, maximum int, echo io.Writer) ([]byte, string) {
		if !completeCreatesuperuserWrite(stderr, []byte(prompt)) {
			return nil, "input_read_failed"
		}
		value, overflow, failure := readBoundedCreatesuperuserLine(ctx, interrupt, fd, maximum, stderr, echo)
		if failure != nil {
			clear(value)
			return nil, failure.Code
		}
		if overflow {
			clear(value)
			return nil, "invalid_input"
		}
		return value, ""
	}
	inputBytes, fileBytes := 0, 0
	for index, field := range description.Fields {
		request.Values[index].Name = field.Name
		label := strconv.Quote(field.Label)
		if field.HasDefault || !field.Required {
			report.OmissionPrompts++
			prompt := "Omit " + label + "? [Y/n]: "
			if field.HasDefault {
				prompt = "Use model default for " + label + "? [Y/n]: "
			}
			answer, failure := read(prompt, 3, visible)
			if failure != "" {
				return request, failure
			}
			omit, valid := firstAccountOmission(answer)
			clear(answer)
			if !valid {
				return request, "invalid_input"
			}
			if omit {
				continue
			}
		}
		if len(field.Choices) != 0 {
			var choices strings.Builder
			choices.WriteString("Choices for " + label + ":\n")
			for _, choice := range field.Choices {
				choices.WriteString("  " + strconv.Quote(choice.Value) + "  " + strconv.Quote(choice.Label) + "\n")
			}
			if !completeCreatesuperuserWrite(stderr, []byte(choices.String())) {
				return request, "input_read_failed"
			}
		}
		report.FieldPrompts++
		prompt, maximum := label+": ", field.MaxBytes
		if field.Upload {
			prompt, maximum = label+" (file path): ", firstaccount.MaxFieldBytes
		}
		value, failure := read(prompt, maximum, visible)
		if failure != "" {
			return request, failure
		}
		if !utf8.Valid(value) || bytes.IndexByte(value, 0) >= 0 {
			clear(value)
			return request, "invalid_input"
		}
		if field.Upload {
			report.FileReads++
			upload, failure := readFirstAccountFile(ctx, interrupt, directory, value, min(description.MaxFileBytes, description.MaxFilesBytes-fileBytes))
			clear(value)
			if failure != "" {
				return request, failure
			}
			fileBytes += len(upload.Content)
			report.FileBytes = fileBytes
			request.Values[index].Present, request.Values[index].Upload = true, upload
			continue
		}
		inputBytes += len(value)
		if inputBytes > firstaccount.MaxInputBytes {
			clear(value)
			return request, "invalid_input"
		}
		request.Values[index].Present, request.Values[index].Text = true, value
	}
	report.PasswordPrompts++
	request.Password, code = read("Password: ", description.MaxPasswordBytes, nil)
	if code != "" {
		return request, code
	}
	if len(request.Password) == 0 {
		return request, "invalid_password"
	}
	report.ConfirmationPrompts++
	confirmation, failure := read("Password (again): ", description.MaxPasswordBytes, nil)
	defer clear(confirmation)
	if failure != "" {
		return request, failure
	}
	if !constantTimeBytesEqual(request.Password, confirmation) {
		return request, "password_mismatch"
	}
	if code = commandInterruption(ctx, interrupt); code != "" {
		return request, code
	}
	return request, ""
}

func firstAccountOmission(answer []byte) (omit, valid bool) {
	switch strings.ToLower(strings.TrimSpace(string(answer))) {
	case "", "y", "yes":
		return true, true
	case "n", "no":
		return false, true
	default:
		return false, false
	}
}
