# nichijouSDK

聊天室插件房的 Go 服务端接入 SDK，封装桥接通道、JWT 验签、状态上报、身份挑战、票据兑换和接入配置读取。

游戏规则、玩家状态和浏览器页面仍由各游戏项目自行实现，通过 SDK 暴露的小型回调接入聊天室。

## 安装

```bash
go get github.com/BlackCarDriver/nichijouSDK@v0.1.0
```

在代码中导入：

```go
import "github.com/BlackCarDriver/nichijouSDK"
```

仓库为私有仓库时，需要让 Go 走 git 拉取（并确保本机有对应 GitHub 凭证）：

```bash
go env -w GOPRIVATE=github.com/BlackCarDriver/*
```

## 快速接入

### 加载公钥并创建桥接

```go
pub, err := nichijouSDK.LoadPublicKey("config/plugin_jwt.pub.pem")
if err != nil {
    log.Fatal(err)
}

bridge := nichijouSDK.NewBridge(nichijouSDK.Config{
    ChatroomWSBase: "wss://chat.example.com",
    RoomID:         roomID,
    IntegrationID:  integrationID,
    BridgeSecret:   bridgeSecret,
    SecretVersion:  secretVersion,
    InstanceID:     "mygame-1",
    PublicKey:      pub,
}, nichijouSDK.Hooks{
    States: func() map[string]nichijouSDK.State {
        return game.PluginStates()
    },
    OnUserLeave: func(nick string) {
        game.RemovePlayer(nick)
    },
    OnChat: func(nick, text string, timestamp int64) {
        game.BroadcastChat(nick, text, timestamp)
    },
    OnRoomClosed: func() {
        game.StopAcceptingPlayers()
    },
})
go bridge.Run()
```

状态变化后调用 `bridge.ScheduleFullReport()`；开局、终局等关键节点调用 `bridge.EventLog("对局开始")`。

### 游戏连接验签

```go
claims, err := nichijouSDK.VerifyJWT(token, pub, integrationID, roomID)
if err != nil {
    http.Error(w, "invalid token", http.StatusUnauthorized)
    return
}
// claims.Sub 是可信身份
```

### 挂载票据兑换接口

```go
mux.Handle("/exchange", nichijouSDK.NewExchangeHandler(nichijouSDK.ExchangeConfig{
    ChatroomBase:   "https://chat.example.com",
    ExchangeSecret: exchangeSecret,
}))
```

页面向游戏服务发送 `POST /exchange`，请求体为 `{"ticket":"..."}`，成功返回 `{"token":"...","nick":"..."}`。`exchangeSecret` 只保存在服务端。

## 版本管理

`version.go` 统一维护三个常量：

| 常量 | 当前值 | 说明 |
| --- | --- | --- |
| `SDKName` | `nichijouSDK` | 标识使用的 SDK 实现 |
| `Version` | `0.1.0` | SDK 发布版本，每次发布更新 |
| `ProtocolVersion` | `v1` | 仅在不兼容的协议变更时更新 |

桥接握手会自动上报这三个值，游戏无需额外配置。每次发布打 `vX.Y.Z` tag，游戏升级依赖后重新构建并重启服务。

## 目录

| 文件 | 作用 |
| --- | --- |
| `version.go` | SDK 名称、SDK 版本和协议版本常量 |
| `bridge.go` | 桥接握手、重连、状态上报、挑战与业务回调 |
| `auth.go` | 公钥加载与 JWT 验签 |
| `exchange.go` | 模板 B 票据兑换代理 |
| `config.go` | 接入配置读取与合并 |

## 示例项目

完整接入示例与协议说明维护在 [ChatRoomGameDemo](https://github.com/BlackCarDriver/ChatRoomGameDemo)：

- [接入教程](https://github.com/BlackCarDriver/ChatRoomGameDemo/blob/main/接入教程.md)
- [协议说明](https://github.com/BlackCarDriver/ChatRoomGameDemo/blob/main/协议说明.md)
