package auth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

type unusableEntropyFunc func([]byte) (int, error)

func (read unusableEntropyFunc) Read(value []byte) (int, error) { return read(value) }

func TestPasswordUsabilityIsRepresentationStateNotValidation(t *testing.T) {
	for _, test := range []struct {
		encoded string
		want    bool
	}{
		{"", false}, {"!", false}, {"!legacy-marker", false},
		{"pbkdf2_sha256$10000$salt$encoding", true}, {"unsupported-or-corrupt", true},
	} {
		if got := IsPasswordUsable(test.encoded); got != test.want {
			t.Fatal("password representation state mismatch")
		}
	}
}

func TestUnusablePasswordGenerationPreservesContextAndPrivateEntropyFailures(t *testing.T) {
	secret := errors.New("private entropy source detail")
	for _, mode := range []string{"nil_context", "canceled", "partial", "reader_error", "late_cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			var source io.Reader = unusableEntropyFunc(func(value []byte) (int, error) {
				calls++
				if mode == "reader_error" {
					return 0, secret
				}
				if mode == "late_cancel" {
					cancel()
				}
				return len(value), nil
			})
			switch mode {
			case "nil_context":
				ctx = nil
			case "canceled":
				cancel()
			case "partial":
				source = bytes.NewReader(make([]byte, 31))
			}
			encoded, err := makeUnusablePassword(ctx, source)
			if err == nil || encoded != "" {
				t.Fatal("failed generation published a marker")
			}
			if (mode == "nil_context" || mode == "canceled") && calls != 0 {
				t.Fatal("invalid context consumed entropy")
			}
			if (mode == "canceled" || mode == "late_cancel") && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation lost")
			}
			if mode == "partial" && !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatal("partial entropy accepted")
			}
			if mode == "reader_error" && !errors.Is(err, secret) {
				t.Fatal("private entropy cause lost")
			}
			if strings.Contains(fmt.Sprintf("%v %+v %#v", err, err, err), secret.Error()) {
				t.Fatal("private source leaked")
			}
		})
	}
}
