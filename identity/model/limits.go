package model

// MaximumAccountGroups bounds both submitted and stored group memberships.
// It is independent of auth.MaximumPermissions: many groups can grant the
// same permission, and a small union does not prove a bounded membership set.
const MaximumAccountGroups = 256
