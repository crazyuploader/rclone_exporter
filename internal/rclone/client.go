package rclone

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// RcloneSizeOutput represents the JSON output of `rclone size --json`.
type RcloneSizeOutput struct {
	Count int64 `json:"count"` // Total number of objects
	Bytes int64 `json:"bytes"` // Total size in bytes
}

// RemoteInfo contains metadata about an rclone remote
type RemoteInfo struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Source      string `json:"source,omitempty"`
	Description string `json:"description,omitempty"`
}

// RemoteSizeWithType extends RcloneSizeOutput with type information
type RemoteSizeWithType struct {
	*RcloneSizeOutput
	RemoteType string
}

// Client defines the interface for interacting with the rclone binary.
type Client interface {
	GetRemoteSize(remoteName string) (*RcloneSizeOutput, error)
	GetRemoteSizeWithType(remoteName string) (*RemoteSizeWithType, error)
	CheckBinaryAvailable() error
	GetVersion() (string, error)
	ListRemotes() ([]RemoteInfo, error)
	GetRemoteType(remoteName string) (string, error)
	InvalidateCache(remoteName string)
	ClearCache()
}

// rcloneClient implements the Client interface.
type rcloneClient struct {
	binaryPath string
	timeout    time.Duration

	// Cache for remote types to avoid repeated config lookups
	remoteTypeCache map[string]string
	cacheMu         sync.RWMutex
	cacheExpiry     time.Duration
	cacheTimestamps map[string]time.Time
}

// NewRcloneClient returns a default rclone client with standard settings.
func NewRcloneClient() Client {
	return &rcloneClient{
		binaryPath:      "rclone",
		timeout:         2 * time.Minute,
		remoteTypeCache: make(map[string]string),
		cacheTimestamps: make(map[string]time.Time),
		cacheExpiry:     5 * time.Minute, // Cache remote types for 5 minutes
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
		binaryPath:      path,
		timeout:         timeout,
		remoteTypeCache: make(map[string]string),
		cacheTimestamps: make(map[string]time.Time),
		cacheExpiry:     5 * time.Minute,
	}
}

// GetRemoteType retrieves the type of a remote from rclone config
func (c *rcloneClient) GetRemoteType(remoteName string) (string, error) {
	// Remove trailing colon if present
	remoteName = strings.TrimSuffix(remoteName, ":")

	// Check cache first
	c.cacheMu.RLock()
	if cachedType, exists := c.remoteTypeCache[remoteName]; exists {
		if time.Since(c.cacheTimestamps[remoteName]) < c.cacheExpiry {
			c.cacheMu.RUnlock()
			log.Debug().
				Str("remote", remoteName).
				Str("type", cachedType).
				Msg("Using cached remote type")
			return cachedType, nil
		}
	}
	c.cacheMu.RUnlock()

	// Fetch from rclone config
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Use `rclone config dump` to get all remote configurations in JSON format
	cmd := exec.CommandContext(ctx, c.binaryPath, "config", "dump")
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Error().
			Err(err).
			Str("remote", remoteName).
			Str("output", string(output)).
			Msg("Failed to dump rclone config")
		return "unknown", fmt.Errorf("failed to get rclone config: %w", err)
	}

	// Handle empty config
	if len(output) == 0 || string(output) == "{}\n" || string(output) == "{}" {
		log.Warn().
			Str("remote", remoteName).
			Msg("Rclone config is empty")
		return "unknown", fmt.Errorf("rclone config is empty")
	}

	// Parse the JSON output
	var configs map[string]map[string]interface{}
	if err := json.Unmarshal(output, &configs); err != nil {
		log.Error().
			Err(err).
			Str("raw_output", string(output)).
			Msg("Failed to parse rclone config dump")
		return "unknown", fmt.Errorf("invalid rclone config JSON: %w", err)
	}

	// Look up the remote
	remoteConfig, exists := configs[remoteName]
	if !exists {
		log.Warn().
			Str("remote", remoteName).
			Int("available_remotes", len(configs)).
			Msg("Remote not found in config")
		return "unknown", fmt.Errorf("remote '%s' not found in config", remoteName)
	}

	// Extract the type
	remoteTypeInterface, hasType := remoteConfig["type"]
	if !hasType {
		log.Warn().
			Str("remote", remoteName).
			Msg("Remote config missing 'type' field")
		return "unknown", fmt.Errorf("remote '%s' has no type field", remoteName)
	}

	remoteType, ok := remoteTypeInterface.(string)
	if !ok {
		return "unknown", fmt.Errorf("remote '%s' type is not a string", remoteName)
	}

	// Update cache
	c.cacheMu.Lock()
	c.remoteTypeCache[remoteName] = remoteType
	c.cacheTimestamps[remoteName] = time.Now()
	c.cacheMu.Unlock()

	log.Debug().
		Str("remote", remoteName).
		Str("type", remoteType).
		Msg("Detected remote type")

	return remoteType, nil
}

