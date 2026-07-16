# 可靠性建议一键处置设计

## 目标

把可靠性卡片从只读建议升级为人工触发的安全处置入口：立即复测、验证备用线路、查看相关计划和暂停异常计划。

## 服务端关联

- 客户端只提交 CLI、Provider ID 和动作。
- 服务端从 ProviderGroup 与 ScheduleStore 重新计算关联关系。
- 相关计划包括直接引用 Provider 的计划，以及绑定包含该 Provider 的 ProviderGroup 的计划。
- 备用验证只允许对启用且主线路匹配的 ProviderGroup 执行。

## 动作

- `retest`：启动一次独立测活，触发来源为 `reliability_remediation`。
- `validate_backup`：使用组内相同合成场景验证第一优先级备用 Provider。
- `pause_schedules`：幂等停用所有相关且当前启用的计划。
- 查看计划为纯路由操作，不修改数据。

## 安全与可观测性

- 所有写操作使用全局 Idempotency-Key。
- 不修改宿主机 Codex、Claude 或 CC Switch 配置。
- 每次处置记录结构化事件，仅包含 Provider、Group、Schedule、Job 和动作摘要。
- 前端逐卡显示 loading 和错误；暂停计划需要二次确认。
- 操作后立即刷新，并在 2.5 秒后再次刷新服务端状态。

## 验收

- 服务端拒绝无关联组的备用验证。
- 复测返回 Job，备用验证返回验证 Job，暂停返回实际暂停的计划。
- 重复暂停不重复改变计划。
- 前端错误不影响其他建议卡。
