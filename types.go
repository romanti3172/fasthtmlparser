package fasthtml

// Tag is a known tag name; index into tagNames.
type Tag int16

type entity struct {
	value  string
	legacy bool
}
