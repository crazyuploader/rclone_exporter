package rclone

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// RcloneSizeOutput represents the JSON output of `rclone size --json`.
type RcloneSizeOutput struct {
	Count int64 `json:"count"` // Total number of objects
	Bytes int64 `json:"bytes"` // Total size in bytes
}

// Client defines the interface for interacting with the rclone binary.
type Client interface {
	GetRemoteSize(remoteName string) (*RcloneSizeOutput, error)
	CheckBinaryAvailable() error
	GetVersion() (string, error)
}

// rcloneClient implements the Client interface.
type rcloneClient struct {
	binaryPath string
	timeout    time.Duration
}

// NewRcloneClient returns a default rclone client with standard settings.
func NewRcloneClient() Client {
	return &rcloneClient{
		binaryPath: "rclone",
		timeout:    2 * time.Minute,
	}
}

// NewRcloneClientWithConfig returns a customizable rclone client.
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

// CheckBinaryAvailable verifies that rclone is executable and accessible.
func (c *rcloneClient) CheckBinaryAvailable() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.binaryPath, "version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Error().
			Err(err).
			Str("output", string(output)).
			Str("path", c.binaryPath).
			Msg("rclone binary check failed")
		return fmt.Errorf("rclone not available or not executable: %w", err)
	}

	version := extractFirstLine(string(output))
	log.Info().Str("version", version).Msg("rclone binary is available")
	return nil
}

// GetVersion returns the first line from `rclone version` output.
func (c *rcloneClient) GetVersion() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.binaryPath, "version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Error().
			Err(err).
			Str("path", c.binaryPath).
			Str("output", string(output)).
			Msg("failed to get rclone version")
		return "", fmt.Errorf("failed to get rclone version: %w", err)
	}

	return extractFirstLine(string(output)), nil
}

// extractFirstLine returns the first line of a string (used for version output).
func extractFirstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx != -1 {
		return s[:idx]
	}
	return s
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
				Msg("rclone size command failed")
			return nil, fmt.Errorf("rclone command failed: %s", string(output))
		}
		log.Error().Err(err).Str("remote", remote).Msg("failed to start rclone command")
		return nil, fmt.Errorf("failed to run rclone: %w", err)
	}

	var result RcloneSizeOutput
	if err := json.Unmarshal(output, &result); err != nil {
		log.Error().
			Err(err).
			Str("remote", remote).
			Str("raw_output", string(output)).
			Msg("failed to parse rclone JSON")
		return nil, fmt.Errorf("invalid rclone JSON output for '%s': %w", remote, err)
	}

	log.Debug().
		Str("remote", remote).
		Int64("bytes", result.Bytes).
		Int64("count", result.Count).
		Msg("rclone probe successful")

	return &result, nil
}
