# AI Watch 深度产品与交互优化设计

**日期：** 2026-07-20
**状态：** 已完成

## 目标

围绕“配置 Provider → 验证 → 自动化 → 查看异常 → 处置”主链路，恢复交互可信度并降低使用成本。优化不重做品牌，而是在现有“实时信号控制台”上强化状态连续性、操作反馈、键盘/触控、响应式密度和首屏性能。

## 当前证据

- 审计基线的完整 Playwright 为 26/33；其中 5 条失败来自测试仍监听原生 `dialog`，未操作现有 `ConfirmHost` 自定义确认弹层。回归恢复后先达到 35/35，随后加入触控、字号、分页、多宽度、三主题状态和 reduced-motion 验收，当前为 41/41。
- 维护窗口 Mock 使用 2026-07-16 固定截止时间，在 2026-07-20 已自然变成“已结束”。
- Mihomo 订阅卡复用 `.dingtalk-secure-config`，造成主题断言命中两个元素，也暴露组件语义耦合。
- 对比重跑写入旧式 `/comparisons/:id`，而产品规范路径为 `/validation/comparisons/:id`。
- 审计基线的生产构建主 JS 约 479 kB；本轮路由级拆包后主入口约 289 kB，并生成 12 个领域视图 chunk。
- 原 1423 行 `styles.css` 已按既有级联顺序拆为 `base-shell.css`、`data-workspaces.css`、`theme-system.css`、`domains.css` 和 `interaction-accessibility.css`；Events 与 Schedules 的真实源规则已迁移到语义 Token，后置迁移覆盖层已删除，剩余硬编码仅保留品牌、终端窗口、JSON 语法和 Portal 适配等有明确语义的例外。
- Playwright 已增加覆盖 13 个主路由的移动端触控、正文/辅助说明字号和多宽度水平溢出审计。44px 主操作与字号下限均已通过；320、768、1024、1440px 的跨领域主流程也无水平溢出。
- Maintenance、SLO 与 Notification Routing 已在写操作成功后立即合并服务端返回实体，Reliability 会同步禁用相关计划；这些视图保留延迟后台刷新作为事实校准来源。
- 请求版本治理已扩展到 Comparison、Reliability、Maintenance、SLO、Notification Routing、Dashboard、Action Center、Schedules、Request Detail、Incidents/Postmortem、Failover、Test Scenarios、Provider Config 与 Diagnostics；主要轮询在页面隐藏时暂停，恢复可见后执行一次校准。共享 `useLatestRequest` 已抽取，并首批迁移 Diagnostics、Request Detail 与 Provider Config，统一请求开始、最新版本判断和卸载失效。
- Reliability、Maintenance、SLO 与 Notification Routing 的成功提示已使用 `role="status"`，并在 4 秒后自动清理；错误横幅保留 `role="alert"` 与重试入口。

## 设计方向

延续深海终端、石墨信号、极昼控制台三套主题和青色信号轨道。视觉风险只放在“状态连续性”这一处：路由加载、操作进行中、成功校准和异常恢复使用同一信号语言，其他表面保持克制，避免通用 AI 紫色、装饰性渐变和无意义动画。

### 交互原则

1. 操作名称、确认按钮、busy 文案和成功提示使用同一动词。
2. 服务端返回新实体时立即合并到页面，随后静默刷新校准，不让用户等待二次轮询。
3. 破坏性操作统一使用 `ConfirmHost`；测试通过 `alertdialog` 操作真实交互，不监听浏览器原生 dialog。
4. 错误说明必须包含失败对象和恢复动作；已有内容读取失败时保留旧数据并标识其可能过期。
5. 路由切换保持规范 URL、浏览器历史和焦点连续性。

## 优先级

### P0：恢复可信交互与自动化

- 修复 7 条 E2E：迁移确认弹层操作、改用相对维护时间、收敛安全配置选择器。
- 对比批次只生成 `/validation/comparisons/:id`，并验证重跑后的前进/后退。
- 为错误横幅补 `role="alert"`，为筛选按钮补 `aria-pressed`。

### P1：首屏性能与导航可访问性

- 用 `React.lazy` / `Suspense` 按领域拆分 Provider、验证、自动化、稳定性、事件详情和设置子页。
- 提供可见的路由加载状态，遵守 reduced motion，不使用阻塞式全屏动画。
- 增加“跳到主要内容”链接；路由变化后把焦点移到主内容，移动菜单遮罩使用按钮语义。
- 统一安全配置共享类 `.secure-config-card`，保留 `.dingtalk-config-card` 与 `.proxy-subscription-config` 领域钩子。

### P1：状态同步与请求治理

- Maintenance/SLO/通知渠道使用 API 返回实体就地更新，后台刷新只做校准。
- Reliability 处置完成后立即更新相关计划状态和动作可用性。
- 为频繁切换时间窗、筛选和详情加载增加 AbortController 或序列号，丢弃过期响应。
- 页面隐藏时暂停轮询，恢复可见时只触发一次合并刷新。

### P2：响应式与视觉精修

- 领域正文/说明不低于 12px，移动端表单正文不低于 16px；技术标签可使用 11–12px 等宽字体。
- 所有主操作和图标操作提供至少 44×44px 命中区，相邻触控目标至少 8px。
- 数据卡从“缩小字体塞入”改为“主事实优先 + 次要事实折叠/换行”。
- 三主题分别校验正文、边框、禁用、焦点、警告与危险状态对比度。
- 可靠性趋势增加文本摘要/数据表替代，避免仅靠颜色和 hover title。

### P2：工程收敛

