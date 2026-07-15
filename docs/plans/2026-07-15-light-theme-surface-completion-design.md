# AI Watch 极昼主题表面补全设计

**日期：** 2026-07-15  
**状态：** 已确认

## 范围

1. 钉钉机器人配置卡全部迁移到全局语义 Token。
2. `html`、`body`、`#root` 和应用容器建立一致的主题背景与最小高度链路。
3. 页面内容结束后的滚动/回弹区域跟随当前主题，不再暴露根节点暗色背景。

## 实现

- 钉钉状态、说明、输入框、徽章、消息、密钥说明和操作区使用 `surface/text/line/status` Token。
- 极昼主题为页面根节点提供浅色背景；深海和石墨主题保持各自背景。
- 页面主区域至少覆盖一个完整视口，短页面不露出 body 背景。

## 验收

- 极昼主题下钉钉配置卡不存在固定暗色表面。
- 所有一级页面滚动到底部后，根节点和页面底色一致。
- 320px 与桌面宽度无新增横向溢出。
# Terminal close interaction addendum

The terminal detail view exposes two equivalent, accessible exit paths: the connection/replay indicator beside the window controls becomes an explicit close-and-return button, while the top-right X uses the danger color token so it reads as the primary dismissal affordance. Both controls carry distinct accessible names, and Escape closes the dialog. Closing the terminal never stops a running job.
