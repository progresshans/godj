package model

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
)

// AccountPermissions is the common admission policy for account management.
// Reads accept View OR Change and all Additional grants. Creation requires
// Add AND Change and all Additional grants. Every other write requires its
// named operation and all Additional grants, including password commands.
type AccountPermissions struct {
	View, Add, Change, Delete auth.Permission
	Additional                []auth.Permission
}

// AccountManagementConfig declares one selected-account management policy.
// Exposure is explicit and independent for each operation. The same password
// policy applies to creation and replacement. Related model effects retain
// their own grants; they cannot inherit authority from the edited account.
// Validators and the clock must be pure; all callbacks must be safe for
// concurrent calls. PrincipalID supplies server identity before the write.
type AccountManagementConfig[M any] struct {
	ReadFields, CreateFields, EditFields []string
	Relations                            []ProfileRelation[M]
	Collections                          []ProfileCollection[M]
	// Files configures the union of CreateFields and EditFields. Each owner
	// receives only its selected policy; Form uploads retain the caller's lifetime.
	Files                                   []ProfileFile[M]
	Permissions                             AccountPermissions
	GroupPermissions, PermissionPermissions DeletionPermissions
	ValidateCreate                          CreationValidator[M]
	ValidateEdit                            AccountEditValidator[M]
	ValidatePassword                        PasswordValidator[M]
	MaxPasswordBytes                        int
	CaseInsensitiveCreationLogin            bool
	PrincipalID                             func(context.Context) (string, error)
	Now                                     func() time.Time
	// These policies apply to EditFields. Creation retains its separate root
	// policy and does not silently reuse an edit's through-creation capability.
	EditThroughCreations     map[string]ThroughCreation
	EditThroughCreationLimit int
}

// AccountManagement composes real lifecycle owners over the exact same
// sessions, credentials, verifier and complete generated host source. It is
// the transport-independent registration capability, not another row store.
type AccountManagement[M any] struct{ state *accountManagementState[M] }
type accountManagementState[M any] struct {
	reader    *ProfileReader[M]
	creator   *Creator[M]
	editor    *AccountEditor[M]
	deleter   *AccountDeleter[M]
	passwords *PasswordEditor[M]
	choices   map[admin.Action][]admin.RelatedChoices
}

func (AccountManagement[M]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.AccountManagement{redacted}"))
}

// NewAccountManagement is normally called by the generated project factory.
// Construction verifies the shared source and every collection/deletion
// capability without performing I/O, hashing, entropy or user callbacks.
func NewAccountManagement[M, G, P, GL, PL any](sessions *SessionStore[M], expected Binding[M], verifier *Authenticator[M], groups orm.RelationObjectDescriptor[G], permissions orm.RelationObjectDescriptor[P], memberships orm.ManyToMany[M, G, GL], grants orm.ManyToMany[M, P, PL], deletion orm.RelationDeleter[M], overlay auth.Authorizer, config AccountManagementConfig[M]) (*AccountManagement[M], error) {
	policy := config.Permissions
	if _, err := auth.RequiredPermissions(policy.View, append([]auth.Permission{policy.Add, policy.Change, policy.Delete}, policy.Additional...)...); err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	passwords, err := NewPasswordEditor(sessions, expected, verifier, overlay, PasswordEditConfig[M]{
		ChangePermission: policy.Change, AdditionalPermissions: policy.Additional,
		MaxPasswordBytes: config.MaxPasswordBytes, ValidatePassword: config.ValidatePassword, Now: config.Now,
	})
	if err != nil {
		return nil, err
	}
	store := sessions.state.credentials
	relations, err := prepareProfileRelations(expected, append(slices.Clone(config.CreateFields), config.EditFields...), config.Relations)
	if err != nil {
		return nil, err
	}
	files, _, err := prepareProfileFiles(expected, append(slices.Clone(config.CreateFields), config.EditFields...), config.Files)
	if err != nil {
		return nil, err
	}
	collections, err := prepareProfileCollections(expected, append(slices.Clone(config.CreateFields), config.EditFields...), config.Collections)
	if err != nil {
		return nil, err
	}
	reader, err := NewProfileReader(store, overlay, ProfileReadConfig{
		Fields: config.ReadFields, ViewPermission: policy.View, ChangePermission: policy.Change, AdditionalPermissions: policy.Additional,
	})
	if err != nil {
		return nil, err
	}
	creator, err := NewCreator(sessions, expected, memberships, grants, overlay, verifier.hasher, CreationConfig[M]{
		CreationPolicy: CreationPolicy[M]{RelatedPermissions: RelatedRowPermissions{Account: DeletionPermissions{Delete: policy.Delete, Change: policy.Change, Additional: policy.Additional}, Group: config.GroupPermissions, Permission: config.PermissionPermissions}, Fields: config.CreateFields, Relations: selectProfileRelations(relations, config.CreateFields), Collections: selectProfileCollections(collections, config.CreateFields), Files: selectProfileFiles(files, config.CreateFields), Validate: config.ValidateCreate, ValidatePassword: config.ValidatePassword,
			MaxPasswordBytes: config.MaxPasswordBytes, CaseInsensitiveLogin: config.CaseInsensitiveCreationLogin, PrincipalID: config.PrincipalID, Now: config.Now},
		AddPermission: policy.Add, ChangePermission: policy.Change, AdditionalPermissions: policy.Additional,
	})
	if err != nil {
		return nil, err
	}
	account := DeletionPermissions{Delete: policy.Delete, Change: policy.Change, Additional: policy.Additional}
	editor, err := NewAccountEditor(sessions, expected, memberships, grants, overlay, AccountEditConfig[M]{
		ThroughCreations: config.EditThroughCreations, ThroughCreationLimit: config.EditThroughCreationLimit,
		Fields: config.EditFields, Relations: selectProfileRelations(relations, config.EditFields), Collections: selectProfileCollections(collections, config.EditFields), Files: selectProfileFiles(files, config.EditFields), ChangePermission: policy.Change, AdditionalPermissions: policy.Additional,
		RelatedPermissions: RelatedRowPermissions{Account: account, Group: config.GroupPermissions, Permission: config.PermissionPermissions},
		Validate:           config.ValidateEdit, Now: config.Now,
	})
	if err != nil {
		return nil, err
	}
	deleter, err := NewAccountDeleter(sessions, expected, groups, permissions, deletion, overlay, AccountDeleteConfig{
		Account: account, Group: config.GroupPermissions, Permission: config.PermissionPermissions, Now: config.Now,
	})
	if err != nil {
		return nil, err
	}
	management := &AccountManagement[M]{state: &accountManagementState[M]{reader: reader, creator: creator, editor: editor, deleter: deleter, passwords: passwords,
		choices: make(map[admin.Action][]admin.RelatedChoices, 2)}}
	if err := bindAccountChoices(management, groups, permissions); err != nil {
		return nil, err
	}
	return management, nil
}

