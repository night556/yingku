# 映库 · Yingku

映库是一款面向家庭使用的自托管影视库。它把本地目录和网盘里的视频整理成海报墙，支持浏览器续播和局域网电视投屏。后端和网页打包成一个 Go 程序。

[使用手册](docs/USER_GUIDE.md) · [Docker / Dokploy 部署](DEPLOYMENT.md)

## 主要功能

- 连接本地目录、WebDAV、百度网盘和夸克网盘。可直接使用本地原文件，也可下载或复制到指定目录。
- 批量识别电影、剧集与季集，预览并确认后入库。可以选配兼容 OpenAI 接口的 AI 服务辅助理解文件命名。
- 读取已有 NFO、海报与字幕。默认可以检索公开的影视资料，也可配置自己的 TMDb 凭据。
- 浏览海报墙和作品详情，在浏览器播放、续播和切换剧集，支持 SRT / VTT 字幕。
- 向同一局域网的 DLNA 接收设备投屏，在网页上控制播放和选集。

## 快速开始

需要 Go 1.24 或更新版本。在仓库根目录运行下面的命令，浏览器打开 [http://127.0.0.1:8787](http://127.0.0.1:8787)。

```sh
go run ./cmd/yingku
```

第一次使用时，在「来源 → 添加来源」选择一个含有 MP4 视频的本地目录。勾选视频，点击「导入所选」，核对作品和季集后加入媒体库。也可以打开总目录，点击「扫描整个目录」批量导入。

Windows 用户可以先执行 `go build -o yingku.exe ./cmd/yingku`，以后双击 `启动映库.cmd` 启动。其他平台可执行 `go build -o yingku ./cmd/yingku`。

## 容器部署

在支持 Docker Compose 的服务器上，从仓库根目录运行下面的命令。

```sh
docker compose up -d --build
```

Dokploy 可以新建 Application，选择 Dockerfile 构建。Dockerfile 路径填 `Dockerfile`，构建上下文填 `.`，容器端口填 `8787`。持久挂载 `/config` 保存配置和观看进度，挂载 `/media` 保存或读取视频。挂载、域名和迁移的详细步骤见 [容器部署说明](DEPLOYMENT.md)。

## 开发

| 目录 | 内容 |
| --- | --- |
| `cmd/yingku/` | 命令行入口 |
| `internal/yingku/` | HTTP 服务、媒体库、来源、下载、资料匹配、投屏及相应测试 |
| `web/` | 内嵌网页和静态资源 |
| `docs/` | 使用手册 |

```sh
go test ./...
go vet ./...
```

映库目前适合单实例的家庭媒体库。浏览器与电视直接解码视频，建议优先使用 MP4 / H.264 / AAC。有关格式兼容、投屏和文件管理的说明见 [使用手册](docs/USER_GUIDE.md)。

映库采用 [MIT 许可证](LICENSE)。项目依赖的许可见 [第三方声明](THIRD_PARTY_NOTICES.md)。
