// Package store 是数据访问层：SQLite 连接、建表迁移、配置项缓存与各表仓储。
//
// 所有 SQL 都收敛在这里，上层不得直接持有 *sql.DB 写查询。
package store
