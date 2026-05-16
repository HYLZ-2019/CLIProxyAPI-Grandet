# 设计展开笔记

基于用户原始想法的设计分析与展开，供实现参考。

---

## 用量统计与分析（展开）

### 潜在需求方向

1. **按 client API key ID 分组**
   - 每个 client key 拥有独立的用量记录
   - 统计维度：成功次数、失败次数、token 用量（input/output）

2. **按上游 provider key 分组**
   - 记录每个 Gemini/Claude/Codex/等 provider key 的用量
   - 实现额度感知：知道哪个 key 快用完了

3. **时序数据**
   - 支持按时间窗口查询（近 1h、24h、7d）

4. **持久化**
   - 当前内存队列机制应配合持久化后端（本地 SQLite 或外部 DB）
   - 至少支持 rolling-window 的持久化统计（重启后不丢数据）

5. **TUI 展示**
   - 在 dashboard 上按 client key 名称展示用量 top list
   - 每个 provider key 的健康状态（正常/配额超限/冷却中）

### 阶段规划（参考）

| 阶段 | 内容 | 状态 |
|------|------|------|
| Phase 1 | API key 唯一 ID + 名称机制 | 已完成 |
| Phase 2 | 用量按 client key ID 分组统计 | 待设计 |
| Phase 3 | token 粒度用量统计 | 待设计 |
| Phase 4 | 持久化存储 | 待设计 |
| Phase 5 | TUI/面板用量可视化 | 待设计 |
