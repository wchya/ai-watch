# AI Watch 侧边栏 History 路由设计

**日期：** 2026-07-15  
**状态：** 已确认

## 目标

让每个侧边栏页面拥有稳定 path，支持地址栏复制、直接访问以及浏览器前进/后退，同时保持当前单页应用和后端 SPA fallback。

## 路径映射

- `/`：总览；
- `/providers`：供应商配置；
- `/reliability`：可靠性；
- `/schedules`：计划任务；
- `/events`：事件记录；
- `/redis`：Redis 管理；
- `/diagnostics`：系统诊断；
- `/settings`：设置与通知。

## 方案

使用浏览器原生 History API，不引入路由依赖。

- 首次渲染根据 `window.location.pathname` 初始化页面；
- 侧栏点击通过 `history.pushState` 更新地址和页面；
- `popstate` 恢复前进/后退对应页面；
- 当前页面重复点击不增加历史记录；
- 未知路径通过 `replaceState` 规范化为 `/`；
- query 和 hash 不参与页面识别，但页面导航会清除旧 query/hash。

## 边界

本轮只覆盖侧边栏一级页面。任务详情、请求详情、弹窗和筛选状态仍由页面内部管理，后续可在需要分享深链接时独立设计。

## 可访问性

- 导航按钮继续使用 `aria-current="page"`；
- 浏览器返回后焦点不强制跳转，避免打断键盘用户；
- 移动侧栏导航后自动关闭；
- 页面标题同步更新为 `AI Watch · <页面名称>`。

## 测试

- 点击每个导航项更新 path；
- 当前页面重复点击不新增 history；
- 浏览器返回和前进恢复页面；
- 直接访问 `/schedules` 等路径正确渲染；
- 未知路径替换为 `/`；
- 移动端导航后侧栏关闭；
- 生产构建和后端 SPA fallback 保持通过。
