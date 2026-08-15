---
name: talos-cluster
description: Stand up a complete Talos Linux Kubernetes cluster on a desktop hypervisor (VirtualBox or VMware Fusion/Workstation) — provisions nodes via the desktop-hypervisor MCP server, configures and bootstraps them via the talos MCP server. Use when the user asks to create/set up a Talos or Kubernetes cluster locally, add Talos nodes, or bootstrap Talos.
---

# Talos Kubernetes cluster on a desktop hypervisor

Builds a 1×control-plane + N×worker Talos cluster end to end.

## Preflight — verify dependencies before any work

This skill requires TWO MCP servers. Check that both tool families are available
(loaded or deferred): `mcp__hypervisor__provider` and `mcp__talos__talos`. If
either is missing, STOP — do not partially provision. Both are on the official
MCP Registry (`io.github.bryanjbelanger/desktop-hypervisor-mcp`,
`io.github.bryanjbelanger/talos-mcp-server`); binaries and .mcpb bundles are on
each repo's latest GitHub release. Claude Code registration:

- `claude mcp add --scope user hypervisor -- <path-to-desktop-hypervisor-mcp>`
- `claude mcp add --scope user talos -- <path-to-talos-mcp-server>`

Then restart the session (MCP servers load at session start).

Verify a hypervisor is actually ready: `provider action=list` reports each
installed hypervisor's status and remediation when not ready. One ready
provider is selected automatically; several → ask the user which to use and
pass `provider=` on every call.

## Parameters (ask only if the user didn't specify)

- CLUSTER: cluster name (default `talos`)
- WORKERS: worker count (default 2)
- NET: cluster network name (default `cluster-net`; VMware always uses vmnet8)
- DIR: config output dir (default `~/.talos/clusters/CLUSTER`)

## Phase 1 — Provision nodes (hypervisor server)

Skip any step whose result already exists (`vm_info action=list` first).
VM names: `CLUSTER-cp-1`, `CLUSTER-worker-1..N`.

1. `network action=ensure_cluster_network name=NET` — intent-based: a network
   all nodes share and the host can reach, on whichever provider is selected
   (no kernel-extension-dependent host-only interfaces).
2. `artifact action=fetch image=talos` → machine image path, resolved for the
   selected provider (VirtualBox vs VMware get their own official OVA),
   digest-verified, idempotent.
3. Per node: `vm_lifecycle action=import path=<artifact> vm=NAME` then
   `vm_config action=resources vm=NAME cpus=C memory_mb=M`
   — control plane 4/4096, workers 2/2048.
4. `vm_lifecycle action=start vm=NAME` each node (headless default); confirm
   with `vm_info action=running`. Nodes boot into Talos maintenance mode.

## Phase 2 — Node addressing (hypervisor server)

5. Per node: `vm_info action=ip vm=NAME` — uses in-guest tools when present,
   else DHCP leases by MAC, so it works for agentless guests like Talos.
6. Reachability depends on the provider: VMware's vmnet8 is host-reachable, so
   talosctl can usually target node IPs directly — try that first. Where the
   host cannot reach a node (VirtualBox NAT networks), forward per node:
   `network action=expose_guest_port guest_ip=NODE_IP guest_port=50000 host_port=5100N`
   (51001, 51002, …) and for the control plane also
   `guest_port=6443 host_port=6443`; then target `127.0.0.1:5100N` below.

## Phase 3 — Configure and bootstrap (talos server)

All via the `talos` tool's typed actions (backend talosctl auto-installs
digest-verified if the host has none; an existing PATH install is preferred).
NODE below is the node's direct IP, or `127.0.0.1:5100N` when forwarded.

7. `talos action=gen_config cluster=CLUSTER endpoint=https://CP_IP:6443 output_dir=DIR`
8. Control plane: `talos action=apply_config node=CP_NODE file=DIR/controlplane.yaml insecure=true`,
   workers the same with `worker.yaml`. Nodes install to disk and reboot.
9. After the control plane reboots (poll `talos action=version node=CP_NODE talosconfig=DIR/talosconfig`
   until it answers; typically 1–3 min), run ONCE:
   `talos action=bootstrap node=CP_NODE talosconfig=DIR/talosconfig`
10. MERGE the context — never overwrite the user's existing talos config:
    `talos action=config_merge file=DIR/talosconfig`
11. `talos action=kubeconfig node=CP_NODE` then verify with
    `talos action=health node=CP_NODE talosconfig=DIR/talosconfig`.
    Anything beyond the typed actions (etcd, logs, reboot): `talos action=run args=[…]`.

## Verification & reporting

Report: node names/IPs (and forwarded ports if used), cluster name, where
talosconfig and kubeconfig were merged, and cluster health output. If any phase
fails, report the exact failing call and stop — do not tear down working nodes.

## Cleanup (only on explicit request)

`vm_lifecycle action=stop hard=true` + `action=delete` per node;
`talos action=run args=['config','context','--remove',CLUSTER]`; leave the
image cache and the cluster network in place.
