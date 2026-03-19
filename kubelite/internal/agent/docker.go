package agent

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	kl "github.com/Avik-creator/pkg/types"
)

// DockerRunner wraps the Docker SDK and provides the operations
// the agent needs: run, stop, status, logs.
type DockerRunner struct {
	cli *client.Client
}

func NewDockerRunner() (*DockerRunner, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("connecting to Docker: %w", err)
	}
	return &DockerRunner{cli: cli}, nil
}

// Run pulls the image if needed, creates, and starts a container.
// Returns the Docker container ID.
func (d *DockerRunner) Run(ctx context.Context, req kl.RunRequest) (string, error) {
	// Pull image (no-op if already present)
	reader, err := d.cli.ImagePull(ctx, req.Image, client.ImagePullOptions{})
	if err != nil {
		return "", fmt.Errorf("pulling image %s: %w", req.Image, err)
	}
	io.Copy(io.Discard, reader) // drain pull output
	reader.Close()

	// Build port bindings from the RunRequest
	portBindings := network.PortMap{}
	exposedPorts := network.PortSet{}
	for _, p := range req.Ports {
		proto := strings.ToLower(p.Protocol)
		if proto == "" {
			proto = "tcp"
		}
		containerPort := network.MustParsePort(fmt.Sprintf("%d/%s", p.ContainerPort, proto))
		exposedPorts[containerPort] = struct{}{}
		portBindings[containerPort] = []network.PortBinding{
			{HostPort: fmt.Sprintf("%d", p.HostPort)},
		}
	}

	// Convert env map to KEY=VALUE slice
	envSlice := make([]string, 0, len(req.Env))
	for k, v := range req.Env {
		envSlice = append(envSlice, fmt.Sprintf("%s=%s", k, v))
	}

	containerCfg := &container.Config{
		Image:        req.Image,
		Env:          envSlice,
		ExposedPorts: exposedPorts,
	}
	hostCfg := &container.HostConfig{
		PortBindings: portBindings,
		// AutoRemove: false — we need to inspect exited containers for exit codes
	}

	resp, err := d.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config:     containerCfg,
		HostConfig: hostCfg,
		Name:       req.Name,
	})
	if err != nil {
		return "", fmt.Errorf("creating container %s: %w", req.Name, err)
	}

	if _, err := d.cli.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{}); err != nil {
		return "", fmt.Errorf("starting container %s: %w", resp.ID, err)
	}

	return resp.ID, nil
}

// Status inspects a container and returns its current state.
func (d *DockerRunner) Status(ctx context.Context, containerID string) (kl.ContainerState, int, error) {
	info, err := d.cli.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	if err != nil {
		return kl.ContainerUnknown, 0, fmt.Errorf("inspecting %s: %w", containerID, err)
	}
	if info.Container.State == nil {
		return kl.ContainerUnknown, 0, nil
	}

	switch {
	case info.Container.State.Running:
		return kl.ContainerRunning, 0, nil
	case info.Container.State.ExitCode != 0:
		return kl.ContainerExited, info.Container.State.ExitCode, nil
	default:
		return kl.ContainerStopped, info.Container.State.ExitCode, nil
	}
}

// Stop stops a container (with a 10s grace period) then removes it.
func (d *DockerRunner) Stop(ctx context.Context, containerID string) error {
	timeout := 10
	if _, err := d.cli.ContainerStop(ctx, containerID, client.ContainerStopOptions{Timeout: &timeout}); err != nil {
		return fmt.Errorf("stopping %s: %w", containerID, err)
	}
	if _, err := d.cli.ContainerRemove(ctx, containerID, client.ContainerRemoveOptions{}); err != nil {
		return fmt.Errorf("removing %s: %w", containerID, err)
	}
	return nil
}

// Logs returns a stream of stdout+stderr for a container.
// tail controls how many lines from the end to start from ("all" for everything).
// follow=true keeps the stream open as new output arrives.
func (d *DockerRunner) Logs(ctx context.Context, containerID string, tail string, follow bool) (io.ReadCloser, error) {
	opts := client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     follow,
		Tail:       tail,
	}
	return d.cli.ContainerLogs(ctx, containerID, opts)
}

// GetContainerIP returns the primary IP address of a running container.
// Tries bridge network first, then the first network found.
func (d *DockerRunner) GetContainerIP(ctx context.Context, containerID string) (string, error) {
	info, err := d.cli.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	if err != nil {
		return "", fmt.Errorf("inspecting %s: %w", containerID, err)
	}
	if info.Container.NetworkSettings == nil {
		return "", nil
	}
	// Prefer bridge network
	if bridge, ok := info.Container.NetworkSettings.Networks["bridge"]; ok && bridge.IPAddress.IsValid() {
		return bridge.IPAddress.String(), nil
	}
	// Fall back to first available network
	for _, net := range info.Container.NetworkSettings.Networks {
		if net.IPAddress.IsValid() {
			return net.IPAddress.String(), nil
		}
	}
	return "", nil
}

// ListRunning returns all containers currently running on this node.
// Used to build the heartbeat payload.
func (d *DockerRunner) ListRunning(ctx context.Context) ([]container.Summary, error) {
	resp, err := d.cli.ContainerList(ctx, client.ContainerListOptions{})
	if err != nil {
		return nil, err
	}
	return resp.Items, nil
}
