# 多 Provider 场景对比实施计划

1. 在 API 增加 `POST /api/scenario-comparisons` 与 `GET /api/scenario-comparisons/:id`。
2. 复用 TestScenarioStore、Job Manager 和 EventStore，记录脱敏批次开始事件。
3. 从活动 Job 与长期 `request_end` 事件组合批次结果。
4. 前端增加批次类型和 API，替换通用 bulkJobs 轮询。
5. 增加数量/CLI/场景校验、Request ID 跳转和结果排序。
6. 补充 API、浏览器验收和隐私断言。
7. 全量验证后重新构建部署。
