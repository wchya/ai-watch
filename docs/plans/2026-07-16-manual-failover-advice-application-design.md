# 人工采用故障切换建议设计

## 目标

建议模式在备用 Provider 使用同一合成场景验证成功后，允许用户在 AI Watch 内人工采用该建议。操作只改变当前 Provider Group 的活跃成员，并重新调度显式绑定该组的计划；不修改 Codex、Claude、CC Switch 或宿主机配置。

## 安全条件

- Provider Group 必须启用且处于建议模式。
- 建议必须仍为 `open`，并包含验证成功产生的 `validationJobId`、`validationRequestId` 和 `suggestedProviderId`。
- 建议目标必须仍属于当前组的备用成员，且不能等于当前活跃成员。
- 维护窗口内拒绝采用建议。
- 请求携带期望的建议更新时间与目标 Provider，防止页面旧快照覆盖新状态。
- 重复提交相同建议保持幂等，不重复重启计划或写入切换事件。

## API

`POST /api/provider-groups/<group-id>/apply-advice`

请求包含：

- `suggestedProviderId`
- `adviceUpdatedAt`
- `confirmGroupId`

响应包含切换前后 Provider、受影响计划数量、验证 Request ID、是否实际发生切换以及 `hostConfigChanged:false`。

## 执行与审计

Manager 在单组互斥锁内重新读取 Provider Group，复核全部安全条件，更新 `activeProviderId` 与 `lastSwitchedAt`，再停止并唤醒绑定该组的计划。

成功后记录：

- `provider_group_manual_switch`
- group ID、previous/active Provider ID
- validation Job/Request ID
- affected schedule count
- `hostConfigChanged:false`

开放事故存在时，同步追加 `manual_switch` 时间线条目。切换后沿用现有主线路恢复探测与连续成功回切机制。

## 界面

建议卡在 `open` 状态显示“采用建议”按钮。点击后打开二次确认弹窗，展示组名、目标 Provider、验证 Request ID、受影响计划数量和宿主配置不变说明。确认期间禁用重复操作；成功后刷新卡片并显示切换结果。

## 验收

- 未验证、过期、成员变化、维护窗口和确认组名错误均拒绝。
- 合法建议只切换绑定组的计划。
- 重复请求幂等，不产生重复审计事件。
- 事件和事故时间线包含完整脱敏事实。
- 前后端、E2E、容器健康和内存验证通过。
