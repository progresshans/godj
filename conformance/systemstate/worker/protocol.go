// Package worker provides the private distinct-process driver used by the
// system-state conformance runner. Its JSON protocol deliberately separates
// safe observations on stdout from bearer material written through an
// anonymous inherited descriptor.
package worker

// Action is one closed worker operation. Unknown values fail before any
// database or HTTP work and are never reflected into an error string.
type Action string

const (
	ActionInitialize     Action = "initialize"
	ActionAuthenticate   Action = "authenticate"
	ActionLogin          Action = "login"
	ActionSessionProbe   Action = "session_probe"
	ActionLogout         Action = "logout"
	ActionOldCookieProbe Action = "old_cookie_probe"
	ActionCSRFSetup      Action = "csrf_setup"
	ActionCookieProbe    Action = "cookie_probe"
	ActionCSRFStale      Action = "csrf_stale"
	ActionCSRFFresh      Action = "csrf_fresh"
	ActionAuditFault     Action = "audit_fault"
	ActionHistoryWrite   Action = "history_write"
	ActionHistoryRead    Action = "history_read"
)

// CookieBundle is transported only through stdin or the descriptor-3 secret
// response. It must never be embedded in Response or an error.
type CookieBundle struct {
	Session string `json:"session"`
	CSRF    string `json:"csrf"`
}

func (CookieBundle) String() string   { return "worker.CookieBundle{redacted}" }
func (CookieBundle) GoString() string { return "worker.CookieBundle{redacted}" }

// Request is the single bounded JSON document read from stdin. Password,
// cookies, and token intentionally remain decodable here; diagnostic
// formatting is always redacted.
type Request struct {
	Action         Action       `json:"action"`
	Database       string       `json:"database"`
	Username       string       `json:"username"`
	Password       string       `json:"password"`
	Cookies        CookieBundle `json:"cookies"`
	Token          string       `json:"token"`
	RepositoryRoot string       `json:"repository_root"`
	ObjectID       int64        `json:"object_id"`
}

func (Request) String() string   { return "worker.Request{redacted}" }
func (Request) GoString() string { return "worker.Request{redacted}" }

// SecretBundle is written only to the anonymous descriptor inherited as FD 3.
// The worker never sends this value to stdout or stderr.
type SecretBundle struct {
	Cookies CookieBundle `json:"cookies"`
	Token   string       `json:"token"`
}

func (SecretBundle) String() string   { return "worker.SecretBundle{redacted}" }
func (SecretBundle) GoString() string { return "worker.SecretBundle{redacted}" }

// AuditEvent is a secret-free durable semantic event snapshot.
type AuditEvent struct {
	Sequence      uint64   `json:"sequence"`
	ActorID       string   `json:"actor_id"`
	Model         string   `json:"model"`
	ObjectID      int64    `json:"object_id"`
	Action        string   `json:"action"`
	ChangedFields []string `json:"changed_fields"`
	DisplayLabel  string   `json:"display_label"`
}

// Response contains only framework-owned codes, statuses, counts, booleans,
// and non-secret semantic state. Every field is emitted so a parent process
// cannot mistake an omitted false/zero observation for an uncollected value.
type Response struct {
	OK        bool   `json:"ok"`
	ErrorCode string `json:"error_code"`
	Action    Action `json:"action"`
	PID       int    `json:"pid"`

	Status         int `json:"status"`
	LoginStatus    int `json:"login_status"`
	AdminStatus    int `json:"admin_status"`
	APIStatus      int `json:"api_status"`
	MutationStatus int `json:"mutation_status"`
	AddStatus      int `json:"add_status"`
	ChangeStatus   int `json:"change_status"`
	DeleteStatus   int `json:"delete_status"`

	Ready                bool `json:"ready"`
	MigrationApplied     bool `json:"migration_applied"`
	Authenticated        bool `json:"authenticated"`
	Active               bool `json:"active"`
	Permission           bool `json:"permission"`
	Rotated              bool `json:"rotated"`
	OldSessionRemoved    bool `json:"old_session_removed"`
	SameCookieHandoff    bool `json:"same_cookie_handoff"`
	Resurrected          bool `json:"resurrected"`
	FaultInjected        bool `json:"fault_injected"`
	RolledBack           bool `json:"rolled_back"`
	StrictlyIncreasing   bool `json:"strictly_increasing"`
	Contiguous           bool `json:"contiguous"`
	AcceptsNonContiguous bool `json:"accepts_non_contiguous"`

	PrincipalID  string   `json:"principal_id"`
	Permissions  []string `json:"permissions"`
	APIErrorCode string   `json:"api_error_code"`

	CredentialRows     int   `json:"credential_rows"`
	SessionRows        int   `json:"session_rows"`
	AuditRows          int   `json:"audit_rows"`
	ArticleRows        int   `json:"article_rows"`
	ArticleRowsBefore  int   `json:"article_rows_before"`
	ArticleRowsAfter   int   `json:"article_rows_after"`
	AuditRowsBefore    int   `json:"audit_rows_before"`
	AuditRowsAfter     int   `json:"audit_rows_after"`
	ArticleDelta       int   `json:"article_delta"`
	AuditDelta         int   `json:"audit_delta"`
	ResurrectionWrites int64 `json:"resurrection_writes"`

	ObjectID       int64        `json:"object_id"`
	AuditCount     int          `json:"audit_count"`
	NewestSequence uint64       `json:"newest_sequence"`
	AuditEvents    []AuditEvent `json:"audit_events"`
	NewestEvents   []AuditEvent `json:"newest_events"`
}

func (Response) String() string   { return "worker.Response{safe-observation}" }
func (Response) GoString() string { return "worker.Response{safe-observation}" }
