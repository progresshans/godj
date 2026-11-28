package firstaccount_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/project/firstaccount"
	"github.com/progresshans/godj/validation"
)

type probeBackend struct{ reads, writes, closes atomic.Int64 }

var _ db.CoordinatedRelationAtomic = (*probeBackend)(nil)

var privateFault = errors.New("private backend or profile detail")

func (probe *probeBackend) ReadSnapshot(context.Context, func(db.Queryer) error) error {
	probe.reads.Add(1)
	return privateFault
}
func (probe *probeBackend) CoordinatedAtomicRelation(context.Context, func(db.RelationSession) error) error {
	probe.writes.Add(1)
	return privateFault
}
func (probe *probeBackend) Close() error { probe.closes.Add(1); return nil }

func commandFixture(t *testing.T, backend *probeBackend, opens *atomic.Int64) firstaccount.Command {
	t.Helper()
	return commandWithOpener(t, func(context.Context) (firstaccount.Backend, error) { opens.Add(1); return backend, nil })
}

func TestCommandNormalizesBeforeModelChecksAndPreservesRawByteEnvelope(t *testing.T) {
	backend, opens := &probeBackend{}, &atomic.Int64{}
	command := commandFixture(t, backend, opens)
	description, err := command.Description()
	if err != nil || description.Fields[0].MaxBytes != auth.MaximumUsernameBytes {
		t.Fatal("login byte envelope not in command fingerprint", err)
	}
	for _, test := range []struct {
		name, raw string
		valid     bool
	}{
		{"contracting", strings.Repeat("e\u0301", 200), true},
		{"fullwidth", "ｆｉｒｓｔ＠ｅｘａｍｐｌｅ．ｔｅｓｔ", true},
		{"expanding", strings.Repeat("ﬃ", 86), false},
		{"raw_bytes", strings.Repeat(" ", auth.MaximumUsernameBytes) + "Fred", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			values := inputValues(description)
			values[0].Text = []byte(test.raw)
			prepared, err := command.Prepare(t.Context(), description.Fingerprint, values)
			if (err == nil) != test.valid {
				t.Fatal("command normalization/byte boundary", err)
			}
			prepared.Clear()
		})
	}
	if opens.Load() != 0 || backend.reads.Load() != 0 || backend.writes.Load() != 0 || backend.closes.Load() != 0 {
		t.Fatal("input policy acquired storage")
	}
}

func commandWithOpener(t *testing.T, open func(context.Context) (firstaccount.Backend, error)) firstaccount.Command {
	return commandWithFields(t, open, []string{"first_name", "username"})
}

