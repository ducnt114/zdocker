# zdocker: nhật ký học tập

zdocker là "anh em" của [kboba](https://github.com/ducnt114/kboba): cùng kiến trúc Bubble Tea (root model điều phối, mỗi màn hình một sub-model, mọi việc chậm đi qua `tea.Cmd`), nhưng nói chuyện với Docker Engine API thay vì Kubernetes. Tài liệu này ghi lại từng phase: làm gì, học được gì, và nằm ở đâu trong code. Những pattern đã giải thích kỹ trong `kboba/docs/LEARNING.md` chỉ được nhắc lại ngắn gọn ở đây.

> Thuật ngữ kỹ thuật giữ nguyên tiếng Anh. Code được tham chiếu theo **file + tên symbol**.

## Mục lục

1. [Bức tranh tổng thể](#1-bức-tranh-tổng-thể)
2. [Phase (a): Skeleton, kết nối, bảng containers](#2-phase-a-skeleton-kết-nối-bảng-containers)
3. [Phase (b): Realtime bằng Docker events](#3-phase-b-realtime-bằng-docker-events)
4. [Phase (c): Images](#4-phase-c-images)
5. [Phase (d): Logs](#5-phase-d-logs)
6. [Phase (e): Inspect, filter, sort](#6-phase-e-inspect-filter-sort)
7. [Phase (f): Thao tác, xác nhận, `--read-only`](#7-phase-f-thao-tác-xác-nhận---read-only)
8. [Phase (g): Stats](#8-phase-g-stats)
9. [Tổng kết MVP và Phase 2](#9-tổng-kết-mvp-và-phase-2)

**Phase 2**

10. [Phase 2.1: Volumes và Networks](#10-phase-21-volumes-và-networks)
11. [Phase 2.2: Nhóm theo Compose project](#11-phase-22-nhóm-theo-compose-project)
12. [Phase 2.3: Docker context](#12-phase-23-docker-context)
13. [Phase 2.4: Pull image](#13-phase-24-pull-image)
14. [Phase 2.5: Prune](#14-phase-25-prune)
15. [Phase 2.6: Shell vào container](#15-phase-26-shell-vào-container)

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

## 5. Phase (d): Logs

### Mục tiêu
Enter trên một container → theo dõi log (`docker logs -f --tail 500`): follow `f`, tìm kiếm `/` `n` `N`, wrap `w`, timestamps `t`, cuộn ngang. Dòng **stderr** có vạch đỏ ở lề trái. Rời view thì stream bị huỷ.

### Học được gì
- **Hai định dạng log của Docker**:
  - Container **có TTY** (`docker run -t`): một stream thô, stdout và stderr đã hoà làm một từ trong container.
  - Container **không có TTY** (mặc định): daemon *ghép kênh* (multiplex) stdout và stderr vào một stream gồm các frame: `[loại stream, 0, 0, 0, độ dài big-endian 4 byte] + dữ liệu`. `stdcopy.StdCopy(stdout, stderr, r)` tách lại.
  - Muốn biết container có TTY không phải `ContainerInspect` trước (`Config.Tty`).
- **Frame không trùng với dòng**: một dòng có thể bị cắt giữa hai frame, một frame có thể chứa nhiều dòng → cần một writer gom byte rồi cắt theo `\n`.
- **Giữ thứ tự giữa stdout và stderr**: `StdCopy` ghi các frame *tuần tự trong một goroutine*. Nếu writer gửi dòng ngay khi đủ (thay vì đọc stdout và stderr bằng hai scanner song song qua hai `io.Pipe`), thứ tự xen kẽ được giữ nguyên.
- **Đánh dấu dữ liệu bằng ký tự điều khiển**: ring buffer chỉ chứa `string`; dòng stderr được tiền tố `\x1f` (Unit Separator — gần như không bao giờ có trong log thật) và bị gỡ trước khi hiển thị/tìm kiếm.
- **API mới của Moby có thể bỏ phần "ghi" của một gói**: `stdcopy` trong module `api` chỉ còn phía đọc (`StdCopy`); `NewStdWriter` thuộc phía daemon → test tự dựng frame, nhờ đó cũng thấy rõ định dạng.

### Thể hiện trong code

**`internal/docker/logs.go` → `ContainerLogs(ctx, id, opts)`**:
1. `ContainerInspect` → `tty := Config.Tty`.
2. `ContainerLogs(ShowStdout, ShowStderr, Follow, Tail: "500", Timestamps)` → một `io.ReadCloser`.
3. Goroutine: `io.Copy(stdoutWriter, body)` (TTY) hoặc `stdcopy.StdCopy(stdoutWriter, stderrWriter, body)`; cuối cùng `flush()` dòng cuối không có `\n`; lỗi (trừ EOF và huỷ) → `errs`; `close(out)`.

**`lineWriter`**: `Write` nối byte vào `buf`, mỗi khi gặp `\n` thì `send` một `LogLine{Text, Stderr}` (bỏ `\r` cuối của TTY); "dòng" dài quá 1MB không có `\n` thì cắt luôn. `send` dùng `select` với `ctx.Done()` và trả `ctx.Err()` → `StdCopy`/`io.Copy` dừng ngay khi huỷ.

**`internal/ui/logs.go` → `logsView`**: chuyển thể từ logs view của kboba (ring buffer 5000 dòng, `receiveBatch`, render một lần mỗi batch, follow tự tắt khi cuộn lên, search với số dòng tuyệt đối, wrap với bảng `lineOffsets`, generation guard) — các file `logbuffer.go`, `logsearch.go` được copy kèm test và ghi chú nguồn. Phần riêng:
- Đẩy vào buffer: `stderrMark + text` cho stderr.
- `render`: `strings.CutPrefix(line, stderrMark)` → gutter `▍ ` đỏ hoặc hai dấu cách; khi wrap, các dòng tiếp theo cũng mang gutter (`ReplaceAll("\n", "\n"+gutter)`), độ rộng wrap trừ 2 cột gutter.
- Không có `c` (container) hay `p` (previous) như kboba: một container Docker chỉ có một luồng log.

**Root**: `openLogsMsg` (Enter trong bảng containers) → `logs.open`; `setActive` huỷ stream khi rời `viewLogs` (một chỗ dọn dẹp duy nhất, như kboba); `backMsg` → bảng containers.

**Fake daemon chạy được**: `GET /containers/{id|tên}/json` (inspect, không TTY) và `/logs` ghi frame thật (5 dòng cũ, rồi với `follow=1` cứ 700ms một dòng, mỗi dòng thứ 4 là stderr `WARN …`), có `timestamps=1`.

### Test liên quan
- `internal/docker/logs_test.go`: `TestContainerLogsDemultiplexes` (dòng bị cắt qua hai frame được nối, thứ tự stdout/stderr xen kẽ đúng, `\r\n`, dòng cuối không `\n`, query có `follow/stdout/stderr/tail=500/timestamps`), `TestContainerLogsTTY`, `TestContainerLogsCancel` (follow không bao giờ kết thúc → huỷ thì channel đóng, không báo lỗi), `TestContainerLogsUnknownContainer`.
- `internal/ui/root_test.go`: `TestLogsView` (gutter stderr, search khớp dòng stderr mà không vướng ký tự đánh dấu, `t` restart với timestamps và huỷ stream cũ, Esc hai tầng rồi huỷ stream), `TestLogsStreamEnded`; cùng các test ring buffer/search copy từ kboba.
- Thủ công với fake daemon: log chảy liên tục, dòng `WARN` có vạch đỏ (kiểm bằng `tmux capture-pane -e`).

### Bẫy
| Bẫy | Cách xử lý |
|---|---|
| Đọc log của container không TTY như text thường → rác 8 byte đầu mỗi frame | Inspect `Config.Tty`, dùng `stdcopy.StdCopy` |
| Dòng bị cắt giữa hai frame | `lineWriter` gom byte, cắt theo `\n` |
| Hai scanner song song cho stdout/stderr → đảo thứ tự | Gửi dòng ngay trong `Write`, cùng goroutine với `StdCopy` |
| Huỷ nhưng `StdCopy` vẫn chặn khi gửi | `send` có `select` trên `ctx.Done()` và trả lỗi |
| Không có `stdcopy.NewStdWriter` để tạo dữ liệu test | Tự dựng frame bằng `encoding/binary` |
| Test kỳ vọng Esc rời view ngay, nhưng search còn giữ sau restart | Hành vi đúng (như kboba): Esc đầu xoá search, Esc sau mới rời |

## 6. Phase (e): Inspect, filter, sort

### Mục tiêu
- `i` trên container hoặc image → `docker inspect` (JSON có tô màu, cuộn ngang, `r` làm mới, Esc về đúng bảng đã mở nó).
- `/` lọc bảng (containers: theo tên, image, compose project; images: theo `repo:tag` và ID).
- `s` chọn cột sắp xếp, `S` đảo chiều, mũi tên trên header — cho cả hai bảng.

### Học được gì
- **Hiện đúng thứ daemon trả về**: `ContainerInspectResult.Raw` là JSON gốc; dùng nó (thay vì encode lại struct đã decode) nghĩa là cả những field mà thư viện client chưa biết (daemon mới hơn client) vẫn hiện ra. Image thì khác: result không có `Raw`, phải xin bằng option `ImageInspectWithRawResponse(&buf)`. Hai API gần giống nhau nhưng không đối xứng — đọc kỹ type.
- **Tách "hiển thị" và "giá trị để sắp xếp"**: SIZE hiện `187MB` nhưng phải sắp theo số byte (dạng chữ thì `"187MB" < "4.26MB" < "5kB"`); AGE hiện `3h` nhưng sắp theo thời điểm tạo. Mỗi dòng mang `sortBy` song song với `cells`. Kboba xử lý AGE bằng một trường hợp đặc biệt; ở đây một cơ chế chung thay cho mọi trường hợp đặc biệt.
- **Mã hoá giá trị để so sánh dạng chuỗi**: `naturalLess` so phần số như số, nên chỉ cần biến giá trị thành *chuỗi số đệm 0*: size → `%020d`; tuổi (trẻ nhất trước) → `MaxInt64 - createdNano` đệm 0.
- **Gom phần lặp lại thành component khi có lần dùng thứ hai**: containers và images cùng cần filter/sort/giữ lựa chọn → `filterTable`.

### Thể hiện trong code

**Inspect** (`internal/docker/inspect.go` → `Inspect(ctx, kind, id)`): container dùng `res.Raw`; image dùng `ImageInspectWithRawResponse(&buf)`; rồi `json.Indent` hai dấu cách. `Kind` (`KindContainer`, `KindImage`) là kiểu riêng của zdocker.

**`inspectView`** (`internal/ui/inspect.go`): viewport + `highlightJSON` (chroma, chạy trong Cmd), id của request (`kind:id`) để bỏ kết quả trễ, `SetHorizontalStep(4)` thay cho wrap (wrap làm hỏng thụt lề) — các ý tưởng từ `detailView` của kboba.

**Quay về đúng bảng** (`Model.returnTo`): khi mở logs hoặc inspect, root ghi nhớ view hiện tại; `backMsg` → `setActive(m.returnTo)`.

**`filterTable`** (`internal/ui/filtertable.go`): `tableModel` + ô `/` + sắp xếp + giữ lựa chọn.

```go
type tableRow struct {
    key    string   // để giữ lựa chọn khi dữ liệu đổi
    cells  []string // hiển thị
    sortBy []string // (tuỳ chọn) giá trị để sắp xếp từng cột
    filter string   // text mà "/" tìm trong đó
    style  lipgloss.Style
}
```

- `SetRows` lưu key đang chọn → lọc → sắp → vẽ lại → đặt con trỏ về key đó. View không còn phải tự lo chuyện giữ lựa chọn (bài học phase (a) nằm ở một chỗ).
- Sắp xếp: `slices.SortStableFunc` với `naturalLess(sortValue(col))`; thế hoà giữ nguyên thứ tự đầu vào (đã sắp theo tên) → dòng bằng nhau không nhảy khi có event. `S` khi chưa chọn cột thì đảo thứ tự mặc định.
- Ô filter đang focus → `Capturing()` true → root đẩy mọi phím vào (gõ `q` không thoát). Esc: huỷ ô nhập; Esc lần nữa xoá filter nếu còn.
- `View(title, note)`: dòng tiêu đề gồm số dòng, ghi chú (`loading…`, `5 images, 345MB`) và filter đang áp dụng.

**Views gọn lại**: `containersView`/`imagesView` chỉ còn (1) nạp dữ liệu, (2) dựng `[]tableRow` (cells, sortBy, filter, màu), (3) phím riêng (`enter`, `i`, `a`, `r`). `items` là map theo key để tra từ lựa chọn ra object.

```go
sortBy: []string{5: ageSortKey(c.Created)}                       // containers: cột 5 = AGE
sortBy: []string{3: fmt.Sprintf("%020d", img.Size), 4: …, 5: ageSortKey(img.Created)} // images
```

(Cú pháp `[]string{5: x}` của Go: phần tử ở index 5, các index trước là chuỗi rỗng → `sortValue` rơi về `cells`.)

### Test liên quan
- `internal/docker/inspect_test.go`: JSON thô có field lạ `FutureField` vẫn hiện; image qua raw option; không tồn tại → lỗi.
- `internal/ui/root_test.go`: `TestFilterContainers` (khớp theo image, tiêu đề hiện filter, `q` khi đang gõ không thoát, Esc hai lần), `TestSortImagesBySizeAndAge` (theo byte đúng thứ tự ngược với chữ, mũi tên `SIZE↑`, AGE trẻ nhất trước, `S` đảo), `TestSelectionSurvivesSortAndFilter`, `TestInspect` (tiêu đề, nội dung, Esc về bảng images, kết quả trễ bị bỏ); `TestNaturalLess` copy từ kboba.
- Thủ công với fake daemon: `/shop` lọc ra cả `web` (nhờ compose project), `i` hiện inspect, sort SIZE↓ đúng theo byte.

### Bẫy
| Bẫy | Cách xử lý |
|---|---|
| Encode lại struct đã decode làm mất field mà client chưa biết | Hiện `Raw` của daemon |
| `ImageInspectResult` không có `Raw` | `ImageInspectWithRawResponse(&buf)` |
| Sắp xếp SIZE/AGE theo chữ hiển thị | `sortBy` song song, chuỗi số đệm 0 |
| Trộn đơn vị khi so sánh (`kB`/`MB`) | So số byte, không so chữ |
| Mỗi view tự viết lại filter/sort/giữ lựa chọn | `filterTable` dùng chung |
| Đường dẫn `/images/shop/api:dev/json` có `/` trong tham chiếu (fake daemon) | Cắt tiền tố/hậu tố thay vì tách theo `/` |

## 7. Phase (f): Thao tác, xác nhận, `--read-only`

### Mục tiêu
Lần đầu tiên tool **thay đổi** thứ gì đó (khác hẳn kboba):

| Phím | Container | Image | Xác nhận |
|---|---|---|---|
| `u` | start | | không |
| `x` | stop (SIGTERM, sau 10s thì SIGKILL) | | có |
| `R` | restart | | có |
| `D` | remove (`-f` nếu đang chạy, và hỏi rõ điều đó) | remove tag / image | có |

Cờ `--read-only` tắt toàn bộ thao tác ghi: header có nhãn `read-only`, các phím ghi biến khỏi help, bấm vào thì báo lỗi ngay.

### Học được gì
- **Thiết kế cho thao tác phá huỷ**: xác nhận rõ ràng *cái gì* sẽ xảy ra ("Container web is running: kill and remove it?"), chỉ `y` mới chạy — **mọi phím khác đều huỷ** (kể cả bấm lại phím vừa bấm), vì một phím lạc tay không được phép xoá thứ gì.
- **Ngữ nghĩa `docker rmi`**: xoá theo `repo:tag` là *gỡ tag*; image chỉ biến mất khi tag cuối cùng bị gỡ. Xoá theo ID một image có nhiều tag thì daemon từ chối (trừ khi force). Image đang được container dùng → daemon từ chối (409) — zdocker cảnh báo trước trong câu hỏi xác nhận nhưng để daemon là người quyết định.
- **Không cập nhật bảng bằng tay sau thao tác**: daemon phát event (`die`, `destroy`, `untag`…) → watcher của phase (b) list lại. Một nguồn sự thật duy nhất.
- **Chặn ở đâu**: ẩn phím trong UI chỉ là trình bày; chặn thật nằm ở client (`docker.ReadOnly`). Và một test bằng `reflect` buộc mọi method mới phải được *phân loại* đọc/ghi.
- **Embed interface trong struct** để làm decorator: `type readOnly struct{ Client }` tự chuyển tiếp mọi method; chỉ cần viết lại các method ghi. Mặt trái: method ghi *mới* cũng được chuyển tiếp — đó là lý do có test phân loại.

### Thể hiện trong code

**Client** (`internal/docker/actions.go`): `StartContainer`, `StopContainer`/`RestartContainer` (`Timeout: &10` giây — giá trị mặc định của `docker stop`, truyền tường minh), `RemoveContainer(id, force)` (không xoá volume), `RemoveImage(ref, force)`.

**`ReadOnly`** (`internal/docker/readonly.go`): `readOnly{Client}` ghi đè 5 method ghi để trả `ErrReadOnly`, không gửi request nào.

**Test phân loại** (`actions_test.go`):
- `TestEveryMethodIsClassified`: duyệt method của interface `Client` bằng `reflect`; mỗi method phải nằm trong `readMethods` hoặc `writeMethods`.
- `TestReadOnlyBlocksEveryWrite`: gọi **từng** method trong `writeMethods` qua `reflect` (context nền + giá trị zero cho các tham số khác) trên `ReadOnly(client)` → đều là `ErrReadOnly`, và fake daemon không nhận request nào.
- `TestWriteRequests`: đúng method/endpoint/query (`POST …/stop?t=10`, `DELETE /containers/c1?force=1`, `DELETE /images/nginx:1.27`).

**UI** (`internal/ui/actions.go`):
- View *mô tả* thao tác, root *thực hiện*: `containerAction`/`imageAction` dựng một `actionMsg{prompt, progress, done, run}` (`run` là closure gọi client). Views không bao giờ tự gọi API ghi.
- Root (`root.go`): nhận `actionMsg` → read-only thì báo lỗi luôn; có `prompt` thì lưu vào `m.confirm`; không thì `startAction`. `handleKey` xử lý `m.confirm` **trước mọi thứ**: `y`/`Y` → chạy, phím khác → "cancelled". Dòng status hiện câu hỏi (`pendingAction.View`).
- `runAction` chạy trong Cmd với `actionTimeout` 60s (stop có thể mất 10s, xoá image lớn mất lâu hơn `requestTimeout`) → `actionDoneMsg` → "web stopped" hoặc lỗi của daemon.
- `keys(readOnly)` của mỗi view bỏ các phím ghi khi read-only; header thêm nhãn `read-only`.

**`main.go`**: `--read-only` → `client = docker.ReadOnly(client)` *và* `Options{ReadOnly: true}` — chặn ở client, ẩn ở UI.

**Fake daemon chạy được**: `POST start/stop/restart` đổi trạng thái và phát event; `DELETE /containers/{id}` (409 nếu đang chạy mà không `force=1`); `DELETE /images/{ref}` gỡ tag hoặc xoá, 409 nếu image đang được dùng; lỗi trả JSON `{"message": …}` với content type JSON giống daemon thật.

### Test liên quan
- `internal/docker/actions_test.go`: ba test ở trên + `TestWriteErrorsComeFromTheDaemon` (409 "must force" truyền nguyên văn).
- `internal/ui/root_test.go`: `TestStopNeedsConfirmation` (`n` huỷ, bấm lại `x` cũng huỷ, `y` chạy và báo "web stopped"), `TestStartNeedsNoConfirmation`, `TestRemoveContainerForceOnlyWhenRunning`, `TestRemoveImageByTagOrID` (dangling → theo ID, có tag → theo tag, cảnh báo "used by 1 container(s)"), `TestActionErrorShown`, `TestReadOnlyMode` (nhãn, phím ẩn, `x` báo lỗi không hỏi, không có write nào).
- Thủ công với fake daemon: `x` `y` → event `die` → bảng tự cập nhật; `D` trên `nginx:1.27` → daemon từ chối với thông báo rõ; `--read-only` → nhãn và lỗi.

### Bẫy
| Bẫy | Cách xử lý |
|---|---|
| Phím lạc tay chạy thao tác phá huỷ | Chỉ `y` xác nhận; mọi phím khác huỷ |
| `docker rm` container đang chạy bị từ chối | Hỏi rõ "kill and remove", rồi `force` |
| Xoá image theo ID khi có nhiều tag | Xoá theo `repo:tag` (gỡ tag); ID chỉ cho image không tag |
| Tự sửa bảng sau thao tác → lệch với daemon | Để event của daemon kích hoạt list lại |
| Read-only chỉ ẩn phím ở UI | `docker.ReadOnly` chặn ở client |
| Embed interface chuyển tiếp cả method ghi mới thêm | `TestEveryMethodIsClassified` + `TestReadOnlyBlocksEveryWrite` |
| Lỗi hiện nguyên JSON vì fake daemon trả `text/plain` | Trả lỗi với `Content-Type: application/json` như daemon thật |

## 8. Phase (g): Stats

### Mục tiêu
`t` trên một container → mức dùng tài nguyên realtime, như `docker stats` cho một container: CPU %, MEM (đã dùng / giới hạn / %), NET ↓↑, BLOCK read/write, PIDS — với **sparkline** 60 mẫu gần nhất (~1 phút) cho CPU và MEM.

### Học được gì
- **Stats của Docker là số đếm luỹ kế**, không phải phần trăm: `cpu_usage.total_usage` (ns CPU container đã dùng từ lúc chạy), `system_cpu_usage` (ns CPU của cả host từ lúc boot). Phần trăm = *chênh lệch* giữa hai lần đọc. Daemon gửi sẵn lần đọc trước trong `precpu_stats`:

  `CPU% = (Δ container / Δ hệ thống) × số CPU × 100` — 100% = một CPU đầy, 4 CPU có thể tới 400%.
- **Mẫu đầu tiên không có lần đọc trước** (`precpu.system = 0`): chênh lệch so với 0 là vô nghĩa (đếm từ lúc boot) → trả 0 ("chưa biết"). Test đã bắt chỗ code nói một đằng làm một nẻo.
- **Bộ nhớ "dùng" không bằng `usage`**: `usage` gồm page cache (có thể thu hồi). Như CLI, trừ `inactive_file` (cgroup v2) hoặc `total_inactive_file` (cgroup v1, ưu tiên nếu có).
- **Đơn vị**: `docker stats` hiện bộ nhớ theo đơn vị nhị phân (MiB, GiB) nhưng mạng/đĩa theo thập phân (kB, MB) — giữ đúng như vậy để người quen CLI không bối rối.
- **Một stream các object JSON**: `json.NewDecoder(body)` + `Decode` lặp lại đọc từng object khi chúng đến (không cần tách dòng).
- **Sparkline** chỉ là ánh xạ giá trị → 8 ký tự `▁▂▃▄▅▆▇█`, theo tỷ lệ với đỉnh trong cửa sổ (có "sàn" để nhiễu nhỏ không bị phóng to thành cả cột).

### Thể hiện trong code

**`internal/docker/stats.go`**:
- `ContainerStats(ctx, id)`: `api.ContainerStats(…, {Stream: true})` → goroutine `Decode` từng `container.StatsResponse` → `newStats` → channel; cùng hợp đồng channel + lỗi như logs/events.
- `newStats` / `cpuPercent`: hàm thuần — toàn bộ phép tính ở trên, test bằng bảng ca. `cpuPercent` dùng `OnlineCPUs`, thiếu thì đếm `PercpuUsage`.
- NET: cộng `RxBytes`/`TxBytes` của mọi interface; BLOCK: cộng `io_service_bytes_recursive` theo `op` `read`/`write` (cả hai kiểu viết hoa, tuỳ cgroup).

**`internal/ui/stats.go` → `statsView`**: vòng đời giống logs (context huỷ được, `gen`, `receiveBatch`, huỷ trong `setActive` khi rời view); `history` giữ tối đa 60 mẫu (cắt slice); `View` tính đỉnh trong cửa sổ, `sparkline(values, width, floor)` (sàn: 1% CPU, 1 MiB), `formatBinary` cho bộ nhớ.

**Fake daemon chạy được**: `/containers/{id}/stats?stream=true` gửi một mẫu mỗi giây với `precpu_stats` là mẫu trước, CPU/MEM dao động theo hàm sin để sparkline có hình.

### Test liên quan
- `internal/docker/stats_test.go`: `TestCPUPercent` (1 trên 4 CPU = 100%, nửa CPU, mẫu đầu = 0, không có Δ hệ thống, dự phòng bằng `PercpuUsage`), `TestMemoryNetworkBlock` (trừ cache v2 và v1 — v1 ưu tiên, cộng nhiều interface, `read`/`Read`, bỏ `total`), `TestContainerStatsStream` (stream nhiều object, `stream=true`).
- `internal/ui/root_test.go`: `TestStatsView` (các con số và định dạng, lịch sử giới hạn 60, mẫu cũ bị bỏ, Esc huỷ stream), `TestSparkline`, `TestFormatBinary`.
- Thủ công với fake daemon: sparkline chạy mỗi giây, mẫu CPU đầu tiên là `▁`.

### Bẫy
| Bẫy | Cách xử lý |
|---|---|
| Hiểu `total_usage` là phần trăm | Lấy chênh lệch với `precpu_stats` |
| Mẫu đầu tiên ra số khổng lồ/vô nghĩa | `precpu.system == 0` → 0 |
| MEM cao hơn thực tế (gồm page cache) | Trừ `inactive_file` / `total_inactive_file` |
| Lẫn đơn vị nhị phân/thập phân | MiB/GiB cho bộ nhớ, kB/MB cho mạng/đĩa — như CLI |
| Test giả định query `stream=1` | Moby gửi `stream=true` — đọc request thật trong fake daemon |

## 9. Tổng kết MVP và Phase 2

MVP gồm 7 phase, mỗi phase một commit: kết nối + bảng containers (a), realtime bằng events (b), images (c), logs (d), inspect + filter + sort (e), thao tác có xác nhận + `--read-only` (f), stats (g).

So với kboba, zdocker cho thấy cùng một kiến trúc Bubble Tea áp vào một API rất khác: không có informer → events + list lại có debounce; có thao tác ghi → mô tả ở view, thực hiện ở root, xác nhận chỉ bằng `y`, chặn thật ở client; stream log ghép kênh → `stdcopy` + writer giữ thứ tự; stats luỹ kế → tính chênh lệch. Và một cách test mới: fake daemon bằng `httptest` cho phép chạy **client Moby thật** mà không cần Docker.

### Phase 2

| Phase | Tính năng | Học được gì | Trạng thái |
|---|---|---|---|
| 2.1 | Volumes, Networks | View generic `listView[T]`, message generic | ✅ [mục 10](#10-phase-21-volumes-và-networks) |
| 2.2 | Nhóm theo Docker Compose project | Nhóm/gập dòng trong bảng, label `com.docker.compose.*` | ✅ [mục 11](#11-phase-22-nhóm-theo-compose-project) |
| 2.3 | Chọn docker context (`:ctx`) | Đọc `~/.docker/contexts`, tạo lại client như kboba đổi kube context | ✅ [mục 12](#12-phase-23-docker-context) |
| 2.4 | `pull` image (có tiến trình) | Stream JSON tiến trình của từng layer, progress bar | ✅ [mục 13](#13-phase-24-pull-image) |
| 2.5 | `prune` (containers đã dừng, images dangling) | Thao tác hàng loạt, xác nhận kèm con số sẽ bị xoá | ✅ [mục 14](#14-phase-25-prune) |
| 2.6 | `exec` vào shell của container | `tea.ExecProcess`: tạm nhường terminal cho `docker exec -it` rồi quay lại TUI | ✅ [mục 15](#15-phase-26-shell-vào-container) |
| 2.7 | Cột CPU/MEM ngay trong bảng containers | Nhiều stream stats song song hoặc lấy mẫu một lần có giới hạn | ⏳ |

---

# Phase 2

## 10. Phase 2.1: Volumes và Networks

### Mục tiêu
`:volumes` (`:vol`, `:v`) và `:networks` (`:net`, `:n`): bảng có filter/sort, cột IN USE (bao nhiêu container dùng), `i` inspect, `D` xoá có xác nhận, cập nhật theo events. Volume/network không ai dùng có màu mờ (ứng viên cho prune); tiêu đề volumes đếm số không dùng.

### Học được gì
- **Generics cho view**: volumes và networks giống nhau về mọi mặt trừ *kiểu dữ liệu* và *cách dựng dòng*. Thay vì chép `imagesView` hai lần, một `listView[T]` nhận một `listSpec[T]` (title, load, key, row, columns, inspect, remove).
- **Message generic trong type switch**: `listLoadedMsg[docker.Volume]` và `listLoadedMsg[docker.Network]` là **hai kiểu khác nhau** — `case listLoadedMsg[docker.Volume]:` trong root định tuyến đúng view mà không cần thêm trường "loại".
- **"Đang được dùng" lại là một phép ghép**: daemon không cho biết volume nào đang được mount hay network nào có container — đếm từ `ContainerList(all)`: `Mounts` có `Type == volume` (một bind mount trùng tên không tính), `NetworkSettings.Networks[*].NetworkID`.
- **Event nào ảnh hưởng gì** mở rộng thêm: event `volume`/`network` → list lại bảng tương ứng; container `create`/`destroy` → list lại images, volumes **và** networks (IN USE của cả ba đổi).
- **Từ chối sớm ở UI những gì chắc chắn thất bại**: `bridge`, `host`, `none` là network dựng sẵn → báo lỗi ngay, không hỏi xác nhận. Những gì *có thể* thất bại (xoá volume đang dùng) thì vẫn hỏi, kèm cảnh báo, và để daemon quyết định.
- **Xoá volume là mất dữ liệu**: câu hỏi xác nhận nói thẳng "and ALL its data".

### Thể hiện trong code

**`internal/docker`**:
- `volumes.go`: `ListVolumes` (`VolumeList` + đếm mount loại volume; `CreatedAt` là chuỗi RFC3339 → `time.Parse`, lỗi thì để zero và UI hiện `-`), `RemoveVolume(name, force)`.
- `networks.go`: `ListNetworks` (`NetworkList` + đếm theo `NetworkID`; subnet đầu tiên của IPAM là `netip.Prefix`; `BuiltIn` cho bridge/host/none), `RemoveNetwork(id)`.
- `allContainers` dùng chung cho các phép đếm.
- `Inspect` thêm `KindVolume`, `KindNetwork` (cả hai có `Raw`).
- Events: filter thêm `volume`, `network`.
- `RemoveVolume`, `RemoveNetwork` được thêm vào `writeMethods` và `ReadOnly` — test phân loại ở phase (f) sẽ fail nếu quên.

**`internal/ui/listview.go`**:

```go
type listSpec[T any] struct {
    title   string
    load    func(ctx context.Context, c docker.Client) ([]T, error)
    key     func(T) string
    row     func(item T, now time.Time) tableRow
    columns func(width int) []tableColumn
    note    func(items []T) string
    inspect func(T) openInspectMsg
    remove  func(T) (actionMsg, error)   // error = từ chối trước khi hỏi
}
type listView[T any] struct { spec listSpec[T]; table filterTable; items map[string]T; … }
```

`listView[T]` làm toàn bộ phần chung: load có `gen`, `filterTable`, `i`, `D`, `r`, help. `volnet.go` chỉ chứa hai giá trị `volumesSpec`, `networksSpec` — thuần dữ liệu và closure.

**Root**: `volumes listView[docker.Volume]`, `networks listView[docker.Network]`, `case listLoadedMsg[docker.Volume]` / `[docker.Network]`, `reloadMsg{containers, images, volumes, networks}`, load cả bốn danh sách khi kết nối.

**Fake daemon chạy được**: 3 volume (2 của project `shop`, 1 vô danh không dùng), 5 network (3 dựng sẵn, `shop_default`, `old_test_net`), container mẫu được gắn mount và network; `DELETE` từ chối volume đang dùng (409) và network dựng sẵn/có endpoint (403), phát event `destroy`.

### Test liên quan
- `internal/docker/volumes_networks_test.go`: `TestListVolumes` (bind mount trùng tên không tính, project, CreatedAt), `TestListNetworks` (đếm theo NetworkID, built-in, subnet, container không có network settings), `TestRemoveVolumeAndNetwork`, `TestInspectVolumeAndNetwork`.
- `internal/ui/root_test.go`: `TestVolumesView` (cột, "1 unused", xác nhận "ALL its data", cảnh báo in-use, inspect, Esc về volumes), `TestNetworksView` (network dựng sẵn bị từ chối không hỏi, xoá network thường), `TestVolumeAndNetworkEventsReload`.
- Thủ công: `:vol`, `:net` trên fake daemon; xoá `old_test_net` → event → bảng còn 4 network.

### Bẫy
| Bẫy | Cách xử lý |
|---|---|
| Hai view gần như giống hệt → hai bản code | `listView[T]` + `listSpec[T]` |
| Phân biệt message của hai view cùng khuôn | Message generic: mỗi `T` là một kiểu riêng |
| Đếm nhầm bind mount trùng tên volume | Chỉ đếm `mount.TypeVolume` |
| Đếm network theo tên (có thể trùng/đổi) | Đếm theo `NetworkID` |
| Hỏi xác nhận cho thao tác chắc chắn bị từ chối | `remove` trả lỗi trước khi hỏi (network dựng sẵn) |
| Fixture test không sắp xếp như client thật | Fixture theo đúng thứ tự client trả về |

## 11. Phase 2.2: Nhóm theo Compose project

### Mục tiêu
`g` trong bảng containers bật/tắt chế độ nhóm theo Docker Compose project:

```
▾ shop                     2/2 running
    api        shop/api:dev   running   Up 2 hours
    web        nginx:1.27     running   Up 2 hours (healthy)
▾ (no project)             0/3 running
    flaky      busybox:1.36   exited    Exited (137) 3 minutes ago
```

`enter` trên dòng tiêu đề gập/mở nhóm (`▾`/`▸`); dòng tiêu đề có màu vàng khi project có service không chạy. Filter và sort vẫn hoạt động: sort chỉ trong từng nhóm, filter giữ tiêu đề của nhóm có thành viên khớp — kể cả nhóm đang gập.

### Học được gì
- **Compose chỉ là label**: Docker Engine không biết "project"; Compose gắn `com.docker.compose.project` (và `…service`, `…container-number`) lên container, và mọi tool (kể cả `docker compose ps`) nhóm dựa vào label đó.
- **Dòng không phải dữ liệu trong một bảng dữ liệu**: dòng tiêu đề nhóm phải đi cùng nhóm khi sort, không được tự lọc mất, không được đếm vào số dòng, và không nhận thao tác của container. Mỗi yêu cầu này là một điều kiện riêng trong code — dễ quên một cái (smoke test bắt được lỗi đếm).
- **Sort ổn định nhiều tầng**: so sánh theo (thứ tự nhóm, tiêu đề trước, giá trị cột) bằng một comparator duy nhất.
- **Xung đột phím giữa view và component**: `tableModel` dùng `g` để về đầu bảng; containers view dùng `g` cho nhóm và xử lý trước khi chuyển phím xuống table, nên `g` của table không còn tới được trong view này (`Home` vẫn dùng được).

### Thể hiện trong code

**`filterTable` biết về nhóm** (`internal/ui/filtertable.go`): `tableRow` có thêm `group string` và `header bool`. Trong `rebuild`:
1. `liveGroups`: nhóm có ít nhất một thành viên (không phải tiêu đề) khớp filter.
2. Giữ tiêu đề nếu bản thân nó khớp *hoặc* nhóm còn sống; giữ thành viên nếu khớp.
3. Một `SortStableFunc` duy nhất: khác nhóm → theo thứ tự nhóm xuất hiện; cùng nhóm → tiêu đề trước; rồi mới đến cột sắp xếp. `S` khi chưa chọn cột → `reverseWithinGroups` (đảo thành viên, giữ tiêu đề và thứ tự nhóm).
4. Số trên tiêu đề bảng chỉ đếm dòng không phải tiêu đề.

Bảng không nhóm (images, volumes…) có mọi dòng cùng `group == ""` → hành vi không đổi.

**Containers view** (`internal/ui/containers.go`):
- `grouped`, `collapsed map[string]bool`.
- `groupedRows`: gom theo `Project` (rỗng → `(no project)`), sắp tên project, `(no project)` cuối; với mỗi project một dòng tiêu đề (`key = "group:"+project`, `cells = ["▾ shop", …, "2/2 running", …]`, `filter` = tên project + tên và image các thành viên để nhóm gập vẫn tìm được) rồi các thành viên (thụt lề 2 dấu cách, cùng `group`) nếu không gập.
- `enter`: trên tiêu đề (`onHeader`) → đảo `collapsed[project]`; trên container → logs như cũ.
- Thao tác (`u x R D i t`) trên tiêu đề không làm gì: `selected()` tra key `group:…` trong `items` (map theo ID container) → không có → bỏ qua. Không cần thêm điều kiện nào.
- Bật/tắt nhóm giữ nguyên container đang chọn (key ID không đổi).

### Test liên quan
- `internal/ui/root_test.go`: `TestGroupByComposeProject` (lựa chọn giữ nguyên sau `g`, thứ tự nhóm, `1/2 running`, số dòng không tính tiêu đề, `S` đảo trong nhóm, gập bằng enter và con trỏ ở lại tiêu đề, filter `web` giữ `▸ shop` đang gập, enter trên container mở logs), `TestGroupHeaderIgnoresContainerActions` (`x D u i t` trên tiêu đề không làm gì).
- Thủ công với fake daemon: nhóm `shop` (2/2) và `(no project)` (0/3), gập/mở.

### Bẫy
| Bẫy | Cách xử lý |
|---|---|
| Sort đẩy tiêu đề nhóm đi chỗ khác | Comparator: nhóm → tiêu đề trước → cột |
| Nhóm gập biến mất khi filter (không còn thành viên nào được đưa vào) | `filter` của tiêu đề chứa tên các thành viên |
| Số dòng trên tiêu đề tính cả dòng nhóm (`[7]` thay vì `[5]`) | Chỉ đếm dòng không phải tiêu đề |
| Thao tác container chạy trên dòng tiêu đề | Tiêu đề có key không nằm trong `items` → `selected()` false |
| `g` của table (về đầu) bị che | Dùng `Home`; ghi chú trong help |
| Test giả định con trỏ nhảy lên tiêu đề sau khi nhóm | Hành vi đúng là giữ container đang chọn; test điều hướng tường minh |

## 12. Phase 2.3: Docker context

### Mục tiêu
- `--context <tên>` lúc khởi động; mặc định là context hiện tại của CLI `docker`.
- `:ctx` hiện các context (NAME, DOCKER ENDPOINT, TLS, CLI = context hiện tại của CLI, DESCRIPTION), `*` đánh dấu context zdocker đang dùng, con trỏ bắt đầu ở đó; `enter` hoặc `:ctx <tên>` chuyển sang context khác.
- Chuyển context **chỉ trong bộ nhớ**: không bao giờ ghi `~/.docker/config.json` (đúng nguyên tắc kboba áp cho kubeconfig).
- Endpoint `unix://`, `tcp://` (kèm TLS) được hỗ trợ; `ssh://` báo lỗi rõ ràng (xem Bẫy).

### Học được gì
- **CLI `docker` lưu context thế nào**: `$DOCKER_CONFIG` (mặc định `~/.docker`) → `contexts/meta/<sha256(tên)>/meta.json` (`Name`, `Metadata.Description`, `Endpoints.docker.Host`) và chứng chỉ ở `contexts/tls/<sha256(tên)>/docker/{ca,cert,key}.pem`. Thư mục đặt theo **SHA-256 của tên** vì tên có thể chứa ký tự không hợp lệ trong đường dẫn.
- **Thứ tự ưu tiên** chọn context: `DOCKER_HOST` (→ `default`) > `DOCKER_CONTEXT` > `currentContext` trong `config.json` > `default`. Context `default` = `DOCKER_HOST` hoặc socket local.
- **Context khác `default` bỏ qua `DOCKER_HOST`**: không dùng `client.FromEnv` cho chúng, chỉ `WithHost` + `WithTLSClientConfig`.
- **Đổi "kết nối" giữa chừng trong Elm**: dừng mọi thứ của kết nối cũ (event stream, stream logs/stats), dựng lại mọi view với client mới, ping lại. Và bỏ qua câu trả lời muộn của client cũ.
- **Closure + model truyền theo giá trị = dữ liệu cũ**: bài học quan trọng nhất của phase này (xem Bẫy).

### Thể hiện trong code

**`internal/docker/contexts.go`**:
- `ConfigDir()`, `ListContexts(configDir)` (luôn có `default` đứng đầu, các context khác sắp theo tên, thư mục không có `meta.json` bị bỏ qua, không có thư mục contexts → chỉ `default`), `currentContextName` (thứ tự ưu tiên ở trên), `contextID` (SHA-256 hex).
- `NewForContext(c)`: `default` → `New("")` (đọc env); `ssh://` → lỗi; còn lại → `WithHost(c.Host)` (+ `WithTLSClientConfig` nếu có `ca.pem`).

**`cmd/zdocker/main.go`**: `connect(name)` = đọc lại danh sách context → `FindContext` → `NewForContext` → bọc `ReadOnly` nếu `--read-only`. `--host` vẫn ưu tiên hơn `--context`. Tên context sai → thoát với lỗi *trước khi* mở TUI (như kboba).

**UI**:
- `Options{Context, Contexts, Connect}` — UI không biết context được lưu ở đâu, chỉ nhận hai hàm.
- View contexts là một `listView[docker.Context]` (`contexts.go` → `contextsSpec`); `listSpec` có thêm `enter`/`enterHelp` tuỳ chọn, và `inspect`/`remove` giờ là tuỳ chọn (nil → phím không làm gì và biến khỏi help).
- `switchContextMsg` → `connectTo` (Cmd: đọc file TLS, dựng client) → `contextSwitchedMsg`:
  - lỗi → báo lỗi, **giữ nguyên** kết nối hiện tại;
  - thành công → `events.stop()`, `setActive(viewContainers)` (dừng logs/stats), `useClient(newClient)` (dựng lại containers, images, volumes, networks, logs, inspect, stats và watcher), `ping`.
- `useClient` cũng được `New` dùng lúc khởi động → một đường duy nhất để "gắn một client".
- `connectedMsg` mang theo `client`: `msg.client != m.client` → câu trả lời của context đã bỏ, bỏ qua.
- Header: `ctx: lab-b  host: tcp://…`.

### Test liên quan
- `internal/docker/contexts_test.go`: `TestListContexts` (tạo context đúng cấu trúc của CLI: SHA-256, meta.json, tls; `config.json` → current; `DOCKER_CONTEXT` thắng `config.json`; `DOCKER_HOST` thắng tất cả và đổi host của `default`), `TestListContextsWithoutConfig`, `TestNewForContext` (context tcp kết nối fake daemon dù `DOCKER_HOST` trỏ chỗ khác; ssh → lỗi).
- `internal/ui/root_test.go`: `TestSwitchContext` (dòng `* default`/`current`, con trỏ ở context đang dùng, enter chuyển sang colima: event stream cũ bị huỷ, bảng hiện container của daemon mới, header đổi, `*` chuyển sang colima, ping muộn của client cũ bị bỏ qua), `TestSwitchContextFailureKeepsCurrent`, `TestSwitchContextStopsLogs`.
- Thủ công: `DOCKER_CONFIG` tạm với 3 context (2 tcp tới 2 fake daemon, 1 ssh); chuyển `lab-a` → `lab-b`; `:ctx far` báo lỗi ssh; `--context nope` thoát với lỗi.

### Bẫy
| Bẫy | Cách xử lý |
|---|---|
| Closure trong `New` bắt biến cục bộ `m`; Model được copy ở mỗi `Update` → closure đọc mãi giá trị cũ (dấu `*` không bao giờ di chuyển) | Giữ tên context trong `*activeContext` dùng chung giữa các bản copy |
| `client.FromEnv` cho mọi context → `DOCKER_HOST` đè lên endpoint của context | Chỉ `default` đọc env |
| Ping muộn của context cũ ghi đè trạng thái context mới | `connectedMsg.client` so với `m.client` |
| Quên dừng stream của kết nối cũ khi chuyển | `events.stop()` + `setActive` (dừng logs/stats) trước `useClient` |
| Con trỏ ở dòng đầu thay vì context đang dùng → Enter nhầm | `filterTable.Select(active)` mỗi lần danh sách được nạp |
| Context `ssh://`: CLI chạy `ssh … docker system dial-stdio` và nối stdin/stdout làm kết nối | Chưa hỗ trợ (cần SSH server để kiểm chứng) — báo lỗi rõ ràng thay vì làm một nửa; có thể làm sau bằng `client.WithDialContext` + một `net.Conn` bọc tiến trình `ssh` |

## 13. Phase 2.4: Pull image

### Mục tiêu
`:pull <image>[:tag]` (hoặc `p` trong bảng images: mở command bar với `pull <repo:tag của dòng đang chọn>`) → một màn hình tiến trình như `docker pull`: thanh tổng có phần trăm, một thanh mỗi layer kèm số byte, dòng trạng thái cuối (`Status: Downloaded newer image for …`). `esc` huỷ pull đang chạy. Xong thì bảng images tự có image mới (event `pull`). Bị chặn khi `--read-only`.

### Học được gì
- **Giao thức pull**: `POST /images/create?fromImage=…&tag=…` trả về một stream các object JSON. Mỗi layer có chuỗi trạng thái riêng (`Pulling fs layer` → `Waiting` → `Downloading` (kèm `progressDetail{current,total}`) → `Verifying Checksum` → `Download complete` → `Extracting` → `Pull complete`, hoặc `Already exists`), **xen kẽ** giữa các layer; cộng vài dòng chung (`Pulling from …`, `Digest: …`, `Status: …`). Lỗi (`manifest unknown`, `pull access denied`) đến *trong* stream dưới dạng `errorDetail`, không phải mã HTTP.
- **Iterator (range-over-func, Go 1.23)**: Moby trả `JSONMessages(ctx) iter.Seq2[jsonstream.Message, error]`; dùng `for msg, err := range resp.JSONMessages(ctx)`. Iterator của Moby tự đóng body khi `ctx` bị huỷ (`context.AfterFunc`).
- **Gấp (fold) một stream sự kiện thành trạng thái**: UI không cần từng message mà cần "mỗi layer đang ở đâu" → `PullProgress{Layers, Status, Done, Err}`, gửi *bản chụp* (snapshot) sau mỗi message.
- **Snapshot phải là bản sao**: slice `Layers` được gửi qua channel trong khi goroutine tiếp tục sửa → phải copy, nếu không UI đọc dữ liệu đang bị ghi (data race, và test đã kiểm tra hai snapshot liên tiếp độc lập).
- **Chỉ snapshot mới nhất là quan trọng**: `receiveBatch` lấy cả loạt, UI chỉ giữ phần tử cuối — gộp hàng trăm update/giây thành một lần vẽ.
- **Component có sẵn**: `bubbles/progress` (`ViewAs(percent)` vẽ thanh tĩnh, có gradient). Cần thêm module `harmonica` (animation lò xo của Charm) mà `progress` phụ thuộc.

### Thể hiện trong code

**`internal/docker/pull.go` → `PullImage(ctx, ref)`**:
- Goroutine: `for msg, err := range resp.JSONMessages(ctx)`; `msg.Error` → `state.Err`, gửi, dừng; dòng không phải của layer (hoặc không có ID) → `state.Status`; dòng của layer → tìm/thêm layer theo ID (giữ thứ tự xuất hiện qua map `index`), cập nhật `Status` và byte nếu có `Total`. Hết stream → `Done = true`.
- `send` copy `Layers` trước khi gửi, và `select` với `ctx.Done()`.
- `LayerProgress.Done()`: chỉ `Pull complete`/`Already exists` (sau `Download complete` còn bước giải nén).
- Pull **tạo image** → là thao tác ghi: thêm vào `writeMethods` và `ReadOnly`.

**`internal/ui/pull.go` → `pullView`**: vòng đời như logs/stats (context huỷ được, `gen`, huỷ trong `setActive` khi rời view). `layerFraction` ước lượng tiến độ một layer (tải = 80%, giải nén = 20%), `pullFraction` = trung bình các layer (1 khi `Done`). `View` vẽ bằng `progress.New(…).ViewAs(…)`.

**Root**: `:pull` (kiểm tra cú pháp, chặn khi read-only, `returnTo = viewImages`), `prefillCommandMsg` mở command bar với chữ có sẵn (`CursorEnd` để gõ tiếp).

**Fake daemon chạy được**: `POST /images/create` phát tiến trình 3 layer trong ~3 giây (huỷ được), rồi thêm image và phát event `pull`; tên bắt đầu bằng `nope` → lỗi `manifest unknown` trong stream.

### Test liên quan
- `internal/docker/pull_test.go`: `TestPullImage` (gấp đúng các layer, `Already exists`, trạng thái cuối, `Done()` không coi `Download complete` là xong, query `fromImage`/`tag`), `TestPullImageError` (lỗi trong stream), `TestPullSnapshotsAreIndependent`.
- `internal/ui/root_test.go`: `TestPullImage` (thanh 100%, layer, trạng thái, `esc` về images), `TestPullFailure`, `TestPullCancelledOnLeave` (pull đang chạy bị huỷ khi rời view), `TestPullKeyPrefillsCommand`, `TestPullReadOnlyAndUsage`, `TestPullFraction`.
- Thủ công với fake daemon: thanh tổng 43% giữa chừng, các layer với byte, rồi 100% và image mới xuất hiện trong bảng.

### Bẫy
| Bẫy | Cách xử lý |
|---|---|
| Lỗi pull đến trong stream (HTTP vẫn 200) | Kiểm tra `msg.Error` từng message |
| Gửi slice đang bị sửa qua channel | Copy `Layers` mỗi snapshot |
| Vẽ lại hàng trăm lần/giây | Chỉ giữ snapshot cuối của mỗi batch |
| Coi `Download complete` là xong | Còn `Extracting`; chỉ `Pull complete`/`Already exists` |
| `progress` cần module `harmonica` | `go get github.com/charmbracelet/bubbles/progress@v1.0.0` |
| Biểu thức `a || b || c && d` dễ hiểu sai (thứ tự ưu tiên) | Viết lại cho rõ, chỉ còn hai điều kiện |

## 14. Phase 2.5: Prune

### Mục tiêu
`:prune` dọn dẹp như `docker container prune` + `docker image prune` mặc định: **container đã dừng** (`exited`, `created`, `dead`) và **image dangling** (không tag). Không bao giờ đụng tới volume (dữ liệu) hay image còn tag. Trước khi làm, zdocker tính và hỏi cụ thể:

`Prune: remove 2 stopped container(s) (flaky, migrate) and 1 dangling image(s) (48.1MB)?`

Xong thì báo **kết quả thật** từ daemon: `removed 2 container(s), 1 image(s), reclaimed 50.5MB`. Không có gì để dọn → `nothing to prune`, không hỏi. Bị chặn khi `--read-only`.

### Học được gì
- **Thao tác hàng loạt cần câu hỏi cụ thể**: "Prune?" không đủ để người dùng quyết định; số lượng, vài cái tên (tối đa 3, phần còn lại `+N`) và dung lượng thì đủ.
- **Xem trước và kết quả có thể khác nhau**: giữa lúc tính và lúc chạy, trạng thái có thể đổi (một container vừa dừng), và một image dangling chỉ bị xoá nếu không còn container nào dùng nó. Vì vậy câu hỏi dùng bản xem trước, còn thông báo cuối dùng **báo cáo của daemon**.
- **Thứ tự quan trọng**: prune container *trước* image — container đã dừng vẫn "giữ" image của nó. Và nếu bước đầu lỗi thì dừng, không làm bước sau với một giả định sai.
- **Báo cáo xoá image** liệt kê cả `Untagged` lẫn `Deleted`; chỉ `Deleted` là image thật sự bị xoá.

### Thể hiện trong code

**`internal/docker/prune.go` → `Prune(ctx)`**: `ContainerPrune` → (lỗi thì trả ngay) → `ImagePrune(Filters: dangling=true)` → `PruneReport{Containers, Images, SpaceReclaimed}`. Là thao tác ghi: `writeMethods` + `ReadOnly`.

**`internal/ui/prune.go`**:
- `planPrune` (Cmd): `ListContainers(all)` + `ListImages` → `prunePlan{containers []string, images, imageBytes}` (image dangling đếm theo ID, không trùng).
- `pruneAction(plan)`: rỗng → `false`; ngược lại một `actionMsg` với câu hỏi cụ thể và `report` — closure đọc biến `report` mà `run` đã gán.

**`actionMsg.report`** (mới, `actions.go`): `runAction` dùng `report()` thay cho chuỗi `done` cố định nếu có và nếu thành công.

**Root**: `:prune` → `planPrune`; `prunePlanMsg` → `nothing to prune` hoặc đưa `actionMsg` qua đúng đường xác nhận như mọi thao tác khác (`return m.Update(a)` — không có đường tắt nào bỏ qua xác nhận hay kiểm tra read-only).

**Fake daemon chạy được**: `POST /containers/prune`, `POST /images/prune` (chỉ image không tag và không container nào dùng), phát event `destroy`/`delete` để bảng tự cập nhật.

### Test liên quan
- `internal/docker/prune_test.go`: `TestPrune` (đếm, cộng dung lượng, `Untagged` không tính, container trước image, filter `dangling`), `TestPruneStopsOnContainerError`.
- `internal/ui/root_test.go`: `TestPrune` (câu hỏi cụ thể, kết quả từ báo cáo), `TestPruneNothingToDo`, `TestPrunePromptListsAFewNames` (`a, b, c, +2`).
- Thủ công: `:prune` trên fake daemon → câu hỏi, `y`, bảng còn container đang chạy, chạy lại → `nothing to prune`.

### Bẫy
| Bẫy | Cách xử lý |
|---|---|
| Câu hỏi chung chung cho thao tác hàng loạt | Đếm + vài tên + dung lượng |
| Báo kết quả theo bản xem trước | Báo theo `PruneReport` của daemon |
| Prune image trước → image của container đã dừng không bị xoá | Container trước, image sau |
| Đếm `Untagged` như image đã xoá | Chỉ đếm `Deleted` |
| Một đường riêng cho prune bỏ qua xác nhận/read-only | Đi qua `actionMsg` như mọi thao tác |

## 15. Phase 2.6: Shell vào container

### Mục tiêu
`e` trên một container đang chạy → một shell tương tác bên trong nó (`bash` nếu image có, không thì `sh`). Thoát shell (`exit`, `ctrl+d`) → quay về zdocker đúng chỗ cũ, status báo `left the shell in web` (hoặc lỗi kèm exit status). Bị chặn khi `--read-only`.

### Học được gì
- **`tea.ExecProcess`**: Bubble Tea tạm dừng (rời alt screen, trả terminal về chế độ thường), chạy một `*exec.Cmd` với stdin/stdout/stderr của terminal, chờ nó kết thúc, rồi khôi phục TUI và gửi một message qua callback. Mọi thứ thuộc về "terminal" — raw mode, resize, tín hiệu — do chương trình con tự lo.
- **Khi nào nên gọi CLI thay vì API**: `docker exec -it` qua API nghĩa là: tạo exec, *hijack* kết nối HTTP thành một luồng hai chiều, đưa terminal vào raw mode, chuyển tiếp resize (`SIGWINCH`), xử lý TTY/không TTY… CLI đã làm đúng tất cả. Đổi lại, zdocker cần `docker` trong PATH — và báo rõ khi thiếu.
- **Cùng một daemon**: lệnh con phải nói chuyện với đúng daemon zdocker đang dùng → `--context <tên>` nếu dùng context, `-H <host>` nếu chạy bằng `--host`, không cờ nào với context `default` (CLI đọc cùng biến môi trường).
- **Chọn shell bằng một script nhỏ**: `if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi` — `exec` để shell thay thế `sh` trung gian (thoát một lần là ra).
- **Test thứ không chạy được trong môi trường test**: hàm tạo lệnh (`lookPath`, `command`) là field của `shellRunner` → test thay bằng hàm ghi lại tham số; smoke test dùng một script `docker` giả trong `PATH`.

### Thể hiện trong code

**`internal/ui/exec.go`**:
- `execArgs(contextName, host, container)` — dòng lệnh docker (hàm thuần, test bằng bảng).
- `shellRunner{lookPath, command}` + `defaultShellRunner` (dùng `exec.LookPath`, `exec.Command`).
- `openShell`: container không chạy → lỗi; không có `docker` → lỗi; ngược lại `tea.ExecProcess(cmd, func(err error) tea.Msg { return shellExitedMsg{…} })`.

**Root**: `openShellMsg` (từ phím `e` của bảng containers) → read-only thì lỗi; không thì `m.shell.openShell(m.ctx.name, m.server.Host, container)`. `shellExitedMsg` → `left the shell in …` hoặc lỗi (bao gồm exit status khác 0 của chính shell). `Model.shell` là field để test thay được.

**Read-only**: lần này kiểm tra ở UI, vì exec không đi qua `docker.Client` (không có method nào để `ReadOnly` chặn) — ghi chú lại để không ai tưởng client đã chặn nó.

### Test liên quan
- `internal/ui/root_test.go`: `TestExecArgs` (default → không cờ, context → `--context`, `--host` → `-H`), `TestOpenShell` (đúng dòng lệnh qua `command` giả, container dừng → lỗi, không có CLI → lỗi), `TestShellReadOnlyAndExit`.
- Thủ công: script `docker` giả in tham số và ngủ 2 giây; tmux cho thấy TUI nhường màn hình (`FAKE docker CLI got: -H tcp://… exec -it <id> sh -c …`) rồi quay lại với `left the shell in api`.

### Bẫy
| Bẫy | Cách xử lý |
|---|---|
| Tự cài `docker exec -it` qua API (raw mode, resize, hijack) | Dùng CLI qua `tea.ExecProcess` |
| Shell nói chuyện với daemon khác zdocker | Truyền `--context`/`-H` tương ứng |
| Image không có `bash` | Script chọn `bash` hoặc `sh` |
| Shell trên container đã dừng | Kiểm tra `State == running` trước |
| `--read-only` không chặn exec (không qua client) | Kiểm tra ở root |
| Không chạy được CLI thật trong test | `shellRunner` với hàm thay được; script `docker` giả khi smoke test |

