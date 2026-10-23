package uploads

import "bytes"

// multipart.Reader can return plain EOF when a new part's headers are missing.
// Observe closing delimiter lines as bytes arrive, without retaining or rereading
// the body. The MIME parser still owns part boundaries and header validation.
type terminator struct {
	marker                       []byte
	matched                      int
	possible, carriage, complete bool
}

func (t *terminator) observe(data []byte) {
	if t.complete {
		return
	}
	for len(data) > 0 {
		end := bytes.IndexByte(data, '\n')
		line := data
		if end >= 0 {
			line = data[:end]
		}
		if t.possible {
			for len(line) > 0 && t.matched < len(t.marker) {
				if line[0] != t.marker[t.matched] {
					t.possible = false
					break
				}
				t.matched++
				line = line[1:]
			}
			if t.possible && t.matched == len(t.marker) {
				for _, char := range line {
					if t.carriage || char != ' ' && char != '\t' && char != '\r' {
						t.possible = false
						break
					}
					if char == '\r' {
						t.carriage = true
					}
				}
			}
		}
		if end < 0 {
			return
		}
		if t.possible && t.matched == len(t.marker) {
			t.complete = true
			return
		}
		t.possible, t.carriage, t.matched = true, false, 0
		data = data[end+1:]
	}
}
func (t *terminator) eof() {
	if t.possible && !t.carriage && t.matched == len(t.marker) {
		t.complete = true
	}
}
