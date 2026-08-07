package protocol

// FormatVersion is the only wire-format version understood by this package.
// Version 2 binds every contract to its expected execution phase.
const FormatVersion = 2

type Profile struct {
	FormatVersion int                `json:"format_version"`
	ID            string             `json:"id"`
	Fingerprint   ProfileFingerprint `json:"fingerprint"`
	Lock          LockMetadata       `json:"lock"`
}

// ProfileSnapshot is embedded in every observation suite. It intentionally
// matches Profile without the wire-format wrapper so a validator can compare
// it to the separately locked profile file.
type ProfileSnapshot struct {
	ID          string             `json:"id"`
	Fingerprint ProfileFingerprint `json:"fingerprint"`
	Lock        LockMetadata       `json:"lock"`
}

type ProfileFingerprint struct {
	DjangoVersion            string `json:"django_version"`
	DjangoCommit             string `json:"django_commit"`
	DjangoDistributionSHA256 string `json:"django_distribution_sha256"`
	PythonImplementation     string `json:"python_implementation"`
	PythonVersion            string `json:"python_version"`
	SQLiteVersion            string `json:"sqlite_version"`
	SQLiteSourceID           string `json:"sqlite_source_id"`
	DatabaseEngine           string `json:"database_engine"`
	UseTZ                    *bool  `json:"use_tz"`
	Timezone                 string `json:"timezone"`
	LanguageCode             string `json:"language_code"`
	Locale                   string `json:"locale"`
	Platform                 string `json:"platform"`
	Architecture             string `json:"architecture"`
}

type LockMetadata struct {
	File           string `json:"file"`
	SHA256         string `json:"sha256"`
	Manager        string `json:"manager"`
	ManagerVersion string `json:"manager_version"`
}

func (p Profile) Snapshot() ProfileSnapshot {
	return ProfileSnapshot{
		ID:          p.ID,
		Fingerprint: p.Fingerprint,
		Lock:        p.Lock,
	}
}

type Manifest struct {
	FormatVersion int        `json:"format_version"`
	ProfileID     string     `json:"profile_id"`
	Contracts     []Contract `json:"contracts"`
}

type Contract struct {
	ID         string                `json:"id"`
	Title      string                `json:"title"`
	Scenario   string                `json:"scenario"`
	Phase      Phase                 `json:"phase"`
	Status     ContractStatus        `json:"status"`
	Provenance []Provenance          `json:"provenance"`
	Comparison []ComparisonDimension `json:"comparison"`
}

type ContractStatus string

const (
	ContractDraft        ContractStatus = "draft"
	ContractOracleLocked ContractStatus = "oracle_locked"
	ContractRed          ContractStatus = "red"
	ContractPassing      ContractStatus = "passing"
	ContractDeviation    ContractStatus = "deviation"
)

type Provenance struct {
	Kind      string `json:"kind"`
	Reference string `json:"reference"`
	Derived   *bool  `json:"derived"`
	License   string `json:"license,omitempty"`
}

type ComparisonDimension string

const (
	CompareResult  ComparisonDimension = "result"
	CompareError   ComparisonDimension = "error"
	CompareDBState ComparisonDimension = "db_state"
	CompareMetrics ComparisonDimension = "metrics"
)

type ObservationSuite struct {
	FormatVersion int             `json:"format_version"`
	Profile       ProfileSnapshot `json:"profile"`
	Contracts     []Observation   `json:"contracts"`
}

type Observation struct {
	ID      string            `json:"id"`
	Status  ObservationStatus `json:"status"`
	Phase   Phase             `json:"phase"`
	Result  *Value            `json:"result,omitempty"`
	Error   *ObservedError    `json:"error,omitempty"`
	DBState *Value            `json:"db_state,omitempty"`
	Metrics *Value            `json:"metrics,omitempty"`
}

type ObservationStatus string

const (
	StatusObserved       ObservationStatus = "observed"
	StatusNotImplemented ObservationStatus = "not_implemented"
)

type Phase string

const (
	PhaseEnvironment  Phase = "environment"
	PhaseMetadata     Phase = "metadata"
	PhaseConstruction Phase = "construction"
	PhaseEvaluation   Phase = "evaluation"
	PhaseCommit       Phase = "commit"
	PhaseRollback     Phase = "rollback"
)

type ObservedError struct {
	Category          string `json:"category"`
	Code              string `json:"code"`
	PythonType        string `json:"python_type,omitempty"`
	Message           string `json:"message,omitempty"`
	MessageIsContract *bool  `json:"message_is_contract"`
}
