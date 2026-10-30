package godj

import "testing"

// loadFixture reads a fresh fixture; expected input stays confined to test code.
func loadFixture[T any](t *testing.T, path string, load func(string) (T, error)) T {
	t.Helper()
	value, err := load(path)
	if err != nil {
		t.Fatalf("load fixture %s: %v", path, err)
	}
	return value
}