- 将全局 CSS 按基础 Token、应用外壳、通用组件和领域页面拆分，清理重复覆盖和固定色。
- 抽取共享请求版本 hook，统一请求开始、最新响应判断和卸载失效；busy、错误、成功、实体合并与后台校准继续保留领域语义，避免用通用抽象掩盖不同写操作行为。
- 为 50 条以上事件/请求列表评估分页窗口或虚拟化；避免为小列表引入复杂度。

## 分阶段实施

1. **回归恢复**：修复 7 条 E2E、规范路由和语义类，建立可靠基线。
2. **交互底座**：懒加载、skip link、焦点管理、统一 route loading 和 live feedback。
3. **状态一致性**：就地合并服务端结果、取消过期请求、收敛轮询。
4. **视觉与响应式**：字体/触控下限、移动端密度、三主题与图表可访问性。
5. **工程减重**：拆分 CSS 和公共异步组件，复测真实容器和数据规模。

## 2026-07-20 进度快照

### 已完成

- 回归恢复与交互底座：阶段性 35 条 Playwright 全部通过，规范化对比深链接，12 个领域视图按需加载，补齐 skip link、路由焦点和移动菜单遮罩语义；扩展验收后完整套件为 41/41。
- 核心状态一致性：Maintenance、SLO、Notification Routing 立即合并写接口返回结果；Reliability 处置后立即更新相关计划可用性。
- 首轮请求治理：Comparison、Reliability、Maintenance、SLO、Notification Routing 丢弃过期读取响应；Comparison 的轮询在页面隐藏时暂停。
- 请求治理扩面：首页聚合、计划日志、请求详情、事故与复盘、故障切换、测试场景、供应商配置和诊断均丢弃过期结果；Comparison、Failover、Scenario 与 Schedule 等轮询在页面恢复可见后只校准一次。
- 反馈一致性：四个主要处置视图提供 live success 状态并在 4 秒后清理，错误状态保留明确对象和重试动作。
- 图表无障碍：Reliability 趋势新增可见文字结论和原生 `<details>` 数据表，24/7/30 天所有时间桶都能通过键盘和读屏访问，视觉柱图不再依赖 `title`。
- 视觉基线：深海、石墨和极昼主题的 muted Token，以及极昼 info Token，已提升到 AA 对比度；独立 `styles/interaction-accessibility.css` 承载跨领域字号、触控和移动表单规则，320px 导出操作不再纵向断字。
- 触控基线：新增 375px、13 个主路由的可见主操作自动审计，按钮、链接、`summary` 和自定义选择器均以 44×44px 为下限，当前定向测试通过。
- 字号基线：13 个主路由的可见 `p` 均不低于 12px，`small`/`dt` 均不低于 11px；同时提升 Reliability 建议、事故摘要、对比历史和系统诊断中的关联紧凑标签与正文。
- 响应式基线：13 个主路由在 320、768、1024、1440px 下逐页检查文档宽度，均无水平溢出；375px 由移动触控审计同时覆盖。
- 样式分层：入口按 Base/Shell → Data Workspaces → Theme System → Domains → Interaction/Accessibility 顺序加载，生产构建保持原级联行为和拆包体积。
- 工程复用：新增 `useLatestRequest` 和共享 `ListPagination`；Schedules 与最多 500 条的 Comparison History 每页只渲染 50 条，页码范围通过 `aria-live` 宣告，Events 与计划日志抽屉继续使用服务端分页。
- 主题状态：新增 `--control-border`、`--surface-input` 和导航状态 Token；三主题文字与状态达到 4.5:1、真实表单边界达到 3:1，焦点和禁用状态通过浏览器组件检查。
- 动效与嵌套交互：reduced-motion 会关闭 Select 与关键弹层动效；Select 打开时 Escape 只关闭下拉，不再同时关闭父抽屉。
- 截图证据：六张极昼 README 截图已人工复核，布局、标签、状态层级与字体密度符合当前设计基线。
- 性能基线：生产主入口由约 478.69 kB / gzip 132.64 kB 降至约 289.72 kB / gzip 88.44 kB，原始体积下降约 39.5%。

### 最终收敛

- Events 与 Schedules 已直接使用语义 Token，`domains.css` 中对应的后置迁移覆盖已删除；三主题、字号、多宽度、触控和 reduced-motion 验收保持通过。
- 其余仍保留局部版本号的视图已经具备过期响应保护；继续迁移到 `useLatestRequest` 属于渐进式工程复用，不作为本任务完成阻塞项。

### 收尾条件

1. [x] 只删除能够证明被后置规则完整覆盖的旧 CSS，不机械替换品牌色、Provider 标识色、终端窗口点、JSON 语法色或 Select Portal 适配色。
2. [x] CSS 清理后重新运行 41 条 Playwright、生产构建、Go 测试、Compose 配置、Trellis context 和差异检查。
3. [x] 更新 Trellis 最后一项验收；真实 Provider 故障样本校准和共享 hook 扩面进入后续持续优化队列。

## 验收与证据

- `npm run build`：至少产生 4 个领域异步 chunk，主入口体积显著低于当前约 479 kB。
- `npm run test:e2e`：41/41；在历史 35 条基线上新增跨领域触控、字号、大列表分页、多宽度水平溢出、三主题状态对比度和 reduced-motion 验收。
- 浏览器键盘验收：Tab 首次可见 skip link，Enter 后焦点进入主内容；前进/后退后标题与焦点一致。
- 320px、375px、768px、1024px、1440px：自动化主流程无水平溢出；375px 可见主操作满足 44px 下限。
- `go test ./...`、`docker compose config --quiet` 通过；不新增敏感信息输出。

## 回滚

- 懒加载可逐个视图回退为静态导入，不影响 API 或路由格式。
- 即时状态更新异常时保留后台刷新为事实校准来源。
- 字体和触控调整以领域覆盖逐批落地，出现密度问题时可按页面回滚，不撤销语义 Token。
