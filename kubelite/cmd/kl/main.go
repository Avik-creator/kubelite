// kl is a thin CLI client for the kube-lite scheduler.
//
// Usage:
//
//	kl [--server <addr>] <command> [flags]
//
// Commands:
//
//	nodes                                 list all registered nodes
//	deploy  --name N --image I            deploy a workload
//	        [--id ID] [--replicas N]
//	        [--restart-policy POLICY]
//	        [--port HOST:CONTAINER ...]
//	        [--env KEY=VALUE ...]
//	        [--health-path /path --health-port P]
//	workloads                             list all workloads
//	status   <workload-id>                show workload detail + running instances
//	scale    <workload-id> --replicas N   change replica count
//	delete   <workload-id>                stop all containers and remove workload
//	logs     <container-id>               stream container logs
//	         [--tail N] [--no-follow]
//	rollout  <workload-id> --image IMAGE  start a rolling image update
//	         [--max-unavailable N] [--max-surge N]
//	rollout-status <workload-id>          show current rollout state
//	discover <workload-name>              list live service endpoints
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	kl "github.com/Avik-creator/pkg/types"
)

var serverAddr string

func main() {
	flag.StringVar(&serverAddr, "server", env("KL_SERVER", "http://localhost:8080"), "scheduler address")
	flag.Usage = usage
	flag.Parse()

	if flag.NArg() == 0 {
		usage()
		os.Exit(1)
	}

	cmd := flag.Arg(0)
	args := flag.Args()[1:]

	var err error
	switch cmd {
	case "nodes":
		err = cmdNodes(args)
	case "deploy":
		err = cmdDeploy(args)
	case "workloads":
		err = cmdWorkloads(args)
	case "status":
		err = cmdStatus(args)
	case "scale":
		err = cmdScale(args)
	case "delete":
		err = cmdDelete(args)
	case "logs":
		err = cmdLogs(args)
	case "rollout":
		err = cmdRollout(args)
	case "rollout-status":
		err = cmdRolloutStatus(args)
	case "discover":
		err = cmdDiscover(args)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		usage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// ─── nodes ───────────────────────────────────────────────────────────────────

func cmdNodes(_ []string) error {
	var nodes []kl.Node
	if err := getJSON("/nodes", &nodes); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tADDRESS\tSTATUS\tCPU\tMEM (MB)\tLAST HEARTBEAT")
	for _, n := range nodes {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%s\n",
			n.ID, n.Address, n.Status, n.CPUCores, n.MemoryMB,
			n.LastHeartbeat.Format("15:04:05"))
	}
	return tw.Flush()
}

// ─── deploy ──────────────────────────────────────────────────────────────────

type multiString []string

func (m *multiString) String() string  { return strings.Join(*m, ",") }
func (m *multiString) Set(v string) error { *m = append(*m, v); return nil }

func cmdDeploy(args []string) error {
	fs := flag.NewFlagSet("deploy", flag.ExitOnError)
	id := fs.String("id", "", "workload ID (auto-generated if empty)")
	name := fs.String("name", "", "workload name (required)")
	image := fs.String("image", "", "container image (required)")
	replicas := fs.Int("replicas", 1, "desired replica count")
	restart := fs.String("restart-policy", "Always", "Always|OnFailure|Never")
	healthPath := fs.String("health-path", "", "HTTP health check path (e.g. /health)")
	healthPort := fs.Int("health-port", 0, "HTTP health check port")
	var ports multiString
	var envVars multiString
	fs.Var(&ports, "port", "port mapping HOST:CONTAINER (repeatable)")
	fs.Var(&envVars, "env", "env var KEY=VALUE (repeatable)")
	fs.Parse(args)

	if *name == "" || *image == "" {
		return fmt.Errorf("--name and --image are required")
	}

	spec := kl.WorkloadSpec{
		ID:            *id,
		Name:          *name,
		Image:         *image,
		Replicas:      *replicas,
		RestartPolicy: kl.RestartPolicy(*restart),
		Env:           parseEnvVars(envVars),
		Ports:         parsePorts(ports),
	}
	if *healthPath != "" && *healthPort > 0 {
		spec.HealthCheck = &kl.HealthCheckSpec{
			Path:             *healthPath,
			Port:             *healthPort,
			IntervalSeconds:  10,
			TimeoutSeconds:   5,
			FailureThreshold: 3,
		}
	}

	var resp map[string]string
	if err := postJSON("/deploy", spec, &resp); err != nil {
		return err
	}
	fmt.Println("deployed:", resp["id"])
	return nil
}

// ─── workloads ────────────────────────────────────────────────────────────────

func cmdWorkloads(_ []string) error {
	type row struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Image    string `json:"image"`
		Replicas int    `json:"replicas"`
		Running  int    `json:"running"`
	}
	var rows []row
	if err := getJSON("/workloads", &rows); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tIMAGE\tDESIRED\tRUNNING")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\n", r.ID, r.Name, r.Image, r.Replicas, r.Running)
	}
	return tw.Flush()
}

