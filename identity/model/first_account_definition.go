package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
)

// FirstAccountDefinition is an immutable, database-free declaration of the
// selected account's initial provisioning policy. Commands can derive typed
// form fields before acquiring a database or asking for a password. Bind
// supplies a real backend later; no declaration is a live account owner.
type FirstAccountDefinition[M any] struct {
	state *firstAccountDefinitionState[M]
}
type firstAccountDefinitionState[M any] struct {
	creation              *creationDefinition[M]
	credentials           credentialStoreState[M]
	fingerprint, evidence string
	declaration           string
}

func (FirstAccountDefinition[M]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.FirstAccountDefinition{redacted}"))
}

// NewFirstAccountDefinition is normally called by the generated factory. All
// descriptors, collection capabilities, selected fields, required profile and
// fixed first-account roles are validated here, without a placeholder backend.
func NewFirstAccountDefinition[M, G, P, GL, PL any](binding Binding[M], groupDescriptor orm.RelationObjectDescriptor[G], permissionDescriptor orm.RelationObjectDescriptor[P], groups orm.ManyToMany[M, G, GL], permissions orm.ManyToMany[M, P, PL], hasher auth.PasswordHasher, config FirstAccountConfig[M]) (FirstAccountDefinition[M], error) {
	var zero FirstAccountDefinition[M]
	forced, err := firstAccountForced(binding, config.EvidenceSHA256)
	if err != nil {
		return zero, err
	}
	credentials, err := prepareCredentialStore(binding, groupDescriptor, permissionDescriptor)
	if err != nil {
		return zero, managementError(CodeInvalidConfig, err)
	}
	creation, err := prepareCreationDefinition(binding, groups, permissions, hasher, config.CreationPolicy, forced)
	if err != nil {
		return zero, err
	}
	fingerprint, err := binding.RoleFingerprint()
	if err != nil {
		return zero, managementError(CodeInvalidConfig, err)
	}
	source, err := binding.state.project.SourceSHA256()
	if err != nil {
		return zero, managementError(CodeInvalidConfig, err)
	}
	encoded, err := json.Marshal(struct {
		Source, Role, Evidence string
		Fields                 []string
		PasswordBytes          int
		IExact                 bool
		Related                RelatedRowPermissions
	}{source, fingerprint, config.EvidenceSHA256, creation.fields, creation.config.MaxPasswordBytes, creation.config.CaseInsensitiveLogin, creation.config.RelatedPermissions})
	if err != nil {
		return zero, managementError(CodeInvalidConfig, err)
	}
	digest := sha256.Sum256(append([]byte("godj.first-account-definition.v2\x00"), encoded...))
	return FirstAccountDefinition[M]{state: &firstAccountDefinitionState[M]{creation: creation, credentials: credentials, fingerprint: fingerprint, evidence: config.EvidenceSHA256, declaration: hex.EncodeToString(digest[:])}}, nil
}

// Fingerprint binds serializable policy and the complete host source. The
// host's EvidenceSHA256 must identify its callback policy; arbitrary Go closure
// behavior cannot be inferred from a function address or hashed by this API.
func (definition FirstAccountDefinition[M]) Fingerprint() string {
	if definition.state == nil {
		return ""
	}
	return definition.state.declaration
}

func (definition FirstAccountDefinition[M]) Binding() (Binding[M], error) {
	if definition.state == nil {
		return Binding[M]{}, managementError(CodeInvalidConfig, nil)
	}
	return definition.state.creation.binding, nil
}
func (definition FirstAccountDefinition[M]) Fields() []string {
	if definition.state == nil {
		return nil
	}
	return slices.Clone(definition.state.creation.fields)
}
func (definition FirstAccountDefinition[M]) Manager() orm.Manager[M] {
	if definition.state == nil {
		return orm.Manager[M]{}
	}
	return definition.state.creation.binding.state.objects
}
func (definition FirstAccountDefinition[M]) MaxPasswordBytes() int {
	if definition.state == nil {
		return 0
	}
	return definition.state.creation.config.MaxPasswordBytes
}
func (definition FirstAccountDefinition[M]) EvidenceSHA256() string {
	if definition.state == nil {
		return ""
	}
	return definition.state.evidence
}

// Bind creates a distinct owner over the declaration's actual typed source.
// It validates the native write/snapshot capabilities without performing I/O.
// The caller owns acquisition and closing of this backend. Each returned owner
// retains the ordinary one-attempt preparation and fresh-domain commit rules.
func (definition FirstAccountDefinition[M]) Bind(backend db.SnapshotReader) (*FirstAccount[M], error) {
	if definition.state == nil || nilModelValue(backend) {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	writer, ok := backend.(db.CoordinatedRelationAtomic)
	if !ok || nilModelValue(writer) {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	credentials := definition.state.credentials
	credentials.backend = backend
	creation := &creationState[M]{creationDefinition: definition.state.creation, store: &CredentialStore[M]{state: &credentials}, backend: writer}
	var err error
	creation.lifecycle, err = credentials.firstLifecycle(creation.store, creation.binding, creationLifecycleConfig(creation.config))
	if err != nil {
		return nil, err
	}
	return &FirstAccount[M]{state: &firstAccountState[M]{creation, definition.state.fingerprint, definition.state.evidence}}, nil
}
