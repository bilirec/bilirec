---
name: bilirec-find-rotation-rooms
description: >-
  Finds Bilibili live rooms that rotate recording segments (PK / split-screen
  layout changes) at high probability, to exercise BiliRec's auto split
  rotation on a real stream. Confines the search to 娱乐 (parentAreaId=1),
  switches card previews to 关键帧, detects composed 900x720 canvases, then
  confirms with the red/blue PK bar. Use when the user asks to find rooms with
  frequent 轮转 / 分段 / 切割, PK 或连麦多的直播间, 多宫格 / 分割画面,
  "分辨率为啥一直变", or wants a live stream to test auto split behavior.
---

# 找高概率分段轮转的直播间

BiliRec 的自动分段靠**视频序列头（分辨率 / SPS-PPS）在直播中途变化**触发。
PK、连麦、多宫格是常见触发场景。本技能是一套人工筛选流程。

## 铁律：只找娱乐区

分区页的 `parentAreaId` 决定命中率。实测（见文末数据）**只有 `parentAreaId=1`
（娱乐）会出现分割画面**，游戏 / 虚拟主播 / 聊天室 / 生活 / 知识 全是单画面。

```
https://live.bilibili.com/p/eden/area-tags?parentAreaId=1&areaId=<子分区>
```

| 子分区 | areaId | 实测分割画面占比 |
|---|---|---|
| 娱乐-全部 | `0` | 38% |
| **颜值** | `145` | 26–37% |
| 舞见 | `207` | 33–36% |
| 视频唱见 | `21` | 0–33%（波动大） |
| 萌宅领域 | `530` | 20–25% |
| 脱口秀 | `868` | 19% |
| 团播 | `1013` | **0%**（全是 405x720 竖屏单人机位） |
| 电台 / 赛事 | `10` / `11` | 5% |

优先 `areaId=145`（颜值）或 `areaId=0`（娱乐-全部）。注意 `530` 是**萌宅领域**，
不是"娱乐全部"。

## 流程

1. 打开上表中的一个子分区页。
2. 把卡片预览切成 **关键帧**。排序条右侧有 `封面图 / 关键帧` 两个标签：

   ```js
   Array.from(document.querySelectorAll('.cover-tabs-wrap .tabs__normal-item'))
     .find(x => x.textContent.includes('关键帧')).click()
   ```

   **必须验证真的切过去了**，否则看到的还是封面图：

   ```js
   // 期望 "关键帧" 带 active，且关键帧层的 display 不是 none
   (() => {
     const on = Array.from(document.querySelectorAll('.cover-tabs-wrap .tabs__normal-item'))
       .filter(e => e.className.includes('active')).map(e => e.textContent.trim());
     const layer = Array.from(document.querySelectorAll('[class*=Item_card-item-ctnr] *'))
       .find(e => (getComputedStyle(e).backgroundImage || '').includes('live-key-frame'));
     return on.join(',') + ' | ' + (layer ? getComputedStyle(layer).display : 'missing');
   })()
   ```

   得到 `关键帧 | block` 才算成功。常见坑：

   - 标签是**整页共用一条**，点一次全网格生效，不是逐卡点。
   - 刷新页面会退回 `封面图`，需要重点。
   - 用坐标点击容易失败：标签常在视口外（实测 x≈1932 超出宽度），
     必须直接对元素 `.click()`。
   - 关键帧是抓拍，会滞后几十秒到一分钟。
   - 图片文件名含抓拍时间：`keyframe1108201100_<roomid>...` 读作 `11-08 20:11`，
     可据此判断新旧。

