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

## Installing

Published on the [official MCP Registry](https://registry.modelcontextprotocol.io)
as `io.github.bryanjbelanger/talos-mcp-server` — that name is what
registry-aware clients install by
([current entry](https://registry.modelcontextprotocol.io/v0/servers?search=io.github.bryanjbelanger/talos-mcp-server&version=latest)).

For hosts that support MCP Bundles, download the `.mcpb` from the
[latest release](https://github.com/bryanjbelanger/talos-mcp-server/releases/latest)
and open it. One bundle covers every platform; it carries a binary per
(os, arch) and picks one at startup. The server needs no configuration —
guest credentials and endpoints are per-call parameters, and `talosctl` is
auto-installed on first use if the host has none.

Otherwise take the bare binary for your platform from the same release, or
build it:

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
