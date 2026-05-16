# Version Design

用于记录计划设计和实现的功能。

---

## 产品最终形态设计（用户原始想法）

### 核心目标

CLIProxyAPI 不仅是一个代理服务器，更是一个 **多租户 API 额度管理与用量分析平台**。每个接入的 client 都应该有完整的身份标识、用量记录和额度统计，使得管理员可以清晰了解"谁用了多少"。

---

### 一、API 秘钥机制升级

#### 当前痛点
现在 client API key 只是一个字符串列表，没有稳定标识符，无法区分不同用户，也无法做精细化的用量追踪。

#### 目标设计

每个 client API key 应该有：

1. **唯一数字 ID**（如 1/2/3/4/...）
   - 自增，永不复用。
   - 删除 key #3 后，下一个新建的 key 应该是 #4，而不是 #3。
   - 通过 `api-keys-next-id` 高水位计数器在 YAML 中持久化，跨重启保持稳定。

2. **名称（name）**
   - 人类可读的字符串标识，如 `"alice-work"`、`"bob-dev"`。
   - 可随时修改，不影响 ID 和 key secret。
   - 可以为空（兼容旧配置）。

3. **key secret**（现有字段）
   - 实际的认证凭据，可以更换。

#### YAML 格式示例

```yaml
api-keys-next-id: 5
api-keys:
  - id: 1
    name: alice-work
    api-key: sk-alice-xxxxxxxx
  - id: 2
    name: bob-dev
    api-key: sk-bob-yyyyyyyy
  - id: 4          # id=3 已删除，编号不复用
    name: carol-test
    api-key: sk-carol-zzzzzzzz
```

---

### 二、用量统计与分析

#### 当前痛点
- 用量数据是内存临时存储，重启即丢失。
- 无法按 client key 分组查看用量。
- 没有 token 粒度的统计（只有请求次数）。

#### 目标设计

用量统计应该：

1. **按 client API key ID 分组**
   - 每个 client key 拥有独立的用量记录。
   - 统计维度：成功次数、失败次数、token 用量（input/output）。

2. **按上游 provider key 分组**
   - 记录每个 Gemini/Claude/Codex/等 provider key 的用量。
   - 实现额度感知：知道哪个 key 快用完了。

3. **时序数据**
   - 支持按时间窗口查询（近 1h、24h、7d）。
   - 支持趋势图展示。

4. **持久化**
   - 当前内存队列机制应配合持久化后端（本地 SQLite 或外部 DB）。
   - 至少支持 rolling-window 的持久化统计（重启后不丢数据）。

5. **TUI 展示**
   - 在 dashboard 上按 client key 名称展示用量 top list。
   - 每个 provider key 的健康状态（正常/配额超限/冷却中）。

---

### 三、阶段规划

| 阶段 | 内容 | 状态 |
|------|------|------|
| Phase 1 | API key 唯一 ID + 名称机制 | **进行中** |
| Phase 2 | 用量按 client key ID 分组统计 | 待设计 |
| Phase 3 | token 粒度用量统计 | 待设计 |
| Phase 4 | 持久化存储 | 待设计 |
| Phase 5 | TUI/面板用量可视化 | 待设计 |
