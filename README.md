# zdocker

A small, k9s-like terminal UI for your **local Docker** containers and
images, built with [Bubble Tea](https://github.com/charmbracelet/bubbletea)
and the official [Moby client](https://pkg.go.dev/github.com/moby/moby/client).

zdocker is a learning project and a sibling of
[kboba](https://github.com/ducnt114/kboba) (the same architecture, for
Kubernetes). See [docs/LEARNING.md](docs/LEARNING.md) (Vietnamese) for a
phase-by-phase walkthrough.

> Work in progress.

## Run

Requires Go 1.24+.

```sh
make build
./bin/zdocker [--host unix:///var/run/docker.sock | tcp://host:2375]
```

Without `--host`, zdocker connects like the `docker` CLI: `$DOCKER_HOST`
(and `$DOCKER_TLS_VERIFY`, `$DOCKER_CERT_PATH`), else the local socket.

### Without Docker

`make run-fake` starts a small fake daemon (`hack/fakedaemon`) with canned
data and runs zdocker against it.

### Sample containers

`make demo` starts a few containers labelled `zdocker.demo=1` (a chatty
logger, a crash loop, nginx, a finished job); `make demo-clean` removes only
those.

## Live updates

zdocker lists containers once, then follows the daemon's event stream and
re-lists when something changes (debounced), so the table is always current
without polling. If the daemon goes away it reconnects with backoff; the
header shows `● live` or `○ reconnecting`.

## Keys

| Key | Action |
|---|---|
| `↑↓ jk` | move |
| `a` | toggle all / running containers |
| `r` | refresh |
| `:` | command (`:containers`, `:q`) |
| `?` | help |
| `q` | quit |

## Layout

```
cmd/zdocker/        flags, wiring
internal/docker/    Moby client wrapper, UI-free
internal/ui/        Bubble Tea: root model + one sub-model per view
hack/fakedaemon/    in-memory fake Docker API for trying zdocker
```
