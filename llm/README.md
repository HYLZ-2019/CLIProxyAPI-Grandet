# LLM Notes

用于记录 AI 助手在本项目中的工作笔记。

## 用途

- 记录用户提出的新需求、约束和设计意图。
- 整理需要同步到 `prompts/version_design.md` 的内容。
- 保留阶段性理解，方便后续继续设计和实现。

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
