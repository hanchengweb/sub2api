# WindHub 微末智学桌面授权

微末智学在 WindHub 网页注册、登录并确认授权，通过 PKCE 回调自动连接桌面端。客户端 `com.weimozhixue.desktop` 独立于风合智联和手机遥控；不共用其回调或会话。

回调白名单：安装版 `weimozhixue://oauth/callback`；开发预览只允许 `http://127.0.0.1:<1024-65535>/oauth/callback`。拒绝 localhost、其他 IP/域名、用户信息、查询参数、片段和额外路径。该客户端要求随机 state（至少 16 位）、43 位 S256 challenge 和标准 43-128 位 verifier。服务端一次性授权码绑定用户、客户端、回调、state、challenge，五分钟过期。

注册、邮箱验证、登录之间只传递校验后的授权字段。注册成功先回到登录页的授权确认，不直接发放桌面连接。已登录用户从注册入口进入时也展示当前账号与授权确认，可切换账号。网页生成回调时只带 code/state，桌面主进程调用 `/api/v1/auth/desktop/token` 完成换码。网页现有密码、人机验证、二次验证与条款策略继续生效。

微末智学主进程随后读取本人信息，按已有用户 API 创建或复用专用设备连接并加密存储；账号管理刷新令牌用完即撤销。桌面实现及验证见 `D:\wxm-dev\development\personal-account.md`。

验证包括客户端回调白名单、跨客户端隔离、错误 state/verifier/redirect、一次性码重放、注册跳转与桌面取消/关闭/重启重试。不使用生产注册、创建密钥或模型计费作为 UI 测试。
