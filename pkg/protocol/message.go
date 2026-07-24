package protocol

type MessageType string

const (
	TypeRegister MessageType = "register"
	TypeRequest  MessageType = "request"
	TypeResponse MessageType = "response"
	TypeError    MessageType = "error"
)

type Message struct {
	Type     MessageType       `json:"type"`
	ID       string            `json:"id,omitempty"`
	JWT      string            `json:"jwt,omitempty"`
	Username string            `json:"username,omitempty"`
	Password string            `json:"password,omitempty"`
	Version  string            `json:"version,omitempty"`
	VHash    string            `json:"vhash,omitempty"`
	App      string            `json:"app,omitempty"`
	Port     int               `json:"port,omitempty"`
	Method   string            `json:"method,omitempty"`
	Path     string            `json:"path,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	Body     string            `json:"body,omitempty"`
	Status   int               `json:"status,omitempty"`
	Error    string            `json:"error,omitempty"`
}
