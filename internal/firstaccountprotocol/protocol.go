// Package firstaccountprotocol declares the bootstrap ABI without importing
// typed account execution, uploads, storage, or third-party modules. Executable
// compatibility probes must remain usable before runtime dependencies are loaded.
package firstaccountprotocol

const (
	Version           uint64 = 4
	DescribeArgument         = "__godj_first_account_describe_v4"
	ProvisionArgument        = "__godj_first_account_provision_v4"
	Magic                    = "GODJFA4"
)
