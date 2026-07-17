# CC Switch Provider 代理策略覆盖设计

## 目标

为 CC Switch Provider 增加 AI Watch 本地维护的 `default/direct` 代理策略覆盖，并修复 Mihomo 更新订阅后可能继续使用旧节点缓存的问题。

## 方案

覆盖配置不写回 CC Switch 数据库或 Redis Provider 快照，而是以 `(cli, providerId)` 为键独立保存。Resolver 读取 CC Switch Provider 时合并覆盖；没有记录时保持现有 `default` 行为。这样启动同步可以继续原子替换 Provider 快照，而不会清除运维人员选择的网络路径。

前端在 CC Switch Provider 行提供“默认代理/直连”切换。当前 CLI 配置仍然只读，手填 Provider 继续使用已有安全配置入口。切换失败时恢复原显示并反馈错误。

所有任务入口复用统一 Resolver 和 Runner，因此覆盖会自然应用到测活、保活、场景、计划及故障切换任务。Runner 的既有语义保持不变：`default` 使用服务默认代理，`direct` 清除路由代理环境变量。

Mihomo 订阅运行配置不再无条件复用固定的 `subscription.yaml` 缓存。新订阅使用不泄露 URL 的新缓存标识或等价的安全刷新流程；节点加载和外部连通验证全部成功后才提交新订阅。失败时恢复旧运行配置及旧节点缓存。

## 首版限制

- CC Switch Provider 不支持自定义代理 URL。
- 不为当前 CLI 配置增加覆盖。
- Provider 暂时消失时不删除覆盖记录。
- 不提供订阅节点的手动选择或按 Provider 绑定具体节点。

## 验证

验证重点包括策略默认值、独立持久化、同步后保留、Provider 隔离、API 校验、前端失败回滚、Runner 直连环境，以及 Mihomo 缓存刷新和失败恢复。真实容器验收要求至少两个 CC Switch Provider 分别产生 `proxy=default` 与 `proxy=direct` 的请求事件。