func commandWithFields(t *testing.T, open func(context.Context) (firstaccount.Backend, error), fields []string) firstaccount.Command {
	t.Helper()
	binding, err := project.BindAccount()
	if err != nil {
		t.Fatal(err)
	}
	collections, err := project.BindCollections()
	if err != nil {
		t.Fatal(err)
	}
	hasher, err := auth.NewDefaultPBKDF2()
	if err != nil {
		t.Fatal(err)
	}
	definition, err := accountmodel.NewFirstAccountDefinition(binding, models.GroupDescriptor{}, models.PermissionDescriptor{}, collections.IdentityUserGroups, collections.IdentityUserPermissions, hasher, accountmodel.FirstAccountConfig[models.User]{CreationPolicy: accountmodel.CreationPolicy[models.User]{Fields: fields}, EvidenceSHA256: strings.Repeat("d", 64)})
	if err != nil {
		t.Fatal(err)
	}
	command, err := firstaccount.New(definition, open, firstaccount.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return command
}

func TestCommandCollectionKeysKeepTypedBoundsBeforeAcquisition(t *testing.T) {
	backend, opens := &probeBackend{}, &atomic.Int64{}
	command := commandWithFields(t, func(context.Context) (firstaccount.Backend, error) {
		opens.Add(1)
		return backend, nil
	}, []string{"username", "groups", "permissions"})
	description, err := command.Description()
	if err != nil || len(description.Fields) != 3 || description.Fields[1].Kind != firstaccount.CollectionKind || description.Fields[1].MaxItems != accountmodel.MaximumAccountGroups || description.Fields[2].MaxItems != auth.MaximumPermissions {
		t.Fatal("selected collection declaration lost its actual limits", err)
	}
	values := func() []firstaccount.Value {
		return []firstaccount.Value{
			{Name: "username", Present: true, Text: []byte("operator")},
			{Name: "groups", Present: true, Keys: []int64{math.MinInt64, 0, math.MaxInt64, 0}},
			{Name: "permissions", Present: true, Keys: []int64{}},
		}
	}
	for _, omitted := range []bool{false, true} {
		input := values()
		if omitted {
			input[2] = firstaccount.Value{Name: "permissions"}
		}
		prepared, err := command.Prepare(t.Context(), description.Fingerprint, input)
		if err != nil {
			t.Fatal("syntactically valid keys required premature choice I/O", err)
		}
		prepared.Clear()
		for i := range input {
			input[i].Clear()
		}
	}
	for _, change := range []func([]firstaccount.Value){
		func(v []firstaccount.Value) { v[1].Keys = make([]int64, accountmodel.MaximumAccountGroups+1) },
		func(v []firstaccount.Value) { v[1].Text = []byte("0") },
		func(v []firstaccount.Value) { v[1].Keys = nil },
		func(v []firstaccount.Value) { v[1].Present = false },
		func(v []firstaccount.Value) {
			v[1].Upload = &firstaccount.Upload{Name: "x", Content: []byte("private")}
		},
		func(v []firstaccount.Value) { v[0].Keys = []int64{} },
		func(v []firstaccount.Value) { v[1], v[2] = v[2], v[1] },
	} {
		input := values()
		change(input)
		if _, err := command.Prepare(t.Context(), description.Fingerprint, input); err == nil {
			t.Fatal("invalid collection kind, order or raw repetition bound accepted")
		}
		for i := range input {
			input[i].Clear()
		}
	}
	if opens.Load() != 0 || backend.reads.Load() != 0 || backend.writes.Load() != 0 || backend.closes.Load() != 0 {
		t.Fatal("collection description/preparation acquired storage")
	}
}

type readOnlyBackend struct{ closes int }

func (*readOnlyBackend) ReadSnapshot(context.Context, func(db.Queryer) error) error {
	return privateFault
}
func (backend *readOnlyBackend) Close() error { backend.closes++; return nil }

func TestCommandOpenPreservesAcquiredResourceOwnership(t *testing.T) {
	backend := &probeBackend{}
	readOnly := &readOnlyBackend{}
	var typedNil *probeBackend
	for _, test := range []struct {
		name        string
		resource    firstaccount.Backend
		err         error
		wantInvalid bool
	}{
		{"native", backend, nil, false},
		{"resource_with_error", backend, privateFault, false},
		{"missing_write_capability", readOnly, nil, true},
		{"nil", nil, nil, true},
		{"typed_nil", typedNil, nil, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			opens := 0
			command := commandWithOpener(t, func(context.Context) (firstaccount.Backend, error) { opens++; return test.resource, test.err })
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := command.Open(ctx); !errors.Is(err, context.Canceled) || opens != 0 {
				t.Fatal("canceled invocation acquired resource", err)
			}
			opened, err := command.Open(t.Context())
			if opened != test.resource || opens != 1 || test.err != nil && !errors.Is(err, test.err) || test.wantInvalid && !errors.Is(err, &firstaccount.Error{Code: firstaccount.InvalidConfig}) || !test.wantInvalid && test.err == nil && err != nil {
				t.Fatal("resource/error ownership changed", err)
			}
			if test.err != nil {
				if !errors.Is(err, &firstaccount.Error{Code: firstaccount.OpenFailed}) || strings.Contains(fmt.Sprintf("%+v", err), privateFault.Error()) {
					t.Fatal("backend acquisition lost its closed, redacted classification", err)
				}
			}
		})
	}
	if backend.closes.Load() != 0 || readOnly.closes != 0 {
		t.Fatal("command closed the process owner's acquired resource")
	}
}
func inputValues(description firstaccount.Description) []firstaccount.Value {
	values := make([]firstaccount.Value, len(description.Fields))
	for i, field := range description.Fields {
		values[i] = firstaccount.Value{Name: field.Name, Present: true, Text: []byte("profile-private")}
		if field.Name == description.Login {
			values[i].Text = []byte("first-command")
		}
	}
	return values
}

