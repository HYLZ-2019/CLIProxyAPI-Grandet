# LLM Notes

用于记录 AI 助手在本项目中的工作笔记。

## 用途

- 记录用户提出的新需求、约束和设计意图。
- 整理需要同步到 `prompts/version_design.md` 的内容。
- 保留阶段性理解，方便后续继续设计和实现。

## 当前需求文档

- [`analytics_requirements.md`](analytics_requirements.md)：当前“用量统计”功能的实现准则。`prompts/version_design.md` 保留历史原始想法；如果两者冲突，以当前需求文档为准。

## 当前上下文

用户希望在 CLIProxyAPI 上拥有 token 使用量统计功能。当前仓库已有 usage 采集管线，主要从模型响应中的 usage 字段被动解析 token 用量；`/v0/management/usage-queue` 读取本地 usage 队列，`/v0/management/api-key-usage` 读取本地内存请求计数。通用 quota 状态主要根据上游错误推断，Antigravity credits 是少数主动查询额度的特例。

---

## 进度记录

### 2026-05-16 — API Key 唯一 ID 机制改造

**目标：** 使每个 client API key 拥有一个永不复用的自增数字 ID，以及一个可编辑的名称字段。

**现状分析：**
- `ClientAPIKey` struct 已有 `ID`、`Name`、`APIKey` 三个字段（位于 `internal/config/sdk_config.go`）。
- `NormalizeClientAPIKeys` 目前用 `maxID + 1` 为无 ID 的条目分配 ID。
- **漏洞**：若列表为 [1,2,3]，删除 #3 后再添加新 key，新 key 会获得 ID 3（复用已删除 ID）。
- TUI (`internal/tui/keys_tab.go`) 已支持 ID/Name 的显示和编辑。
- 管理 API (`internal/api/handlers/management/config_lists.go`) 已支持按 ID 定位 key。

**改造方案：**
1. 在 `SDKConfig` 中新增 `APIKeysNextID int`（yaml: `api-keys-next-id,omitempty`）——高水位计数器，记录历史曾用过的最大 ID。
2. 新增 `NormalizeClientAPIKeysWithHint(entries []ClientAPIKey, nextIDHint int) ([]ClientAPIKey, int)` 函数：
   - 分配新 ID 时从 `max(当前最大ID, nextIDHint)` 开始递增，而非 `maxID + 1`。
   - 返回规范化后的条目和新的高水位值（已分配过的最大 ID + 1）。
3. `NormalizeClientAPIKeys` 保持原签名（调用 `NormalizeClientAPIKeysWithHint(entries, 0)`），保持向后兼容。
4. 在 `LoadConfigOptional` 和 `ParseConfigBytes` 中用新函数替换旧调用，并同步更新 `cfg.APIKeysNextID`。
5. 管理 API 的所有写操作（PATCH/PUT/DELETE）在修改 `h.cfg.APIKeys` 后，用新函数同步 `h.cfg.APIKeysNextID`。
6. `sdk/config/config.go` 补充新函数的 re-export。

**文件变更清单：**
- `internal/config/sdk_config.go` — 新增字段和函数
- `sdk/config/config.go` — 新增 re-export
- `internal/config/parse.go` — 更新 `ParseConfigBytes`
- `internal/config/config.go` — 更新 `LoadConfigOptional`（行 642）
- `internal/api/handlers/management/config_lists.go` — 更新 6 处 `NormalizeClientAPIKeys` 调用（行 111、131、150、205、223、230）

**不需要改动的调用：**
- `internal/watcher/diff/config_diff.go` 行 329-330（只做 diff 对比，不分配新 ID）
- `internal/access/config_access/provider.go` 行 139（注册认证用，key 在加载时已有 ID）
- `config_lists.go` 行 316 的 `existing = config.NormalizeClientAPIKeys(existing)`（辅助函数内部）

**向后兼容：**
- 旧 YAML 无 `api-keys-next-id` 字段时，该值默认为 0，`max(maxID+1, 0) = maxID+1`，行为与旧版一致。
- 首次启动会自动修复 `APIKeysNextID = max(所有现有 ID) + 1`，后续操作不再复用。

**状态：已实现**

---

### 2026-05-16/17 — 分析统计系统（Analytics & Usage Tracking）

**目标：** 在本地 SQLite 数据库中记录每次请求的 Token 用量、429 配额耗尽事件、每小时聚合数据，并主动轮询上游 provider 配额 API。前端增加 Analytics 开关和日志保留天数配置。

**架构：**
- SQLite 驱动：`modernc.org/sqlite`（纯 Go，无 CGO，兼容 `CGO_ENABLED=0` 的 Docker 构建）
- 数据库路径：`{config_dir}/analytics.db`
- 四张表：`query_logs`（原始 per-query）、`hourly_aggregates`（每小时聚合）、`quota_exhaustion_events`（429 事件）、`quota_snapshots`（主动轮询快照）
- Client key 追踪：`config_access/provider.go` 将 `api_key_id` 写入 `result.Metadata`，`AuthMiddleware` 注入到 request context（`analytics.ClientKeyIDCtxKey`），analytics plugin 从 context 读取
- 配额轮询：复用 `authManager.List()` 中已有的 OAuth access token，每小时轮询 Claude/Codex/Gemini CLI 的配额 API

**新增文件：**
- `internal/analytics/analytics.go` — Store、DB 初始化、schema 迁移、所有查询方法
- `internal/analytics/plugin.go` — 实现 `usage.Plugin`，`init()` 自注册
- `internal/analytics/cleanup.go` — 每小时定期清理超过保留期的原始日志
- `internal/analytics/quota_poller.go` — 主动轮询上游 provider 配额 API

