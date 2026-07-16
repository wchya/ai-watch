# 通知路由中心设计

## 目标

把当前单一全局钉钉机器人扩展为可复用的加密渠道库，并按消息类型选择专用渠道。专用渠道不可用或发送失败时回退全局默认渠道。

## 模型

通知渠道包含 ID、名称、说明、类型、启用状态、Webhook 密文、掩码、创建和更新时间。首版渠道类型仅支持钉钉。

路由配置包含六类消息：

- `incident_opened`：新事故；
- `incident_recovered`：事故恢复；
- `reliability_alert`：可靠性告警；
- `reliability_recovered`：可靠性恢复；
- `reliability_digest`：定时摘要；
- `job_notification`：任务通知。

每类消息保存一个渠道 ID；空值表示使用全局默认机器人。

## 路由语义

发送时先读取类型对应的专用渠道：

1. 渠道存在、启用且可解密时，优先发送到该渠道；
2. 专用渠道未配置、停用、解密失败或发送失败时，尝试全局默认机器人；
3. 专用渠道与全局默认 Webhook 相同时不重复发送；
4. 两个渠道都不可用时返回失败，但不改变任务、事故或告警状态；
5. 写结构化事件记录消息类型、目标渠道 ID、结果、回退原因和是否回退，不记录 Webhook。

## API

- `GET /api/notification-channels`：列出渠道，仅返回掩码；
- `POST /api/notification-channels`：创建渠道；
- `PUT /api/notification-channels/:id`：编辑渠道，Webhook 留空表示保留；
- `DELETE /api/notification-channels/:id`：删除渠道并把引用切回默认；
- `POST /api/notification-channels/:id/test`：发送测试消息；
- `GET /api/notification-routes`：读取六类路由；
- `PUT /api/notification-routes`：整体保存路由。

Webhook 使用现有 AES-GCM 密钥加密。写操作使用全局幂等和严格字段校验。

## 页面

新增 `/notification-routing` 与侧边栏入口。页面上半区是六类消息的“分流轨道”，下半区是渠道库。支持创建、编辑、测试、启停和删除渠道；操作提供确认、loading、立即刷新和 2.5 秒延迟刷新。

页面沿用语义主题变量，极昼主题不出现固定暗色，移动端将轨道和渠道卡改为单列。

## 验证

- SQLite 与 Redis 覆盖渠道密文、路由和删除引用；
- 路由服务覆盖专用成功、失败回退、停用回退、同 Webhook 去重和全部失败；
- 事故、可靠性告警、摘要和任务通知接入对应类型；
- API 覆盖 CRUD、测试、掩码、不回显密文和审计；
- Playwright 覆盖路由保存、渠道创建/测试/删除、极昼主题和移动端；
- 全量测试、镜像构建与运行时只读冒烟通过后部署。