func TestCommandDescriptionAndModelInputStayBeforeAcquisition(t *testing.T) {
	backend, opens := &probeBackend{}, &atomic.Int64{}
	command := commandFixture(t, backend, opens)
	description, err := command.Description()
	if err != nil || firstaccount.ValidateDescription(description) != nil || description.Fields[0].Name != "username" {
		t.Fatal("invalid pure prompt description", err)
	}
	for _, mutate := range []func(*firstaccount.Description){
		func(d *firstaccount.Description) { d.Fields[0].Name = "encoded_password" },
		func(d *firstaccount.Description) { d.Fields[0].Label = "bad\x1b[2J" },
		func(d *firstaccount.Description) { d.Fields[0].Kind = "foreign_key" },
		func(d *firstaccount.Description) { d.Fields[0].MaxBytes = 4097 },
		func(d *firstaccount.Description) { d.RoleSHA256 = strings.Repeat("f", 64) },
	} {
		copy := description
		copy.Fields = slices.Clone(description.Fields)
		mutate(&copy)
		if firstaccount.ValidateDescription(copy) == nil {
			t.Fatal("changed prompt source retained its identity")
		}
	}
	values := inputValues(description)
	for _, mutate := range []func([]firstaccount.Value){
		func(v []firstaccount.Value) { v[0].Name = "encoded_password" },
		func(v []firstaccount.Value) { v[0].Text = []byte{0} },
		func(v []firstaccount.Value) { v[0].Text = []byte{0xff} },
		func(v []firstaccount.Value) { v[0].Text = []byte(strings.Repeat("a", firstaccount.MaxFieldBytes+1)) },
		func(v []firstaccount.Value) { v[0].Present = false },
	} {
		copy := slices.Clone(values)
		mutate(copy)
		if _, err := command.Prepare(t.Context(), description.Fingerprint, copy); err == nil {
			t.Fatal("invalid bounded input reached preparation")
		}
	}
	if _, err := command.Prepare(t.Context(), strings.Repeat("a", 64), values); err == nil {
		t.Fatal("foreign prompt source accepted")
	}
	missing := inputValues(description)
	missing[0].Present, missing[0].Text = false, nil
	if _, err := command.Prepare(t.Context(), description.Fingerprint, missing); err == nil {
		t.Fatal("required typed login disappeared")
	} else if diagnostics, rejected := validation.Rejected(err); !rejected || diagnostics.ByField("username").Empty() {
		t.Fatal("model form required diagnostic lost", err)
	}
	prepared, err := command.Prepare(t.Context(), description.Fingerprint, values)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{values[1], prepared, command} {
		for _, text := range []string{fmt.Sprintf("%v", value), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value)} {
			if strings.Contains(text, "profile-private") {
				t.Fatal("prepared command diagnostics exposed profile")
			}
		}
	}
	encoded, err := json.Marshal(prepared)
	if err != nil || strings.Contains(string(encoded), "profile-private") {
		t.Fatal("prepared command JSON exposed input", err)
	}
	prepared.Clear()
	if _, err := command.Provision(t.Context(), backend, strings.Repeat("a", 64), prepared, "password"); !errors.Is(err, &firstaccount.Error{Code: firstaccount.Consumed}) {
		t.Fatal("abandoned input was reused", err)
	}
	if opens.Load() != 0 || backend.reads.Load() != 0 || backend.writes.Load() != 0 || backend.closes.Load() != 0 {
		t.Fatal("description or input cleaning acquired a backend")
	}
}

func TestCommandPreparationHasOneOwnerAndOneAttempt(t *testing.T) {
	backend, opens := &probeBackend{}, &atomic.Int64{}
	command := commandFixture(t, backend, opens)
	other := commandFixture(t, backend, opens)
	description, _ := command.Description()
	prepared, err := command.Prepare(t.Context(), description.Fingerprint, inputValues(description))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := command.Provision(ctx, backend, strings.Repeat("a", 64), prepared, "password"); !errors.Is(err, context.Canceled) {
		t.Fatal("pre-entry cancellation lost", err)
	}
	if _, err := other.Provision(t.Context(), backend, strings.Repeat("a", 64), prepared, "password"); !errors.Is(err, &firstaccount.Error{Code: firstaccount.InvalidInput}) {
		t.Fatal("another owner claimed input", err)
	}
	start, done := make(chan struct{}), make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := command.Provision(t.Context(), backend, strings.Repeat("a", 64), prepared, "password")
			done <- err
		}()
	}
	close(start)
	failures, consumed := 0, 0
	for range 2 {
		err := <-done
		if errors.Is(err, privateFault) {
			failures++
		} else if errors.Is(err, &firstaccount.Error{Code: firstaccount.Consumed}) {
			consumed++
		} else {
			t.Fatal("unexpected command attempt outcome", err)
		}
		if strings.Contains(fmt.Sprintf("%+v", err), privateFault.Error()) {
			t.Fatal("private failure leaked")
		}
	}
	if failures != 1 || consumed != 1 || opens.Load() != 0 || backend.reads.Load() != 1 || backend.writes.Load() != 0 || backend.closes.Load() != 0 {
		t.Fatal("command input retried or acquired/closed another owner's resource")
	}
}
