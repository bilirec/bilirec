# 贡献指南

感谢参与 Bilirec 项目。

## 文档分工

- **用户文档**（安装、配置、API 说明、FAQ）在 [bilirec-docs](https://github.com/bilirec/bilirec-docs) 仓库，线上地址 [www.bilirec.org](https://www.bilirec.org)。
- 本仓库 **README** 仅保留项目简介与快速入门；详细内容请改 docs，不要再把长文写回 README。
- 修改 docs 时只编辑 `src/content/docs/zh-cn/`；繁体由 `pnpm convert:zh-tw` 自动生成，勿手改 `zh-tw`。

## FAQ 维护

- 仅当错误提示/UI 文案无法让用户自行理解时，才在 [guides/faq](https://www.bilirec.org/zh-cn/guides/faq/) 新增条目。
- FAQ 正文用白话，环境变量与公式放在专题页链接里。
- 行为变更时同步更新 FAQ 及相关专题页的「常见问题」嵌入块。

## 代码贡献

1. Fork 并创建分支。
2. 本地开发：`make dev`（`go run ./cmd/backend`）。
3. 构建：`make build`（默认 Windows exe）。其他目标：`make build os=linux|darwin|android`。
4. 提交 PR，说明环境（OS、Docker/二进制、录制路数）与重现步骤。

## API 变更

若修改 REST 接口：

1. 更新 Swagger：`swag init -g internal/modules/rest/rest.go -o docs`（或 `go generate`）。
2. 同步 bilirec-docs 的 `src/content/docs/zh-cn/api/` 对应页面。
3. 运行 `pnpm convert:zh-tw` 后确保 docs 构建通过。

## 语言

- 用户面向文档与注释以**简体中文**为主。
- 繁体中文文档由 OpenCC 转换生成。
