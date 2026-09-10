package queryplan

import "strings"

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// EscapeLike escapes literal pattern characters with one immutable replacer.
// Backend SQL operators and collation behavior remain dialect-specific.
func EscapeLike(value string) string { return likeEscaper.Replace(value) }
