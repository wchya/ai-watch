# AI Watch 项目进度与剩余验收

**更新日期：** 2026-07-20
**状态：** 本轮深度产品与交互优化已完成；功能、状态治理、无障碍、响应式、三主题、样式收敛与全量验证均有直接证据

## 已实现并有自动化证据

- Go 后端、React/Vite 前端、Docker Compose 基础结构。
- Provider 扫描、手填 Provider 加密配置、CC Switch 启动同步。
- Redis Store、启动预热、诊断与加密配置；通用 Redis 管理 API 和页面已退役。
- 测活、保活、计划任务、批量任务、停止与通知聚合。
- 任务事件 Redis 缓存、24 小时 TTL、条数与容量限制。
- 测活请求事件：`request_start`、`request_log`、`request_end`，包含唯一请求 ID、目标、代理模式、耗时、退出码和分类结果。
- 深海终端、石墨信号、极昼控制台三套主题，设置保存到 Redis。
- 七个一级领域入口与小屏供应商操作区适配。
- `go test ./...`、前端生产构建、`docker compose config --quiet` 和 `docker compose build` 已通过。
- Compose 已实际启动；Redis、Mihomo、AI Watch 均为 healthy，健康接口返回 `status: ok`，服务仅绑定 `127.0.0.1:8787`。
- 请求日志已记录 DNS 多地址/错误、可信连接 IP、手动/批量/计划/恢复来源、CLI 版本、安全请求摘要、返回摘要和下一次运行时间。
- 事件记录与请求日志已拆分；普通事件支持列表/时间线，请求按 `requestId` 聚合并展开详情。
- 全局主题已重构为语义 Token，顶栏可直接切换；深海、石墨、极昼主题覆盖领域导航、计划任务和供应商列表。
- 启动时会删除旧版本长期事件中的 `request_log`；运行验证删除 2 条历史记录，API 查询为 0。
- 实际 Codex 测活成功，日志中的 Prompt 为 `[REDACTED]`；Redis 只读扫描已知 Prompt 原文匹配数为 0。
- Playwright 浏览器验收已加入，稳定 Mock 后端 API，不依赖 Docker、Redis 或真实 CLI。
- 三套主题的顶栏切换、即时应用和设置持久化请求已有真实 Chromium 证据。
- 供应商页面已覆盖 320、375、768、1024 和 1440px，验证无横向溢出、编辑操作可见且抽屉不越界。
- 新增 Provider 可靠性趋势与对比：支持 24 小时、7 天和 30 天时间窗，展示成功率、平均/P95 延迟、失败分布、连续失败和异常时段。
- 可靠性统计只读取脱敏 `request_end` 事件；主动停止不进入成功率和延迟统计，保留策略不足时明确提示部分覆盖。
- Playwright 已覆盖可靠性页面的 Provider 对比、时间窗切换和 320px 移动端布局。
- 新增 Provider 可靠性告警设置、滚动 24 小时评估、冷却抑制、发送失败记录和连续成功恢复通知。
- 修复计划任务遇到未知 `lastStatus` 时访问 `undefined.tone` 导致整页黑屏的问题。
- 修复测活终端遗漏 `request_start`、`request_log`、`request_end` SSE 监听的问题，恢复脱敏命令、实时输出和返回摘要。
- Diagnostic Bus 和测活终端已迁移到语义主题 Token，极昼主题不再显示固定暗色面板。
- 侧边栏由 15 个入口收拢为 7 个领域中心；路由注册表统一 path、标题和导航关系，支持直接访问、刷新以及浏览器前进/后退。
- 新增合成测试场景 CRUD，任务和计划可引用 `scenarioId`；同一场景支持多 Provider 批量运行和终态对比。
- 新增 Provider 建议式故障切换组；连续失败后使用同一场景验证备用线路，只生成建议，不修改宿主机或计划任务配置。
- 新增事故中心；相同 Provider/Provider Group 的失败只形成一条开放事故，支持错误聚合、关联请求、确认、静默、备注、关闭、重开和连续成功自动恢复。
- SQLite 迁移版本已更新至 v18，启动时删除退役的供应商示例表与 Redis 集合。
- `App.tsx` 已降至 194 行，`server.go` 已降至 213 行；前端功能组件和后端 Handler 均按职责拆分。
- Playwright 浏览器验收已从历史 35 条扩展为 41 条并全部通过；新增跨领域触控、字号、多宽度水平溢出、120 条分页、三主题状态对比度和 reduced-motion 验收。
- 2026-07-15 重建容器后，Redis、Mihomo、AI Watch 均为 healthy；`/api/health` 返回 `status: ok`。空闲内存约为 AI Watch 18.5 MiB，三容器合计约 95 MiB。
- 2026-07-20 重建中英文 README，接入 6 张极昼主题页面截图、工作流图和系统架构图；截图生成测试、相对链接、draw.io 结构、前端生产构建、Go 全量测试与 Compose 配置校验均通过。
- 2026-07-20 完成首批深度交互优化：恢复 7 条回归，规范对比深链接，拆分 12 个领域异步 chunk，主入口 JS 从约 479 kB 降至约 289 kB，并补充 skip link、路由焦点和关键触控目标。
- Maintenance、SLO、Notification Routing 已在写操作成功后立即合并服务端返回实体；Reliability 处置会即时禁用相关计划，后台刷新只承担事实校准。
- Comparison、Reliability、Maintenance、SLO、Notification Routing 已通过请求版本号丢弃过期响应；四个主要处置视图的成功提示具有 live 语义并在 4 秒后自动清理。
- 请求治理已进一步覆盖 Dashboard、Action Center、Schedules、Request Detail、Incidents/Postmortem、Failover、Test Scenarios、Provider Config 与 Diagnostics；轮询页面隐藏时暂停，恢复后单次校准。
- Reliability 趋势新增文字结论与键盘可展开数据表，移除 hover-only 数据依赖；Deep/Graphite muted 与 Arctic muted/info 语义 Token 已提升至 AA 对比度。
- 原 1423 行 `styles.css` 已按原级联顺序拆成 Base/Shell、Data Workspaces、Theme System、Domains、Interaction/Accessibility 五层；生产构建体积保持稳定，后续只需继续清理重复覆盖和固定色。
- 375px 跨领域触控审计已通过，覆盖可见按钮、链接、`summary` 和自定义选择器的 44×44px 下限；13 路由字号审计也已通过，正文 `p` 不低于 12px，`small`/`dt` 不低于 11px。
- 13 个主路由在 320、768、1024、1440px 下均无水平溢出；375px 由触控审计同步覆盖。
- 新增共享 `useLatestRequest`，首批迁移 Diagnostics、Request Detail、Provider Config；新增共享 `ListPagination`，Schedules 与 Comparison History 每页只渲染 50 条并提供可访问页码摘要。
- 新增 `--control-border`、`--surface-input` 和导航状态 Token，三主题文字/状态达到 4.5:1、真实输入边界达到 3:1；Select 的 Escape 不再连带关闭父抽屉。