3. 用**图片尺寸**粗筛分割画面。关键帧的原始尺寸直接暴露机位布局：

   | 尺寸 | 含义 |
   |---|---|
   | `1280x720` | 单路 PC 摄像头 / 采集卡 |
   | `405x720` | 单路手机竖屏 |
   | **`900x720`（5:4）** | **多路合成的画布 —— 分屏 / 多宫格 / PK 布局** |
   | 其他非 16:9 尺寸 | 合成画布（同样值得看） |

   ```js
   // 数一页里有多少张合成画布
   (async () => {
     const urls = Array.from(document.querySelectorAll('[class*=Item_card-item-ctnr]'))
       .map(c => {
         const el = Array.from(c.querySelectorAll('*')).find(e =>
           (getComputedStyle(e).backgroundImage || '').includes('live-key-frame'));
         const m = el && (getComputedStyle(el).backgroundImage || '').match(/url\("([^"]+)"/);
         return m && m[1];
       }).filter(Boolean);
     const dims = await Promise.all(urls.map(u => new Promise(r => {
       const i = new Image();
       i.onload = () => r(i.naturalWidth + 'x' + i.naturalHeight);
       i.onerror = () => r('0');
       i.src = u;
     })));
     const t = {};
     dims.forEach(d => t[d] = (t[d] || 0) + 1);
     return JSON.stringify(t);
   })()
   ```

   同时用眼看：画面被切成左右两半、九宫格、或小窗叠大窗，都属于分割画面。
   单人的正脸大图不属于。

4. 点进候选房间，确认播放器里有没有**红蓝 Bar**（PK / 连麦对战条，一侧红
   一侧蓝，中间有比分或进度）：

   ```
   https://live.bilibili.com/<room_id>
   ```

   有红蓝 Bar → 轮转概率极高。

   **不要用 DOM 判断**：`universal-pk-box`、`anchor-team-pk-player` 等 PK 容器
   在没打 PK 时也挂载着（实测可见 `1865x1049` 且 `display:flex`），必须看图确认。

## 浏览器工具优先级

按下列顺序选工具，前一个不可用或结果不可信再降级：

1. **BrowserOS**（`user-browser-os`）
2. **Chrome DevTools**（`plugin-devtools-for-agents-chrome-devtools`）
3. **Computer Use**（`cursor-computer-use`）
4. **Cursor 内置浏览器**（`cursor-ide-browser`）

页面有 UWP / 跨域 iframe 限制，或截图为空白时，直接降一级，不要在原工具上反复试。

单次采样耗时较长，`evaluate_script` 一次别塞十个分区，容易超时；分批跑。

## 判断依据

| 看到的现象 | 轮转概率 | 原因 |
|---|---|---|
| 关键帧是合成画布 **且** 有红蓝 PK 条 | 极高 | PK 结束回到单画面，分辨率与编码参数通常会变 |
| 关键帧是合成画布，无红蓝 Bar | 中高 | 多宫格会随人数重排，规律性不如 PK |
| 单人画面 + 有红蓝 Bar | 中 | 取决于是否真连上对手 |
| 单人画面、无 Bar | 低 | 一般只有下播 / 重连才换序列头 |

红蓝 Bar 是主信号，合成画布是关键帧阶段的粗筛。

**量化产出（2026-11-08 实测，供排期参考）**：530 区红蓝 Bar 房间在 30 分钟录制
窗口内可轮转 3–5 次（`-1` 到 `-5` 分段），普通房间 0 次。要在一次录製窗口内
抓到 ≥2 次轮转（例如验证跨轮转状态继承），必须用 PK 房——随机池房间大概率
整个窗口零轮转。

## 免浏览器的 API 取房法

人工翻页慢；先试 API 直接拿候选列表，浏览器流程只留给 API 拿不到的场景。

**新版分区分页 API 会被风控挡**（未登录返回 `{"code":-352}`，带 Cookie 也常被挡）：

```
https://api.live.bilibili.com/xlive/web-interface/v1/second/getList?...   ← -352
```

**旧版 Area API 匿名可用**（2026-11-08 实测），且无需 WBI 签名：

```bash
curl "https://api.live.bilibili.com/room/v1/Area/getRoomList?area_id=530&parent_area_id=1&page=1&page_size=30" \
  -H "User-Agent: Mozilla/5.0" -H "Referer: https://live.bilibili.com/"
```

