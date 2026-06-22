package server

import "fmt"

// ErrCode classifies tool-level failures for agent-side parsing.
type ErrCode string

const (
	ErrAuth        ErrCode = "AUTH"
	ErrNotFound    ErrCode = "NOT_FOUND"
	ErrForbidden   ErrCode = "FORBIDDEN"
	ErrRateLimit   ErrCode = "RATE_LIMIT"
	ErrTransient   ErrCode = "TRANSIENT"
	ErrValidation  ErrCode = "VALIDATION"
	ErrExternalCI  ErrCode = "EXTERNAL_CI"
	ErrUnavailable ErrCode = "UNAVAILABLE"
	ErrGeneric     ErrCode = "GENERIC"
)

// formatToolError renders a plain-text error the agent can parse:
// ERROR: <code> | <message> | hint: <next action>
func formatToolError(code ErrCode, message, hint string) string {
	return fmt.Sprintf("ERROR: %s | %s | hint: %s", code, message, hint)
}

// formatToolOK prefixes successful compact responses when a machine-readable
// header helps the client distinguish success from failure.
func formatToolOK(summary string) string {
	return "OK: " + summary
}
