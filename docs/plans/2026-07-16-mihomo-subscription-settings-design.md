# Mihomo 订阅页面配置

本功能的已确认需求、技术设计、实施步骤和验证门槛记录在 Trellis 任务：

- `.trellis/tasks/07-16-mihomo-subscription-settings/prd.md`
- `.trellis/tasks/07-16-mihomo-subscription-settings/design.md`
- `.trellis/tasks/07-16-mihomo-subscription-settings/implement.md`

核心决策：订阅 URL 加密存储；后端生成受控 Mihomo 配置；保存后通过私有 Controller 立即热重载；失败时回滚上一份可用配置；系统设置页面不提供任意 YAML 编辑和手动节点切换。
