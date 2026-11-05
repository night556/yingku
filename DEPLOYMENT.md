# Docker / Dokploy 部署映库

仓库已经提供 `Dockerfile`、`compose.yaml` 和 `.env.example`。前端编译在同一个 Go 程序里，不需要另起网页、数据库或下载服务。构建镜像需要访问 Go 模块仓库与 Docker 镜像仓库；运行时不需要 Go 或 Node.js。

## 提交到 Git 的文件

提交 Go 源码和测试、`go.mod`、`go.sum`、`web/`、`Dockerfile`、`.dockerignore`、`.gitignore`、`THIRD_PARTY_NOTICES.md`、部署文档和不含真实凭据的 `.env.example`。如果保留 Compose 部署方式，也提交 `compose.yaml`。

`data/`、运行时的 `config/`、`media/`、`imports/`、真实 `.env`、`work/`、`outputs/` 和本地编译的可执行文件留在 Git 之外。`state.json` 保存媒体库、路径、进度和加密后的来源凭据，`secret.key` 用来解密这些凭据；连同迁移备份一起私下保存。`.gitignore` 不会停止跟踪已经提交的文件，可先用 `git ls-files -- data work outputs .env` 检查实际用于部署的仓库。

## 推荐的目录约定

| 用途 | 容器内路径 | 内容 |
| --- | --- | --- |
| 配置与资料 | `/config` | `state.json`、`secret.key`、海报缓存、字幕与进度 |
| 视频 | `/media` | 已有视频、网盘下载和主动复制整理的视频，包括未完成的下载临时文件 |

默认只持久挂载 `/config` 和 `/media`。添加本地来源时填写 `/media`、`/media/movie` 或 `/media/tv`，不填写宿主机目录，也不填写手机上的路径。同一个来源可以分别进入 movie、tv 进行扫描。已有视频直接入库，不复制、不改名；网盘下载也可以保存到同一个 `/media` 下。

下载时也可以选择 `/media/movie`、`/media/tv` 等子目录。容器重建后，只有映射到宿主机目录或数据卷里的文件会保留。

## 在 Dokploy 中通过 Dockerfile 部署

1. 新建 **Application**，连接包含映库源码的 Git 仓库和分支。选择 **Dockerfile** 构建类型，Dockerfile 路径填 `Dockerfile`，构建上下文填 `.`，构建阶段留空以使用最终运行镜像。
2. 在 **Advanced → Mounts** 添加两个持久挂载。用命名数据卷存配置时，将卷挂到 `/config`；有现成视频目录时，将服务器上的目录绑定到 `/media`，没有现成目录也可使用命名数据卷。镜像已设置这两个容器内路径。
3. 用反代域名时，将 **Domains** 的容器端口设为 `8787`，路径为 `/`；直接通过服务器 IP 访问时，在 **Advanced → Ports** 将宿主机端口映射到容器的 `8787`。`YINGKU_PUBLIC_URL` 可留空，真实凭据可在映库设置或 Dokploy 的运行环境中填写。
4. 保持一个实例，部署并打开映库。以后重新构建、更新镜像时继续使用相同的挂载。

