package rclone

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"github.com/rs/zerolog/log"
)

// RcloneSizeOutput represents the JSON output of `rclone size --json`.
type RcloneSizeOutput struct {
	Count int64 `json:"count"`
	Bytes int64 `json:"bytes"`
}

// Client defines the rclone interaction interface.
type Client interface {
	GetRemoteSize(remoteName string) (*RcloneSizeOutput, error)
}

// rcloneClient implements the Client interface.
type rcloneClient struct {
	binaryPath string
	timeout    time.Duration
}

// NewRcloneClient returns a client with default settings.
func NewRcloneClient() Client {
	return &rcloneClient{
		binaryPath: "rclone",
		timeout:    2 * time.Minute,
	}
}

// NewRcloneClientWithConfig returns a client with custom rclone binary path and timeout.
func NewRcloneClientWithConfig(path string, timeout time.Duration) Client {
	if path == "" {
		path = "rclone"
	}
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	return &rcloneClient{
		binaryPath: path,
		timeout:    timeout,
	}
}

// GetRemoteSize runs `rclone size <remote> --json` and parses the output.
func (c *rcloneClient) GetRemoteSize(remote string) (*RcloneSizeOutput, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.binaryPath, "size", remote, "--json")
	output, err := cmd.CombinedOutput()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			log.Error().
				Int("exit_code", exitErr.ExitCode()).
				Str("remote", remote).
				Str("output", string(output)).
				Msg("rclone command failed")
			return nil, fmt.Errorf("rclone command failed: %s", string(output))
		}
		log.Error().Err(err).Str("remote", remote).Msg("failed to start rclone")
		return nil, fmt.Errorf("failed to run rclone: %w", err)
	}

	var result RcloneSizeOutput
	if err := json.Unmarshal(output, &result); err != nil {
		log.Error().
			Err(err).
			Str("remote", remote).
			Str("raw_output", string(output)).
			Msg("failed to parse JSON")
		return nil, fmt.Errorf("invalid rclone JSON output for '%s': %w", remote, err)
	}

	log.Debug().Str("remote", remote).Int64("bytes", result.Bytes).Int64("count", result.Count).Msg("rclone probe successful")
	return &result, nil
}
