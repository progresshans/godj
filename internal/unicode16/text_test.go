package unicode16

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"hash"
	"io"
	"strconv"
	"strings"
	"testing"
)

//go:embed testdata/ucd/NormalizationTest.txt.gz
var normalizationData []byte

//go:embed testdata/python314-unicode16.json
var pythonData []byte

//go:embed sources.json
var sourceManifest []byte

func TestNFKCUnicode16OfficialNormalizationCorpus(t *testing.T) {
	var manifest struct {
		Files map[string]struct {
			SHA256 string `json:"sha256"`
			Size   int
		}
	}
	if err := json.Unmarshal(sourceManifest, &manifest); err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(normalizationData))
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	closed := reader.Close()
	if err != nil || closed != nil {
		t.Fatal(err, closed)
	}
	digest := sha256.Sum256(data)
	want := manifest.Files["NormalizationTest.txt"]
	if len(data) != want.Size || hex.EncodeToString(digest[:]) != want.SHA256 {
		t.Fatal("Unicode conformance source hash mismatch")
	}
	decode := func(value string) string {
		var result strings.Builder
		for _, item := range strings.Fields(value) {
			number, err := strconv.ParseInt(item, 16, 32)
			if err != nil {
				t.Fatal(err)
			}
			result.WriteRune(rune(number))
		}
		return result.String()
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	rows, comparisons := 0, 0
	for line := 1; scanner.Scan(); line++ {
		text, _, _ := strings.Cut(scanner.Text(), "#")
		text = strings.TrimSpace(text)
		if text == "" || strings.HasPrefix(text, "@") {
			continue
		}
		columns := strings.Split(text, ";")
		if len(columns) != 6 {
			t.Fatal("invalid Unicode row", line)
		}
		expected := decode(columns[3])
		for column, raw := range columns[:5] {
			input := decode(raw)
			if got := NFKC(input); got != expected {
				t.Fatalf("line %d column %d: input %U got %U want %U", line, column, []rune(input), []rune(got), []rune(expected))
			}
			comparisons++
		}
		rows++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if rows != 19965 || comparisons != rows*5 {
		t.Fatal("incomplete official corpus", rows, comparisons)
	}
	t.Logf("Unicode %s: %d official rows, %d NFKC comparisons", Version, rows, comparisons)
}

type pythonReference struct {
	Django, Python, Unicode string
	Scalars                 int
	SHA256                  map[string]string
	LowerContexts           []string `json:"lower_contexts"`
	Sequences               []struct{ Input, NFKC, Lower string }
}

func TestUnicode16EveryScalarAndCasingContextMatchesPinnedPython(t *testing.T) {
	var reference pythonReference
	if err := json.Unmarshal(pythonData, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || reference.Python != "3.14.3" || reference.Unicode != Version || reference.Scalars != 0x110000-0x800 || len(reference.LowerContexts) != 4 {
		t.Fatal("reference authority/count mismatch")
	}
	hashes := map[string]hash.Hash{"nfkc": sha256.New(), "lower": sha256.New(), "properties": sha256.New(), "lower_context": sha256.New()}
	var frame [8]byte
	emit := func(name string, cp rune, value string) {
		binary.BigEndian.PutUint32(frame[:4], uint32(cp))
		binary.BigEndian.PutUint32(frame[4:], uint32(len(value)))
		hashes[name].Write(frame[:])
		hashes[name].Write([]byte(value))
	}
	count := 0
	for cp := rune(0); cp <= 0x10ffff; cp++ {
		if cp >= 0xd800 && cp <= 0xdfff {
			continue
		}
		count++
		value := string(cp)
		emit("nfkc", cp, NFKC(value))
		emit("lower", cp, Lower(value))
		var flags byte
		if IsAlphanumeric(cp) {
			flags |= 1
		}
		if IsDigit(cp) {
			flags |= 2
		}
		if IsSpace(cp) {
			flags |= 4
		}
		binary.BigEndian.PutUint32(frame[:4], uint32(cp))
		frame[4] = flags
		hashes["properties"].Write(frame[:5])
		for _, context := range reference.LowerContexts {
			emit("lower_context", cp, Lower(strings.ReplaceAll(context, "{}", value)))
		}
	}
	if count != reference.Scalars {
		t.Fatal("scalar coverage truncated", count)
	}
	for name, h := range hashes {
		if got := hex.EncodeToString(h.Sum(nil)); got != reference.SHA256[name] {
			t.Errorf("%s all-scalar behavior: got %s, Python %s", name, got, reference.SHA256[name])
		}
	}
}

func TestUnicode16SequencesDoNotInsertCharactersOrRepairMalformedUTF8(t *testing.T) {
	var reference pythonReference
	if err := json.Unmarshal(pythonData, &reference); err != nil {
		t.Fatal(err)
	}
	for i, row := range reference.Sequences {
		if got := NFKC(row.Input); got != row.NFKC {
			t.Fatalf("sequence %d NFKC %U want %U", i, []rune(got), []rune(row.NFKC))
		}
		if got := Lower(row.Input); got != row.Lower {
			t.Fatalf("sequence %d Lower %U want %U", i, []rune(got), []rune(row.Lower))
		}
	}
	for _, value := range []string{string([]byte{255}), "start" + string([]byte{0xc0, 0x80}) + "end"} {
		if NFKC(value) != value || Lower(value) != value {
			t.Fatal("malformed input was silently repaired")
		}
	}
	if TrimSpace("\x1c\u2003value\x1f") != "value" {
		t.Fatal("Python whitespace semantics lost")
	}
	for _, value := range []rune{-1, 0x110000, 0xd800} {
		if IsAlphanumeric(value) || IsDigit(value) || IsSpace(value) {
			t.Fatal("non-scalar classified")
		}
	}
}
