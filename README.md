# Bilirec

一个专为低配设备优化的高性能 Bilibili 直播录制后端。

**完整文档：** [www.bilirec.org/zh-cn/](https://www.bilirec.org/zh-cn/) · [常见问题](https://www.bilirec.org/zh-cn/guides/faq/)

## 文档

| 主题 | 链接 |
|------|------|
| 安装 | [guides/installation](https://www.bilirec.org/zh-cn/guides/installation/) |
| 快速开始 | [guides/quick-start](https://www.bilirec.org/zh-cn/guides/quick-start/) |
| 配置与调优 | [configuration/overview](https://www.bilirec.org/zh-cn/configuration/overview/) |
| REST API | [api/overview](https://www.bilirec.org/zh-cn/api/overview/)（运行时根路径 `/` 另有 Swagger UI） |
| 常见问题 | [guides/faq](https://www.bilirec.org/zh-cn/guides/faq/) |

原 README 长文章节对照：[`#配置`](https://www.bilirec.org/zh-cn/configuration/overview/) · [`#rest-api`](https://www.bilirec.org/zh-cn/api/overview/) · [`#内存占用估算`](https://www.bilirec.org/zh-cn/configuration/memory-estimation/) · [`#android嵌入-app`](https://www.bilirec.org/zh-cn/guides/android/)

## 特性

- 开箱即用，Web 界面管理任务；支持 HTTP-FLV / HLS-TS / HLS-fMP4
- 多路并发录制、自动恢复与分段轮转；可选录完转 MP4
- 订阅开播自动录制；Web Push / SSE 通知
- REST API、[bilirec-web](https://github.com/bilirec/bilirec-web)（PWA）、[bilirec-mobile](https://github.com/bilirec/bilirec-mobile)（Android）
- 内置 FRP 内网穿透；针对树莓派与 microSD 的 I/O 优化

## 快速开始

### 二进制

从 [Releases](https://github.com/bilirec/bilirec/releases) 下载对应平台文件后启动：

```bash
chmod +x bilirec-linux-amd64 && ./bilirec-linux-amd64
# Windows：双击 bilirec-windows.exe
```

### Docker

```bash
docker pull eric1008818/bilirec:latest
docker run -d --name bilirec -p 8080:8080 \
  -v /path/to/records:/app/records \
  -v /path/to/secrets:/app/secrets \
  -v /path/to/database:/app/database \
  eric1008818/bilirec:latest
```

启动后打开 [app.bilirec.org](https://app.bilirec.org/)，在登录页填写你的后端地址（默认 `http://localhost:8080`）。更多步骤见 [快速开始](https://www.bilirec.org/zh-cn/guides/quick-start/)。

## 生态

- **[bilirec-web](https://github.com/bilirec/bilirec-web)** — Web 管理界面（PWA）
- **[bilirec-mobile](https://github.com/bilirec/bilirec-mobile)** — Android 客户端，可在手机内运行后端
- **[bilirec-docs](https://github.com/bilirec/bilirec-docs)** — 官方文档站

## 性能

树莓派 5B 等低配设备上的多路录制实测见 [性能实测](https://www.bilirec.org/zh-cn/guides/performance-benchmark/)；规划内存请用 [内存占用估算](https://www.bilirec.org/zh-cn/configuration/memory-estimation/)。

## 贡献

请参阅 [CONTRIBUTING.md](CONTRIBUTING.md)。

## 许可证

请参阅项目许可证文件。
