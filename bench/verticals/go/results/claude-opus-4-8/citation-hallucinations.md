# Citation hallucinations

Citations the answer printed that did not resolve against the repo checked out at the benchmarked commit. **Hallucinated** = line number beyond end-of-file (a made-up number). **Unresolved** = file not in the repo, or symbol not within ±5 lines of the cited line.

Reported for transparency; not folded into the headline score.

## baseline

### baseline/consul  — 278/279 grounded

**Unresolved**
- `internal/multicluster/.../v1compat/controller.go:82` — file not found at internal/multicluster/.../v1compat/controller.go

### baseline/pebble  — 41/42 grounded

**Unresolved**
- `.../merging_iter.go:335` — file not found at .../merging_iter.go

## sense

### sense/consul  — 171/191 grounded

**Unresolved**
- `.../peerstream/server.go:36` — file not found at .../peerstream/server.go
- `.../peerstream/server.go:37` — file not found at .../peerstream/server.go
- `.../peerstream/server.go:40` — file not found at .../peerstream/server.go
- `.../peerstream/subscription_manager.go:46` — file not found at .../peerstream/subscription_manager.go
- `.../peerstream/subscription_view.go:27` — file not found at .../peerstream/subscription_view.go
- `.../connectca/server.go:29` — file not found at .../connectca/server.go
- `.../dataplane/server.go:24` — file not found at .../dataplane/server.go
- `.../serverdiscovery/server.go:22` — file not found at .../serverdiscovery/server.go
- `.../configentry/server.go:35` — file not found at .../configentry/server.go
- `.../acl/server.go:20` — file not found at .../acl/server.go
- `.../resource/server_ce.go:39` — file not found at .../resource/server_ce.go
- `.../resource/server.go:46` — file not found at .../resource/server.go
- `.../v1compat/controller.go:82` — file not found at .../v1compat/controller.go
- `.../subscription_manager.go:46` — file not found at .../subscription_manager.go
- `.../stream_resources.go:345` — file not found at .../stream_resources.go
- `.../subscription_view.go:27` — file not found at .../subscription_view.go
- `.../subscription_view.go:78` — file not found at .../subscription_view.go
- `.../configentry/server.go:38` — file not found at .../configentry/server.go
- `.../subscribe/subscribe.go:24` — file not found at .../subscribe/subscribe.go
- `.../acl/server.go:23` — file not found at .../acl/server.go

### sense/consul  — 172/194 grounded

**Unresolved**
- `agent/consul/agent.go:1667` — file not found at agent/consul/agent.go
- `/connectca/server.go:31` — file not found at /connectca/server.go
- `/peerstream/server.go:37` — file not found at /peerstream/server.go
- `/subscription_manager.go:50` — file not found at /subscription_manager.go
- `/subscription_view.go:28` — file not found at /subscription_view.go
- `/configentry/server.go:37` — file not found at /configentry/server.go
- `/connectca/server.go:32` — file not found at /connectca/server.go
- `/resource/server_ce.go:37` — file not found at /resource/server_ce.go
- `/v1compat/controller.go:81` — file not found at /v1compat/controller.go
- `/register.go:11` — file not found at /register.go
- `/configentry/server.go:35` — file not found at /configentry/server.go
- `/subscribe/subscribe.go:24` — file not found at /subscribe/subscribe.go
- `/connectca/server.go:28` — file not found at /connectca/server.go
- `/dataplane/server.go:24` — file not found at /dataplane/server.go
- `/serverdiscovery/server.go:22` — file not found at /serverdiscovery/server.go
- `/acl/server.go:20` — file not found at /acl/server.go
- `/dataplane/server.go:26` — file not found at /dataplane/server.go
- `/serverdiscovery/server.go:24` — file not found at /serverdiscovery/server.go
- `/peerstream/server.go:41` — file not found at /peerstream/server.go
- `/resource/server_ce.go:35` — file not found at /resource/server_ce.go
- `/peerstream/subscription_manager.go:50` — file not found at /peerstream/subscription_manager.go
- `/peerstream/subscription_view.go:28` — file not found at /peerstream/subscription_view.go

### sense/dolt  — 302/303 grounded

**Unresolved**
- `go/.../dolt_procedures_history_table.go:115` — file not found at go/.../dolt_procedures_history_table.go