func (management *AccountManagement[M]) Reader() *ProfileReader[M] {
	if management == nil || management.state == nil {
		return nil
	}
	return management.state.reader
}
func (management *AccountManagement[M]) Creator() *Creator[M] {
	if management == nil || management.state == nil {
		return nil
	}
	return management.state.creator
}
func (management *AccountManagement[M]) Editor() *AccountEditor[M] {
	if management == nil || management.state == nil {
		return nil
	}
	return management.state.editor
}
func (management *AccountManagement[M]) Deleter() *AccountDeleter[M] {
	if management == nil || management.state == nil {
		return nil
	}
	return management.state.deleter
}
func (management *AccountManagement[M]) Passwords() *PasswordEditor[M] {
	if management == nil || management.state == nil {
		return nil
	}
	return management.state.passwords
}

// AccountDescription is detached declaration metadata, never read authority.
type AccountDescription struct {
	Identity                                                                          ir.ModelIdentity
	KeyField, LoginField, PasswordField, RevisionField, GroupsField, PermissionsField string
	Permissions                                                                       AccountPermissions
}

func (management *AccountManagement[M]) Description() (AccountDescription, error) {
	if management == nil || management.state == nil {
		return AccountDescription{}, managementError(CodeInvalidConfig, nil)
	}
	state := management.state
	role := state.reader.state.store.state.binding.state.role
	metadata := state.creator.state.metadata
	key, _ := primaryField(metadata)
	return AccountDescription{Identity: role.Model, KeyField: key.Name, LoginField: role.Login, PasswordField: role.Password,
		RevisionField: role.Revision, GroupsField: role.Groups, PermissionsField: role.Permissions,
		Permissions: AccountPermissions{View: state.reader.state.view[0], Add: state.creator.state.required[0], Change: state.editor.state.required[0],
			Delete: state.deleter.state.required[0], Additional: slices.Clone(state.reader.state.view[1:])}}, nil
}

func (management *AccountManagement[M]) Manager() orm.Manager[M] {
	if management == nil || management.state == nil {
		return orm.Manager[M]{}
	}
	return management.state.creator.Manager()
}

// ChoiceSources supplies bounded, operation-specific assignments. The result
// and its immutable source metadata are not authority for a later write.
func (management *AccountManagement[M]) ChoiceSources(action admin.Action) ([]admin.RelatedChoices, error) {
	if management == nil || management.state == nil {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	if action != admin.ActionAdd && action != admin.ActionChange {
		return nil, managementError(CodeInvalidInput, nil)
	}
	return slices.Clone(management.state.choices[action]), nil
}
