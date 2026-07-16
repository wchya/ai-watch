# 多 Provider 场景对比设计

## 目标

使用同一个已启用的合成测试场景，对 2–10 个同 CLI Provider 发起独立的一次测活，并在一个批次中比较断言结果、耗时、错误类型和关联请求。

## 数据流

1. 前端提交场景 ID、CLI 和 Provider ID 列表。
2. 服务端验证场景状态、CLI 兼容性、数量上限和重复 Provider。
3. 服务端创建稳定批次 ID，为每个 Provider 启动独立 `probe_once` Job，触发来源为 `scenario_comparison`。
4. `scenario_comparison_started` 结构化事件只保存批次 ID、场景摘要、Provider ID、Job ID 和启动错误。
5. 查询批次时优先读取活动 Job；Job 已回收时从对应 `request_end` 事实恢复状态、Request ID、耗时、错误类型和脱敏响应摘要。
6. 前端轮询批次，完成后按通过、耗时和 Provider 名称排序，并允许打开统一请求详情。

## 安全与幂等

- Prompt 和期望文本不复制到批次事件。
- 不保存 API Key、代理凭证或完整 CLI 输出。
- POST 继续使用全局 `Idempotency-Key`，重复提交返回同一批次响应。
- 每个 Provider 在一个批次中只允许出现一次。
- 对比不会修改 Provider、计划任务、故障切换组或宿主机配置。

## 验收

- 只能选择 2–10 个同 CLI、可用于该场景的 Provider。
- 每个 Provider 产生独立 Job 和 Request ID。
- 页面展示通过状态、耗时、错误类型和请求详情入口。
- 页面刷新或 Job 从活动内存回收后仍可通过事件恢复批次结果。
- 全量 Go、前端构建、E2E 和容器冒烟通过。
