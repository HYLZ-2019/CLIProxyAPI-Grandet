# CLIProxyAPI-Grandet（葛朗台）

CLIProxyAPI-Grandet（葛朗台）是从 [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) 衍生出来的分支版本。它保留上游 CLIProxyAPI 的代理、鉴权、OAuth、管理 API 和配置文件格式，并在此基础上加入更适合长期自用和额度精算的功能。

## 额外功能

相比原版 CLIProxyAPI，本项目主要增加或强化了以下能力：

- **Analytics 用量统计**：使用本地 SQLite 记录请求、模型、client API key、token 用量、小时聚合等数据。
- **供应商额度折线图**：按 provider 展示额度剩余、CLIProxyAPI 记录到的累计消耗、429 事件点和预期刷新点。
- **Token 价格解算**：根据 provider 额度变化和每小时 token 用量，按天解算不同模型、不同 token 类型的额度单价。
- **429 事件与预计刷新时间记录**：记录配额耗尽事件，并尽量从响应头或响应体中提取预计刷新时间。
- **API Key 管理增强**：client API key 支持稳定 ID、名称和独立管理页面，适合频繁增删改 key。
- **Web 管理面板增强**：配置面板、Analytics 页面、API Key 管理页面和整体 UI 做了更适合日常维护的调整。
- **配置兼容**：尽量兼容原版 `config.yaml` 和认证文件；新增字段均有默认行为。

## 安装与启动

推荐使用 Docker Compose 部署。

### 1. 准备项目

```bash
git clone <本仓库地址> CLIProxyAPI-Grandet
cd CLIProxyAPI-Grandet
```

准备配置文件：

```bash
cp config.example.yaml config.yaml
```

至少需要在 `config.yaml` 中配置管理密钥和客户端 API key，例如：

```yaml
port: 8317

remote-management:
  secret-key: "your-management-secret"

api-keys:
  - name: "default"
    api-key: "your-client-api-key"
```

如需启用 Analytics：

```yaml
analytics:
  enabled: true
  raw-log-retention-days: 7
```

### 2. 启动 Docker Compose

```bash
mkdir -p auths logs data
docker compose up -d --build
```

查看日志：

```bash
docker compose logs -f
```

停止：

```bash
docker compose down
```

默认挂载路径：

| 内容 | 宿主机路径 | 容器路径 | 环境变量覆盖 |
| --- | --- | --- | --- |
| 配置文件 | `./config.yaml` | `/CLIProxyAPI/config.yaml` | `CLI_PROXY_CONFIG_PATH` |
| 认证/OAuth 文件 | `./auths` | `/root/.cli-proxy-api` | `CLI_PROXY_AUTH_PATH` |
| 日志 | `./logs` | `/CLIProxyAPI/logs` | `CLI_PROXY_LOG_PATH` |
| Analytics 数据 | `./data` | `/CLIProxyAPI/data` | `CLI_PROXY_DATA_PATH` |

启动后默认服务端口是 `8317`。管理面板入口沿用 CLIProxyAPI 的管理面板入口；如果你的配置允许远程管理，可用浏览器访问对应地址并输入 `remote-management.secret-key`。

### 3. 本机直接运行（可选）

如果不使用 Docker，可以直接编译运行：

```bash
go build -o ./CLIProxyAPI ./cmd/server
./CLIProxyAPI
```

程序默认读取当前目录的 `config.yaml`。OAuth 登录等命令仍沿用原版 CLIProxyAPI 的行为；认证文件目录应与 `config.yaml` 中的 `auth-dir` 或 Docker 挂载目录保持一致。

## 从已运行的原版 CLIProxyAPI 迁移

假设服务器上已有一个配置好并正在运行的原版 CLIProxyAPI，迁移的核心是：**备份原数据 → 停止原进程/容器 → 复制配置和认证文件 → 用本项目启动**。

下面以原版目录 `/opt/CLIProxyAPI`、新目录 `/opt/CLIProxyAPI-Grandet` 为例。

### 1. 确认并备份原数据

常见需要迁移的数据：

| 数据 | 常见位置 | 是否必须 |
| --- | --- | --- |
| `config.yaml` | 原版工作目录内 | 必须 |
| OAuth / 认证文件 | `auths/`、`~/.cli-proxy-api` 或 compose 挂载目录 | 必须 |
| 日志 | `logs/` | 可选 |
| 原版其它数据目录 | 按你的部署方式而定 | 可选 |

