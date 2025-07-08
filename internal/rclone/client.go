package rclone

import (
	"context"       // For managing command context (cancellation, timeouts)
	"encoding/json" // For marshaling/unmarshaling JSON data
	"fmt"           // For formatted error messages
	"os/exec"       // For executing external commands
	"time"          // For context timeouts (optional but good practice)
)

// RcloneSizeOutput represents the JSON structure returned by `rclone size --json`.
// We use struct tags (`json:"count"`) to map JSON keys to Go struct fields.
// This ensures correct parsing even if Go field names differ (though here they match).
type RcloneSizeOutput struct {
	Count int64 `json:"count"` // Total number of objects
	Bytes int64 `json:"bytes"` // Total size in bytes
}

// Client is an interface that defines the contract for interacting with rclone.
// By defining an interface, we achieve dependency inversion and testability.
// Any type that implements GetRemoteSize (and any other methods added later)
// will satisfy this interface.
type Client interface {
	GetRemoteSize(remoteName string) (*RcloneSizeOutput, error)
	// You could add more methods here as you expand:
	// ListRemotes() ([]string, error)
	// GetRemoteAbout(remoteName string) (*RcloneAboutOutput, error)
}

// NewRcloneClient creates and returns a new default rcloneClient implementation.
// This is a "constructor" function. It returns the interface type,
// hiding the underlying concrete implementation.
func NewRcloneClient() Client {
	return &rcloneClient{} // Return a pointer to the concrete type
}

// rcloneClient is the concrete implementation of the Client interface.
// It's intentionally kept unexported (lowercase 'r') because it's only used
// within this package and returned via the Client interface.
type rcloneClient struct{}

// GetRemoteSize executes `rclone size <remoteName> --json` and parses its output.
// It takes a context to allow for cancellation/timeouts of the external command.
func (c *rcloneClient) GetRemoteSize(remoteName string) (*RcloneSizeOutput, error) {
	// Best Practice: Use context.WithTimeout for external commands.
	// This prevents the command from running indefinitely if rclone hangs or network issues.
	// The timeout duration should be configurable, or determined based on expected run times.
	// For now, let's use a sensible default.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute) // 2-minute timeout
	defer cancel()                                                          // Always call cancel to release context resources

	// Construct the rclone command.
	// We use CommandContext to link the command's lifecycle to the context.
	cmd := exec.CommandContext(ctx, "rclone", "size", remoteName, "--json")

	// Execute the command and capture its combined output (stdout + stderr).
	// Using CombinedOutput is often helpful for debugging, as rclone sometimes
	// prints errors to stderr even when exiting with a non-zero status.
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Error Handling: Distinguish between different types of errors from exec.Command.
		// exec.ExitError is specifically for when the command runs but exits with a non-zero status.
		if exitErr, ok := err.(*exec.ExitError); ok {
			// If it's an ExitError, we can get the exit code and stderr output.
			// This provides richer error information.
			return nil, fmt.Errorf("rclone command 'size %s --json' failed (exit status %d): %s",
				remoteName, exitErr.ExitCode(), string(output)) // Use 'output' here for combined stdout/stderr
		}
		// For other errors (e.g., command not found, permission denied to execute rclone),
		// we just return the original error wrapped for context.
		return nil, fmt.Errorf("failed to execute rclone command 'size %s --json': %w", remoteName, err)
	}

	var rcloneOutput RcloneSizeOutput
	// Unmarshal the JSON output into our Go struct.
	// This is where `encoding/json` comes into play.
	err = json.Unmarshal(output, &rcloneOutput)
	if err != nil {
		// If JSON unmarshaling fails, it's a parsing error.
		return nil, fmt.Errorf("failed to unmarshal rclone JSON output for '%s': %w, raw output: %s",
			remoteName, err, string(output))
	}

	return &rcloneOutput, nil // Return the successfully parsed data and a nil error
}
