# AI Watch 开发规则

## 核心流程

Provider 配置 → 手动测活或场景验证 → 创建计划 → 查看异常 → 处置或切换线路。

## 产品结构

- 一级入口固定为：总览、Provider、验证中心、自动化、稳定性、事件、设置。
- 前端路径、标题和导航关系统一维护在 `frontend/src/navigation.ts`。
- 请求详情深链接 `/requests/:requestId` 必须保持可直接访问和浏览器回退。
- Redis 是内部存储，只通过系统诊断展示健康信息，不恢复通用 Key 管理页面或 `/api/redis/*`。

## 工程边界

- `frontend/src/App.tsx` 只负责应用外壳、路由协调和全局任务弹层。
- API Handler 按领域放在 `internal/api/*_handlers.go`，`server.go` 只保留 Server、路由入口和通用 HTTP 辅助函数。
- 所有非 GET/HEAD/OPTIONS 前端请求自动携带 `Idempotency-Key`；新增写操作必须保持幂等。
- 计划请求日志只按 `scheduleId` 查询，不使用最近 Job 推断旧日志。
- Provider Group 的维护窗口只能在维护窗口中心管理，不在故障切换编辑器重复提供入口。

## 验证命令

```bash
go test ./...
cd frontend && npm run build && npm run test:e2e
docker compose config --quiet
```

部署使用：

```bash
docker compose build ai-watch
docker compose up -d ai-watch
```
