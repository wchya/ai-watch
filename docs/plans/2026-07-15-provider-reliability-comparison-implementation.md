# AI Watch Provider 可靠性趋势与对比实施计划

**设计：** `docs/plans/2026-07-15-provider-reliability-comparison-design.md`

## 阶段 1：聚合核心

1. 新增可靠性领域模型和三个固定时间窗。
2. 分页读取 `request_end` 事件并执行脱敏聚合。
3. 计算分类分布、成功率、平均/P95、连续失败和时间桶。
4. 增加纯单元测试与隐私字段测试。

## 阶段 2：API

1. 新增 `GET /api/reliability` 路由。
2. 校验时间窗并返回稳定错误码。
3. 合并当前 Provider 清单，标记历史配置。
4. 增加 API 成功、非法参数和存储失败测试。

## 阶段 3：前端页面

1. 增加可靠性类型和 API 客户端。
2. 增加“可靠性”导航页、时间窗切换与刷新。
3. 实现全局指标、Provider 对比、趋势桶和异常时段。
4. 增加空状态、部分覆盖和保留旧结果的错误状态。

## 阶段 4：浏览器与交付

1. 扩展 API Mock 和 Playwright 时间窗/响应式验收。
2. 更新 README 与项目进度文档。
3. 运行 Go、前端构建、Playwright、Compose 和 diff 检查。
