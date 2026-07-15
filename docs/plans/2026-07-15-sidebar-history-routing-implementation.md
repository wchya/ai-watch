# AI Watch 侧边栏 History 路由实施计划

**设计：** `docs/plans/2026-07-15-sidebar-history-routing-design.md`

1. 建立 View 与 path 的双向映射和未知路径规范化。
2. 使用当前 path 初始化 App view。
3. 封装侧栏导航，统一 `pushState`、移动菜单关闭和重复点击处理。
4. 监听 `popstate` 并同步页面标题。
5. 增加 Playwright 点击、直接访问、返回、前进和未知路径测试。
6. 更新 README 并运行全量门禁。
