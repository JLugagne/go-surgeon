package domain

// ExtractInterfaceRequest represents a request to extract an interface from a struct.
type ExtractInterfaceRequest struct {
	FilePath      string
	StructName    string
	InterfaceName string
	OutPath       string // optional
	MockFile      string // optional
	MockName      string // optional
}
