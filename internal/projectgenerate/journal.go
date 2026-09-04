package projectgenerate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	publicationJournalFormatVersion = 1
	maximumPublicationJournalBytes  = 16 << 20
	maximumPublicationJournalFiles  = 65_536
	maximumPublicationPathBytes     = 4_096
	maximumPublicationPathDepth     = 64
	maximumPublicationJSONDepth     = 8
	maximumPublicationObjectMembers = 64
)

type publicationJournal struct {
	FormatVersion  int                  `json:"format_version"`
	TransactionID  string               `json:"transaction_id"`
	SnapshotSHA256 string               `json:"snapshot_sha256"`
	PriorManifest  journalManifestState `json:"prior_manifest"`
	NextManifest   journalManifestState `json:"next_manifest"`
	Directories    []journalDirectory   `json:"directories"`
	Files          []journalFile        `json:"files"`
}

type journalManifestState struct {
	Present bool   `json:"present"`
	SHA256  string `json:"sha256"`
}

type journalFile struct {
	Path  string           `json:"path"`
	Prior journalFileState `json:"prior"`
	Next  journalFileState `json:"next"`
}

type journalDirectory struct {
	Path         string `json:"path"`
	PriorPresent bool   `json:"prior_present"`
}

type journalFileState struct {
	Present bool   `json:"present"`
	Owned   bool   `json:"owned"`
	SHA256  string `json:"sha256"`
	Mode    uint32 `json:"mode"`
}

func encodePublicationJournal(journal publicationJournal) ([]byte, error) {
	if err := validatePublicationJournal(journal); err != nil {
		return nil, err
	}
	document, err := json.Marshal(journal)
	if err != nil {
		return nil, fmt.Errorf("encode publication journal: %w", err)
	}
	document = append(document, '\n')
	if len(document) > maximumPublicationJournalBytes {
		return nil, fmt.Errorf("encode publication journal: document exceeds limit")
	}
	return document, nil
}