先备份：

```bash
sudo mkdir -p /opt/cliproxy-backup
sudo cp -a /opt/CLIProxyAPI/config.yaml /opt/cliproxy-backup/config.yaml
sudo cp -a /opt/CLIProxyAPI/auths /opt/cliproxy-backup/auths 2>/dev/null || true
sudo cp -a /opt/CLIProxyAPI/logs /opt/cliproxy-backup/logs 2>/dev/null || true
```

如果你的认证文件不在 `auths/`，请以原 `docker-compose.yml` 的 volume 或 `config.yaml` 里的 `auth-dir` 为准。

### 2. 停止原版 CLIProxyAPI

如果原版是 Docker Compose 启动的：

```bash
cd /opt/CLIProxyAPI
docker compose down
```

如果原版是 systemd 服务：

```bash
sudo systemctl stop cli-proxy-api
sudo systemctl disable cli-proxy-api
```

如果原版是手动后台运行的，先查找进程再结束：

```bash
pgrep -af 'CLIProxyAPI|cli-proxy-api'
sudo pkill -f 'CLIProxyAPI|cli-proxy-api'
```

确认端口已释放：

```bash
ss -ltnp | grep ':8317' || true
```

### 3. 部署本项目并复制数据

```bash
sudo mkdir -p /opt/CLIProxyAPI-Grandet
sudo chown "$USER":"$USER" /opt/CLIProxyAPI-Grandet

git clone <本仓库地址> /opt/CLIProxyAPI-Grandet
cd /opt/CLIProxyAPI-Grandet

cp /opt/cliproxy-backup/config.yaml ./config.yaml
cp -a /opt/cliproxy-backup/auths ./auths 2>/dev/null || mkdir -p ./auths
cp -a /opt/cliproxy-backup/logs ./logs 2>/dev/null || mkdir -p ./logs
mkdir -p ./data
```

如果原版 Docker Compose 使用了自定义挂载路径，也可以不复制数据，而是在本项目的 `.env` 中直接指向旧路径：

```bash
CLI_PROXY_CONFIG_PATH=/opt/CLIProxyAPI/config.yaml
CLI_PROXY_AUTH_PATH=/opt/CLIProxyAPI/auths
CLI_PROXY_LOG_PATH=/opt/CLIProxyAPI/logs
CLI_PROXY_DATA_PATH=/opt/CLIProxyAPI-Grandet/data
```

### 4. 检查配置兼容性

原版 `config.yaml` 通常可以直接使用。建议至少检查：

```yaml
remote-management:
  secret-key: "不要留空，否则管理 API / Web 面板不可用"

auth-dir: "/root/.cli-proxy-api"  # Docker 部署时建议与 compose 容器内挂载路径一致
```

如需使用本项目新增统计功能，加入：

```yaml
analytics:
  enabled: true
  raw-log-retention-days: 7
```

原版纯字符串形式的 API keys 仍兼容：

```yaml
api-keys:
  - "sk-xxx"
```

也可以改为带名称的形式：

```yaml
api-keys:
  - name: "my-client"
    api-key: "sk-xxx"
```

### 5. 启动本项目

```bash
docker compose up -d --build
docker compose logs -f
```

如果日志正常，确认管理面板和代理接口可用后，迁移完成。

### 6. 回滚方式

如果需要回滚：

```bash
cd /opt/CLIProxyAPI-Grandet
docker compose down

cd /opt/CLIProxyAPI
docker compose up -d
```

如果原版是 systemd 服务，则重新启用并启动原服务：

```bash
sudo systemctl enable cli-proxy-api
sudo systemctl start cli-proxy-api
```

## 迁移注意事项

- 不要同时运行原版和 Grandet，除非你明确修改了监听端口，否则会端口冲突。
- 认证文件目录必须迁移正确，否则 Claude / Gemini / Codex 等 OAuth 登录状态会丢失。
- `data/analytics.db` 是 Grandet 的新增数据；从原版迁移时没有这个文件是正常的，启用 Analytics 后会自动创建。
- 迁移前建议完整备份 `config.yaml` 和认证文件目录。