响应 `data` 是**顶层数组**（不是 `.data.list`），字段为 `roomid / uname / title / online`。
按 `online` 降序取头部房间即可拿到高人气的 530 区候选。注意 API 只给"在播 +
分区"，**不给 PK 状态**——红蓝 Bar 仍需进房间确认，API 列表只能当粗筛池。

## 用集成测试直接验证（跳过生产实例）

找到候选房间后，不必起生产实例开录；测试基座支持钉房间：

```powershell
$env:BILIBILI_TEST_ROOM_IDS = "2063309371,31663229,1740235436"  # 逗号分隔
go test ./internal/services/recorder -run TestZZZ_Final_Concurrent3WayFlvRecord -count=1 -timeout 45m
```

- `concurrent=3`：钉 8 个左右候选，测试会自动验证在线并取前 3 个；全部离线该次
  attempt 跳过。周末白天优先房常批量下线，兜底做法是 unset 该环境变量走随机池。
- 本地默认录 3 分钟；要抓多次轮转需临时把 `internal/testutil/recording/integration.go`
  的 `IntegrationRecordDuration()` 提到 30 分钟（**用完回退**，见 fix 计划惯例）。
- **重轮转房间可能顶到活 heap 预算断言**：清理后断言的是 `heap_held`
  （`HeapSys - HeapReleased`），3 会话预算 72MB。长时间轮转后若量到超限，先看
  `memory retention` 那一行的 `alloc`：`alloc` 正常而只有 `heap_held` 高出，通常是
  scavenger 还没把空闲页还回去。用官方环境变量放宽即可聚焦音頻/分段断言：

  ```powershell
  $env:RECORDER_TEST_MAX_RETAINED_HEAP_HELD_MB = "220"
  ```

## 找到之后

```bash
curl -X POST http://127.0.0.1:8080/room/<room_id>
curl -X PUT http://127.0.0.1:8080/room/<room_id>/config \
  -H "Content-Type: application/json" \
  -d '{"auto_record":true,"qn":150,"record_danmaku":true}'
```

只有 FLV 会自动分段。房间若限定了 `stream_profiles`，必须包含 `http-flv`
（`hls-ts` 不轮转；`hls-fmp4` 只在 init 变化时分段）。

## 验证真的轮转了

三处任一即可：

- 输出目录出现续号文件：`<标题>-<时间>-1.flv`、`-2.flv`（首段无序号）。
- 日志出现 `🔀 视频序列头已变化，触发管道轮转` 与
  `收到策略要求轮转文件的信号，开始分割录制文件`。
- `bilirec_room_recording_rotations_total{room_id="..."}` 增长
  （`http://<host>:<METRICS_PORT>/metrics`，`METRICS_PORT` 默认 `9090`）。

有红蓝 Bar 但整个 PK 周期计数没涨，说明该场分辨率没变。换房间，不要据此
判断轮转功能坏了。

## 数据来源与局限

上表为 2026-11-08 20:1x 的单次抓样，每个分区取一页（26–80 个在播房间），
用关键帧原始尺寸统计 `900x720` 合成画布占比。分区间差异（娱乐 20–38% vs
其他分区 0%）足够稳定；分区内部的排序会随开播情况变化，且小样本有噪声
（个别分区复采时出现 0%，是页面未加载完，不是真的没有）。用它选分区方向，
不要当精确概率。开播高峰、连麦活动期间比例会更高。

**PK 房间轮转的时序特征（2026-11-08 晚三路实测补充）**：

- PK 的**开始和结束**都可能换序列头——连上对手合成画布、PK 完回单画面各触发
  一次，所以一次 PK 周期通常贡献 1–2 次轮转，间隔约 6–12 分钟。
- 轮转触发的是**分辨率/编码参数变化**；若对手与主播同分辨率同参数，PK 了也
  可能不触发（实测出现过 PK 全程零轮转的房间）。红蓝 Bar 只是高概率信号，
  不是充分条件。
- 长时间录制（30 分钟）里 PK 房可能轮转 5+ 次，分段可达 GB 级；扫全部分段
  （字节级 / ffprobe / remux）的磁盘与耗时成本要预留。
