# AI Watch 深度产品与交互优化实施计划

**更新日期：** 2026-07-20
**状态：** 已完成；功能、交互、无障碍、响应式、主题状态、工程收敛与全量验证均已通过

## 第一批：回归与交互底座

- [x] 将 5 处原生 dialog 测试迁移到 `ConfirmHost` 的 `alertdialog`。
- [x] 把维护窗口初始 Mock 时间改为相对当前时间。
- [x] 对比重跑使用规范化详情 URL，并覆盖后退行为。
- [x] 安全配置卡拆分共享/领域语义类，修复极昼主题选择器歧义。
- [x] 领域视图使用 `React.lazy` + `Suspense` 拆包，增加可访问加载状态。
- [x] 增加 skip link、主内容焦点管理和移动菜单遮罩按钮语义。
- [x] 为错误横幅与筛选状态补全 ARIA。

## 第二批：状态一致性

- [x] Maintenance、SLO、Notification Routing 写操作使用服务端返回实体立即更新，并保留延迟后台校准。
- [x] Reliability 处置后立即禁用相关计划的本地状态，避免等待下一次完整刷新。
- [x] Comparison、Reliability、Maintenance、SLO、Notification Routing 使用请求版本号丢弃过期读取响应。
- [x] Reliability、Maintenance、SLO、Notification Routing 的成功提示使用 `role="status"` 并在 4 秒后自动消失；错误状态保留 `role="alert"` 与重试动作。
- [x] 把请求版本号扩展到首页聚合、请求详情、事故/复盘、故障切换、测试场景、供应商配置、诊断和计划日志等高风险读取视图。
- [x] 统一 Comparison、Failover、Scenario、Schedule、Dashboard 等轮询的页面隐藏/恢复策略，恢复可见时只触发一次校准刷新。

## 第三批：响应式和视觉精修

- [x] 为核心处置、筛选和移动导航补充 44px 触控目标，并加入覆盖 13 个主路由的 375px E2E 审计；定向测试已通过。
- [x] 通过跨 13 个主路由的 `p >= 12px`、`small/dt >= 11px` 自动审计，并同步提升 Reliability、Incidents、Comparison、Diagnostics 中未被元素类型审计覆盖的关联正文和技术标签。
- [x] 13 个主路由在 320px、768px、1024px、1440px 下无水平溢出；375px 可见主操作同时满足 44×44px 命中区。
- [x] 验收三主题的文字、边框、焦点、禁用和状态对比度；语义文字/状态达到 4.5:1，真实输入框边界达到 3:1，键盘焦点与禁用状态通过浏览器测试。
- [x] Reliability 趋势图增加可见文字结论、键盘可展开数据表和表格语义，移除对 hover `title` 的依赖。
- [x] 完成多宽度和 reduced-motion 验收，并人工复核六张极昼 README 截图；未发现标签重叠、截断、暗色残留或异常密度。

## 第四批：工程减重

- [x] 按原级联顺序将 1423 行 `styles.css` 拆为 `base-shell.css`、`data-workspaces.css`、`theme-system.css`、`domains.css` 和 `interaction-accessibility.css`，构建产物体积保持稳定。
- [x] 清理五个样式层中的低风险遗留覆盖与固定色；Events/Schedules 已直接使用语义 Token，对应后置迁移覆盖已删除，品牌与 Portal 等明确例外保留。
- [x] 抽取共享 `useLatestRequest`，提供请求开始、仅接收最新版本和卸载失效能力；Diagnostics、Request Detail、Provider Config 已完成首批迁移。
- [x] 为 Schedules 和 Comparison History 增加每页 50 条的共享客户端分页与 `aria-live` 页码摘要；Events 和计划日志抽屉继续使用既有服务端分页。
- [x] 更新项目进度、公开设计/实施计划和 Trellis 任务记录；六张 README 截图已重新生成并复核，无需额外改写产品说明。

## 当前验证快照

- [x] `npm run build`：生成 12 个领域异步 chunk，主入口约 289.72 kB / gzip 88.44 kB。
- [x] 完整 `npm run test:e2e`：41/41。
- [x] 新增 375px 跨领域 44px 触控审计：1/1。
- [x] 新增跨领域字号审计：1/1，13 个主路由全部通过。
- [x] 新增 120 条计划/对比历史分页审计：1/1，每页稳定渲染 50 条。
- [x] 新增 13 路由 × 4 档宽度水平溢出审计：1/1。
- [x] 新增三主题文字/状态/焦点/禁用/表单边界对比度审计：1/1。
- [x] 新增 reduced-motion 弹层与控件动效审计：1/1。
- [x] `go test ./...`。
- [x] `docker compose config --quiet`。
- [x] `git diff --check`。
- [x] CSS 收敛后的完整复验：41/41，六张 README 截图人工复核无重叠、截断或暗色泄漏。

## 后续持续优化队列

1. 将其余局部版本号渐进迁移到 `useLatestRequest`，每批保持领域行为与轮询语义不变。
2. 评估把控件边界、嵌套 Escape 与长时响应式测试约束沉淀到 Trellis 前端规范。
3. 基于真实 Provider 故障样本继续校准事故严重程度和恢复阈值。

## 收尾判定

- **阻塞项：** 无。计划内验收项均已有当前代码和全量测试证据。
- **非阻塞项：** 其余视图已具备局部请求版本保护；共享 hook 扩面只改善复用，不改变当前正确性。
- **不在本任务内：** 真实 Provider 故障样本积累与事故阈值校准需要运行期数据，继续作为产品运营型长期工作。
