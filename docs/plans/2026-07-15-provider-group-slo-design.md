# Provider Group SLO 与错误预算设计

## 目标

为 Provider Group 提供滚动窗口 SLO。用户可以设置目标成功率、统计窗口和最小样本数，并查看允许失败数、实际失败数、预算剩余、燃烧速率与风险状态。

首版只治理 Provider Group，不为单个 Provider 建立重复配置。统计复用脱敏 `request_end` 事件以及现有 Provider Group、计划任务和维护窗口数据。

## 数据与配置

每个 Provider Group 增加可选 SLO 配置：

- `sloEnabled`：是否启用；
- `sloTargetPercent`：目标成功率，范围 90% 至 99.999%；
- `sloWindow`：`24h`、`7d` 或 `30d`；
- `sloMinimumSamples`：最小有效样本数，范围 1 至 100000。

配置继续随 Provider Group 存储，不新增独立配置表。创建、修改、暂停和恢复写入结构化审计事件。

## 统计口径

统计窗口为以当前时间为终点的滚动窗口。只读取绑定该 Provider Group 的计划任务产生的脱敏 `request_end`：

- `success` 计为成功；
- `stopped`、`running` 不进入样本；
- 其他终态计为失败；
- 维护窗口生效期间产生的请求保留事实，但从 SLO 样本中排除；
- 没有 Group 绑定信息的历史请求不做推断，避免错误归属。

允许失败预算按 `样本数 × (1 - 目标成功率)` 计算。预算消耗比例为实际失败数除以允许失败预算；当目标和样本组合导致允许失败预算小于 1 时仍保留小数计算，界面同时展示理论预算与实际失败数。

燃烧速率使用“当前失败率 / 允许失败率”。1× 表示当前失败率恰好等于目标允许值；2× 以上为快速消耗，10× 以上为严重风险。无失败时为 0×。

## 状态

- `disabled`：未启用；
- `insufficient`：有效样本少于最小样本数；
- `healthy`：预算消耗低于 50%，燃烧速率低于 2×；
- `burning`：预算消耗达到 50% 或燃烧速率达到 2×；
- `critical`：预算消耗达到 90% 或燃烧速率达到 10×；
- `exhausted`：预算剩余小于等于 0。

状态判断按 `exhausted → critical → burning → healthy` 的优先级执行。

## API 与页面

新增：

- `GET /api/slos`：返回全部 Provider Group 的配置和当前指标；
- `PUT /api/slos/:groupId`：创建或更新 SLO；
- `POST /api/slos/:groupId/pause`：暂停 SLO；
- `POST /api/slos/:groupId/resume`：恢复 SLO。

新增 `/slos` 页面和侧边栏入口。页面提供状态汇总、状态筛选、每组预算卡片、配置弹窗、暂停和恢复操作，并可跳转到可靠性、事故和维护窗口页面。

所有写操作遵循全局幂等键，按钮提供确认、loading、立即刷新和 2.5 秒延迟刷新。页面使用现有语义主题变量，覆盖极昼主题和移动端，不引入固定暗色背景。

## 错误处理与安全边界

- 无效目标、窗口或样本数返回 400；
- Group 不存在返回 404；
- 事件读取失败返回明确错误，不返回看似正常的零值；
- SLO 只做观测和治理，不自动切换 Provider、不停止任务、不发送新通知；
- API 和事件不包含 Prompt、凭证、Webhook、代理明文或 CLI 原始输出。

## 验证

- 单元测试覆盖成功率、错误预算、燃烧速率、状态边界、维护窗口排除和样本不足；
- API 测试覆盖配置、暂停、恢复、校验、审计事件和不存在的 Group；
- Playwright 覆盖 `/slos` 深链接、配置操作、loading、2.5 秒刷新、白色主题和移动端无溢出；
- 全量 Go、前端构建、E2E、Docker 构建与运行时只读冒烟通过后部署。
