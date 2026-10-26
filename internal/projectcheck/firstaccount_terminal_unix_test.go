//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/creack/pty"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/codegen"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/modeldef"
	"github.com/progresshans/godj/identity/models"
	identityproject "github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/internal/firstaccountwire"
	"github.com/progresshans/godj/project/firstaccount"
	"golang.org/x/term"
)

func firstAccountParentDescription(t *testing.T) (firstaccount.Description, codegen.ProjectSpec) {
	t.Helper()
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	definition, err := identityproject.NewFirstAccountDefinition(hasher, accountmodel.FirstAccountConfig[models.User]{
		CreationPolicy: accountmodel.CreationPolicy[models.User]{Fields: []string{"username", "first_name", "email"}, MaxPasswordBytes: 64},
		EvidenceSHA256: strings.Repeat("d", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	command, err := firstaccount.New(definition, func(context.Context) (firstaccount.Backend, error) {
		t.Error("description acquired a database")
		return nil, errors.New("private opener")
	})
	if err != nil {
		t.Fatal(err)
	}
	description, err := command.Description()
	if err != nil {
		t.Fatal(err)
	}
	spec, err := modeldef.ProjectSpec(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return description, spec
}

type firstAccountTerminalResult struct {
	request firstaccountwire.Request
	code    string
	report  FirstAccountTerminalReport
}

func firstAccountFieldPrompt(field firstaccount.Field) string {
	return strconv.Quote(field.Label) + ": "
}
func firstAccountOmissionPrompt(field firstaccount.Field) string {
	if field.HasDefault {
		return "Use model default for " + strconv.Quote(field.Label) + "? [Y/n]: "
	}
	return "Omit " + strconv.Quote(field.Label) + "? [Y/n]: "
}

func TestFirstAccountTerminalPreservesRawFieldsOmissionEmptyAndPassword(t *testing.T) {
	description, _ := firstAccountParentDescription(t)
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	before, err := term.GetState(int(slave.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	prompts := newObservedPromptWriter()
	done := make(chan firstAccountTerminalResult, 1)
	go func() {
		var result firstAccountTerminalResult
		result.request, result.code = readFirstAccountTerminal(ctx, nil, slave, prompts, description, &result.report)
		done <- result
	}()
	login := "  ｐｒｏｆｉｌｅ＠ｅｘａｍｐｌｅ．ｔｅｓｔ  "
	waitForPrompt(t, prompts, firstAccountFieldPrompt(description.Fields[0]))
	writePTYInput(t, master, login+"\n")
	waitForPrompt(t, prompts, firstAccountOmissionPrompt(description.Fields[1]))
	writePTYInput(t, master, "\n")
	waitForPrompt(t, prompts, firstAccountOmissionPrompt(description.Fields[2]))
	writePTYInput(t, master, "n\n")
	waitForPrompt(t, prompts, firstAccountFieldPrompt(description.Fields[2]))
	writePTYInput(t, master, "\n")
	waitForPrompt(t, prompts, "Password: ")
	password := "  private password 한  "
	writePTYInput(t, master, password+"\n")
	waitForPrompt(t, prompts, "Password (again): ")
	writePTYInput(t, master, password+"\n")
	result := awaitTerminalResult(t, done)
	defer result.request.Clear()
	values := result.request.Values
	if result.code != "" || len(values) != 3 || !values[0].Present || string(values[0].Text) != login || values[1].Present ||
		!values[2].Present || len(values[2].Text) != 0 || string(result.request.Password) != password || result.request.Fingerprint != description.Fingerprint {
		t.Fatal("terminal changed model input presence or raw bytes", result.code)
	}
	if result.report != (FirstAccountTerminalReport{Checks: 1, FieldPrompts: 2, OmissionPrompts: 2, PasswordPrompts: 1, ConfirmationPrompts: 1, RestoreAttempts: 1}) {
		t.Fatal("terminal lifecycle count differs", result.report)
	}
	after, err := term.GetState(int(slave.Fd()))
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("terminal state was not restored", err)
	}
	for _, output := range []string{prompts.String(), string(readAvailablePTY(t, master)), fmt.Sprint(result.request)} {
		if strings.Contains(output, login) || strings.Contains(output, password) {
			t.Fatal("redirected prompt or diagnostics retained private input")
		}
	}
}

func TestFirstAccountTerminalFailuresClearAllInputAndRestoreTTY(t *testing.T) {
	for _, name := range []string{"field_bytes", "field_nul", "field_utf8", "omission_answer", "empty_password", "password_bytes", "confirmation", "cancel", "restore"} {
		t.Run(name, func(t *testing.T) {
			description, _ := firstAccountParentDescription(t)
			master, slave, err := pty.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer master.Close()
			defer slave.Close()
			before, err := term.GetState(int(slave.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			prompts := newObservedPromptWriter()
			done := make(chan firstAccountTerminalResult, 1)
			go func() {
				var result firstAccountTerminalResult
				result.request, result.code = readFirstAccountTerminal(ctx, nil, slave, prompts, description, &result.report)
				done <- result
			}()
			want := "invalid_input"
			waitForPrompt(t, prompts, firstAccountFieldPrompt(description.Fields[0]))
			login := "private-profile"
			switch name {
			case "field_bytes":
				login = strings.Repeat("x", description.Fields[0].MaxBytes+1)
			case "field_nul":
				login += "\x00"
			case "field_utf8":
				login += "\xff"
			}
			writePTYInput(t, master, login+"\n")
			if !strings.HasPrefix(name, "field_") {
				waitForPrompt(t, prompts, firstAccountOmissionPrompt(description.Fields[1]))
				if name == "omission_answer" {
					writePTYInput(t, master, "?\n")
				} else {
					writePTYInput(t, master, "y\n")
					waitForPrompt(t, prompts, firstAccountOmissionPrompt(description.Fields[2]))
					writePTYInput(t, master, "yes\n")
					waitForPrompt(t, prompts, "Password: ")
					switch name {
					case "empty_password":
						writePTYInput(t, master, "\n")
						want = "invalid_password"
					case "password_bytes":
						writePTYInput(t, master, strings.Repeat("p", description.MaxPasswordBytes+1)+"\n")
					case "confirmation":
						writePTYInput(t, master, "private-password\n")
						waitForPrompt(t, prompts, "Password (again): ")
						writePTYInput(t, master, "different\n")
						want = "password_mismatch"
					case "cancel":
						cancel()
						want = "project_canceled"
					case "restore":
						_ = slave.Close()
						want = "terminal_state_failed"
					}
				}
			}
			result := awaitTerminalResult(t, done)
			if result.code != want || len(result.request.Values) != 0 || len(result.request.Password) != 0 || result.request.Fingerprint != "" || result.report.RestoreAttempts != 1 {
				t.Fatal("failure retained inputs or changed its meaning", result.code, result.report)
			}
			if name != "restore" {
				after, err := term.GetState(int(slave.Fd()))
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatal("failed input left altered terminal state", err)
				}
			}
		})
	}
}

func TestFirstAccountTerminalRejectsInvalidDeclarationBeforeTTY(t *testing.T) {
	description, _ := firstAccountParentDescription(t)
	var report FirstAccountTerminalReport
	invalid := description
	invalid.Fingerprint = strings.Repeat("f", 64)
	if request, code := readFirstAccountTerminal(t.Context(), nil, nil, nil, invalid, &report); code != "invalid_config" || report.Checks != 0 || len(request.Values) != 0 {
		t.Fatal("invalid declaration reached terminal input")
	}
	if _, code := readFirstAccountTerminal(t.Context(), nil, nil, nil, description, &report); code != "input_not_terminal" || report.Checks != 1 || report.RestoreAttempts != 0 {
		t.Fatal("nonterminal input was accepted")
	}
}

func TestPrivateTerminalBackspaceClearsRemovedStorage(t *testing.T) {
	for _, value := range [][]byte{[]byte("private한"), []byte("private\xff"), []byte("x")} {
		storage := bytes.Clone(value)
		remaining := removeLastCreatesuperuserInputRune(storage)
		if !bytes.Equal(storage[len(remaining):], make([]byte, len(storage)-len(remaining))) {
			t.Fatal("erased input survives outside the shortened slice")
		}
	}
}
