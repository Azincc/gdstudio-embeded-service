# 项目信息

## 项目定位

GDStudio 嵌入式下载微服务 - 为 Echo 音乐客户端提供音频下载与元数据刮削服务

## 仓库信息

- **项目路径**: `/Users/azin/PycharmProjects/gdstudio-embeded-service`
- **语言**: Go 1.22+
- **关联项目**: Echo Flutter Client (`/Users/azin/echo`)

## 文档位置

1. **详细设计文档**: `/Users/azin/echo/docs/gdstudio_embed_service_plan.md`
   - 完整的架构设计
   - GDStudio API 接口规范
   - Go 技术栈选型
   - 实施细节与代码示例

2. **快速开始**: `./QUICKSTART.md`
   - 部署指南
   - 常见问题
   - 性能调优

3. **项目说明**: `./README.md`
   - 功能特性
   - API 接口
   - 开发路线图

## 当前状态

**阶段**: 脚手架搭建完成，待实现核心代码

**已完成**:
- ✅ 项目目录结构
- ✅ Docker 配置（Dockerfile + docker-compose.yml）
- ✅ 配置文件模板（config.yaml + .env.example）
- ✅ Makefile 构建脚本
- ✅ 完整设计文档

**待实现（M1 最小可用）**:
- [ ] Go 模块初始化（需要 Go 环境）
- [ ] API 层实现（Gin handlers）
- [ ] Worker 任务处理器（asynq）
- [ ] GDStudio 客户端（API 调用 + 签名）
- [ ] 音频标签写入（taglib CGO）
- [ ] Navidrome 集成（Subsonic API）
- [ ] 数据库模型与迁移（GORM）

## 技术栈

- **框架**: Gin (Web) + asynq (队列)
- **音频**: taglib (CGO)
- **数据库**: PostgreSQL/SQLite + GORM
- **缓存**: Redis
- **日志**: zap
- **监控**: Prometheus

## 下一步行动

### 如果你有 Go 环境：

```bash
cd /Users/azin/PycharmProjects/gdstudio-embeded-service
go mod init github.com/azin/gdstudio-embed-service
go mod tidy

# 开始实现核心代码
# 1. cmd/api/main.go
# 2. internal/service/gdstudio/client.go
# 3. internal/worker/tasks/download_task.go
```

### 如果使用 Docker 开发：

```bash
# 直接编写代码，通过 docker-compose 构建运行
docker-compose build
docker-compose up -d
```

## 部署建议

**开发环境**: 使用 docker-compose，快速启动所有依赖

**生产环境**:
- API + Worker 容器化部署
- Redis Sentinel 高可用
- PostgreSQL 主从复制
- Prometheus + Grafana 监控
- Nginx/Caddy 反向代理（HTTPS）

## 预计工作量

- **M1（最小可用）**: 2-3 周（核心功能实现）
- **M2（增强）**: 1-2 周（FLAC、幂等、监控）
- **M3（生产化）**: 2-3 周（批量、高可用、优化）

总计：5-8 周完整实现

## 联系方式

- Echo 客户端仓库: `/Users/azin/echo`
- 设计文档位置: `/Users/azin/echo/docs/gdstudio_embed_service_plan.md`

---

**创建时间**: 2026-02-19
**最后更新**: 2026-02-19
