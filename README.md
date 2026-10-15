# zdocker

A small, k9s-like terminal UI for your **local Docker** containers, images,
volumes and networks (with "in use" counts), built with [Bubble Tea](https://github.com/charmbracelet/bubbletea)
and the official [Moby client](https://pkg.go.dev/github.com/moby/moby/client).

zdocker is a learning project and a sibling of
[kboba](https://github.com/ducnt114/kboba) (the same architecture, for
Kubernetes). See [docs/LEARNING.md](docs/LEARNING.md) (Vietnamese) for a
phase-by-phase walkthrough.

## Run

Requires Go 1.24+.

```sh
make build
./bin/zdocker [--host unix:///var/run/docker.sock | tcp://host:2375]
```

`--read-only` disables every change (start, stop, restart, remove): it is
enforced in the client, not just hidden in the UI.

Without `--host`, zdocker connects like the `docker` CLI: `$DOCKER_HOST`
(and `$DOCKER_TLS_VERIFY`, `$DOCKER_CERT_PATH`), else the local socket.

### Without Docker

`make run-fake` starts a small fake daemon (`hack/fakedaemon`) with canned
data and runs zdocker against it.

### Sample containers

`make demo` starts a few containers labelled `zdocker.demo=1` (a chatty
logger, a crash loop, nginx, a finished job); `make demo-clean` removes only
those.

## Logs

`enter` on a container follows its logs (`docker logs -f --tail 500`):
`f` follow, `/` `n` `N` search, `w` wrap, `t` timestamps, `←/→` scroll,
`esc` back. Lines written to stderr are marked with a red bar.

## Live updates

zdocker lists containers once, then follows the daemon's event stream and
re-lists when something changes (debounced), so the table is always current
without polling. If the daemon goes away it reconnects with backoff; the
header shows `● live` or `○ reconnecting`.

## Keys

| Key | Action |
|---|---|
| `↑↓ jk` | move |
| `enter` | logs of the selected container |
| `i` | inspect the selected container / image (JSON) |
| `/` | filter (name, image, compose project / repo:tag, ID) |
| `s`, `S` | sort by next column, reverse |
| `u` | start container |
| `x` | stop container (asks first) |
| `R` | restart container (asks first) |
| `D` | remove container / image / volume / network (asks first; a running container is killed) |
| `t` | live stats (CPU, memory, network, block I/O) with sparklines |
| `a` | toggle all / running containers |
| `g` | group containers by Docker Compose project (`enter` on a project folds it) |
| `r` | refresh |
| `:` | command (`:containers`/`:ps`, `:images`/`:img`, `:volumes`/`:vol`, `:networks`/`:net`, `:q`) |
| `?` | help |
| `q` | quit |

## Layout

```
cmd/zdocker/        flags, wiring
internal/docker/    Moby client wrapper, UI-free
internal/ui/        Bubble Tea: root model + one sub-model per view
hack/fakedaemon/    in-memory fake Docker API for trying zdocker
```
