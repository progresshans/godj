package accountstoretest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/mail"
)

type resetAccountReadFault struct {
	Backend
	active bool
	reads  int
	fault  error
}

func (owner *resetAccountReadFault) ReadSnapshot(ctx context.Context, read func(db.Queryer) error) error {
	if owner.active {
		owner.reads++
		return owner.fault
	}
	return owner.Backend.ReadSnapshot(ctx, read)
}

func accountResetAcknowledgement(t *testing.T, open Open) {
	for _, surface := range []string{"form", "json"} {
		t.Run(surface, func(t *testing.T) {
			for _, mode := range []string{"known", "unknown", "inactive", "unusable", "delivery_error", "delivery_unknown", "delivery_unknown_accepted", "delivery_panic", "delivery_cancel", "snapshot_error", "reporter_panic"} {
				t.Run(mode, func(t *testing.T) {
					backend, reopen := open(t)
					f := provisionSelfPassword(t, backend)
					if mode == "inactive" {
						if _, err := models.UserObjects.Patch(t.Context(), backend, f.target, models.UserPatch{}.WithActive(false)); err != nil {
							t.Fatal(err)
						}
					}
					if mode == "unusable" {
						encoded, err := auth.MakeUnusablePassword(t.Context())
						if err != nil {
							t.Fatal(err)
						}
						if _, err := models.UserObjects.Patch(t.Context(), backend, f.target, models.UserPatch{}.WithEncodedPassword(encoded)); err != nil {
							t.Fatal(err)
						}
					}
					private := errors.New("private recipient and token delivery failure")
					memory := selectedResetMemory(t)
					sends := 0
					var sentTokens []string
					sender := resetNativeSender(func(ctx context.Context, message mail.Message) error {
						sends++
						principal, token := selectedResetDelivery(t, message)
						if principal != f.target.PrincipalID {
							t.Fatal("acknowledgement sent another account's token")
						}
						sentTokens = append(sentTokens, token)
						switch mode {
						case "delivery_error", "reporter_panic":
							return private
						case "delivery_unknown":
							return errors.Join(&mail.Error{Code: mail.CodeOutcomeUnknown}, private)
						case "delivery_unknown_accepted":
							if err := memory.Send(ctx, message); err != nil {
								return err
							}
							return errors.Join(&mail.Error{Code: mail.CodeOutcomeUnknown}, private)
						case "delivery_panic":
							panic(private)
						case "delivery_cancel":
							return context.Canceled
						}
						return memory.Send(ctx, message)
					})
					fault := &resetAccountReadFault{Backend: backend, fault: private}
					h := newResetAccountHTTP(t, f, fault, sender, nil)
					fault.active = mode == "snapshot_error"
					if mode == "reporter_panic" {
						h.reportHook = func(error) { panic("private reporter panic") }
					}
					before := userReferenceSnapshot(t, reopen(t))
					entropy := f.entropy.count()
					f.owner.boundary.calls = 0
					email := f.target.Email
					if mode == "unknown" {
						email = "unknown@example.test"
					}
					response, body := h.requestMail(t, h.client, surface, email)
					resetHTTPHeaders(t, response)
					want, expected, location := 302, "Found\n", "/account/reset/sent/"
					if surface == "json" {
						want, expected, location = 204, "", ""
					}
					if response.StatusCode != want || body != expected || response.Header.Get("Location") != location || len(response.Cookies()) != 0 {
						t.Fatal("public reset acknowledgement disclosed account/delivery state", mode, response.StatusCode)
					}
					wantSends := 1
					if mode == "unknown" || mode == "inactive" || mode == "unusable" || mode == "snapshot_error" {
						wantSends = 0
					}
					if sends != wantSends {
						t.Fatal("reset request retried a recipient or lost eligibility", sends, wantSends)
					}
					messages, err := memory.Snapshot()
					if err != nil {
						t.Fatal(err)
					}
					accepted := 0
					if mode == "known" || mode == "delivery_unknown_accepted" {
						accepted = 1
					}
					if len(messages) != accepted {
						t.Fatal("reset acknowledgement confused receiver acceptance and reported outcome")
					}
					wantReports := 0
					if strings.HasPrefix(mode, "delivery_") || mode == "snapshot_error" || mode == "reporter_panic" {
						wantReports = 1
					}
					if len(h.reports) != wantReports {
						t.Fatal("reset private failure was not reported exactly once")
					}
					if wantReports == 1 {
						reported := h.reports[0]
						if mode == "delivery_cancel" {
							if !errors.Is(reported, context.Canceled) {
								t.Fatal("reported provider cancellation lost its cause")
							}
						} else if mode != "delivery_panic" && !errors.Is(reported, private) {
							t.Fatal("reported reset failure lost its private cause")
						}
						if strings.HasPrefix(mode, "delivery_unknown") && !errors.Is(reported, &mail.Error{Code: mail.CodeOutcomeUnknown}) {
							t.Fatal("reported delivery uncertainty lost its classification")
						}
						material := append([]string{private.Error(), f.target.EncodedPassword, email}, sentTokens...)
						selfPasswordPrivate(t, reported, material...)
					}
					if mode == "snapshot_error" && fault.reads != 1 {
						t.Fatal("public acknowledgement retried a failed snapshot")
					}
					if f.owner.hasher.hashes.Load() != 0 || f.owner.hasher.verifies.Load() != 0 || f.owner.policies.Load() != 0 || f.owner.boundary.calls != 0 || f.entropy.count() != entropy {
						t.Fatal("mail acknowledgement changed password/session work")
					}
					assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
				})
			}
		})
	}
}
