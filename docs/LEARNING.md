# zdocker: nhật ký học tập

zdocker là "anh em" của [kboba](https://github.com/ducnt114/kboba): cùng kiến trúc Bubble Tea (root model điều phối, mỗi màn hình một sub-model, mọi việc chậm đi qua `tea.Cmd`), nhưng nói chuyện với Docker Engine API thay vì Kubernetes. Tài liệu này ghi lại từng phase: làm gì, học được gì, và nằm ở đâu trong code. Những pattern đã giải thích kỹ trong `kboba/docs/LEARNING.md` chỉ được nhắc lại ngắn gọn ở đây.

> Thuật ngữ kỹ thuật giữ nguyên tiếng Anh. Code được tham chiếu theo **file + tên symbol**.

## Mục lục

1. [Bức tranh tổng thể](#1-bức-tranh-tổng-thể)
2. [Phase (a): Skeleton, kết nối, bảng containers](#2-phase-a-skeleton-kết-nối-bảng-containers)

---

## 1. Bức tranh tổng thể

```
cmd/zdocker/main.go    flag --host → docker.New → tea.Program
        │
        ▼
internal/ui            Bubble Tea; chỉ biết interface docker.Client
        │
        ▼
internal/docker        Moby client (github.com/moby/moby/client); KHÔNG import bubbletea
        │  HTTP qua unix socket (/var/run/docker.sock) hoặc TCP
        ▼
   Docker daemon
```

### Khác gì so với kboba

| | kboba (Kubernetes) | zdocker (Docker) |
|---|---|---|
| Kết nối | kubeconfig, nhiều context | một daemon: `$DOCKER_HOST` hoặc socket local |
| Realtime | informer (list + watch có resourceVersion) | `/events` stream + list lại (phase b) |
| Ghi | tuyệt đối read-only | có start/stop/rm… với xác nhận, và `--read-only` (phase f) |
| Test không cần hạ tầng | fake clientset của client-go | **fake daemon** bằng `httptest`: client Moby thật nói HTTP với server giả |

## 2. Phase (a): Skeleton, kết nối, bảng containers

### Mục tiêu
Chạy được `zdocker`: kết nối daemon, hiện bảng containers (NAME, IMAGE, STATE, STATUS, PORTS, AGE) có màu theo trạng thái, `a` chuyển giữa all / running, `r` làm mới.

### Học được gì
- **Moby client mới** (`github.com/moby/moby/client`, 2025+). So với `github.com/docker/docker/client` cũ: mọi method nhận một struct `…Options` và trả về `…Result` (ví dụ `ContainerList(ctx, ContainerListOptions{All: true}) → ContainerListResult{Items}`), và **thương lượng API version bật mặc định** (`WithAPIVersionNegotiation` giờ là no-op): lần gọi đầu tiên tự hỏi `/_ping` để biết daemon hỗ trợ version nào.
- **`client.FromEnv`**: đọc `DOCKER_HOST`, `DOCKER_TLS_VERIFY`, `DOCKER_CERT_PATH`, `DOCKER_API_VERSION` giống CLI `docker`; `WithHost` đặt sau sẽ ghi đè host.
- **Docker API chỉ là HTTP**: vì vậy có thể test client thật bằng một `httptest.Server` đóng vai daemon, không cần Docker.
- **Giữ lựa chọn theo ID chứ không theo vị trí** khi danh sách thay đổi (test đã bắt lỗi này, xem Bẫy).

### Thể hiện trong code

**Kết nối** (`internal/docker/client.go` → `New`): `mobyclient.New(mobyclient.FromEnv, [WithHost(host)])`. Không có request nào được gửi lúc tạo client; lần đầu là `Ping` trong một `tea.Cmd` (`ui/root.go` → `ping`). Lỗi (daemon chưa chạy, không có quyền đọc socket) hiện ở status bar: `cannot reach the Docker daemon: …`, và zdocker **không** list containers khi chưa kết nối được.

**Kiểu domain** (`internal/docker/containers.go` → `Container`, `newContainer`): UI không thấy `container.Summary` của Moby.
- `Name` bỏ dấu `/` đầu (`/web` → `web`; API trả tên kèm `/` vì lý do lịch sử).
- `Ports` format giống `docker ps`: `0.0.0.0:8080->80/tcp, [::]:8080->80/tcp, 443/tcp` — port publish cả IPv4 lẫn IPv6 xuất hiện hai lần như CLI; địa chỉ IPv6 trong ngoặc vuông (`netip.Addr.Is6`).
- `Project` lấy từ label `com.docker.compose.project` (dùng cho các phase sau).
- `ShortID` = 12 ký tự đầu, như CLI.

**Bảng** (`internal/ui/containers.go` → `containersView`): dùng lại `tableModel` copy từ kboba (`internal/ui/table.go`, có ghi chú nguồn).
- `load()` tăng `gen` và gọi `ListContainers` trong Cmd; `containersLoadedMsg` mang `gen` → kết quả cũ (ví dụ bấm `r` hai lần nhanh) bị bỏ qua. Đây là generation counter của kboba, áp cho lời gọi một lần.
- `showAll` mặc định `true` (giống `docker ps -a`), `a` đảo và load lại với `all=false/true`.
- `setRows(selectedID)`: đặt con trỏ lại đúng container theo **ID**.

**Màu** (`internal/ui/colors.go` → `containerHealth`): Docker không có "conditions" như Kubernetes; trạng thái nằm trong `State` và chuỗi `Status` của daemon:
- `(unhealthy)` → đỏ; `(health: starting)` → vàng (health check của Dockerfile).
- `exited` + mã thoát khác 0 (đọc bằng regex `^Exited \((\d+)\)`) → đỏ; mã 0 → xám (job chạy xong).
- `restarting`, `paused`, `removing` → vàng; `dead` → đỏ; `created` → xám.

**Fake daemon cho test** (`internal/docker/fakedaemon_test.go`): `httptest.Server` bỏ tiền tố version (`/v1.47/containers/json` → `/containers/json`), trả header `Api-Version` cho bước thương lượng, và ghi lại mọi request để test kiểm tra (ví dụ có `all=1`). Client test là **client Moby thật** (`New("tcp://"+addr)`), nên test bao cả phần HTTP, JSON và thương lượng version.

**Fake daemon chạy được** (`hack/fakedaemon`): cùng ý tưởng nhưng là một chương trình độc lập với dữ liệu mẫu (web healthy, worker restarting, migrate exit 0, flaky exit 137…). `make run-fake` chạy nó rồi mở zdocker vào — dùng để thử TUI khi không có Docker (như môi trường phát triển của dự án này).

### Test liên quan
- `internal/docker/containers_test.go`: `TestPing` (version thương lượng, OSType, host), `TestListContainers` (sắp xếp, tên bỏ `/`, ShortID, project, ports IPv4/IPv6, query `all=1`), `TestListContainersRunningOnly`, `TestDaemonUnreachable`.
- `internal/ui/root_test.go`: `TestStartupListsContainers`, `TestToggleAll`, `TestStaleLoadIgnored`, `TestCursorFollowsContainer`, `TestDaemonUnreachable` (không list khi chưa kết nối), `TestUnknownCommand`, `TestContainerHealth` (bảng trạng thái).
- Thủ công: `hack/fakedaemon` + tmux — 5 container, màu, `a` chuyển running/all.

### Bẫy
| Bẫy | Cách xử lý |
|---|---|
| Tìm container đang chọn **sau** khi đã thay danh sách → lấy index cũ trên dữ liệu mới, chọn nhầm | Lấy ID đang chọn *trước* khi thay `items` (`selectedID` → `setRows`) |
| Tên container có `/` ở đầu | `strings.TrimPrefix(name, "/")` |
| Port IPv6 hiện sai định dạng | `[::]:8080->80/tcp` khi `IP.Is6()` |
| Môi trường dev không có Docker daemon | Fake daemon bằng `httptest` (test) và `hack/fakedaemon` (thử tay) |
| Biến môi trường `DOCKER_*` của máy chạy test làm test sai | Fake daemon test xoá `DOCKER_HOST`, `DOCKER_TLS_VERIFY`, `DOCKER_API_VERSION` bằng `t.Setenv` |
