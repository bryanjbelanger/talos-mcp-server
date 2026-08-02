---
name: talos-cluster
description: Stand up a complete Talos Linux Kubernetes cluster on VirtualBox — provisions nodes via the virtualbox MCP server, configures and bootstraps them via the talos MCP server. Use when the user asks to create/set up a Talos or Kubernetes cluster on VirtualBox, add Talos nodes, or bootstrap Talos.
---

# Talos Kubernetes cluster on VirtualBox

Builds a 1×control-plane + N×worker Talos cluster end to end.

## Preflight — verify dependencies before any work

This skill requires TWO MCP servers. Check that both tool families are available
(loaded or deferred): `mcp__virtualbox__vm_info` and `mcp__talos__talos`. If either
is missing, STOP — do not partially provision. Tell the user exactly which server
is absent and how to get it:

- virtualbox: `claude mcp add --scope user virtualbox -- <path-to-virtualbox-mcp-server binary>`
  (build from github.com/bryanjbelanger/virtualbox-mcp-server)
- talos: `claude mcp add --scope user talos -- <path-to-talos-mcp-server binary>`

Then restart the session (MCP servers load at session start). Also verify VirtualBox
itself: `vm_info action=list topic=systemproperties` — an execution error here means
VBoxManage is not installed/in PATH.

## Parameters (ask only if the user didn't specify)

- CLUSTER: cluster name (default `talos`)
- WORKERS: worker count (default 2)
- NET: NAT network name (default `talos-net`, subnet 192.168.100.0/24)
- DIR: config output dir (default `~/.talos/clusters/CLUSTER`)

## Phase 1 — Provision nodes (virtualbox server)

Skip any step whose result already exists (check with `vm_info` first: list vms,
list natnets). VM names: `CLUSTER-cp-1`, `CLUSTER-worker-1..N`.

1. `network action=natnetwork args=['add','--netname',NET,'--network','192.168.100.0/24','--enable','--dhcp','on']`
   — NAT network, NOT host-only: host-only requires macOS kernel-extension approval; NAT networks work everywhere.
2. `image action=fetch name=talos` → OVA path (digest-verified, idempotent).
3. Per node: `appliance action=import file_path=<ova> args=['--vsys','0','--vmname',NAME]`
   then `vm_config action=modify vm_name=NAME args=['--cpus',C,'--memory',M,'--nic1','natnetwork','--nat-network1',NET]`
   — control plane 4/4096, workers 2/2048.
4. `vm_lifecycle action=start` each node; confirm all present in `vm_info action=list topic=runningvms`.
   Nodes boot into Talos maintenance mode and wait.

## Phase 2 — Node IPs and host access (virtualbox server)

5. Per node: MAC from `vm_info action=show vm_name=NAME` (NIC 1), then
   `execute_command 'dhcpserver findlease --network=NET --mac-address=XXXXXXXXXXXX'` → node IP.
6. Port-forwards so talosctl on the host reaches the nodes:
   `network action=natnetwork args=['modify','--netname',NET,'--port-forward-4','NAME-talos:tcp:[]:5100N:[NODE_IP]:50000']`
   (one per node, unique host ports 51001, 51002, …) and for the control plane also
   `'cp-k8s:tcp:[]:6443:[CP_IP]:6443'`.

## Phase 3 — Configure and bootstrap (talos server)

All via the `talos` tool's typed actions (backend talosctl auto-installs
digest-verified if the host has none; an existing PATH install is preferred).

7. `talos action=gen_config cluster=CLUSTER endpoint=https://CP_IP:6443 output_dir=DIR`
8. Apply through the forwarded ports (host side): control plane
   `talos action=apply_config node=127.0.0.1:51001 file=DIR/controlplane.yaml insecure=true`,
   workers the same with `worker.yaml` and their ports. Nodes install to disk and reboot.
9. After the control plane reboots (poll `talos action=version node=127.0.0.1:51001 talosconfig=DIR/talosconfig`
   until it answers; typically 1–3 min), run ONCE:
   `talos action=bootstrap node=127.0.0.1:51001 talosconfig=DIR/talosconfig`
10. MERGE the context — never overwrite the user's existing talos config:
    `talos action=config_merge file=DIR/talosconfig`
11. `talos action=kubeconfig node=127.0.0.1:51001` then verify with
    `talos action=health node=127.0.0.1:51001 talosconfig=DIR/talosconfig`.
    Anything beyond the typed actions (etcd, logs, reboot): `talos action=run args=[…]`.

## Verification & reporting

Report: node names/IPs/ports table, cluster name, where talosconfig and kubeconfig
were merged, and `kubectl get nodes` equivalent output (via talosctl health). If any
phase fails, report the exact failing call and stop — do not tear down working nodes.

## Cleanup (only on explicit request)

`vm_lifecycle action=poweroff` + `action=unregister` per node;
`network action=natnetwork args=['remove','--netname',NET]`;
`talosctl args=['config','context','--remove',CLUSTER]` equivalents; leave images cached.
