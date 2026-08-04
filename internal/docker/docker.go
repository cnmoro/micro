// Package docker provides a thin wrapper around the docker/docker-compose
// CLI for listing and managing containers, images, networks, volumes and
// compose projects. Everything shells out to the `docker` binary and parses
// its `--format json` output; there is no dependency on the Docker daemon
// API/SDK.
package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"time"
)

const cmdTimeout = 8 * time.Second
const remoteCmdTimeout = 20 * time.Second

// Container is a row from `docker ps -a`.
type Container struct {
	ID      string
	Names   string
	Image   string
	Command string
	Status  string
	State   string
	Ports   string
	Labels  string
}

// ComposeProject returns the value of the com.docker.compose.project label,
// or "" if this container was not started by compose.
func (c Container) ComposeProject() string {
	return labelValue(c.Labels, "com.docker.compose.project")
}

// Running reports whether the container is currently running.
func (c Container) Running() bool {
	return strings.EqualFold(c.State, "running")
}

// Image is a row from `docker images`.
type Image struct {
	ID         string
	Repository string
	Tag        string
	Size       string
	CreatedAt  string `json:"CreatedSince"`
}

// Network is a row from `docker network ls`.
type Network struct {
	ID     string
	Name   string
	Driver string
	Scope  string
}

// Volume is a row from `docker volume ls`.
type Volume struct {
	Name       string
	Driver     string
	Mountpoint string
}

// ComposeProject is a row from `docker compose ls`.
type ComposeService struct {
	Name        string
	Status      string
	ConfigFiles string
}

func labelValue(labels, key string) string {
	for _, kv := range strings.Split(labels, ",") {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 && parts[0] == key {
			return parts[1]
		}
	}
	return ""
}

// RemoteHost, when non-empty, is an SSH destination (e.g. "user@host" or a
// ~/.ssh/config alias) that every docker CLI invocation is pointed at via
// `-H ssh://<RemoteHost>`, using the Docker CLI's own built-in SSH
// transport - the same mechanism `docker context create --docker
// host=ssh://...` uses - so no separate remote Docker API client is needed.
var RemoteHost string

func run(args ...string) ([]byte, error) {
	timeout := cmdTimeout
	if RemoteHost != "" {
		args = append([]string{"-H", "ssh://" + RemoteHost}, args...)
		timeout = remoteCmdTimeout
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(errOut.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.Bytes(), &CmdError{Args: args, Msg: msg}
	}
	return out.Bytes(), nil
}

// CmdError wraps a failed docker CLI invocation with the command's stderr.
type CmdError struct {
	Args []string
	Msg  string
}

func (e *CmdError) Error() string {
	return "docker " + strings.Join(e.Args, " ") + ": " + e.Msg
}

// parseNDJSON parses newline-delimited JSON objects, as produced by
// `docker ... --format '{{json .}}'`, into a slice of T.
func parseNDJSON[T any](data []byte) ([]T, error) {
	var out []T
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var v T
		if err := json.Unmarshal(line, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// Available reports whether the docker CLI is installed and the daemon is
// reachable.
func Available() (bool, string) {
	out, err := run("info", "--format", "{{.ServerVersion}}")
	if err != nil {
		if _, lookErr := exec.LookPath("docker"); lookErr != nil {
			return false, "docker is not installed"
		}
		return false, "docker daemon is not reachable"
	}
	return true, strings.TrimSpace(string(out))
}

func ListContainers() ([]Container, error) {
	out, err := run("ps", "-a", "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	return parseNDJSON[Container](out)
}

func ListImages() ([]Image, error) {
	out, err := run("images", "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	return parseNDJSON[Image](out)
}

func ListNetworks() ([]Network, error) {
	out, err := run("network", "ls", "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	return parseNDJSON[Network](out)
}

func ListVolumes() ([]Volume, error) {
	out, err := run("volume", "ls", "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	return parseNDJSON[Volume](out)
}

// ListCompose lists compose projects known to the docker CLI (`docker
// compose ls`, which returns a single JSON array rather than NDJSON).
func ListCompose() ([]ComposeService, error) {
	out, err := run("compose", "ls", "--format", "json")
	if err != nil {
		// docker-compose plugin may not be installed; treat as "no projects"
		return nil, nil
	}
	var services []ComposeService
	out = bytes.TrimSpace(out)
	if len(out) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(out, &services); err != nil {
		return nil, err
	}
	return services, nil
}

func StopContainer(id string) error    { _, err := run("stop", id); return err }
func StartContainer(id string) error   { _, err := run("start", id); return err }
func RestartContainer(id string) error { _, err := run("restart", id); return err }
func RemoveContainer(id string, force bool) error {
	args := []string{"rm"}
	if force {
		args = append(args, "-f")
	}
	args = append(args, id)
	_, err := run(args...)
	return err
}

func RemoveImage(id string, force bool) error {
	args := []string{"rmi"}
	if force {
		args = append(args, "-f")
	}
	args = append(args, id)
	_, err := run(args...)
	return err
}

func RemoveNetwork(id string) error {
	_, err := run("network", "rm", id)
	return err
}

func RemoveVolume(name string, force bool) error {
	args := []string{"volume", "rm"}
	if force {
		args = append(args, "-f")
	}
	args = append(args, name)
	_, err := run(args...)
	return err
}

func PruneContainers() error {
	_, err := run("container", "prune", "-f")
	return err
}

func PruneImages() error {
	_, err := run("image", "prune", "-f")
	return err
}

func PruneVolumes() error {
	_, err := run("volume", "prune", "-f")
	return err
}

func PruneNetworks() error {
	_, err := run("network", "prune", "-f")
	return err
}

// Inspect returns pretty-printed JSON for the given resource kind
// ("container", "image", "network", "volume") and id/name.
func Inspect(kind, id string) (string, error) {
	out, err := run(kind, "inspect", id)
	if err != nil {
		return "", err
	}
	var v any
	if jerr := json.Unmarshal(out, &v); jerr == nil {
		pretty, perr := json.MarshalIndent(v, "", "  ")
		if perr == nil {
			return string(pretty), nil
		}
	}
	return string(out), nil
}

// LogsArgs returns the argv (excluding "docker" itself) to stream logs for
// a container, suitable for handing to the integrated terminal panel.
func LogsArgs(id string) []string {
	return []string{"docker", "logs", "-f", "--tail", "200", id}
}

// ExecArgs returns the argv to open an interactive shell inside a running
// container, trying bash and falling back to sh.
func ExecArgs(id string) []string {
	return []string{"docker", "exec", "-it", id, "sh", "-c", "exec bash || exec sh"}
}
