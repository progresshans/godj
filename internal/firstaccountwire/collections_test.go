package firstaccountwire_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"slices"
	"testing"

	"github.com/progresshans/godj/internal/firstaccountprotocol"
	"github.com/progresshans/godj/internal/firstaccountwire"
	"github.com/progresshans/godj/project/firstaccount"
)

func collectionRequest() firstaccountwire.Request {
	request := uploadRequest()
	request.Values = append(request.Values,
		firstaccount.Value{Name: "topics", Present: true, Keys: []int64{math.MinInt64, 0, math.MaxInt64, -7, 0}},
		firstaccount.Value{Name: "empty_topics", Present: true, Keys: []int64{}},
		firstaccount.Value{Name: "omitted_topics"})
	return request
}

func TestRequestCollectionsKeepSignedOrderDuplicatesEmptyAndOwnedLifetime(t *testing.T) {
	request := collectionRequest()
	document, err := firstaccountwire.EncodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := firstaccountwire.ReadRequest(bytes.NewReader(document))
	if err != nil {
		t.Fatal(err)
	}
	request.Clear()
	clear(document)
	if len(decoded.Values) != 9 || !slices.Equal(decoded.Values[6].Keys, []int64{math.MinInt64, 0, math.MaxInt64, -7, 0}) ||
		decoded.Values[7].Keys == nil || len(decoded.Values[7].Keys) != 0 || decoded.Values[8].Present || decoded.Values[8].Keys != nil || decoded.Values[3].Upload == nil {
		t.Fatal("collection wire changed input kinds, presence, order or signed key values")
	}
	keys := decoded.Values[6].Keys
	decoded.Clear()
	if !slices.Equal(keys, make([]int64, 5)) || decoded.Values != nil {
		t.Fatal("clear retained collection keys")
	}
}

func TestRequestCollectionEnvelopeRejectsMixedKindsAndForgedCounts(t *testing.T) {
	for _, mutate := range []func(*firstaccountwire.Request){
		func(r *firstaccountwire.Request) { r.Values[6].Keys = make([]int64, firstaccount.MaxCollectionItems+1) },
		func(r *firstaccountwire.Request) { r.Values[6].Text = []byte("0") },
		func(r *firstaccountwire.Request) { r.Values[6].Upload = &firstaccount.Upload{Name: "x"} },
		func(r *firstaccountwire.Request) { r.Values[6].Present = false },
		func(r *firstaccountwire.Request) { r.Values[7].Present = false },
	} {
		request := collectionRequest()
		mutate(&request)
		if frame, err := firstaccountwire.EncodeRequest(request); err == nil || frame != nil {
			t.Fatal("mixed or unbounded collection encoded")
		}
		request.Clear()
	}
	request := firstaccountwire.Request{Fingerprint: fingerprint, OperationID: operationID, Password: []byte("password"), Values: []firstaccount.Value{{Name: "topics", Present: true, Keys: []int64{0, -7}}}}
	document, err := firstaccountwire.EncodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	header := len(firstaccountprotocol.Magic) + 4 + 64 + 64 + 1 + 2
	// Independent byte oracle for the public frame's signed big-endian keys.
	if !bytes.Equal(document[header+4+len("topics"):header+4+len("topics")+16], []byte{0, 0, 0, 0, 0, 0, 0, 0, 255, 255, 255, 255, 255, 255, 255, 249}) {
		t.Fatal("collection key wire is not signed big-endian int64")
	}
	for _, mutate := range []func([]byte){
		func(b []byte) { b[header+1] = 4 },
		func(b []byte) { b[header+1] = 0 },
		func(b []byte) { binary.BigEndian.PutUint16(b[header+2:], firstaccount.MaxCollectionItems+1) },
		func(b []byte) { binary.BigEndian.PutUint16(b[header+2:], 3) },
		func(b []byte) { binary.BigEndian.PutUint16(b[header+2:], 1) },
		func(b []byte) { b[len(firstaccountprotocol.Magic)-1] = '3' },
	} {
		bad := bytes.Clone(document)
		mutate(bad)
		if decoded, err := firstaccountwire.DecodeRequest(bad); err == nil {
			decoded.Clear()
			t.Fatal("forged collection count/kind or obsolete wire accepted")
		}
	}
	for n := range len(document) {
		if _, err := firstaccountwire.DecodeRequest(document[:n]); err == nil {
			t.Fatal("truncated collection accepted", n)
		}
	}
	request.Clear()
	request = firstaccountwire.Request{Fingerprint: fingerprint, OperationID: operationID, Password: []byte("password")}
	for i := range firstaccount.MaxInputBytes / (8 * firstaccount.MaxCollectionItems) {
		request.Values = append(request.Values, firstaccount.Value{Name: fmt.Sprintf("keys%d", i), Present: true, Keys: make([]int64, firstaccount.MaxCollectionItems)})
	}
	document, err = firstaccountwire.EncodeRequest(request)
	if err != nil {
		t.Fatal("maximum complete key budget rejected", err)
	}
	decoded, err := firstaccountwire.DecodeRequest(document)
	if err != nil {
		t.Fatal(err)
	}
	decoded.Clear()
	request.Values = append(request.Values, firstaccount.Value{Name: "overflow", Present: true, Keys: []int64{0}})
	if _, err := firstaccountwire.EncodeRequest(request); err == nil {
		t.Fatal("aggregate key bytes escaped input budget")
	}
	request.Clear()
}
