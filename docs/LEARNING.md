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

