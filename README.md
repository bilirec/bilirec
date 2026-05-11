# Bilirec - Bilibili 直播录制工具

一个用 Go 语言编写的 Bilibili 直播录制工具，支持自动录制直播流。

## 功能特性

- ✅ 手动触发录制任务，实时录制直播流
- ✅ 支持多格式录制（HTTP-FLV / HLS-TS / HLS-fMP4）
- ✅ 自动录制 - 为直播间配置自动开播录制
- ✅ 可选录制时长 - 手动开始录制时可指定时长上限，时间到后自动停止；也可在订阅配置中预设自动录制的时长上限；支持设为无限录制
- ✅ 直播通知 - 实时推送开播通知（网页/手机推送）
- ✅ 自动分段轮转 - 当直播过程中发生直播 PK 等分辨率变更时，自动切换到新的录制分段文件，避免文件花屏或损坏
- ✅ 支持多个直播间同时录制
- ✅ 自动处理流中断和恢复（默认自动选择可用流格式，也支持手动指定 profile）
- ✅ FFmpeg 转换冷却机制 - 其中一个档案转换失败时自动退让给其他档案转换任务
- ✅ RESTful API 管理录制任务
- ✅ 文件管理和下载功能
- ✅ 在线播放 - 在浏览器中直接预览和播放已录制的视频
- ✅ 支持匿名登录或账号登录
- ✅ 自动刷新 Cookie 保持登录状态
- ✅ 低内存与低 CPU 占用，适合在资源受限设备（如树莓派）上运行

## 安装

### 使用二进制文件

