# Redis Store 设计

## 目标

Redis 成为 AI Watch 唯一运行时持久层。现有 SQLite 数据库只作为首次迁移来源；迁移成功后写入 Redis marker，原数据库文件保留为只读备份。

## 兼容边界

定义 `store.Store` 接口，保持现有设置、任务摘要、事件、供应商示例和计划任务方法签名。SQLite `store.JSON` 继续实现该接口，用于迁移与测试；生产入口使用 `store.Redis`。

## Redis 数据结构

- settings：单 JSON key。
- summaries：hash 保存摘要，sorted set 使用单调序号排序并按上限裁剪。
- events：hash 保存事件 JSON 与逻辑字节，sorted set 按发生时间排序，counter 生成 ID。
- provider examples：hash，以 example ID 为 field。
- schedules：hash，以 schedule ID 为 field，运行状态覆盖原记录，不建立执行历史。
- migration：固定 marker key，记录 SQLite 已成功迁移。

事件写入和裁剪使用 Lua，在同一原子操作内完成写入，以及最大天数、最大条数和最大逻辑字节裁剪。手动清空事件同样使用 Lua；上层 Manager 的事件队列屏障语义保持不变。

## 启动流程

1. 连接并 ping Redis。
2. marker 不存在时，读取 SQLite 全量数据并写入 Redis。
3. 所有实体迁移成功后写 marker；失败不写 marker，下次启动可重试。
4. 预热 settings、summaries、provider examples 和 schedules 的进程内快照。
5. 运行期只访问 Redis；SQLite 文件不再写入或删除。
