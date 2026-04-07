package domain

// ImplementRequest contains the parameters for generating an interface implementation.
type ImplementRequest struct {
	Interface string // e.g. "io.Reader" or "github.com/JLugagne/go-surgeon/internal/surgeon/domain/service.SurgeonCommands"
	Receiver  string // e.g. "MyStruct" or "*MyStruct"
	FilePath  string // Target file to append the methods to
}
