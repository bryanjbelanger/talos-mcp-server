# Talos MCP Server

A Model Context Protocol server for [Talos Linux](https://www.talos.dev) cluster operations — one thin tool, typed actions, zero runtime dependencies.

Companion to [virtualbox-mcp-server](https://github.com/bryanjbelanger/virtualbox-mcp-server): that server provisions the nodes, this one turns them into a Kubernetes cluster.

## Design

One tool (`talos`) with typed actions for the cluster hot path — no CLI syntax needed:

| Action | Does |
|---|---|
| `gen_config` | Generate cluster config + talosconfig (`cluster`, `endpoint`, optional `output_dir`) |
| `apply_config` | Apply machine config to a node (`node`, `file`, `insecure` for maintenance mode) |
| `bootstrap` | Bootstrap etcd on the control plane (once per cluster) |
| `kubeconfig` | Retrieve the cluster kubeconfig |
| `health` | Cluster health validation |
| `config_merge` | Merge a talosconfig context (never overwrites `~/.talos/config`) |
| `version` | Client or node version |
| `run` | Raw `talosctl` argv for the long tail (etcd, logs, reboot, upgrade…) |
| `resolve` | Report platform/install state without downloading or running |

**Backend:** `talosctl` — the official gRPC client of the Talos machine API. An existing PATH install is always preferred; otherwise the correct binary for the host's OS/architecture is auto-installed from the official Sidero release, verified against GitHub's asset digest.

The full cluster recipe ships to clients in the MCP initialize handshake, so a fresh session can bring up a cluster without external knowledge.

## Prerequisites

None beyond a hypervisor for the nodes. The server is a single Go binary; `talosctl` is auto-installed if absent.

## Building

```bash
go build -o talos-mcp-server .
```

## Registration with Claude Code

```bash
claude mcp add --scope user talos -- /path/to/talos-mcp-server
```

Verify with `claude mcp list` (`✔ Connected`), then restart the session.

## Bundled skill

[`skills/talos-cluster`](skills/talos-cluster/SKILL.md) — the end-to-end procedure: provision nodes via the virtualbox MCP server, configure and bootstrap via this one. Install by copying (or symlinking) into `~/.claude/skills/`.

## License

MIT
