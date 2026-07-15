# 全系统写操作幂等实施计划

1. 在 Redis 增加幂等记录的抢占、读取与完成能力，TTL 为 24 小时。
2. 在 API 最外层增加写请求幂等中间件和无 Redis 回退。
3. 前端请求封装为所有 POST、PUT、PATCH、DELETE 自动生成 `Idempotency-Key`。
4. 增加并发、回放、冲突和 Redis 存储测试。
5. 运行 Go、前端构建、Playwright、Compose 与线上回放验证。
