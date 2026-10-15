# Anam 官方完整效果试用

2026-10-15：用户否决 Simli 效果并明确要求使用其新 Anam 账号接入应用。保留既有财务账本，仅通过 `DIGITAL_TEACHER_ANAM_USERS` 为指定用户开放 Anam 效果试用。该白名单优先于原供应商配置；普通用户沿用原链路。

配置 `ANAM_API_KEY_FILE=/app/data/anam-api-key`，文件仅服务进程可读。凭证不进入代码、客户端、日志或文档。部署仍仅重建 gateway，保留 speech、数据库、Redis、nginx；发布前备份 compose 与数据库并核对旧镜像和二进制摘要。

## 契约

- `POST /studio/sessions` 先持久化幂等记录并检查用户、连接、算力包、分钟授权、并发和每日限制，再由服务器调用 Anam `POST /v1/engine/session`。每次90秒、每个供应商分别按24小时5次，不扣 WindHub 积分或分钟，消耗 Anam 账号额度。
- `personaConfig.maxSessionLengthSeconds=90`，`avatarModel=cara-4`，中文识别 `languageCode=zh` / `voiceDetectionOptions.sttProvider=anam`。`sessionOptions` 请求1152×768 / high。真实回执已验证 Cara 4、Cartesia sonic-3.6、GPT 4.1；ElevenLabs 等其他组合以用户选择及实际可用性为准。
- 浏览器使用官方 `@anam-ai/js-sdk@4.28.0`。它得到 WindHub HMAC 凭据；`/anam-sdk/v1/engine/session` 只读取一个已创建的会话，不接受客户端覆盖配置，不创建第二个供应商会话。供应商一小时 session token 不下发。
- engineHost 实际包含 `/v1/webrtc/engines/…` 路径；仅允许官方 connect / connect-us / connect-eu 域名。连接资料加密存储，停止时清除；列表不返回连接资料。
- 结束调用 `POST /v1/sessions/{id}/stop`，随后 GET 同一ID核对 `clientLabel`、`endTime`、`sessionLengthMs`。200停止响应和本地超时均不冒充结束回执；后台持续恢复。创建响应丢失时仅按服务器生成的完整 clientLabel 精确匹配，禁止时间猜配或重复创建。
- 公共形象、音色、模型按分页拉取并短期缓存。私有形象和声音必须有 `digital_teacher_assets` 的当前用户 / anam 授权。已有账户形象按确切ID绑定，不对全体用户公开供应商私有目录。
- 照片入口使用 `POST /v1/avatars` multipart `imageFile` / `displayName` / `avatarModel=cara-4`，限制4.5MB、显式照片授权。仅保存私有缩略图和幂等制作记录；未知结果只按唯一任务名称恢复。完成后自动绑定当前用户。

## 验证和边界

聚焦测试：`WINDHUB_DIGITAL_TEACHER_TEST=1 go test ./internal/digitalteacher` 在专用回环 PostgreSQL 55439 验证迁移、私有资产、只创建一次、撤权、同一ID回执、未知创建恢复、照片契约和零扣费。handler/routes 运行原回归。

真实官方链路首次测试收到1152×768、1187帧、真实音轨、两轮中文问答，回执49716ms。账号当时免费计划剩余30分钟。该证据只证明链路，不能替代用户对形象与声音的质量验收。免费账号用于本次效果试用；正式商业计费、真实物理麦克风和客户网络尚须另行验收。部署身份与最终应用实录另存任务交付记录。

官方参考：
- https://anam.ai/docs/api-reference/engine/create-engine-session
- https://anam.ai/docs/api-reference/sessions/get-session
- https://anam.ai/docs/api-reference/sessions/stop-session
- https://anam.ai/docs/api-reference/avatars/create-avatar
- https://anam.ai/docs/personas/session/video
- https://anam.ai/docs/personas/voices/multilingual

## 微末智学智能体模式（2026-10-15）

新客户端显式发送 `brain_mode=agent`。服务端在校验老师资产后使用官方
`llmId=CUSTOMER_CLIENT_V1` 创建 Cara 4 会话，不再向 Anam 发送授课提示或自动开场白。
返回的票据必须包含 `brainMode=agent`，客户端才允许进入该模式。老师摘要参与幂等键
冲突检查，同一请求不得从托管模型切为外部智能体或反向切换。原客户端托管模型
契约保留；保存为外部模式的老师不能由旧客户端静默启动。

问题由桌面已有 Session API 和模型账户执行，工具授权沿用工作台；只把最终可朗读
正文送给 Anam。视频试用限制不变，智能体模型和工具费用按其原账户规则独立计算，
不能把视频零扣费描述为整个智能体免费。服务端不持有桌面会话权限，也无需公网
回调或新增模型密钥。该变更无数据库迁移，仍按同一上游ID核对结束回执。

依据：https://anam.ai/docs/javascript-sdk/examples/custom-llm 。本地 PostgreSQL 回归已
验证外部模型配置、幂等模式隔离、撤权、结束回执和零视频扣费；真实部署后音视频
结果以桌面交付记录为准。