// ─── status ──────────────────────────────────────────────────────────────────

func cmdStatus(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: kl status <workload-id>")
	}
	var resp struct {
		Spec      kl.WorkloadSpec        `json:"spec"`
		Instances []kl.ContainerInstance `json:"instances"`
	}
	if err := getJSON("/workloads/"+args[0], &resp); err != nil {
		return err
	}
	fmt.Printf("Workload:  %s (%s)\n", resp.Spec.Name, resp.Spec.ID)
	fmt.Printf("Image:     %s\n", resp.Spec.Image)
	fmt.Printf("Replicas:  %d desired\n", resp.Spec.Replicas)
	fmt.Printf("Restart:   %s\n\n", resp.Spec.RestartPolicy)

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CONTAINER\tNODE\tSTATE\tHEALTH\tIP\tSTARTED")
	for _, inst := range resp.Instances {
		id := inst.ID
		if len(id) > 12 {
			id = id[:12]
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			id, inst.NodeID, inst.State, inst.Health, inst.IP,
			inst.StartedAt.Format("15:04:05"))
	}
	return tw.Flush()
}

// ─── scale ───────────────────────────────────────────────────────────────────

func cmdScale(args []string) error {
	fs := flag.NewFlagSet("scale", flag.ExitOnError)
	replicas := fs.Int("replicas", 0, "desired replica count")
	fs.Parse(args)
	if fs.NArg() == 0 || *replicas == 0 {
		return fmt.Errorf("usage: kl scale <workload-id> --replicas N")
	}
	return putJSON("/workloads/"+fs.Arg(0)+"/scale", map[string]int{"replicas": *replicas}, nil)
}

// ─── delete ──────────────────────────────────────────────────────────────────

func cmdDelete(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: kl delete <workload-id>")
	}
	return doDelete("/workloads/" + args[0])
}

// ─── logs ────────────────────────────────────────────────────────────────────

func cmdLogs(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	tail := fs.String("tail", "100", "number of lines from end (or 'all')")
	noFollow := fs.Bool("no-follow", false, "do not follow new output")
	fs.Parse(args)
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: kl logs <container-id>")
	}
	follow := "true"
	if *noFollow {
		follow = "false"
	}
	url := fmt.Sprintf("%s/logs/%s?tail=%s&follow=%s", serverAddr, fs.Arg(0), *tail, follow)
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
	}
	_, err = io.Copy(os.Stdout, resp.Body)
	return err
}

// ─── rollout ─────────────────────────────────────────────────────────────────

func cmdRollout(args []string) error {
	fs := flag.NewFlagSet("rollout", flag.ExitOnError)
	image := fs.String("image", "", "new container image (required)")
	maxUnavailable := fs.Int("max-unavailable", 1, "max old replicas down at once")
	maxSurge := fs.Int("max-surge", 1, "max extra containers during update")
	fs.Parse(args)
	if fs.NArg() == 0 || *image == "" {
		return fmt.Errorf("usage: kl rollout <workload-id> --image IMAGE")
	}
	spec := kl.RolloutSpec{
		WorkloadID:     fs.Arg(0),
		NewImage:       *image,
		MaxUnavailable: *maxUnavailable,
		MaxSurge:       *maxSurge,
	}
	var state kl.RolloutState
	if err := postJSON("/rollout", spec, &state); err != nil {
		return err
	}
	fmt.Printf("rollout started: %s\nold image: %s\nnew image: %s\n",
		state.ID, state.OldImage, state.NewImage)
	return nil
}

