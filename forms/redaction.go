package forms

import "fmt"

// Form inputs may contain passwords or other private data. Formatting is not a
// serialization API: callers must explicitly select values through accessors.
func (Value) Format(state fmt.State, _ rune)  { fmt.Fprint(state, "forms.Value{redacted}") }
func (Data) Format(state fmt.State, _ rune)   { fmt.Fprint(state, "forms.Data{redacted}") }
func (Values) Format(state fmt.State, _ rune) { fmt.Fprint(state, "forms.Values{redacted}") }
func (Entry) Format(state fmt.State, _ rune)  { fmt.Fprint(state, "forms.Entry{redacted}") }
func (Form) Format(state fmt.State, _ rune)   { fmt.Fprint(state, "forms.Form{redacted}") }
