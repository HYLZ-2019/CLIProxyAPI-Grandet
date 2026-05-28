# 用量统计当前需求

本文档记录 Grandet 当前希望保留和继续迭代的“用量统计”功能范围。`prompts/version_design.md` 是历史原始想法；如果两者冲突，以本文档为当前实现准则。

## 数据采集与归属

- 记录每次请求的 token usage：input、output、cached input、reasoning、cache read、cache creation、total。
- token 总量口径：`total = input + output + cached`。不直接信任上游返回的 total，避免不同 provider 口径不一致。
- 每条 usage 需要归属到：
  - provider；
  - auth 文件；
  - model；
  - Client API Key 的不可逆数字 ID。
- 前端只能展示 Client API Key 的备注名、label 或数字 ID；不得展示 raw API key。
- 无法归属到 Client API Key 的历史或异常记录显示为“未归因”。

## 存储与聚合

- Analytics 使用本地 SQLite 数据库。
- 原始 per-query 记录用于短窗口精细统计。
- 聚合记录用于长窗口查询。
- `from/to` 是所有前端用量统计查询的统一时间窗口。
- 短窗口趋势保持 5 分钟粒度；长窗口可退化到小时聚合。
- quota snapshots 以 5 分钟轮询为目标，并和 5 分钟聚合边界对齐。

## 当前前端展示范围

Analytics 页面展示以下内容：

1. 汇总卡片
   - 请求总数。
   - 成功率。
   - token 总量，并拆出 input/output/cached。
   - 错误数和配额事件数。
2. 用量趋势图
   - requests。
   - input tokens。
   - output tokens。
   - cached tokens。
   - 用户可以勾选显示或隐藏各条曲线。
3. 热门模型
   - 按请求数展示 Top Models。
   - 点击模型柱子展开 token breakdown：input、output、cached input、cache read、make cache、reasoning。
4. 热门 Client API Key
   - 按请求数展示 Top Client API Keys。
   - 显示备注名/label；无归属显示“未归因”。
5. Auth 文件配额折线
   - 用户选择一个 auth 文件。
   - 同时展示该 auth 的 5h 配额窗口和 7d 配额窗口。
   - 图表展示跨度由全局 `from/to` 控制，和配额窗口类型解耦。
   - 可显示/隐藏 429 点和预计刷新点。
   - 可选择在 429 或预计刷新时重置 CLIProxyAPI 累计 USD 曲线。
6. 官方 token 单价表
   - 显示当前时间范围内出现过的 provider/auth/model/token type 的官方 USD / 1M tokens 单价。
   - 支持按 auth 文件筛选。

## 时间范围 UX

- 不使用固定分档按钮 `1h / 5h / 24h / 3d / 7d`。
- 使用连续的开始时间和结束时间选择。
- 默认范围是近 24 小时。
- 保留快捷键：近 24 小时、近 7 天。
- 如果开始时间晚于或等于结束时间，不发请求，并显示本地错误。

## 当前后端 API 契约

前端继续使用现有 management analytics API：

- `GET /analytics/summary?from=&to=`
- `GET /analytics/hourly?from=&to=`
- `GET /analytics/by-model?from=&to=`
- `GET /analytics/by-client?from=&to=`
- `GET /analytics/provider-quota-lines?from=&to=&window=5h|7d&reset_on_429=&reset_on_refresh=`
- `GET /analytics/token-prices?from=&to=`
- `GET /analytics/config`
- `PUT /analytics/config`

语义要求：

- `from/to` 控制展示和统计跨度。
- `window=5h|7d` 只控制 provider 配额窗口类型，不能被当成图表横轴跨度。
- 所有 analytics endpoint 同时返回 503 时，前端显示“尚未启用用量统计”。
- 单个 endpoint 失败时，前端尽量保留其它成功模块的数据。

## 已废弃或当前不展示

以下内容是历史设计或曾经的中间实现，不属于当前 Analytics 页面范围：

- 配额封顶热力图。
- 最近 429 事件列表。
- 配额快照列表。
- 前端手动价格解算按钮。
- 前端线性方程解 token 单价 UI。
- 使用官方 used percent 的配额图时，不再显示 `100 - used_percent` 的剩余额度曲线。
- 离散展示范围按钮 `1h / 5h / 24h / 3d / 7d`。