func cmdRolloutStatus(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: kl rollout-status <workload-id>")
	}
	var state kl.RolloutState
	if err := getJSON("/rollout/"+args[0], &state); err != nil {
		return err
	}
	fmt.Printf("Rollout ID:  %s\n", state.ID)
	fmt.Printf("Status:      %s\n", state.Status)
	fmt.Printf("Image:       %s → %s\n", state.OldImage, state.NewImage)
	fmt.Printf("Replicas:    %d desired / %d updated / %d old\n",
		state.DesiredReplicas, state.UpdatedReplicas, state.OldReplicas)
	fmt.Printf("Wave:        %d\n", state.Wave)
	if state.Message != "" {
		fmt.Printf("Message:     %s\n", state.Message)
	}
	return nil
}

// ─── discover ────────────────────────────────────────────────────────────────

func cmdDiscover(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: kl discover <workload-name>")
	}
	var endpoints []kl.ServiceEndpoint
	if err := getJSON("/discover/"+args[0], &endpoints); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CONTAINER\tNODE\tIP\tHEALTH\tPORTS")
	for _, ep := range endpoints {
		id := ep.ContainerID
		if len(id) > 12 {
			id = id[:12]
		}
		var portStrs []string
		for _, p := range ep.Ports {
			portStrs = append(portStrs, fmt.Sprintf("%d:%d/%s", p.HostPort, p.ContainerPort, p.Protocol))
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			id, ep.NodeID, ep.IP, ep.Health, strings.Join(portStrs, ","))
	}
	return tw.Flush()
}

// ─── HTTP helpers ─────────────────────────────────────────────────────────────

func getJSON(path string, out any) error {
	resp, err := http.Get(serverAddr + path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func postJSON(path string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, err := http.Post(serverAddr+path, "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func putJSON(path string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPut, serverAddr+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func doDelete(path string) error {
	req, err := http.NewRequest(http.MethodDelete, serverAddr+path, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	fmt.Println("deleted")
	return nil
}

// ─── input parsing ────────────────────────────────────────────────────────────

func parseEnvVars(pairs []string) map[string]string {
	if len(pairs) == 0 {
		return nil
	}
	m := make(map[string]string, len(pairs))
	for _, p := range pairs {
		k, v, _ := strings.Cut(p, "=")
		m[k] = v
	}
	return m
}

func parsePorts(mappings []string) []kl.PortMapping {
	if len(mappings) == 0 {
		return nil
	}
	out := make([]kl.PortMapping, 0, len(mappings))
	for _, m := range mappings {
		parts := strings.SplitN(m, ":", 2)
		if len(parts) != 2 {
			continue
		}
		host, _ := strconv.Atoi(parts[0])
		ctr, _ := strconv.Atoi(parts[1])
		out = append(out, kl.PortMapping{HostPort: host, ContainerPort: ctr, Protocol: "tcp"})
	}
	return out
}

// ─── misc ─────────────────────────────────────────────────────────────────────

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func usage() {
	fmt.Fprintf(os.Stderr, `kube-lite CLI

Usage: kl [--server http://host:port] <command> [flags]

Commands:
  nodes                                  list all registered worker nodes
  deploy  --name NAME --image IMAGE      deploy (or update) a workload
          [--id ID] [--replicas N]
          [--restart-policy Always|OnFailure|Never]
          [--port HOST:CONTAINER] (repeatable)
          [--env KEY=VALUE]       (repeatable)
          [--health-path /path --health-port PORT]
  workloads                              list all workloads
  status    <workload-id>                workload detail + running instances
  scale     <workload-id> --replicas N   change replica count
  delete    <workload-id>                stop all containers and remove workload
  logs      <container-id>               stream container logs
            [--tail N] [--no-follow]
  rollout   <workload-id> --image IMAGE  start a rolling image update
            [--max-unavailable N] [--max-surge N]
  rollout-status <workload-id>           show current rollout state
  discover  <workload-name>              list live service endpoints

Environment:
  KL_SERVER   scheduler address (default: http://localhost:8080)
`)
}