## 尚需完成或补强

### 前端最终视觉验收

- [x] 补主题切换、领域路由和小屏供应商操作区的浏览器测试。
- [x] 自动检查 320px、手机、平板和桌面宽度。
- [x] 人工抽查 README 六张关键截图及两张流程/架构图中的字体渲染、标签重叠和信息密度。
- [x] 极昼主题高风险固定暗色已迁移到语义 Token，六张 README 截图未发现暗色残留；剩余工作仅限可证明级联等价的低风险旧规则。

### 自动化回归恢复

- [x] 2026-07-20 修复确认弹层测试漂移、维护窗口固定日期、深链接与安全配置语义类问题。
- [x] 回归恢复阶段达到 Playwright 35/35；加入触控、字号、分页、多宽度、三主题状态和 reduced-motion 验收后，完整套件为 41/41。

### 持续补强

- [x] 13 个主路由的自动字号审计全部通过；Reliability、Incidents、Comparison、Diagnostics 的关联正文和技术标签也已同步提升。
- [x] 375px 跨领域主操作满足 44px 下限，320px、768px、1024px、1440px 跨领域主流程无水平溢出。
- [x] 三主题语义状态、真实控件边界、焦点与禁用状态已自动复核；故障切换、可靠性、对比历史等极昼截图已人工抽查。
- [x] 完成原 1423 行 `styles.css` 的五层结构拆分并保持导入顺序和构建结果稳定。
- [x] 完成低风险遗留覆盖与固定色收敛：Events/Schedules 源规则已 Token 化，后置迁移覆盖层已删除；品牌、终端窗口点、JSON 语法色和 Select Portal 适配色按语义保留。
- [x] Schedules 与最多 500 条的 Comparison History 已增加每页 50 条分页；Events 与计划日志抽屉保留既有服务端分页。
- [x] 六张 PNG 已人工复核，变化来自有效视觉更新，未发现标签重叠、截断、暗色残留或异常密度。

### 非阻塞后续

- 继续收集真实 Provider 故障样本，校准事故严重程度和恢复阈值的默认值；该项依赖运行期数据，不阻塞当前 Trellis 任务收尾。
- 将其余已具备局部版本号保护的读取视图逐步迁移到共享 `useLatestRequest`，保持领域行为不变。

已补充的容器证据：

- 单次测活结束后 `/run/ai-watch/jobs` 无残留文件或任务目录。
- 重启 AI Watch 后，同一任务的 `request_start`、`request_end` 和脱敏返回摘要仍可回放。
- 单元测试覆盖 API Key、自定义代理 URL、钉钉 Webhook 和错误输出脱敏；运行 Redis 扫描确认默认 Prompt 原文匹配数为 0。

## 当前活动任务

- Trellis：`.trellis/tasks/07-20-deep-product-interaction-optimization`
- 状态：验收完成，进入 Trellis 收尾归档
- 已完成阶段：回归恢复、交互底座、状态一致性、请求治理、图表无障碍、字号/触控/多宽度、三主题对比度、reduced-motion、截图复核、CSS 结构拆分、共享 hook 首批迁移和大列表分页。
- 当前阶段：计划内实现和复验已完成；41 条 Playwright、生产构建、Go、Compose、Trellis context 和差异检查均通过。
- 收尾结论：六张 README 截图已人工复核，无重叠、截断、暗色泄漏或异常密度；任务可进入归档。
- 后续队列：扩大共享 hook 迁移范围和基于真实 Provider 样本校准事故阈值，均不阻塞当前任务。

## 完成标准

当前 Trellis 任务的计划内完成标准已经满足。真实 Provider 样本校准和共享 hook 扩面属于持续优化，不作为本轮完成条件。
