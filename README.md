# Pocket HTTP 2.0 · Windows 原生版

Go + Win32 原生控件实现的轻量 HTTP 调试工具，适用于 Windows 10 / 11 x64。独立窗口、单文件 exe、免安装；不需要浏览器、WebView、Electron、Java、Node 或 Go 运行环境，也不启动本地 HTTP 服务。

## 使用与升级

解压后双击 PocketHTTP.exe。关闭窗口会等待当前编辑写入成功，再退出程序；保存失败时保留窗口并提示。使用系统原生主题、树形列表、标签页、文本框、剪贴板和文件对话框，支持系统 DPI 缩放。

升级前请先退出 v1.1 浏览器版（点击旧版「退出程序」），然后启动原生版。原生版继续读取 `%APPDATA%\PocketHTTP\workspace.json`，兼容 v1.1 的 Collections、认证配置、当前草稿、选中请求、展开状态和历史。首次使用现有文件前会备份为 `workspace.json.before-native.bak`，不会反复覆盖此备份。原生版使用互斥锁限制同一工作区只能运行一个原生进程。

浏览器版尚未落盘的恢复日志不会由原生版读取，请先在旧版完成保存再退出。仅有 v1.0 浏览器收藏的用户，可先从旧版导出收藏 JSON，再在原生版导入。

## Collections

- 左侧「＋ 分组」创建 Collection，「＋ 文件夹」在选定分组 / 文件夹下创建子文件夹。
- 点击请求即可载入；编辑已归档请求时会自动更新该请求。
- 「保存到 Collection」将新草稿归档。「整理」可改名、移动分组内的请求或文件夹；Collection 本身可改名。
- 「删除」删除当前选中节点，包括它的子文件夹与请求，并会先确认。删除当前请求后会保留编辑器内容作为草稿。
- 「导入」追加本应用 v1.1 Collections 格式或旧版收藏数组；「导出」生成与 v1.1 兼容的 Collections JSON。不支持 Postman 原生 Collection 文件格式。
- 最多 100 条请求、合计 300 个分组 / 文件夹 / 请求节点，树层级最多 8 层。

## Authorization

独立标签页支持：

- No Auth：不生成认证；手动填写的认证请求头仍可发送。
- Bearer Token：生成 Authorization: Bearer ...。
- Basic Auth：用户名 / 密码按 UTF-8 编码生成 Basic 头；用户名不能包含冒号。
- API Key：自定义 Key / Value，可放入 Header 或 Query Params。

生成的认证会覆盖同名手动请求头或查询参数；不会改写编辑器原始文本。发送与复制 cURL 使用同一套认证逻辑。密钥输入默认隐藏，可勾选显示。认证按请求保存，暂不提供 Collection 认证继承或 OAuth 登录流程。

认证密钥明文保存在工作区、备份以及导出的 Collections 文件中，请妥善保管。输入框隐藏不等于加密。

## 请求与响应

支持 GET、POST、PUT、PATCH、DELETE、HEAD、OPTIONS；URL 查询参数；每行一个 Key: Value 的自定义请求头；Raw、JSON、URL Encoded 请求体；JSON 格式化；1–600 秒超时、取消、跟随重定向开关和 HTTPS 校验开关（默认验证）。请求由 Go 直接发出，可访问 localhost 和内网，无浏览器 CORS 限制。

响应显示状态码、耗时、大小、Body 和 Headers，支持原文 / 格式化切换、复制、下载。二进制响应通过下载保存原始字节。复制 cURL 为 POSIX shell 格式，可用于 Git Bash / WSL。

最近请求可载入或清空。连续发送配置完全相同的请求时只保留一条记录；中间发送过其他请求后再次发送则保留新记录。不包含 multipart 文件上传、WebSocket、脚本或团队同步。

## 自动保存与内存

编辑停止约 350 ms 后，后台工作线程写入工作区文件；窗口底部显示保存状态。关闭窗口立即提交最新编辑并等待保存完成。写入采用临时文件、同步写入及替换；Windows 短暂文件占用时有限重试，失败保留上次有效数据。不轮询、不联网更新、不上传数据。

保存内容包含 Collections、认证、未发送草稿、当前所选请求、文件夹展开状态和最近请求；不保存响应正文。工作区 UTF-8 JSON 上限 2 MiB，超限会提示未保存，需要缩小正文或导出并删除部分记录。强制终止进程或突然断电不能保证最后几个字符写入。

内存控制：

- 单进程原生界面，不包含浏览器引擎进程。
- Go 运行时内存软目标 32 MiB、GCPercent 50；该目标不是进程总内存或工作集的硬上限。
- 最多一个并发上游请求；请求正文最大 2 MiB，上游响应头最大 64 KiB。
- 响应最多保留解压后的 2 MiB；超出明确提示截断，下载也只包含保留部分。
- 界面预览最多 256 KiB，大响应不自动格式化；复制 / 下载可取得本次保留的完整响应。
- 历史最多 30 条 / 512 KiB；单条超过 64 KiB 不写入历史。
- 不保留多个响应或 Base64 副本；保存队列仅保留当前写入和最新待写入版本。

## 验证范围

使用 Go 1.24.6、CGO_ENABLED=0 构建 Windows amd64 GUI 可执行文件，包含 Windows 通用控件 v6 和 DPI 清单。可执行文件未签名。

已通过：

- Linux 上核心逻辑的 go test -race 与 go vet。
- Windows 目标的静态检查（对直接 Win32 回调所需的 unsafe.Pointer 转换关闭 unsafeptr 检查）。
- 将实际 Windows 测试 exe 运行在 Wine 10 + Xvfb 中：旧版工作区恢复与备份、HTTP 请求、Bearer / UTF-8 Basic / API Key、重定向、HTTPS 验证、取消、二进制及大小限制。
- 实际 Win32 控件操作：Collection / 文件夹创建、请求保存、改名和移动，原生导入 / 导出对话框，原生删除确认，关闭瞬间保存大正文，再创建窗口恢复状态。
- 发布版 exe 连续发送 115 次本地请求，检查可继续响应和保存。Wine 下观测工作集从约 43.6 MiB 到约 47.7 MiB；这些数字包含兼容层，不是原生 Windows 内存实测，也不能拿来与旧版 Linux 后端的 RSS 直接比较。

当前环境不是原生 Windows，真实 Windows 的内存占用和显示效果仍需在实际机器验收。提供 cmd/winprobe 源码，可在真实 Windows 上通过 GetProcessMemoryInfo 读取程序工作集。

## 从源码构建

依赖仅为 Go 标准库与 Windows 系统 DLL。源码包包含 resource_windows_amd64.syso，可离线构建，无需资源编译器。

Windows PowerShell：

```powershell
$env:CGO_ENABLED="0"
go build -trimpath -ldflags="-s -w -H=windowsgui" -o PocketHTTP.exe .
```

Linux / macOS 交叉编译：

```sh
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags='-s -w -H=windowsgui' -o PocketHTTP.exe .
```

运行测试：Linux 核心测试 `go test -race ./...`；Windows 原生交互测试 `go test -run TestNativeWindow -v`（会打开测试窗口，使用独立临时工作区）。修改清单后可用 `go run github.com/akavel/rsrc@v0.10.2 -manifest app.manifest -arch amd64 -o resource_windows_amd64.syso` 重建资源；此工具不是应用运行依赖。

测试可设置 POCKET_DATA_DIR 使用隔离目录。正常使用无需设置环境变量。