// GetRemoteSizeWithType combines size information with remote type
func (c *rcloneClient) GetRemoteSizeWithType(remoteName string) (*RemoteSizeWithType, error) {
	// Get size
	sizeOutput, err := c.GetRemoteSize(remoteName)
	if err != nil {
		return nil, err
	}

	// Get type (best effort - don't fail if type detection fails)
	remoteType, typeErr := c.GetRemoteType(remoteName)
	if typeErr != nil {
		log.Warn().
			Err(typeErr).
			Str("remote", remoteName).
			Msg("Failed to detect remote type, using 'unknown'")
		remoteType = "unknown"
	}

	return &RemoteSizeWithType{
		RcloneSizeOutput: sizeOutput,
		RemoteType:       remoteType,
	}, nil
}

// InvalidateCache removes a specific remote from the type cache
func (c *rcloneClient) InvalidateCache(remoteName string) {
	remoteName = strings.TrimSuffix(remoteName, ":")
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()

	delete(c.remoteTypeCache, remoteName)
	delete(c.cacheTimestamps, remoteName)

	log.Debug().
		Str("remote", remoteName).
		Msg("Invalidated cache for remote")
}

// ClearCache clears the entire remote type cache
func (c *rcloneClient) ClearCache() {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()

	c.remoteTypeCache = make(map[string]string)
	c.cacheTimestamps = make(map[string]time.Time)

	log.Debug().Msg("Cleared entire remote type cache")
}

// ListRemotes runs `rclone listremotes --long --json` and returns the list of remotes with details.
func (c *rcloneClient) ListRemotes() ([]RemoteInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Try with --long flag first for more details
	cmd := exec.CommandContext(ctx, c.binaryPath, "listremotes", "--long", "--json")
	output, err := cmd.CombinedOutput()

	// Fallback to basic listremotes if --long is not supported
	if err != nil {
		log.Debug().Msg("Falling back to basic listremotes (--long not supported)")
		cmd = exec.CommandContext(ctx, c.binaryPath, "listremotes", "--json")
		output, err = cmd.CombinedOutput()
		if err != nil {
			log.Error().
				Err(err).
				Str("output", string(output)).
				Str("path", c.binaryPath).
				Msg("Failed to list rclone remotes")
			return nil, fmt.Errorf("failed to list rclone remotes: %w", err)
		}
	}

	// Handle empty output
	if len(output) == 0 || string(output) == "[]\n" || string(output) == "[]" {
		log.Info().Msg("No rclone remotes configured")
		return []RemoteInfo{}, nil
	}

	var remotes []RemoteInfo
	if err := json.Unmarshal(output, &remotes); err != nil {
		// Try parsing as simple string array (older rclone versions)
		var remoteNames []string
		if err := json.Unmarshal(output, &remoteNames); err != nil {
			log.Error().
				Err(err).
				Str("raw_output", string(output)).
				Msg("Failed to parse rclone listremotes JSON output")
			return nil, fmt.Errorf("invalid rclone listremotes JSON output: %w", err)
		}

		// Convert string array to RemoteInfo array
		remotes = make([]RemoteInfo, len(remoteNames))
		for i, name := range remoteNames {
			name = strings.TrimSuffix(name, ":")
			remotes[i] = RemoteInfo{
				Name: name,
				Type: "unknown",
			}
		}
	}

	// Enrich with type information from cache or config
	for i := range remotes {
		if remotes[i].Type == "" || remotes[i].Type == "unknown" {
			if remoteType, err := c.GetRemoteType(remotes[i].Name); err == nil {
				remotes[i].Type = remoteType
			}
		}
	}

	log.Debug().
		Int("count", len(remotes)).
		Msg("Listed rclone remotes")

	return remotes, nil
}

