## Telepresence agent dialing port 9913 (connection refused) — how to diagnose and fix

### What is port 9913?
- **Proxy port**: When a Service uses a numeric `targetPort`, the traffic-agent avoids an iptables loop by dialing a computed proxy port instead of the container port when no intercept is active.
- **Formula**: `proxyPort = agentPort + 11 + numberOfPossibleIntercepts`.
  - Default `agentPort` is `9900`, so proxy ports often land around `9911–9925`. Seeing `9913` is expected.

### Why “connection refused” happens
- The agent dials `<podIP>:<proxyPort>` (e.g., `10.x.x.x:9913`). An init‑container sets a DNAT rule that should forward that to the app port (e.g., `8080`). If that DNAT rule is missing or not effective, the dial is refused.

### Verify iptables rules (inside the pod)
- Replace `<ns>` and `<pod>`. The agent container name is typically `traffic-agent`.

```bash
kubectl exec -n <ns> <pod> -c traffic-agent -- iptables -t nat -L TEL_OUTPUT_TCP -n -v
```

- You should see a DNAT rule like:
  - Destination `<podIP>:<proxyPort>` → DNAT to `<podIP>:<containerPort>` (e.g., `9913 → 8080`).

Also check redirects for incoming traffic:

```bash
kubectl exec -n <ns> <pod> -c traffic-agent -- iptables -t nat -L TEL_PREROUTING_TCP -n -v
```

### Check the init-container logs
- The init re-applies iptables each start and logs any DNAT it adds for the proxy port:

```bash
kubectl logs -n <ns> <pod> -c traffic-agent-init | grep -i "output DNAT"
```

You should see a line similar to:

```
output DNAT <podIP>:<proxyPort> -> <podIP>:<containerPort>
```

### Confirm the Service uses a numeric targetPort
- The proxy/redirect behavior is only used when the Service’s port has a numeric `targetPort` pointing to the container port:

```bash
kubectl get svc <service> -n <ns> -o yaml | grep -A4 -n "ports:"
```

Look for something like:

```yaml
ports:
  - port: 80
    targetPort: 8080   # numeric (triggers proxy port)
```

### Inspect the agent config in the pod
- The sidecar config includes `AgentPort` and all defined interceptable ports (used to compute the proxy port):

```bash
kubectl exec -n <ns> <pod> -c traffic-agent -- sh -lc 'printenv TELEPRESENCE_AGENT_CONFIG'
```

If `jq` is available in the image:

```bash
kubectl exec -n <ns> <pod> -c traffic-agent -- sh -lc 'printenv TELEPRESENCE_AGENT_CONFIG | jq .'
```

Confirm `AgentPort` (default 9900) and count of intercepts (which affects the proxy port).

### Common fixes
- **Init didn’t program rules**: Restart the pod to re-run the init and reapply iptables.
  - Ensure the init has the required privileges (NET_ADMIN) and isn’t blocked by PSP/PSA.
- **Service mesh interaction**: Mesh iptables can be reloaded or reordered. A full pod restart ensures Telepresence chains are appended after mesh rules.
- **Pod IP changed**: DNAT rule references the pod IP; if it changed since init, restart the pod.
- **Wrong container port or app not listening**: Verify the app is actually listening on the expected container port (e.g., 8080).
- **nftables vs iptables mismatch**: On some nodes, `iptables` may be an nft wrapper while rules are applied with legacy (or vice versa). Ensure consistency on the node/CNI.

### Quick checklist
- [ ] TEL_OUTPUT_TCP shows DNAT `<podIP>:<proxyPort> → <podIP>:<containerPort>`
- [ ] Init logs contain “output DNAT …” for the expected proxy port
- [ ] Service has numeric `targetPort` (proxy port behavior expected)
- [ ] App listens on the expected container port
- [ ] Pod restarted after mesh/PSP or IP changes

### Notes
- Default agent sidecar port is `9900`.
- Proxy port (e.g., `9913`) is normal; a “connection refused” means the DNAT to the container port isn’t present or isn’t taking effect.