参考：[Dokploy Dockerfile 构建设置](https://docs.dokploy.com/docs/core/applications/build-type#dockerfile)、[持久挂载与端口](https://docs.dokploy.com/docs/core/applications/advanced)。

## 在 Dokploy 中通过 Compose 部署（可选）

1. 在项目中新建 **Docker Compose** 服务，接入包含本项目源码的 Git 仓库。Compose 文件路径填写 `compose.yaml`，选择普通 Docker Compose 模式。该文件使用 `build`，不能直接用不支持构建的 Stack 模式。
2. 在 **Environment** 中按需粘贴 `.env.example` 的内容。局域网 IP 和反代域名访问都不需要填写 `YINGKU_PUBLIC_URL`，保持留空即可。
3. 按需修改配置和视频这两个宿主机目录，或者保留 `../files/yingku/` 下的默认目录。不要把持久数据放在 Git 检出目录的 `./data` 或 `./media` 下。
4. 部署。默认监听 `0.0.0.0:8787` 并发布服务器端口 8787，直接打开 `http://服务器局域网IP:8787`。
5. 如果使用域名，直接在 **Domains** 添加该域名，服务选择 `yingku`，容器端口 **8787**，路径 `/`。不需要同步填写 `YINGKU_PUBLIC_URL`。Dokploy 自动处理路由，不需要手写 Traefik 标签。仅使用域名时，可将 Compose 中的 `ports` 改成 `expose: ["8787"]`。
6. 打开映库，在设置中确认默认保存位置是 `/media`。添加网盘并登录，或者添加 `/media` 本地来源即可。

这里的 Environment 变量已经在 `compose.yaml` 的 `environment` 中逐项引用，会传入容器。Dokploy 的环境变量编辑器本身只生成 `.env`，并不会自动把所有变量传入容器。

使用服务器现有的视频目录时，例如文件实际在 `/mnt/nas/videos`：

```dotenv
YINGKU_CONFIG_PATH=../files/yingku/config
YINGKU_MEDIA_PATH=/mnt/nas/videos
```

以上 `/mnt/nas/videos` 是运行 Docker 的服务器上的已有磁盘或 NAS 挂载点。先在服务器上完成挂载，确认视频存在；不要填写仓库内的临时目录。它会映射到 `/media`，可以同时作为已有视频来源和下载位置。`../files` 是 Dokploy 文档推荐的、位于检出目录之外的持久位置。目录创建、读写权限及 NAS 挂载由宿主机管理。镜像默认使用 root，使用 Compose 的 `user: "1000:1000"` 时，应先让该 UID 对配置和视频目录拥有写入权限。

`/imports` 只是可选的只读来源入口，不是程序要求的目录。如果想让已有视频和新下载分开，可以在服务的 `volumes` 列表额外添加 `/mnt/nas/existing:/imports:ro`，之后添加 `/imports` 本地来源。默认 Compose 不包含这个挂载，也无需设置 `YINGKU_IMPORT_PATH`。

也可以在 Dokploy 的挂载设置中使用 Docker 命名数据卷，分别挂到 `/config`、`/media`，并移除 Compose 中对应的绑定挂载，避免同时占用同一目标路径。命名数据卷可使用 Dokploy 的 Volume Backups；绑定挂载目录由你自行备份。

只有一个媒体库实例读取同一个 `/config`，不要增加副本。更新时保留挂载路径，替换镜像并重新部署即可。默认停止宽限期 20 秒，服务收到 SIGTERM 后会停止接收新请求，并让下载任务保存检查点。

## 自己构建和运行

可以直接使用默认配置；需要自定义路径时，先复制 `.env.example` 为 `.env` 并修改宿主机目录，然后：

```sh
docker compose up -d --build
docker compose logs -f yingku
```

或自行构建镜像、使用单容器运行：

```sh
docker build -t yingku:local .
docker run -d --name yingku --restart unless-stopped --stop-timeout 20 \
  -p 8787:8787 \
  -v /srv/yingku/config:/config \
  -v /srv/yingku/media:/media \
  yingku:local
```

示例命令用于 Linux 服务器。镜像已设置 `/config`、`/media` 和 `0.0.0.0:8787`，无需重复传入。使用上述宿主机目录之前先创建它们，并确认 NAS 已挂载。

## 可配置的启动参数

| 环境变量 | 命令行参数 | 作用 |
| --- | --- | --- |
| `YINGKU_ADDR` | `-addr` | 服务监听地址；普通程序默认 `0.0.0.0:8787` |
| `YINGKU_DATA_DIR` | `-data` | 配置、密钥、任务、进度和缓存目录；普通程序默认 `data`，镜像默认 `/config` |
| `YINGKU_MEDIA_DIR` | `-media` | 固定默认下载/复制目录；镜像默认 `/media`，普通程序留空时沿用网页设置 |
| `YINGKU_PUBLIC_URL` | `-public-url` | 可选，仅覆盖页面显示的访问地址；不影响域名或 IP 访问，也不设置投屏回连地址 |
| `YINGKU_BAIDU_CLIENT_ID` | — | 可选百度应用 App Key；也可在网页保存应用配置 |
| `YINGKU_BAIDU_CLIENT_SECRET` | — | 可选百度应用 Secret Key；与 App Key 配套 |
| `TZ` | — | 容器时区，默认 `Asia/Shanghai` |

显式命令行参数优先于同名环境变量。`-media=""` 可以清除环境变量的覆盖，重新允许网页修改默认目录。没有启动覆盖时，会沿用上次保存的位置，而不是重置它。

设置 `YINGKU_MEDIA_DIR` 后，网页会显示保存位置由部署配置管理。更换这个值只修改以后导入的默认位置，**不会移动已有视频，也不会替换数据库里的旧文件路径**。更改容器内目录时，还需要同步更改挂载目标；提供的 Compose 已将两者绑定到同一变量。通常只改 `YINGKU_MEDIA_PATH` 等宿主机路径，保持容器内部 `/config`、`/media` 稳定更方便。

资料目录启动后不能通过网页切换。海报与字幕跟随资料目录保存，下载临时文件位于目标视频旁；不需要单独部署缓存服务。

`0.0.0.0:8787` 决定服务监听哪些网卡。反向代理可直接使用自己的域名，不需要在映库中重复声明；`YINGKU_PUBLIC_URL` 只覆盖页面展示地址，不参与域名白名单或投屏视频链接生成。服务保留同源、页面令牌与浏览器跨站请求检查。直接连接仍限本机/私网，已有密码设置继续生效；经过反代的请求不会因为代理连接来自 127.0.0.1 就自动获得本机管理权限。密码依然可选；无密码模式下，能够到达服务的人也可以管理媒体库。此版本没有增加公网多用户权限系统。

自行设置反代时，需要保留原始 Host，保证浏览器的同源请求正常，例如 Nginx 中使用 `proxy_set_header Host $http_host;`，并传递常规的 `X-Forwarded-For` / `X-Forwarded-Proto`。上游使用服务器本机或私网地址；外部 HTTPS 可由反代终止。映库网页的接口和视频地址使用相对路径，不需要预设外部域名。不支持将应用直接挂载到 `/yingku` 等子路径。

### 在页面中设置地址

打开「设置 → 手机访问与 DLNA 投屏 → 访问与投屏地址」：

- **访问地址**：例如 `https://media.example.com`，仅用于页面展示。留空不影响访问。若设置了 `YINGKU_PUBLIC_URL` 或 `-public-url`，以启动配置为准，页面中显示为只读。
- **投屏回连地址**：例如 `http://192.168.1.100:18787`，用于生成发给电视的视频链接。留空时自动选择通往电视的局域网 IP 和服务端口；容器映射或多网卡导致自动地址不正确时，再填写电视实际可访问的地址与映射端口。

两个配置互不替代，保存到资料库并在重启后保留；可以清空恢复默认。新的回连地址只影响之后开始的投屏，不修改正在播放的会话。电视是否支持 HTTPS 取决于设备，局域网通常使用 HTTP。它只解决视频回连地址，不替代 DLNA 多播发现所需的网络连通性。

镜像自带健康检查，每 30 秒检查 `/healthz`。也可在容器内执行 `/app/yingku -healthcheck`，正常返回 0，启动失败或服务停止时返回非 0。健康检查本身不会创建数据目录或修改资料。

## DLNA 与容器网络

普通 Docker 桥接网络适合网页访问、下载和浏览器播放。DLNA 的发现依赖局域网 UDP 多播，电视还需要回连服务；只映射 TCP 8787 不能保证这两项正常。

如果服务器与电视在同一个局域网，并需要 DLNA，可在 Linux 上将 Compose 的 `ports` 删除，给服务添加 `network_mode: host`，保持监听 `0.0.0.0:8787`。此时直接使用服务器局域网 IP；不要同时依赖 Dokploy 自动注入的桥接网络和域名路由。提供的默认 Compose 保持桥接模式，不宣称默认支持跨容器或跨网投屏。部署在外网云服务器上，也不会自动发现家中的电视。

## 从 Windows 迁移已有媒体库

迁移前停止旧服务，完整复制 `data/` 到服务器上对应的 `/config` 挂载目录，**同时保留 `state.json` 和 `secret.key`**，再单独复制或挂载视频。只有完整迁移这两个文件，才能保留并解密已经保存的来源凭据。

已有资料库中保存的是绝对路径，例如 `E:\Media\...`。Linux 容器不能直接使用这种 Windows 路径。仅修改 `YINGKU_MEDIA_DIR` 不会修复旧作品中的视频路径；当前没有自动批量路径迁移工具。若要同时保留原库和观看进度，应先离线统一转换文件路径、本地来源根目录与字幕路径，再启动；不要在不了解对应关系时直接替换原始资料库。也可用一个空 `/config` 新建媒体库后，扫描挂载到 `/media` 的视频。此部署适配没有修改你现有电脑上的资料库。

参考：[Dokploy Compose 与持久挂载](https://docs.dokploy.com/docs/core/docker-compose)、[Dokploy 域名配置](https://docs.dokploy.com/docs/core/docker-compose/domains)。
