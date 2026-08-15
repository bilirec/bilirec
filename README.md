# Bilirec

一个专为低配设备优化的高性能 Bilibili 直播录制后端。

**完整文档：** [www.bilirec.org/zh-cn/](https://www.bilirec.org/zh-cn/) · [常见问题](https://www.bilirec.org/zh-cn/guides/faq/)

**交流与反馈：** QQ 群 [834229325](https://qm.qq.com/q/oMTN3EsGBy)，用于交流和问题反映。

## 目录

- [功能特性](#功能特性)
- [效能指标](#效能指标)
- [快速开始](#快速开始)
- [生态](#生态)
- [文档](#文档)
- [贡献与许可](#贡献与许可)

## 功能特性

- ✅ **开箱即用** — 零配置起步，运行后直接访问 Web 界面即可管理任务
- ✅ **多模式录制** — 手动开始录制，或订阅直播间自动开播录制
- ✅ **多格式支持** — HTTP-FLV / HLS-TS / HLS-fMP4
- ✅ **灵活录制时长** — 指定时长上限、定时停止或无限时长
- ✅ **开播即时通知** — Web Push / SSE 推送开播动态
- ✅ **弹幕与互动记录** — 可在手动录制或房间自动录制时同时保存弹幕与礼物等互动记录
- ✅ **自动分段轮转** — PK 或分辨率变更时自动切文件，减少花屏与损坏
- ✅ **多路并发录制** — 低配硬件上也能稳定多路同时录
- ✅ **故障自动恢复** — FLV 时间戳修复与断流重连，连接波动时尽量不中断
- ✅ **自动 MP4 转换** — 本地 FFmpeg 或 CloudConvert 云端转码
- ✅ **监控指标** — 可选开启独立监控端口，长期观察录制与资源状态
- ✅ **多端运行与接入** — RESTful API、[bilirec-web](https://github.com/bilirec/bilirec-web)（PWA）、[bilirec-mobile](https://github.com/bilirec/bilirec-mobile)（Android 内嵌后端）
- ✅ **FRP 内网穿透** — 无公网 IP 也可外网访问管理界面与文件
- ✅ **文件管理与播放** — 列表浏览、批量删除、内置播放器支持礼物特效与弹幕滚动回放
- ✅ **账号登录与刷新** — 匿名 / 扫码 / Controller 模式，Cookie 自动刷新
- ✅ **低配与 microSD 优化** — 大块缓冲、序列化写入、跳过极短直播写盘；默认开启录製中定期清理旧文件缓存，压低容器监控内存

配置详解、API、调优方案见 [官方文档站](https://www.bilirec.org/zh-cn/configuration/overview/)。

## 效能指标

> 测试环境：**树莓派 5B 16GB** · Docker 容器（**1 GB 内存限制**）· 默认配置（含 `DROP_FILE_PAGE_CACHE` + 冷缓存释放）· 每路 **1080p**。
>
> 下表「内存峰值」为 **Docker 等容器监控里显示的内存用量**，改配置或版本后会有偏差。规划容器 **limit** 请仍用 [内存占用估算](https://www.bilirec.org/zh-cn/configuration/memory-estimation/)（偏保守）。

| 并发路数 | CPU 峰值占用 | 内存峰值占用 |
| -------- | ------------ | ------------ |
| 初始闲置 | ~0.0% | ~7 MB |
| 1 路并发 | ~2.5% | ~85 MB |
| 2 路并发 | ~3.0% | ~106 MB |
| 3 路并发 | ~4.0% | ~135 MB |
| 4 路并发 | ~4.5% | ~164 MB |
| 5 路并发 | ~5.3% | ~193 MB |
| 恢复闲置 | ~0.0% | ~54 MB |

**内存规划速查（默认 1080p）：**

| 路数 N | 计算 | 预计总峰值 |
| ------ | ---- | ---------- |
| 1 | `(43~50) + 1×49` | 约 92~99 MB |
| 3 | `(43~50) + 3×49` | 约 190~197 MB |
| 5 | `(43~50) + 5×49` | 约 288~295 MB |

默认 **4K 单路**约 `(43~50) + 1×122` → **165~172 MB**。公式与变量说明见 [内存占用估算](https://www.bilirec.org/zh-cn/configuration/memory-estimation/)。

## 快速开始

### 二进制

从 [Releases](https://github.com/bilirec/bilirec/releases) 下载对应平台文件：

```bash
chmod +x bilirec-linux-amd64 && ./bilirec-linux-amd64
# Windows：双击 bilirec-windows.exe
```

### Docker

```bash
# Docker Hub
docker pull eric1008818/bilirec:latest

# GHCR（国内若 Docker Hub 拉取困难，可改用 GitHub Container Registry）
docker pull ghcr.io/bilirec/bilirec:latest

docker run -d --name bilirec -p 8080:8080 \
  -v /path/to/records:/app/records \
  -v /path/to/secrets:/app/secrets \
  -v /path/to/database:/app/database \
  eric1008818/bilirec:latest
```

`docker run` 时把镜像名换成 `ghcr.io/bilirec/bilirec:latest` 即可使用 GHCR。

启动后打开 [app.bilirec.org](https://app.bilirec.org/)，在登录页填写后端地址（默认 `http://localhost:8080`）。详细步骤见 [快速开始](https://www.bilirec.org/zh-cn/guides/quick-start/)。

### Android（嵌入 App）

- 官方客户端：[bilirec-mobile](https://github.com/bilirec/bilirec-mobile)
- 库接口与构建：`make android` → `dist/android/<abi>/libbilirec.so`
- 说明见 [Android 指南](https://www.bilirec.org/zh-cn/guides/android/) · [Android 库接口](https://www.bilirec.org/zh-cn/development/android-library/)

> [!IMPORTANT]
> 请关闭电池优化并允许通知。华为 / 鸿蒙、OPPO / ColorOS、小米 / HyperOS、vivo / OriginOS 等大陆国产系统还可能在锁屏、切换应用或清理多任务时终止 Bilirec，造成录制中断；这类设备请按 [国产手机后台设置](https://www.bilirec.org/zh-cn/guides/android-mainland/) 额外放行后台活动并锁定多任务卡片。

## 生态

| 项目 | 说明 |
| ---- | ---- |
| [bilirec-web](https://github.com/bilirec/bilirec-web) | Web 管理界面（PWA） |
| [bilirec-mobile](https://github.com/bilirec/bilirec-mobile) | Android 客户端，可在手机内运行后端 |
| [bilirec-docs](https://github.com/bilirec/bilirec-docs) | 官方文档站 |

## 文档

| 主题 | 链接 |
| ---- | ---- |
| 安装 | [guides/installation](https://www.bilirec.org/zh-cn/guides/installation/) |
| 快速开始 | [guides/quick-start](https://www.bilirec.org/zh-cn/guides/quick-start/) |
| 配置与调优 | [configuration/overview](https://www.bilirec.org/zh-cn/configuration/overview/) |
| 树莓派 / microSD 默认 | [configuration/pi5-defaults](https://www.bilirec.org/zh-cn/configuration/pi5-defaults/) |
| 内存占用估算 | [configuration/memory-estimation](https://www.bilirec.org/zh-cn/configuration/memory-estimation/) |
| REST API | [api/overview](https://www.bilirec.org/zh-cn/api/overview/)（运行时根路径 `/` 另有 Swagger UI） |
| 监控指标 | [configuration/metrics](https://www.bilirec.org/zh-cn/configuration/metrics/) |
| 常见问题 | [guides/faq](https://www.bilirec.org/zh-cn/guides/faq/) |
| 性能实测 | [guides/performance-benchmark](https://www.bilirec.org/zh-cn/guides/performance-benchmark/) |

## 贡献与许可

请参阅 [CONTRIBUTING.md](CONTRIBUTING.md) 与项目许可证文件。
