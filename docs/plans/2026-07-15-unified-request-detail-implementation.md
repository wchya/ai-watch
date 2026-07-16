# 统一请求详情实施计划

1. 为事件过滤模型与 SQLite/Redis 查询增加 `requestId`。
2. 新增请求详情聚合结构和 `GET /api/requests/:requestId`。
3. 新增前端类型、API 方法、动态路由解析和独立详情页。
4. 接入事件请求日志、计划请求时间线及已有 Provider 请求入口。
5. 增加 Go、Playwright、主题与移动端测试。
6. 全量构建、部署并核验真实请求详情 API 和生产 bundle。

