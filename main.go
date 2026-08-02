// Talos MCP Server — Talos Linux cluster operations via talosctl.
//
// Companion to virtualbox-mcp-server (or any hypervisor MCP server that can
// provision nodes): this server owns the OS-specific layer — running talosctl
// with automatic, digest-verified installation when the host has none.
//
// Ships as a single self-contained binary with no runtime dependencies.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const talosRepo = "siderolabs/talos"
const commandTimeout = 600 * time.Second

// ------------------------------------------------------------------ execution

func runCmd(bin string, workingDir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = workingDir
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	name := filepath.Base(bin)
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("%s timed out after %s", name, commandTimeout)
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		return "", fmt.Errorf("%s failed: %s", name, msg)
	}
	out := strings.TrimSpace(stdout.String())
	if out == "" {
		out = strings.TrimSpace(stderr.String())
	}
	if out == "" {
		out = "(ok)"
	}
	return out, nil
}

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

// ------------------------------------------------------- download & verify

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func fetchHTTPS(ctx context.Context, rawURL, destPath, wantSHA string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" {
		return "", fmt.Errorf("only https URLs are allowed")
	}
	want := strings.ToLower(strings.TrimSpace(wantSHA))
	if want != "" {
		if sum, err := fileSHA256(destPath); err == nil && sum == want {
			return fmt.Sprintf("already present with matching sha256: %s", destPath), nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 30 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed: HTTP %s", resp.Status)
	}
	tmp := destPath + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	start := time.Now()
	n, err := io.Copy(io.MultiWriter(f, h), resp.Body)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		os.Remove(tmp)
		if err == nil {
			err = closeErr
		}
		return "", fmt.Errorf("download failed after %d bytes: %w", n, err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if want != "" && got != want {
		os.Remove(tmp)
		return "", fmt.Errorf("sha256 mismatch: expected %s, got %s (file discarded)", want, got)
	}
	if err := os.Rename(tmp, destPath); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return fmt.Sprintf("downloaded %s (%.1f MB in %s, sha256 %s)",
		rawURL, float64(n)/1024/1024, time.Since(start).Round(time.Second), got), nil
}

// ------------------------------------------------------- GitHub release lookup

type ghAsset struct {
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	Digest             string `json:"digest"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type ghRelease struct {
	TagName string    `json:"tag_name"`
	Assets  []ghAsset `json:"assets"`
}

func githubRelease(ctx context.Context, repo, version string) (*ghRelease, error) {
	api := "https://api.github.com/repos/" + repo + "/releases/latest"
	if version != "" && version != "latest" {
		if !strings.HasPrefix(version, "v") {
			version = "v" + version
		}
		api = "https://api.github.com/repos/" + repo + "/releases/tags/" + version
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub API request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API: HTTP %s for %s", resp.Status, api)
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

func (r *ghRelease) findAsset(name string) (*ghAsset, error) {
	for i := range r.Assets {
		if r.Assets[i].Name == name {
			return &r.Assets[i], nil
		}
	}
	return nil, fmt.Errorf("no asset %q in release %s", name, r.TagName)
}

func (a *ghAsset) sha256() string {
	return strings.TrimPrefix(a.Digest, "sha256:")
}

// ------------------------------------------------------------------- talosctl

func talosctlPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	name := "talosctl"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(home, ".talos-mcp", "bin", name)
}

// findTalosctl prefers an existing talosctl on PATH, then the managed install
// location; returns the path to use and whether it exists yet.
func findTalosctl() (string, bool) {
	if p, err := exec.LookPath("talosctl"); err == nil {
		return p, true
	}
	bin := talosctlPath()
	if _, err := os.Stat(bin); err == nil {
		return bin, true
	}
	return bin, false
}

// ensureTalosctl returns a usable talosctl path, installing a digest-verified
// binary for this platform when the host has none.
func ensureTalosctl(ctx context.Context, version string) (string, error) {
	bin, installed := findTalosctl()
	if installed {
		return bin, nil
	}
	assetName := fmt.Sprintf("talosctl-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		assetName += ".exe"
	}
	rel, err := githubRelease(ctx, talosRepo, version)
	if err != nil {
		return "", err
	}
	asset, err := rel.findAsset(assetName)
	if err != nil {
		return "", err
	}
	if _, err := fetchHTTPS(ctx, asset.BrowserDownloadURL, bin, asset.sha256()); err != nil {
		return "", fmt.Errorf("talosctl install failed: %w", err)
	}
	if err := os.Chmod(bin, 0o755); err != nil {
		return "", err
	}
	return bin, nil
}

// talosIn is the single thin tool surface: typed actions for the cluster hot
// path (no CLI syntax needed), `run` as the raw passthrough for the long tail
// (etcd, logs, reboot, upgrade, …). talosctl is the execution backend — it is
// the official gRPC client of the Talos machine API, plus the local-only
// operations (gen config, config merge) the raw API does not offer.
type talosIn struct {
	Action         string   `json:"action" jsonschema:"gen_config|apply_config|bootstrap|kubeconfig|health|config_merge|version|resolve|run"`
	Cluster        string   `json:"cluster,omitempty" jsonschema:"cluster name (gen_config)"`
	Endpoint       string   `json:"endpoint,omitempty" jsonschema:"cluster endpoint URL (gen_config), e.g. https://192.168.100.10:6443"`
	Node           string   `json:"node,omitempty" jsonschema:"target node, host or host:port (apply_config/bootstrap/kubeconfig/health/version)"`
	File           string   `json:"file,omitempty" jsonschema:"machine config yaml (apply_config) or talosconfig to merge (config_merge)"`
	OutputDir      string   `json:"output_dir,omitempty" jsonschema:"gen_config output dir (default ~/.talos/clusters/<cluster>) or kubeconfig destination"`
	Talosconfig    string   `json:"talosconfig,omitempty" jsonschema:"talosconfig path for authenticated actions (omit to use the merged default)"`
	Insecure       bool     `json:"insecure,omitempty" jsonschema:"apply_config to a maintenance-mode node without client certs"`
	Args           []string `json:"args,omitempty" jsonschema:"extra flags for any action; the full talosctl argv for action=run"`
	InstallVersion string   `json:"install_version,omitempty" jsonschema:"talosctl release tag to install when absent (default latest)"`
	WorkingDir     string   `json:"working_dir,omitempty" jsonschema:"working directory for the command"`
}

func talosTool(ctx context.Context, _ *mcp.CallToolRequest, in talosIn) (*mcp.CallToolResult, any, error) {
	if in.Action == "resolve" {
		assetName := fmt.Sprintf("talosctl-%s-%s", runtime.GOOS, runtime.GOARCH)
		if runtime.GOOS == "windows" {
			assetName += ".exe"
		}
		bin, installed := findTalosctl()
		state := "not installed"
		if installed {
			if v, err := runCmd(bin, "", "version", "--client", "--short"); err == nil {
				state = fmt.Sprintf("installed at %s: %s", bin, strings.ReplaceAll(v, "\n", " "))
			} else {
				state = fmt.Sprintf("installed at %s (version check failed)", bin)
			}
		}
		rel, err := githubRelease(ctx, talosRepo, in.InstallVersion)
		if err != nil {
			return nil, nil, err
		}
		asset, err := rel.findAsset(assetName)
		if err != nil {
			return nil, nil, err
		}
		install := fmt.Sprintf("would install %s (%.1f MB, sha256 %s) to %s", rel.TagName,
			float64(asset.Size)/1024/1024, asset.sha256(), talosctlPath())
		if installed {
			install = "no install needed (existing binary is used)"
		}
		return text(fmt.Sprintf("platform: %s/%s → asset %s\nstate: %s\n%s",
			runtime.GOOS, runtime.GOARCH, assetName, state, install)), nil, nil
	}

	need := func(v, name string) (string, error) {
		if v == "" {
			return "", fmt.Errorf("'%s' is required for action '%s'", name, in.Action)
		}
		return v, nil
	}
	withAuth := func(argv []string) []string {
		if in.Talosconfig != "" {
			argv = append(argv, "--talosconfig", in.Talosconfig)
		}
		return argv
	}

	var argv []string
	switch in.Action {
	case "gen_config":
		cluster, err := need(in.Cluster, "cluster")
		if err != nil {
			return nil, nil, err
		}
		endpoint, err := need(in.Endpoint, "endpoint")
		if err != nil {
			return nil, nil, err
		}
		outDir := in.OutputDir
		if outDir == "" {
			home, _ := os.UserHomeDir()
			outDir = filepath.Join(home, ".talos", "clusters", cluster)
		}
		argv = []string{"gen", "config", cluster, endpoint, "--output-dir", outDir}
	case "apply_config":
		node, err := need(in.Node, "node")
		if err != nil {
			return nil, nil, err
		}
		file, err := need(in.File, "file")
		if err != nil {
			return nil, nil, err
		}
		argv = []string{"apply-config", "--nodes", node, "--file", file}
		if in.Insecure {
			argv = append(argv, "--insecure")
		} else {
			argv = withAuth(argv)
		}
	case "bootstrap":
		node, err := need(in.Node, "node")
		if err != nil {
			return nil, nil, err
		}
		argv = withAuth([]string{"bootstrap", "--nodes", node, "--endpoints", node})
	case "kubeconfig":
		node, err := need(in.Node, "node")
		if err != nil {
			return nil, nil, err
		}
		argv = []string{"kubeconfig"}
		if in.OutputDir != "" {
			argv = append(argv, in.OutputDir)
		}
		argv = withAuth(append(argv, "--nodes", node, "--endpoints", node))
	case "health":
		node, err := need(in.Node, "node")
		if err != nil {
			return nil, nil, err
		}
		argv = withAuth([]string{"health", "--nodes", node, "--endpoints", node})
	case "config_merge":
		file, err := need(in.File, "file")
		if err != nil {
			return nil, nil, err
		}
		argv = []string{"config", "merge", file}
	case "version":
		if in.Node == "" {
			argv = []string{"version", "--client"}
		} else {
			argv = withAuth([]string{"version", "--nodes", in.Node, "--endpoints", in.Node})
		}
	case "run":
		if len(in.Args) == 0 {
			return nil, nil, fmt.Errorf("action 'run' needs the talosctl argv in args")
		}
		argv = in.Args
	default:
		return nil, nil, fmt.Errorf("unknown action: %s", in.Action)
	}
	if in.Action != "run" {
		argv = append(argv, in.Args...)
	}

	bin, err := ensureTalosctl(ctx, in.InstallVersion)
	if err != nil {
		return nil, nil, err
	}
	out, err := runCmd(bin, in.WorkingDir, argv...)
	if err != nil {
		return nil, nil, err
	}
	return text(out), nil, nil
}

// ----------------------------------------------------------------------- main

const serverInstructions = `Talos Linux cluster operations through one thin tool. The talos tool's typed actions cover the cluster hot path; action=run passes any talosctl argv for the long tail (etcd, logs, reboot, upgrade, dmesg, …). talosctl is the execution backend (it is the official gRPC client of the Talos machine API); a digest-verified binary is auto-installed for this host's OS/arch when none exists — an existing PATH install is always preferred.

Cluster bring-up over nodes provisioned by a hypervisor MCP server (e.g. virtualbox-mcp-server) — nodes boot the Talos image into maintenance mode first:
1. Discover node IPs via the hypervisor server (VirtualBox NAT network: MAC from vm_info show → execute_command 'dhcpserver findlease --network=NET --mac-address=…'; port-forward 50000/tcp per node and 6443/tcp for the control plane when reaching nodes through the host).
2. talos action=gen_config cluster=NAME endpoint=https://CP_IP:6443
3. Per node: talos action=apply_config node=IP file=<controlplane.yaml|worker.yaml> insecure=true
4. After the control plane reboots (poll action=version node=CP_IP talosconfig=DIR/talosconfig), ONCE: talos action=bootstrap node=CP_IP talosconfig=DIR/talosconfig
5. talos action=config_merge file=DIR/talosconfig  (merge — never overwrite the user's ~/.talos/config)
6. talos action=kubeconfig node=CP_IP, then action=health node=CP_IP talosconfig=DIR/talosconfig

Sizing guidance: control plane 4 CPUs/4096MB (minimum 2/2048), workers 2 CPUs/2048MB (minimum 1/1024), disks 10GB+.`

func main() {
	server := mcp.NewServer(&mcp.Implementation{Name: "talos-mcp-server", Version: "1.1.0"},
		&mcp.ServerOptions{Instructions: serverInstructions})

	mcp.AddTool(server, &mcp.Tool{Name: "talos", Description: "Talos Linux operations: typed actions for the cluster path (gen_config, apply_config, bootstrap, kubeconfig, health, config_merge, version) plus action=run for any raw talosctl argv. Backend is talosctl — the official Talos machine-API gRPC client — auto-installed digest-verified when the host has none (existing PATH install preferred). action=resolve reports platform/install state without downloading or running."}, talosTool)

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}
