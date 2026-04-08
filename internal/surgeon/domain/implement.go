package domain

// ImplementRequest contains the parameters for generating an interface implementation.
type ImplementRequest struct {
	Interface string // e.g. "io.Reader" or "github.com/JLugagne/go-surgeon/internal/surgeon/domain/service.SurgeonCommands"
	Receiver  string // e.g. "MyStruct" or "*MyStruct"
	FilePath  string // Target file to append the methods to
}

// MockRequest contains the parameters for generating a function-field mock struct.
type MockRequest struct {
	Interface string // e.g. "domain/repositories/book.BookRepository"
	Receiver  string // e.g. "MockBookRepository"
	FilePath  string // Target file to write the mock to
}

// InterfaceActionRequest contains the parameters for add/update/delete-interface commands.
type InterfaceActionRequest struct {
	FilePath   string // --file: file containing the interface
	Identifier string // --id: interface name (required for update/delete)
	Content    string // stdin: interface type declaration source (add/update)
	MockFile   string // --mock-file: target file for the generated mock (add/update)
	MockName   string // --mock-name: name of the mock struct (add/update)
}
