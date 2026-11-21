package site

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/identity"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/mail"
)

func projectPasswordReset(context.Context) (*passwordResetSettings, error) {
	key, err := os.ReadFile(os.Getenv("STARTER_RESET_KEY_FILE"))
	if err != nil {
		return nil, err
	}
	defer clear(key)
	keys, err := auth.NewPasswordResetKeyRing(key)
	if err != nil {
		return nil, err
	}
	from, err := mail.ParseAddress("sender@example.test")
	if err != nil {
		return nil, err
	}
	return &passwordResetSettings{
		Tokens:      accountmodel.RuntimeResetConfig{Keys: keys},
		Sender:      recipientMailbox(os.Getenv("STARTER_MAILBOX")),
		Mail:        identity.PasswordResetMailConfig{From: from, Origin: os.Getenv("STARTER_RESET_ORIGIN"), ConfirmPath: "/account/reset"},
		ReportError: func(context.Context, error) { fmt.Fprintln(os.Stderr, "private reset delivery failed") },
	}, nil
}

// The browser reads the recipient's private copy. A second delivery is an
// error in this scenario. The mailbox is outside the project and CI artifacts.
type recipientMailbox string

func (directory recipientMailbox) Send(ctx context.Context, message mail.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	recipients := message.Recipients()
	if len(recipients) != 1 {
		return errors.New("unexpected recipient count")
	}
	file, err := os.OpenFile(filepath.Join(string(directory), "message.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	err = json.NewEncoder(file).Encode(map[string]string{"from": message.From().Mailbox(), "to": recipients[0].Mailbox(), "text": message.Text()})
	return errors.Join(err, file.Close())
}
