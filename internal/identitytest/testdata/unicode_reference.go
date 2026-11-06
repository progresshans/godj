package identityreference

import "embed"

//go:embed unicode-*.json
var unicodeReferences embed.FS

func UnicodeInputs() ([]byte, error) {
	return unicodeReferences.ReadFile("unicode-inputs.json")
}

// Unicode returns the unchanged pinned Python/Django normalization capture.
func Unicode() ([]byte, error) {
	return unicodeReferences.ReadFile("unicode-django61.json")
}
