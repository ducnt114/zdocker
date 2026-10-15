# zdocker: nhật ký học tập

zdocker là "anh em" của [kboba](https://github.com/ducnt114/kboba): cùng kiến trúc Bubble Tea (root model điều phối, mỗi màn hình một sub-model, mọi việc chậm đi qua `tea.Cmd`), nhưng nói chuyện với Docker Engine API thay vì Kubernetes. Tài liệu này ghi lại từng phase: làm gì, học được gì, và nằm ở đâu trong code. Những pattern đã giải thích kỹ trong `kboba/docs/LEARNING.md` chỉ được nhắc lại ngắn gọn ở đây.

> Thuật ngữ kỹ thuật giữ nguyên tiếng Anh. Code được tham chiếu theo **file + tên symbol**.

## Mục lục

1. [Bức tranh tổng thể](#1-bức-tranh-tổng-thể)
2. [Phase (a): Skeleton, kết nối, bảng containers](#2-phase-a-skeleton-kết-nối-bảng-containers)
3. [Phase (b): Realtime bằng Docker events](#3-phase-b-realtime-bằng-docker-events)
4. [Phase (c): Images](#4-phase-c-images)

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

## 3. Phase (b): Realtime bằng Docker events

### Mục tiêu
Bảng tự cập nhật khi container được tạo, start, die, xoá… — **không poll**. Mất kết nối (daemon restart, socket biến mất) thì tự kết nối lại, và header cho biết trạng thái: `● live container worker: die` hoặc `○ reconnecting`.

### Học được gì
- **Docker không có informer.** Kubernetes có list + watch với `resourceVersion` nên client có thể duy trì một cache chính xác. Docker chỉ có `/events`: một stream "chuyện gì vừa xảy ra" (`container web: die`), không kèm trạng thái đầy đủ của object. Cách tự nhiên: **list một lần, theo dõi events, list lại khi có thay đổi**.
- **List lại thay vì tự vá từng dòng**: list qua socket local rất rẻ, và luôn đúng kể cả khi bỏ lỡ event — không phải tái tạo logic "event X thì sửa field Y" của daemon.
- **Debounce**: `docker compose up` sinh hàng chục event trong một giây; chỉ cần *một* lần list sau khi "lắng xuống".
- **Reconnect với exponential backoff**: 1s, 2s, 4s… tối đa 30s, và reset khi daemon trả lời bình thường trở lại.
- **Một API Go cần đọc kỹ**: channel `Messages` trong `EventsResult` của Moby **không bao giờ bị đóng**; lỗi (kể cả hết stream) đến qua `Err`.

### Thể hiện trong code

**k8s-less "watch"** (`internal/docker/events.go` → `Events(ctx)`):

```go
filters := mobyclient.Filters{}.Add("type", "container", "image")
res := c.api.Events(ctx, mobyclient.EventsListOptions{Filters: filters})
go func() {
    defer close(out)
    for {
        select {                                   // phải nghe cả hai channel
        case m := <-res.Messages: … out <- newEvent(m) …
        case err := <-res.Err:  if err != nil && ctx.Err() == nil { errs <- err }; return
        case <-ctx.Done():      return
        }
    }
}()
```

Trả về đúng "hợp đồng" quen thuộc từ `StreamLogs` của kboba: một channel dữ liệu được đóng khi stream kết thúc, lỗi (nếu có, trừ khi do mình huỷ) được gửi *trước* khi đóng. `newEvent` lấy tên container từ `Actor.Attributes["name"]`.

**Watcher** (`internal/ui/events.go` → `eventsWatcher`): không phải một view — chỉ có state và `Update`, sống trong root model.

```
connected → events.start() ──► eventsStartedMsg ──► waitForEvents (subscribe)
dockerEventsMsg  → đánh dấu dirty (container / image), chưa có timer thì tea.Tick(200ms) → debounceMsg
debounceMsg      → reloadMsg{containers, images} → root gọi containers.load()
eventsEndedMsg   → stop(), state = reconnecting, backoff = next, tea.Tick(backoff) → reconnectMsg
reconnectMsg     → start() lại + reloadMsg{tất cả}  (event bị lỡ khi mất kết nối → đồng bộ lại bằng list)
```

- Mọi message mang `gen` của stream; mỗi lần `start()` tăng `gen`, nên thứ còn sót lại từ stream cũ (một `eventsEndedMsg` đến muộn) bị bỏ qua.
- **Debounce bằng một cờ**: `debouncing` đảm bảo chỉ có một timer; event đến trong lúc chờ chỉ đánh dấu dirty. Timer kết thúc → một `reloadMsg` gộp tất cả.
- **Backoff**: `nextBackoff` = 1s, rồi gấp đôi, tối đa 30s. `healthy()` reset về 0 — root gọi nó mỗi khi `ListContainers` thành công. Không reset lúc stream vừa "start" vì `Events()` trả channel ngay cả khi daemon đang chết (lỗi đến ngay sau đó) → reset ở đó sẽ tạo vòng lặp 1s mãi mãi.
- Header (`Model.headerView`): `● live <event cuối>` (xanh) hoặc `○ reconnecting` (vàng).

**Fake daemon chạy được** có `/events` thật (JSON lines, `Flush` sau mỗi event) và tham số `-restart-every`: container `worker` đổi running ↔ restarting định kỳ, kèm event `die`/`start` — đủ để nhìn bảng cập nhật realtime khi không có Docker.

### Test liên quan
- `internal/docker/events_test.go`: `TestEventsStream` (fake daemon stream JSON lines; chuyển đổi Kind/Action/Name/Time; query có filter container+image; daemon "biến mất" → channel đóng *và* có lỗi), `TestEventsCancelIsNotAnError`.
- `internal/ui/root_test.go`: `TestEventsTriggerDebouncedReload` (hai event liên tiếp → chưa list; debounce → đúng **một** lần list; event image không list containers; header có `● live` và event cuối), `TestEventsReconnectWithBackoff` (backoff 1s, context stream cũ bị huỷ, reconnect → stream mới + list lại → backoff reset; `eventsEndedMsg` cũ bị bỏ qua), `TestBackoffGrows` (1, 2, 4, 8, 16, 30, 30).
- Thủ công với `hack/fakedaemon -restart-every 2s`: `worker` đổi trạng thái theo event; kill daemon → `○ reconnecting` + lỗi "Cannot connect to the Docker daemon…"; chạy lại daemon → tự về `● live`.

### Bẫy
| Bẫy | Cách xử lý |
|---|---|
| `EventsResult.Messages` không bao giờ đóng → goroutine chờ mãi nếu chỉ `range` | `select` trên cả `Messages`, `Err`, `ctx.Done()` |
| Huỷ context bị báo như lỗi | Chỉ gửi lỗi khi `ctx.Err() == nil` |
| Burst event → list hàng chục lần | Debounce 200ms bằng một timer duy nhất |
| Event bị lỡ khi mất kết nối | Reconnect luôn kèm một lần list lại toàn bộ |
| Reset backoff khi stream "start" → vòng lặp 1s khi daemon chết | Chỉ reset khi một lệnh list thật sự thành công |
| Test harness bỏ Cmd chờ channel sau 50ms (bài học từ kboba 3.1) | Test đưa `dockerEventsMsg`/`debounceMsg`/`eventsEndedMsg` vào trực tiếp; luồng stream thật được test ở `internal/docker` |

## 4. Phase (c): Images

### Mục tiêu
`:images` (`:img`, `:i`) hiện bảng REPOSITORY, TAG, IMAGE ID, SIZE, IN USE, AGE giống `docker images`, cộng tổng số image và dung lượng trên tiêu đề. Image không container nào dùng (ứng viên cho `docker image prune`) có màu mờ. Bảng cũng cập nhật theo events.

### Học được gì
- **Một image, nhiều tag:** `ImageList` trả mỗi image *một lần* với `RepoTags []string`; CLI hiện mỗi tag một dòng. Dòng không còn duy nhất theo ID → khoá dòng là `ID + repo:tag`. Tổng dung lượng phải đếm mỗi ID một lần.
- **Tách `repo:tag` đúng cách:** dấu `:` cuối cùng *không* luôn là tag — `localhost:5000/app` có cổng registry mà không có tag. Quy tắc: lấy `:` cuối; nếu phần sau nó chứa `/` thì đó là cổng, không phải tag.
- **Ghép dữ liệu phía client:** trường `Containers` của image summary mặc định là `-1` (daemon chỉ tính khi được yêu cầu dữ liệu disk-usage, tốn kém). Một `ContainerList(all)` rất rẻ → tự đếm `ImageID` → `IN USE`.
- **Event nào ảnh hưởng view nào:** container `create`/`destroy` làm thay đổi `IN USE` của image, nên watcher đánh dấu cả images là dirty cho hai action này (start/stop thì không).
- **Định dạng dung lượng kiểu docker CLI:** đơn vị thập phân (kB = 1000), ~3 chữ số có nghĩa.

### Thể hiện trong code

**k8s-less list** (`internal/docker/images.go`):
- `ListImages`: `ImageList` + `ContainerList(All: true)` → `inUse[ImageID]++` → mỗi `RepoTags` một `Image`; image dangling (không tag) → `<none>:<none>`. Sắp xếp theo repository rồi tag.
- `splitRef`: `LastIndex(":")`, bỏ qua nếu phần sau có `/`.
- `Image.Ref()` (`repo:tag`, hoặc short ID khi không có tag) — dùng cho các thao tác ở phase sau (`rmi`).

**UI** (`internal/ui/images.go` → `imagesView`): cùng khuôn với `containersView` (load có `gen`, giữ lựa chọn theo khoá *trước* khi thay danh sách — bài học của phase (a)), khác ở khoá dòng (`imageRowKey`) và màu (không ai dùng → `finished`/xám).
- `formatSize` (`format.go`): chia 1000 cho tới khi < 1000, in `%.3g`. Bẫy: `%.3g` của 999.95 cho ra `1e+03` → phát hiện chữ `e` và in số nguyên.

**Root**: `viewImages`; `:images`/`:img`/`:i`; load images lúc kết nối cùng containers; `reloadMsg.images` → `images.load()`; `imagesLoadedMsg` thành công cũng gọi `events.healthy()`.

**Events** (`events.go`): container `create`/`destroy` → `dirtyImages = true`.

**Fake daemon chạy được**: thêm `/images/json` với 5 image (nginx 2 tag, một image dangling) và gán `ImageID` cho các container mẫu.

### Test liên quan
- `internal/docker/images_test.go`: `TestListImages` (một dòng mỗi tag, dangling `<none>`, registry có cổng, IN USE từ danh sách container `all=1`, `Ref`/`ShortID`), `TestSplitRef`.
- `internal/ui/root_test.go`: `TestImagesView` (cột, `-` cho không dùng, tổng `2 images, 187MB` không đếm trùng tag), `TestContainerCreateReloadsImages` (`start` không list images, `create` thì có), `TestFormatSize` (gồm ca `1000MB`).
- Thủ công: `:img` trên fake daemon — IN USE khớp số container, dangling màu mờ.

### Bẫy
| Bẫy | Cách xử lý |
|---|---|
| Khoá dòng theo image ID → trùng khi một image có nhiều tag | Khoá `ID + repo:tag` |
| Tổng dung lượng đếm một image nhiều lần | Đếm theo ID đã thấy |
| `localhost:5000/app` bị tách thành repo `localhost`, tag `5000/app` | Phần sau `:` cuối chứa `/` → không phải tag |
| `Containers` của image summary là `-1` | Tự đếm từ `ContainerList(all)` |
| `%.3g` in `1e+03` | Phát hiện và in dạng số nguyên |

