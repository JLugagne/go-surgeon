package domain

// TagRequest represents a request to update struct tags.
type TagRequest struct {
	FilePath   string
	StructName string
	FieldName  string // optional
	SetTag     string // optional
	AutoFormat string // optional (e.g. "json", "bson")
}