可以从 [GitHub Releases](https://github.com/eric2788/bilirec/releases) 页面下载预编译的二进制文件，选择适合你系统的版本:

- `bilirec-amd64`：适用于 x86_64 架构的 Linux 系统
- `bilirec-arm64`：适用于 ARM64 架构的 Linux 系统
- `bilirec-windows`：适用于 Windows 系统（包含 .exe 后缀）

启动服务：

```bash
# 如果你下载了 amd64 版本
./bilirec-amd64
# 或者如果你下载了 arm64 版本
./bilirec-arm64
```

如果你是 Windows 用户，直接双击 `bilirec-windows.exe` 启动服务。

### 使用 Docker

可以通过构建镜像或直接运行容器来启动 Bilirec。

从源码构建镜像并运行（示例）：

```bash
# 在仓库根目录构建镜像
docker build -t bilirec:latest .

# 运行容器（示例）
docker run -d \
  --name bilirec \
  -p 8080:8080 \
  -e PORT=8080 \
  -e FRONTEND_URL=http://localhost:8080 \
  -v /path/to/records:/app/records \
  -v /path/to/secrets:/app/secrets \
  -v /path/to/database:/app/database \
  # 可选：启用 CloudConvert（替换为你的 API key）
  -e CLOUDCONVERT_API_KEY=your_api_key \
  bilirec:latest
```

你也可以直接從 Docker Hub 拉取并运行镜像：

```bash
docker pull eric1008818/bilirec:latest # 最新测试版本请用 :edge
docker run -d \
  --name bilirec \
  -p 8080:8080 \
  -e PORT=8080 \
  -e FRONTEND_URL=http://localhost:8080 \
  -v /path/to/records:/app/records \
  -v /path/to/secrets:/app/secrets \
  -v /path/to/database:/app/database \
  # 可选：启用 CloudConvert（替换为你的 API key）
  -e CLOUDCONVERT_API_KEY=your_api_key \
  eric1008818/bilirec:latest
```

## 配置

所有配置通过环境变量设置：

| 环境变量 | 说明 | 默认值 |
| ------- | ---- | ------ |
| `ANONYMOUS_LOGIN` | 是否使用匿名登录 | `false` |
| `PORT` | API 服务端口 | `8080` |
| `MAX_CONCURRENT_RECORDINGS` | 最大同时录制数 | `1` |
| `MAX_RECORDING_HOURS` | 单次录制最长时间（小时） | `5` |
| `MAX_RECOVERY_ATTEMPTS` | 单次录制的最大重连尝试次数 | `5` |
| `MAX_RETRY_MINUTES` | 直播中断后判断是否仍在直播的最长容忍时间（分钟） | `10` |
| `OUTPUT_DIR` | 录制文件保存目录 | `records` |
| `SECRET_DIR` | Cookie 和 Token 保存目录 | `secrets` |
| `CONVERT_TO_MP4` | 录制完成后是否将可转换源文件（如 FLV/TS）转为 MP4 | `false` |
| `DELETE_SOURCE_AFTER_CONVERT` | 转换后是否删除原始源文件 | `false` |
| `BACKEND_HOST` | 后端主机（用于生成Cookie域名） | `localhost:8080` |
| `FRONTEND_URL` | 前端 URL（用于 CORS 与 cookie 域） | `http://localhost:8080` |
| `WEBPUSH_SUBSCRIBER` | Web Push VAPID 的 subject（建议使用 `mailto:you@example.com`） | `mailto:webpush@example.com` |
| `USERNAME` | 可选：启用用户名/密码认证时的用户名 | (未设置) |
| `PASSWORD` | 可选：启用用户名/密码认证时的密码 | (未设置) |
| `VIEWER_USERNAME` | 可选：仅查看权限的访客用户名 | (未设置) |
| `VIEWER_PASSWORD` | 可选：仅查看权限的访客密码 | (未设置) |
| `JWT_SECRET` | JWT 签名密钥 | `bilirec_secret` |
| `DEBUG` | 启用调试模式（会开启 pprof 和临时 hex token） | `false` |
| `PRODUCTION_MODE` | 启用生产模式（影响 cookie 与 CORS） | `false` |
| `SILENT_ACCESS_LOG` | 启用静默访问日志（仅记录 4xx/5xx 响应） | `false` |
| `DATABASE_DIR` | 本地数据库目录（bbolt，用于持久化转换任务等） | `database` |
| `CLOUDCONVERT_THRESHOLD` | 使用 CloudConvert 的文件大小阈值（字节） | `1073741824` (1 GB) |
| `CLOUDCONVERT_API_KEY` | 可选：CloudConvert API Key（为空则禁用 CloudConvert） | (未设置) |
| `CLOUDCONVERT_CHECK_INTERVAL_SECS` | CloudConvert 任务状态轮询间隔（秒） | `180` |
| `CLOUDCONVERT_MAX_CONCURRENT_DOWNLOADS` | CloudConvert 最大并发下载数 | `1` |
| `FFMPEG_CHECK_INTERVAL_SECS` | 本地 FFmpeg 转换任务轮询间隔（秒） | `60` |
| `FFMPEG_MAX_CONCURRENT_TASKS` | 本地 FFmpeg 最大并发转换任务数 | `1` |
| `FFMPEG_ALLOW_DURING_RECORDING` | 是否允许在录制进行中时执行 FFmpeg 转换任务 | `false` |
| `FFMPEG_ALLOW_DURING_RECORDING_MAX_ACTIVE_RECORDINGS` | 仅当活跃录制数 `<=` 此值时，才允许在录制中执行 FFmpeg；`<1` 表示不设门槛（仍可通过 `FFMPEG_ALLOW_DURING_RECORDING` 控制） | `1` |
| `UPLOAD_BUFFER_SIZE` | 上传时或向外部服务（如 CloudConvert）传输文件使用的缓冲区大小（字节） | `5242880` (5 MB) |
| `DOWNLOAD_BUFFER_SIZE` | 文件下载 / 导出时使用的缓冲区大小（字节） | `5242880` (5 MB) |
| `STREAM_WRITER_BUFFER_SIZE` | 流写入器（写入文件）缓冲区大小（字节） | `1048576` (1 MB) |
| `LIVE_STREAM_WRITER_BUFFER_SIZE` | 实时流写入缓冲区（用于直播录制或实时下载，字节）；更大的值减少 flush 频率，降低 SD 卡磨损 | `8388608` (8 MB) |
| `LIVE_STREAM_WRITER_SYNC_PERIOD_SECS` | 实时流写入器执行周期性 `sync` 的周期（秒）；设为 0 禁用周期性 sync（仅在 Close 时 sync），大幅减少 SD 卡磨损 | `0` |
| `LIVE_STREAM_WRITER_FLUSH_PERIOD_SECS` | 实时流写入器执行周期性 `flush` 的周期（秒）；值越大 flush 频率越低，越有利于减少 SD 卡写入频次 | `10` |
| `LIVE_STREAM_WRITER_CHAN_BUFFER_SIZE` | 实时流写入器通道缓冲区大小（数据块数）；更大的值可容忍写入延迟突变，但会增加内存占用 | `64` |
| `LIVE_STREAM_WRITER_BYTES_POOL_SIZE` | 实时流写入器内存池的单个缓冲区大小（字节）；应与实际流 chunk 大小相匹配 | `524288` (512 KB) |
| `SKIP_SMALL_FLUSH` | 启用 microSD 磨损保护：若录制总写入量低于缓冲区大小则跳过 flush，避免写入小块数据（特别是低比特率流） | `true` |
| `MIN_DISK_SPACE_BYTES` | 录制所需的最小磁盘空间（字节），低于此值将拒绝新录制任务 | `5368709120` (5 GB) |

### 示例配置

```bash
export ANONYMOUS_LOGIN=false
export PORT=8080
export MAX_CONCURRENT_RECORDINGS=1
export MAX_RECORDING_HOURS=10
export MAX_RECOVERY_ATTEMPTS=5
export MAX_RETRY_MINUTES=10
export OUTPUT_DIR=/path/to/records
export SECRET_DIR=/path/to/secrets
export DATABASE_DIR=/path/to/database
export CONVERT_TO_MP4=false
export DELETE_SOURCE_AFTER_CONVERT=false
# 可选：CloudConvert（如果启用会对大文件使用云端转换）
export CLOUDCONVERT_THRESHOLD=1073741824
export CLOUDCONVERT_API_KEY=
export CLOUDCONVERT_CHECK_INTERVAL_SECS=180
export CLOUDCONVERT_MAX_CONCURRENT_DOWNLOADS=1
# FFmpeg 本地转换参数
export FFMPEG_CHECK_INTERVAL_SECS=60
export FFMPEG_MAX_CONCURRENT_TASKS=1
export FFMPEG_ALLOW_DURING_RECORDING=false
export FFMPEG_ALLOW_DURING_RECORDING_MAX_ACTIVE_RECORDINGS=1
export BACKEND_HOST=localhost:8080
export FRONTEND_URL=http://localhost:8080
export WEBPUSH_SUBSCRIBER=mailto:webpush@example.com
export UPLOAD_BUFFER_SIZE=5242880
export DOWNLOAD_BUFFER_SIZE=5242880
export STREAM_WRITER_BUFFER_SIZE=1048576
export LIVE_STREAM_WRITER_BUFFER_SIZE=8388608
export LIVE_STREAM_WRITER_SYNC_PERIOD_SECS=0
export LIVE_STREAM_WRITER_FLUSH_PERIOD_SECS=10
export LIVE_STREAM_WRITER_CHAN_BUFFER_SIZE=64
export LIVE_STREAM_WRITER_BYTES_POOL_SIZE=524288
export SKIP_SMALL_FLUSH=true
export JWT_SECRET=bilirec_secret
export DEBUG=false
# 可选：启用 REST API 认证
export USERNAME=admin
export PASSWORD=changeme
export PRODUCTION_MODE=false
```

如果你是使用二进制文件，启动服务后会生成 `.env` 文件，里面包含当前的环境变量配置（不包含敏感信息）。你可以编辑这个文件来修改配置，或者直接设置环境变量覆盖。

Web Push 的 VAPID key 会由后端在启动时自动生成并写入 `SECRET_DIR`（默认 `secrets`）下的 `_webpush_public_key` 与 `_webpush_private_key`。后续重启会优先复用已存在的 key。

### 树莓派 4B 默认配置（microSD）

Bilirec 当前默认配置已针对树莓派 4B + microSD 场景优化，在“降低写入次数”和“控制内存占用”之间取平衡：

| 优化项 | 说明 |
| ----- | ---- |
| `LIVE_STREAM_WRITER_SYNC_PERIOD_SECS=0` | **禁用周期性 fsync**（最昂贵的操作），数据仅在录制结束时同步到磁盘，减少 I/O 突峰。权衡：意外断电可能丢失最后一段尚未持久化的数据。 |
| `LIVE_STREAM_WRITER_BUFFER_SIZE=8388608` (8MB) | 8MB 进一步降低 flush 频率，优先降低 SD 卡写入频次。@1080p30fps (4.5Mbps) 约每 14.2 秒触发一次满缓冲写入。 |
| `LIVE_STREAM_WRITER_FLUSH_PERIOD_SECS=10` | 将周期性 flush 改为 10 秒，优先降低 SD 卡磨损；代价是异常断电时未 flush 数据窗口会略增。 |
| `LIVE_STREAM_WRITER_CHAN_BUFFER_SIZE=64` | 控制在途内存占用。以 512KB chunk 估算，单录制任务约 32MB 队列数据；比 128（约 64MB）更适合 4GB 设备。 |
| `SKIP_SMALL_FLUSH=true` | **启用小块跳过保护**，若录制总写入量 < 缓冲区大小则跳过 flush。特别有效于低比特率流（如 240p），防止多次小块写入磨损。 |
| `LIVE_STREAM_WRITER_BYTES_POOL_SIZE=524288` (512KB) | 与常见 stream chunk 大小一致，减少额外分配与拷贝。 |
| `MAX_CONCURRENT_RECORDINGS=1` | 保守并发上限，直接限制 RAM 峰值。 |

容器运行时默认值也同步为树莓派 4B 取向：`GOMEMLIMIT=768MiB`、`GOGC=100`。

### HDD/SSD 使用者调整建议

如果你的存储介质是 HDD 或 SSD（而非 microSD），可以优先提高数据安全性并放宽吞吐设置：

```bash
# 建议 HDD/SSD 起始配置
export LIVE_STREAM_WRITER_SYNC_PERIOD_SECS=30   # 或 45，启用周期性 sync
export SKIP_SMALL_FLUSH=false                   # 优先落盘完整性
export LIVE_STREAM_WRITER_BUFFER_SIZE=8388608   # 8MB
export LIVE_STREAM_WRITER_CHAN_BUFFER_SIZE=128  # 更高突发容忍
export MAX_CONCURRENT_RECORDINGS=5              # 视 CPU/磁盘性能再提高
```

调参建议：

| 场景 | 建议 |
| ---- | ---- |
| HDD/SSD + 更高可靠性 | 开启 `LIVE_STREAM_WRITER_SYNC_PERIOD_SECS=30~45` |
| HDD/SSD + 更高吞吐 | 提高 `LIVE_STREAM_WRITER_CHAN_BUFFER_SIZE` 到 `128` 或更高 |
| 仍遇到内存压力 | 优先降低 `MAX_CONCURRENT_RECORDINGS`，再调小 `LIVE_STREAM_WRITER_CHAN_BUFFER_SIZE` |

**注意**：如果你使用高性能存储（NVME/SSD）或需要更高可靠性，可以恢复周期性 sync：

```bash
export LIVE_STREAM_WRITER_SYNC_PERIOD_SECS=45
export SKIP_SMALL_FLUSH=false
```

## 使用方法

### 启动服务

请参阅上面的安装和配置部分，设置好环境变量后启动服务。

首次启动如果未使用匿名登录，会显示二维码，使用 Bilibili 手机 APP 扫码登录。

### Web 页面

1. 设置你的 `FRONTEND_URL` 为 `https://bilirec.ericlamm.com/`

2. 直接访问 `https://bilirec.ericlamm.com/` 进入登入界面

3. 根据你所设置的 `USERNAME` 和 `PASSWORD` 进行登录（如果未设置则直接进入）

### API 接口

> Swagger UI 会在服务器运行时于根路径 `/` 提供 — 在浏览器中打开该地址即可查看与测试 API。

#### 认证

如果设置了 `USERNAME` 与 `PASSWORD`，REST API 会启用基于 JWT 的认证（登录会在 cookie 中设置 `jwtToken`）。使用：

```http
POST /login
Content-Type: application/json

{ "user": "<username>", "pass": "<password>" }
```

登录成功后会在响应中设置 JWT cookie（键名 `jwtToken`），随后对需要认证的接口请携带该 cookie。若未设置用户名/密码，API 默认为公开访问。

#### 录制管理

- **开始录制**

  ```http
  POST /record/:roomID/start?duration_minutes=<N>&stream_profile=<profile>
  ```

  `duration_minutes` 为**可选** query 参数：

  | 值 | 说明 |
  | -- | ---- |
  | 不传 | 使用系统预设（`MAX_RECORDING_HOURS`） |
  | `0` | 使用系统预设（`MAX_RECORDING_HOURS`）（同不传） |
  | `-1` | 无限录制，不自动停止 |
  | `N`（正整数） | N 分钟后自动停止 |

  `stream_profile` 也是**可选** query 参数，用于优先指定录制流格式：

  | 值 | 说明 |
  | -- | ---- |
  | 不传 | 自动选择可用流（默认行为） |
  | `http-flv` | 仅使用 HTTP-FLV 录制 |
  | `hls-ts` | 仅使用 HLS-TS 录制 |
  | `hls-fmp4` | 仅使用 HLS-fMP4 录制 |

- **停止录制**

  ```http
  POST /record/:roomID/stop
  ```

- **获取录制状态**

  ```http
  GET /record/:roomID/status
  ```

- **获取录制统计**

  ```http
  GET /record/:roomID/stats
  ```

  返回：

  ```json
  {
    "bytes_written": 1048576,
    "status": "recording",
    "start_time": 1234567890,
    "elapsed_seconds": 120,
    "output_path": "records/tester-123456/live-title-20260428_210000-1.flv"
  }
  ```

  `output_path` 表示当前正在写入的录制文件路径。如果使用FLV流录制直播过程中发生直播 PK 等分辨率变更，录制器会自动轮转到新的分段文件，后续分段文件名会追加 `-1`、`-2` 等后缀。

- **列出所有录制任务**

  ```http
  GET /record/list
  ```

#### 文件管理

- **列出文件**

  ```http
  GET /files/browse/*
  ```

  支持以下查询参数：

  | 参数 | 说明 | 默认值 |
  | ---- | ---- | ------ |
  | `offset` | 分页偏移量（跳过前 N 条） | `0` |
  | `limit` | 每页最大条数（0 表示不限制，最大 200） | `0` |
  | `search` | 按文件名搜索（大小写不敏感） | (不过滤) |

  返回 `PagedTree` 对象：

  ```json
  {
    "total": 42,
    "items": [
      { "name": "title-20260428.flv", "is_dir": false, "path": "room123/title-20260428.flv", "size": 1048576 }
    ]
  }
  ```

  `total` 为过滤/搜索后的总条数（不受 `offset`/`limit` 影响），`items` 为当前页的文件列表。

- **下载文件**

  ```http
  GET /files/download/*
  ```

  下载接口直接返回存储的文件。
  若要将录制的源文件（如 FLV/TS）转为 MP4，请启用 `CONVERT_TO_MP4`：在录制完成时，recorder 会将可转换文件加入转换队列，由后台任务异步转换为 MP4（转换行为受 `DELETE_SOURCE_AFTER_CONVERT` 控制）。
  当同时设置了 `CLOUDCONVERT_API_KEY` 且文件大小 >= `CLOUDCONVERT_THRESHOLD`（默认 1 GB）时，系统会优先使用 CloudConvert（异步任务，可通过 `/convert/tasks` 查询转换状态）；否则由本地 ffmpeg 后台任务处理。

- **临时 / 预签名下载（Presigned）**

  ```http
  POST /files/presigned/{path}?ttl=<seconds>
  ```

  该接口需要身份认证（JWT），用于为文件创建一个临时的预签名下载令牌（`ttl` 可选，单位秒，默认 3600）。成功创建后会返回包含临时令牌或 URL 的响应。使用该令牌可以进行匿名下载：

  ```http
  GET /files/tempdownload?presigned=<token>
  ```

  `GET /files/tempdownload` 无需认证，但必须提供有效的 `presigned` 查询参数。该临时链接会在创建时设置过期时间，过期后将无法使用。

- **删除多个文件**

  ```http
  DELETE /files/batch
  ```

  请求体：JSON 数组，包含要删除的相对文件路径，示例：

  ```json
  ["room123/20250101.flv", "room456/20250102.flv"]
  ```

- **删除目录**

  ```http
  DELETE /files/{path}
  ```

- **在线播放视频**

  ```http
  GET /files/playback/{path}
  ```

  在浏览器中直接播放已录制的 MP4 视频（VOD）。该接口返回视频流，浏览器会直接在网页中显示播放器而非下载文件。

  支持的格式：
  - `video/mp4` - MP4 格式视频

  使用示例：

  ```html
  <video controls width="100%">
    <source src="/files/playback/username-roomID/20250101.mp4" type="video/mp4">
    Your browser does not support HTML5 video.
  </video>
  ```

  **注意**：
  - 只支持 MP4 的文件（可启用 `CONVERT_TO_MP4` 后转换得到）
  - 无法播放正在进行中的录制文件
  - 支持浏览器的 Range 请求（可快进/快退）

#### 转换任务

- **列出进行中的转换任务**

  ```http
  GET /convert/tasks
  ```

- **取消转换任务**

  ```http
  DELETE /convert/tasks/:task_id
  ```

  返回 `204 No Content` 表示取消成功，若任务不存在返回 `404`。

#### 房间信息

- **获取房间信息**

  ```http
  GET /room/:roomID/info
  ```

  获取指定房间的详细信息。

- **批量获取房间信息**

  ```http
  GET /room/infos?roomIDs=123,456,789
  ```

  通过逗号分隔的房间 ID 列表获取多个房间的信息。

- **检查直播状态**

  ```http
  GET /room/:roomID/live
  ```

  检查指定房间的直播是否进行中。

#### 房间订阅管理

- **订阅房间**

  ```http
  POST /room/:roomID
  ```

  订阅一个直播房间，当房间开播时会接收更新。
  - 状态 `200`: 订阅成功
  - 状态 `409`: 已订阅此房间
  - 状态 `400`: 无效的房间 ID

- **取消订阅**

  ```http
  DELETE /room/:roomID
  ```

  取消订阅直播房间。
  - 状态 `200`: 取消订阅成功
  - 状态 `404`: 未订阅此房间
  - 状态 `400`: 无效的房间 ID

- **检查订阅状态**

  ```http
  GET /room/subscribe/:roomID
  ```

  检查是否已订阅指定房间。返回：

  ```json
  {
    "room_id": 123,
    "is_subscribed": true
  }
  ```

- **列出所有订阅房间**

  ```http
  GET /room/subscribe
  ```

  获取所有已订阅房间的 ID 列表。返回：

  ```json
  {
    "room_ids": [123, 456, 789]
  }
  ```

- **获取房间配置**

  ```http
  GET /room/:roomID/config
  ```

  获取指定房间的配置（自动录制、通知等）。返回：

  ```json
  {
    "room_id": 123,
    "auto_record": true,
    "notify": true,
    "record_duration_minutes": 120
  }
  ```

- **更新房间配置**

  ```http
  PUT /room/:roomID/config
  ```

  更新房间的配置（自动录制、通知等）。请求体：

  ```json
  {
    "auto_record": true,
    "notify": true,
    "record_duration_minutes": 120
  }
  ```

  `record_duration_minutes` 为**可选**字段，仅在 `auto_record: true` 时生效，作为自动录制的时长上限：

  | 值 | 说明 |
  | -- | ---- |
  | `0`（默认） | 使用系统预设（`MAX_RECORDING_HOURS`） |
  | `-1` | 无限录制，不自动停止 |
  | `N`（正整数） | N 分钟后自动停止 |

  > ![NOTE]
  > 如果你想让这个录制时长套用到手动触发的录制任务，请自行在调用 `/record/:roomID/start` 时传入相同的 `duration_minutes` 参数；否则手动录制将不受此配置影响，仍然使用系统预设或你在启动录制时指定的时长。

#### 实时通知

- **获取 Web Push 公钥**

  ```http
  GET /notify/public-key
  ```

  获取前端订阅 Web Push 所需的 VAPID 公钥。

- **注册 Web Push 订阅**

  ```http
  POST /notify/subscribe
  Content-Type: application/json
  ```

  请求体为浏览器 Push API 产生的 subscription JSON（包含 `endpoint` 与 `keys`）。

- **取消 Web Push 订阅**

  ```http
  DELETE /notify/subscribe
  ```

  可通过 query 参数 `endpoint` 或 JSON body 提供 endpoint。

## 开发与调试

- **启用调试**：设置环境变量 `DEBUG=true` 启用调试模式，服务器启动时会在日志中打印一个临时十六进制令牌（hex token）。
- **pprof 性能分析**：调试模式下会在 `/debug/pprof` 挂载 pprof 以便性能分析。该路由受保护：可以在请求头 `Authorization` 中填入启动日志中显示的 hex 令牌来访问。
- **实现参考**：该逻辑位于 `internal/modules/rest/rest.go` 中（`DEBUG` 控制是否启用，令牌授权访问）。

## 项目结构

```text
.
├── .github/                          # CI / workflows
├── Dockerfile
├── go.mod
├── LICENSE
├── README.md
├── main.go
├── swagger.go
├── dotenv.go
├── main_test.go
├── internal/                         # 内部包（不对外暴露）
│   ├── controllers/                  # HTTP 控制器
│   │   ├── convert/                  # 转换任务管理
│   │   ├── file/                     # 文件管理
│   │   ├── notify/                   # 实时通知（Web Push）
│   │   ├── record/                   # 录制管理
│   │   └── room/                     # 房间信息与订阅
│   ├── modules/                      # 核心模块
│   │   ├── bilibili/                 # Bilibili API 封装与认证
│   │   ├── config/                   # 配置管理
│   │   └── rest/                     # REST 服务
│   ├── processors/                   # 流处理器
│   └── services/                     # 业务逻辑服务
│       ├── convert/                  # 转换服务
│       ├── file/                     # 文件操作
│       ├── notify/                   # 实时通知服务
│       ├── path/                     # 路径管理
│       ├── recorder/                 # 直播录制
│       ├── room/                     # 房间信息与订阅
│       ├── stream/                   # 流处理
│       ├── subcheck/                 # 订阅检查与自动录制
│       └── subscribe/                # 房间订阅管理
├── pkg/                              # 可复用库与工具
│   ├── cloudconvert/                 # CloudConvert API 客户端
│   ├── db/                           # 数据库抽象层
│   ├── ds/                           # 数据结构
│   ├── flv/                          # FLV 格式处理
│   ├── hls/                          # HLS 格式处理
│   ├── fp/                           # 函数式编程工具（maps、slices）
│   ├── monitor/                      # 监控与统计
│   ├── pipeline/                     # 流处理管道
│   ├── pool/                         # 内存池
│   ├── signeddownload/               # 预签名下载
│   └── swr/                          # Stale-While-Revalidate 缓存
└── utils/                            # 工具函数库
```

## 核心实现

### 录制流程

1. 通过 [`bilibili.Client`](internal/modules/bilibili/bilibili.go) 获取直播流地址（默认自动选择可用 profile，也可按需指定 `stream_profile`）
2. 使用 [`stream.Service`](internal/services/stream/stream.go) 读取流数据
3. [`recorder.Service`](internal/services/recorder/recorder.go) 管理录制任务（自动重连与恢复）
4. 数据写入到录制文件（扩展名依流格式可能为 `.flv`、`.ts` 或 `.fmp4`），保存在配置的输出目录:

- **HTTP-FLV 格式**： 当检测到直播 PK 等 FLV 文件头变更时（分辨率切换）录制器会自动轮转到新的分段文件；

- **HLS-TS / HLS-fMP4 格式**: 不执行文件轮转，直播 PK 等不连续性由 HLS播放列表层自然处理，录制器仅在流中断时重连并继续写入同一文件。

首个文件名形如 `标题-时间戳.ext`，后续分段（HTTP-FLV 轮转后）会命名为 `标题-时间戳-1.ext`、`标题-时间戳-2.ext`。如果启用了 `CONVERT_TO_MP4`，每个可转换分段在完成时都会自动加入转换队列，并由后台任务异步转换/封装为 `.mp4` 文件（转换行为受 `DELETE_SOURCE_AFTER_CONVERT` 控制，转换任务可通过 `/convert/tasks` 查询）。

### 关键特性

- **自动恢复**: 当流中断时自动重连，详见 [`recorder.Service`](internal/services/recorder/recorder.go)
- **自动分段轮转（HTTP-FLV）**: 仅适用于 HTTP-FLV 格式；当检测到 FLV 文件头变更（直播 PK 等分辨率切换）时，自动轮转到新的分段文件并重写文件头，降低画质切换导致输出异常的风险。HLS-TS / HLS-fMP4 格式不使用文件轮转，不连续性由 HLS 播放列表层自然处理
- **自动录制**: 为订阅的直播间配置自动开播录制，后台定期检查直播间状态并自动启动录制，详见 [`subcheck.Service`](internal/services/subcheck/check.go)
- **实时通知**: 通过 Web Push 推送直播开播通知和自动录制状态，详见 [`notify.Service`](internal/services/notify/notify.go)
- **缓冲池**: 使用 [`pool.BufferPool`](pkg/pool/pool.go) 减少内存分配
- **SWR 缓存**: 房间信息缓存采用 Stale-While-Revalidate 策略（[`pkg/swr`](pkg/swr/)），结合 singleflight 去重，缓存命中时立即返回旧数据并在后台异步刷新，软 TTL 5 分钟、硬 TTL 30 分钟，有效降低 Bilibili API 请求压力
- **定期刷盘**: 每 30 秒自动同步写入文件，防止数据丢失
- **低资源占用**: 设计注重低内存和低 CPU 使用，适合树莓派等资源受限设备
- **文件管理**: 支持列出、预览、下载（可转换格式）、批量删除文件及删除目录，详见 `internal/controllers/file/file.go`
- **自动转换**: 如果启用 `CONVERT_TO_MP4`，录制完成时会自动将可转换源文件（例如 FLV、TS）转为 MP4；可通过 `DELETE_SOURCE_AFTER_CONVERT` 控制是否删除原始源文件。已修复部分不支持的编解码器导致转换失败的问题，并具备 FFmpeg 任务冷却机制避免卡死失败任务
- **在线播放**: 支持在浏览器中直接播放已转换的 MP4 视频，提供原生 HTML5 video 标签体验，支持暂停/快进/全屏等操作
- **实时修复（Realtime Fixer）**: 在流式写入场景下逐个修复 FLV Tag 的时间戳并输出，包含重复 Tag 去重（可查询去重统计），并通过内存池、去重缓存与周期清理来保持低延迟与低内存占用，适合边录制边推送或实时下载的场景。
- **函数式编程工具**: 提供 [`fp`](pkg/fp/) 包含便捷的 maps 和 slices 操作函数
- **REST API 文档**: Swagger UI 在根路径 `/` 提供（由 `swag` 生成，参见 `internal/modules/rest`）
- **认证与调试**: 可选用户名/密码登录（设置 `USERNAME` 和 `PASSWORD`）启用 JWT 认证；调试模式下可通过 `/debug/pprof` 访问 pprof（受临时 token 或基本 auth 保护）
- **Cookie 管理**: 自动刷新 Bilibili Cookie 保持登录状态

## 依赖项

主要依赖库：

- [github.com/gofiber/fiber/v3](https://github.com/gofiber/fiber) - Web 框架
- [github.com/CuteReimu/bilibili/v2](https://github.com/CuteReimu/bilibili) - Bilibili API 客户端
- [go.uber.org/fx](https://github.com/uber-go/fx) - 依赖注入框架
- [github.com/sirupsen/logrus](https://github.com/sirupsen/logrus) - 日志库

## 许可证

请参阅项目许可证文件。

## 贡献

欢迎提交 Issue 和 Pull Request！
