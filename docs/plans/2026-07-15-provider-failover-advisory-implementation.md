# Provider 建议式故障切换实施计划

1. 增加 ProviderGroup/FailoverAdvice 模型和 Store 接口。
2. 增加 SQLite v13 与 Redis Hash 持久化。
3. 增加 CRUD API 和输入校验。
4. 在 request_end 后执行失败阈值、验证锁、备用测活、建议和恢复逻辑。
5. 增加故障切换管理页面与请求详情入口。
6. 补齐单元、API、E2E、构建和部署验证。