**改动文件：**
- `go.mod` — 新增 `modernc.org/sqlite v1.36.1`（需用户运行 `go get modernc.org/sqlite@latest && go mod tidy`）
- `internal/config/sdk_config.go` — 新增 `AnalyticsConfig`（`enabled`、`raw-log-retention-days`）
- `sdk/config/config.go` — re-export `AnalyticsConfig`
- `internal/api/server.go` — analytics init + quota poller 启动；AuthMiddleware 注入 client key ID；注册 analytics 路由
- `internal/api/handlers/management/handler.go` — 新增 `analyticsDBPath` 字段和 setter
- `internal/api/handlers/management/analytics.go` — 8 个 management API 端点
- `web/src/types/visualConfig.ts` — 新增 `analyticsEnabled`、`analyticsRetentionDays` 字段
- `web/src/hooks/useVisualConfig.ts` — YAML 读写和 dirty tracking
- `web/src/components/config/VisualConfigEditor.tsx` — 新增 Analytics ConfigSection（section ID: `analytics`，indexLabel: `10`）
- `web/src/i18n/locales/{en,zh-CN,zh-TW,ru}.json` — analytics i18n 字符串

**Management API 端点（均在 `/v0/management/analytics/` 下）：**
- `GET /summary` — 过去 24 小时请求数、Token 总量、错误数
- `GET /hourly` — 按小时聚合（`?from=&to=` Unix 时间戳）
- `GET /by-model` — 按模型分组
- `GET /by-client` — 按 client key ID 分组
- `GET /quota-events` — 429 事件列表
- `GET /quota-snapshots` — 配额快照列表
- `GET /config` — 返回 AnalyticsConfig JSON
- `PUT /config` — 更新 enabled 和 retention days

**状态：已实现；Go 依赖已 tidy。`go test ./...` 全量通过。前端 `type-check`、`lint`、Node v20.20.2 下的 `build` 均通过，并已用 Vite dev server smoke test `/analytics` 路由入口；尚未用真实浏览器手动点验 Analytics UI。**

---

### 2026-05-18 — Grandet 小服务器部署与更新工作流

**背景：** 用户的小服务器容量约 8GB，实际架构为 `aarch64`。Docker 构建会拉取/缓存 Go builder、Node builder、npm 依赖、Go modules 和镜像层，空间压力过大，因此改为本地交叉编译预构建包，在小服务器上用 systemd 直接运行。

**Release 包：**
- GitHub Release：`grandet-v0.1.0`
- Release 地址：`https://github.com/HYLZ-2019/CLIProxyAPI-Grandet/releases/tag/grandet-v0.1.0`
- 已上传资产：
  - `CLIProxyAPI-Grandet-linux-amd64.tar.gz`
  - `CLIProxyAPI-Grandet-linux-amd64.tar.gz.sha256`
  - `CLIProxyAPI-Grandet-linux-arm64.tar.gz`
  - `CLIProxyAPI-Grandet-linux-arm64.tar.gz.sha256`
- 小服务器应使用 `linux-arm64` 包；之前使用 `linux-amd64` 会导致 systemd `status=203/EXEC`，因为二进制架构不匹配。

**小服务器部署目录来源：**
- `/home/ubuntu/CLIProxyAPI-Grandet` 不是源码目录，而是从 release tarball 解压出的部署目录。
- 部署包内主要包含：
  - `CLIProxyAPI`：预编译 ARM64 后端二进制
  - `static/management.html`：已构建好的 Grandet 前端管理面板
  - `config.example.yaml`：示例配置
  - `README.md`：项目说明
  - `INSTALL.md`：安装说明
- 运行时额外生成或保留：
  - `config.yaml`：服务器实际配置
  - `auths/`：OAuth/auth 文件
  - `logs/`：日志
  - `data/`：Analytics 数据库等数据

**systemd 服务：**
- 服务名：`cliproxy-grandet`
- 服务文件：`/etc/systemd/system/cliproxy-grandet.service`
- 工作目录：`/home/ubuntu/CLIProxyAPI-Grandet`
- 启动命令：`/home/ubuntu/CLIProxyAPI-Grandet/CLIProxyAPI`
- 关键环境变量：`MANAGEMENT_STATIC_PATH=/home/ubuntu/CLIProxyAPI-Grandet/static`
- “重启代理”指执行：`sudo systemctl restart cliproxy-grandet`，用于让进程重新读取 `config.yaml` 并加载新的二进制/前端静态文件。

**数据延续规则：**
- 持久数据可以延续，关键是不要删除或覆盖：
  - `config.yaml`
  - `auths/`
  - `logs/`
  - `data/`
- Analytics 数据库位于：`/home/ubuntu/CLIProxyAPI-Grandet/data/analytics.db`。
- 日常更新时只覆盖 `CLIProxyAPI` 和 `static/management.html`，即可保留配置、认证文件和用量统计数据。
- 避免重复执行会覆盖配置/数据的操作，例如 `cp config.example.yaml config.yaml` 或 `rm -rf data auths logs`。

**推荐迭代工作流：**
- 不在小服务器上构建 Go、Node 或 Docker；在本地开发机完成构建。
- 只改前端时：本地执行 `npm --prefix web run build`，然后把 `web/dist/index.html` 传到小服务器的 `static/management.html`，最后重启 `cliproxy-grandet`。
- 改了 Go 后端时：本地交叉编译 `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ...`，把生成的二进制传到小服务器的 `CLIProxyAPI`，`chmod +x` 后重启服务。
- 需要稳定分发给服务器时，再打新的 GitHub Release 包。

**状态：** 已创建 `grandet-v0.1.0` release，已补充 ARM64 包；小服务器应改用 `CLIProxyAPI-Grandet-linux-arm64.tar.gz`。