// CheckBinaryAvailable verifies that rclone is executable and accessible.
func (c *rcloneClient) CheckBinaryAvailable() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Resolve the full path to the rclone binary
	resolvedPath, lookErr := exec.LookPath(c.binaryPath)
	if lookErr != nil {
		log.Error().
			Err(lookErr).
			Str("path", c.binaryPath).
			Msg("Failed to find rclone binary in PATH")
		return fmt.Errorf("rclone binary not found in PATH: %w", lookErr)
	}

	// Update internal binary path to the resolved absolute path
	c.binaryPath = resolvedPath

	cmd := exec.CommandContext(ctx, c.binaryPath, "version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Error().
			Err(err).
			Str("output", string(output)).
			Str("path", c.binaryPath).
			Msg("Rclone binary check failed")
		return fmt.Errorf("rclone not available or not executable at '%s': %w", c.binaryPath, err)
	}

	version := extractFirstLine(string(output))
	log.Info().
		Str("version", version).
		Str("path", c.binaryPath).
		Str("resolved_path", resolvedPath).
		Msg("Rclone binary is available")
	return nil
}

// GetVersion returns the first line from `rclone version` output.
func (c *rcloneClient) GetVersion() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.binaryPath, "version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Error().
			Err(err).
			Str("path", c.binaryPath).
			Str("output", string(output)).
			Msg("Failed to get rclone version")
		return "", fmt.Errorf("failed to get rclone version from '%s': %w", c.binaryPath, err)
	}

	return extractFirstLine(string(output)), nil
}

// extractFirstLine returns the first line of a string (used for version output).
func extractFirstLine(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexByte(s, '\n'); idx != -1 {
		return strings.TrimSpace(s[:idx])
	}

	return s
}

// GetRemoteSize runs `rclone size --json` and parses the output.
func (c *rcloneClient) GetRemoteSize(remote string) (*RcloneSizeOutput, error) {
	if remote == "" {
		return nil, fmt.Errorf("remote name cannot be empty")
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	// Use --fast-list and --no-traverse for better performance
	cmd := exec.CommandContext(ctx, c.binaryPath, "size", remote, "--json", "--fast-list")

	log.Debug().
		Str("remote", remote).
		Str("command", cmd.String()).
		Dur("timeout", c.timeout).
		Msg("Executing rclone size command")

	startTime := time.Now()
	output, err := cmd.CombinedOutput()
	duration := time.Since(startTime)

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			log.Error().
				Str("remote", remote).
				Dur("timeout", c.timeout).
				Dur("actual_duration", duration).
				Msg("Rclone command timed out")
			return nil, fmt.Errorf("rclone command timed out after %v for remote '%s'", c.timeout, remote)
		}

		if exitErr, ok := err.(*exec.ExitError); ok {
			log.Error().
				Int("exit_code", exitErr.ExitCode()).
				Str("remote", remote).
				Str("stderr", string(output)).
				Dur("duration", duration).
				Msg("Rclone size command failed")
			return nil, fmt.Errorf("rclone command failed for remote '%s' (exit code %d): %s",
				remote, exitErr.ExitCode(), strings.TrimSpace(string(output)))
		}

		log.Error().
			Err(err).
			Str("remote", remote).
			Dur("duration", duration).
			Msg("Failed to start rclone command")
		return nil, fmt.Errorf("failed to run rclone for remote '%s': %w", remote, err)
	}

	if len(output) == 0 {
		log.Error().
			Str("remote", remote).
			Dur("duration", duration).
			Msg("Rclone returned empty output")
		return nil, fmt.Errorf("rclone returned empty output for remote '%s'", remote)
	}

	var result RcloneSizeOutput
	if err := json.Unmarshal(output, &result); err != nil {
		log.Error().
			Err(err).
			Str("remote", remote).
			Str("raw_output", string(output)).
			Dur("duration", duration).
			Msg("Failed to parse rclone JSON output")
		return nil, fmt.Errorf("invalid rclone JSON output for remote '%s': %w", remote, err)
	}

	// Validate the result
	if result.Bytes < 0 || result.Count < 0 {
		log.Warn().
			Str("remote", remote).
			Int64("bytes", result.Bytes).
			Int64("count", result.Count).
			Dur("duration", duration).
			Msg("Rclone returned negative values")
		return nil, fmt.Errorf("rclone returned invalid negative values for remote '%s'", remote)
	}

	log.Debug().
		Str("remote", remote).
		Int64("bytes", result.Bytes).
		Int64("count", result.Count).
		Dur("duration", duration).
		Msg("Rclone probe successful")

	return &result, nil
}
