package ui

// ConnectionState describes the app's connection to an external service.
type ConnectionState int

const (
	ConnectionDisconnected ConnectionState = iota
	ConnectionConnecting
	ConnectionConnected
	ConnectionNotConfigured
	ConnectionError
)

// Marker returns the text-based status marker used throughout the UI. The
// shapes themselves carry the meaning, so it survives NO_COLOR terminals.
func (s ConnectionState) Marker() string {
	switch s {
	case ConnectionConnected:
		return "●"
	case ConnectionError:
		return "✗"
	default:
		return "○"
	}
}
