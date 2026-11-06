package helpdesk

import (
	"context"
	"crypto/sha256"
	"slices"

	"github.com/progresshans/godj/binaryvalue"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk/models"
)

// The caller has already written this Ticket in the same transaction, holding
// its row lock. Read the stored JSON first: PostgreSQL JSONB can normalize number
// tokens. A no-op/collection-only update must not call this helper because its
// earlier read alone would not serialize a concurrent payload update.
func synchronizePayloadDigest(ctx context.Context, session db.Session, stored models.Ticket) (models.Ticket, error) {
	var digest *binaryvalue.Value
	if stored.ExternalPayload != nil {
		sum := sha256.Sum256([]byte(stored.ExternalPayload.Text))
		digest = &binaryvalue.Value{Data: string(sum[:])}
	}
	if samePayloadDigest(stored.ExternalPayloadDigest, digest) {
		return stored, nil
	}
	patch := models.TicketPatch{}.WithExternalPayloadDigestNull()
	if digest != nil {
		patch = models.TicketPatch{}.WithExternalPayloadDigest(*digest)
	}
	return models.TicketObjects.Patch(ctx, session, stored, patch)
}

func samePayloadDigest(left, right *binaryvalue.Value) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func payloadDigestChanges(changed []string, before, after models.Ticket) []string {
	if !samePayloadDigest(before.ExternalPayloadDigest, after.ExternalPayloadDigest) && !slices.Contains(changed, "external_payload_digest") {
		changed = append(changed, "external_payload_digest")
	}
	return changed
}