func decodePublicationJournal(document []byte) (publicationJournal, error) {
	if len(document) == 0 || len(document) > maximumPublicationJournalBytes {
		return publicationJournal{}, fmt.Errorf("decode publication journal: invalid document size")
	}
	if err := rejectDuplicateJSONMembers(document); err != nil {
		return publicationJournal{}, fmt.Errorf("decode publication journal: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	var journal publicationJournal
	if err := decoder.Decode(&journal); err != nil {
		return publicationJournal{}, fmt.Errorf("decode publication journal: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return publicationJournal{}, fmt.Errorf("decode publication journal: %w", err)
	}
	if err := validatePublicationJournal(journal); err != nil {
		return publicationJournal{}, err
	}
	canonical, err := encodePublicationJournal(journal)
	if err != nil {
		return publicationJournal{}, err
	}
	if !bytes.Equal(document, canonical) {
		return publicationJournal{}, fmt.Errorf("decode publication journal: noncanonical document")
	}
	return journal, nil
}

func validatePublicationJournal(journal publicationJournal) error {
	if journal.FormatVersion != publicationJournalFormatVersion {
		return fmt.Errorf("validate publication journal: unsupported format version")
	}
	if !validTransactionID(journal.TransactionID) {
		return fmt.Errorf("validate publication journal: invalid transaction id")
	}
	if !validSHA256(journal.SnapshotSHA256) {
		return fmt.Errorf("validate publication journal: invalid snapshot digest")
	}
	if err := validateJournalManifestState(journal.PriorManifest, true); err != nil {
		return fmt.Errorf("validate publication journal: prior manifest: %w", err)
	}
	if err := validateJournalManifestState(journal.NextManifest, false); err != nil {
		return fmt.Errorf("validate publication journal: next manifest: %w", err)
	}
	if len(journal.Files) > maximumPublicationJournalFiles {
		return fmt.Errorf("validate publication journal: file count exceeds limit")
	}
	if len(journal.Directories) > maximumPublicationJournalFiles {
		return fmt.Errorf("validate publication journal: directory count exceeds limit")
	}
	previousDirectory := journalDirectory{}
	for index, directory := range journal.Directories {
		if err := validatePublicationRelativePath(directory.Path); err != nil {
			return fmt.Errorf("validate publication journal: directories[%d]: %w", index, err)
		}
		if directory.Path == ".godj" || strings.HasPrefix(directory.Path, ".godj/") {
			return fmt.Errorf("validate publication journal: directories[%d] uses reserved control directory", index)
		}
		if index > 0 && !publicationDirectoryLess(previousDirectory.Path, directory.Path) {
			return fmt.Errorf("validate publication journal: directories are not in canonical order")
		}
		previousDirectory = directory
	}
	previous := ""
	nextDirectories := make(map[string]struct{})
	for index, file := range journal.Files {
		if err := validatePublicationRelativePath(file.Path); err != nil {
			return fmt.Errorf("validate publication journal: files[%d]: %w", index, err)
		}
		if file.Path == generatedManifestRelativePath || file.Path == ".godj" || strings.HasPrefix(file.Path, ".godj/") {
			return fmt.Errorf("validate publication journal: files[%d]: path uses reserved control directory", index)
		}
		if !validGeneratedFilename(pathBase(file.Path)) {
			return fmt.Errorf("validate publication journal: files[%d]: path is outside the generated namespace", index)
		}
		if previous != "" && file.Path <= previous {
			return fmt.Errorf("validate publication journal: files are not strictly ordered")
		}
		previous = file.Path
		if err := validateJournalFileState(file.Prior, true); err != nil {
			return fmt.Errorf("validate publication journal: files[%d].prior: %w", index, err)
		}
		if err := validateJournalFileState(file.Next, false); err != nil {
			return fmt.Errorf("validate publication journal: files[%d].next: %w", index, err)
		}
		if !file.Prior.Present && !file.Next.Present {
			return fmt.Errorf("validate publication journal: files[%d] has no prior or next state", index)
		}
		if file.Prior.Present && !file.Prior.Owned {
			if !file.Next.Present || file.Prior.SHA256 != file.Next.SHA256 || file.Prior.Mode != file.Next.Mode {
				return fmt.Errorf("validate publication journal: files[%d] mutates an unowned prior file", index)
			}
		}
		if file.Prior.Owned && !journal.PriorManifest.Present {
			return fmt.Errorf("validate publication journal: files[%d] has owned prior state without a prior manifest", index)
		}
		if file.Next.Present {
			current := pathDir(file.Path)
			for current != "." {
				nextDirectories[current] = struct{}{}
				if len(nextDirectories) > maximumPublicationJournalFiles {
					return fmt.Errorf("validate publication journal: next directory count exceeds limit")
				}
				current = pathDir(current)
			}
		}
	}
	if len(nextDirectories) != len(journal.Directories) {
		return fmt.Errorf("validate publication journal: directory inventory differs from next files")
	}
	for _, directory := range journal.Directories {
		if _, exists := nextDirectories[directory.Path]; !exists {
			return fmt.Errorf("validate publication journal: directory inventory differs from next files")
		}
	}
	return nil
}

func pathBase(value string) string {
	index := strings.LastIndexByte(value, '/')
	if index < 0 {
		return value
	}
	return value[index+1:]
}

func pathDir(value string) string {
	index := strings.LastIndexByte(value, '/')
	if index < 0 {
		return "."
	}
	return value[:index]
}

func publicationDirectoryLess(left, right string) bool {
	leftDepth := strings.Count(left, "/")
	rightDepth := strings.Count(right, "/")
	if leftDepth != rightDepth {
		return leftDepth < rightDepth
	}
	return left < right
}

func validateJournalManifestState(state journalManifestState, allowAbsent bool) error {
	if !state.Present {
		if !allowAbsent || state.SHA256 != "" {
			return errors.New("invalid absent state")
		}
		return nil
	}
	if !validSHA256(state.SHA256) {
		return errors.New("invalid digest")
	}
	return nil
}

func validateJournalFileState(state journalFileState, prior bool) error {
	if !state.Present {
		if state.Owned || state.SHA256 != "" || state.Mode != 0 {
			return errors.New("invalid absent state")
		}
		return nil
	}
	if !prior && !state.Owned {
		return errors.New("next state is not owned")
	}
	if !validSHA256(state.SHA256) {
		return errors.New("invalid digest")
	}
	if state.Mode != 0o644 {
		return errors.New("invalid generated file mode")
	}
	return nil
}

func validTransactionID(value string) bool {
	if len(value) != 32 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 16 && value == strings.ToLower(value)
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func digestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func rejectDuplicateJSONMembers(document []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	if err := scanJSONValue(decoder, 0); err != nil {
		return err
	}
	return requireJSONEOF(decoder)
}

func scanJSONValue(decoder *json.Decoder, depth int) error {
	if depth > maximumPublicationJSONDepth {
		return fmt.Errorf("JSON nesting exceeds limit")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if value, ok := token.(string); ok && len(value) > maximumPublicationPathBytes {
		return fmt.Errorf("JSON string exceeds limit")
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		members := 0
		for decoder.More() {
			members++
			if members > maximumPublicationObjectMembers {
				return fmt.Errorf("JSON object member count exceeds limit")
			}
			member, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := member.(string)
			if !ok {
				return errors.New("object member name is not a string")
			}
			if len(name) > maximumPublicationPathBytes {
				return fmt.Errorf("JSON member name exceeds limit")
			}
			if _, duplicate := seen[name]; duplicate {
				return fmt.Errorf("duplicate object member %q", name)
			}
			seen[name] = struct{}{}
			if err := scanJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("invalid object closing delimiter")
		}
	case '[':
		entries := 0
		for decoder.More() {
			entries++
			if entries > maximumPublicationJournalFiles {
				return fmt.Errorf("JSON array entry count exceeds limit")
			}
			if err := scanJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("invalid array closing delimiter")
		}
	default:
		return errors.New("invalid opening delimiter")
	}
	return nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}
