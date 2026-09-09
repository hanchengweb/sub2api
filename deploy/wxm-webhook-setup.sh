#!/usr/bin/env bash
# 配置 ToAPIs 任务 Webhook 的签名密钥。
#
# 密钥由 ToAPIs 控制台生成（编辑 Token → 生成 32 字节签名密钥），只显示一次。
# 本脚本负责把它安全地写进服务端配置：
#   - read -s 隐藏输入，不回显
#   - 走 psql 的 stdin heredoc，不进 ps 进程列表（不要用 psql -c "…密钥…"）
#   - 不写 shell 历史，不落任何临时文件
#
# 用法:
#   deploy/wxm-webhook-setup.sh set        # 配置/更新密钥
#   deploy/wxm-webhook-setup.sh rotate     # 轮换：当前密钥移到 previous，再填新的
#   deploy/wxm-webhook-setup.sh status     # 查看配置状态（不显示密钥本身）
#   deploy/wxm-webhook-setup.sh disable    # 停用 Webhook
set -euo pipefail

HOST="${WXM_HOST:-wxm-tenant-platform-hz-01}"
PG="docker exec -i sub2api-postgres psql -U sub2api -d sub2api -At -F'|'"

ssh_psql() { ssh -o ConnectTimeout=20 -o BatchMode=yes "$HOST" "$PG"; }

# 只报告"有没有配、多长、指纹前 8 位"，永远不回显密钥本身。
# 指纹用 sha256 而不是明文片段：足以确认两端是否一致，又不泄漏内容。
show_status() {
  ssh_psql <<'SQL'
SELECT '启用       : '||COALESCE(NULLIF((SELECT value FROM settings WHERE key='toapis_webhook_enabled'),''),'（未配置）');
SELECT '当前密钥   : '||CASE
    WHEN COALESCE((SELECT value FROM settings WHERE key='toapis_webhook_secret'),'')=''
    THEN '（未配置）'
    ELSE '已配置，'||length((SELECT value FROM settings WHERE key='toapis_webhook_secret'))||' 字符，指纹 '
         ||left(encode(sha256(convert_to((SELECT value FROM settings WHERE key='toapis_webhook_secret'),'UTF8')),'hex'),8)
  END;
SELECT '上一密钥   : '||CASE
    WHEN COALESCE((SELECT value FROM settings WHERE key='toapis_webhook_secret_previous'),'')=''
    THEN '（无，非轮换期正常）'
    ELSE '已配置，指纹 '
         ||left(encode(sha256(convert_to((SELECT value FROM settings WHERE key='toapis_webhook_secret_previous'),'UTF8')),'hex'),8)
  END;
SQL

  # 事件数单独查：Postgres 在解析阶段就校验表名，写成 CASE 的未走分支照样报错，
  # 所以先探测表存在与否（迁移随部署应用，未部署时该表还没建）。
  local has_table
  has_table=$(ssh_psql <<'SQL'
SELECT CASE WHEN to_regclass('public.webhook_events') IS NULL THEN 'no' ELSE 'yes' END;
SQL
)
  if [ "$has_table" = "yes" ]; then
    ssh_psql <<'SQL'
SELECT '近期事件   : '||count(*)||' 条' FROM webhook_events WHERE provider='toapis';
SQL
  else
    echo "近期事件   : （表未建，待部署后生成）"
  fi
}

read_secret() {
  printf '请粘贴 ToAPIs 控制台生成的签名密钥（输入不回显，回车确认）: ' >&2
  read -rs SECRET
  printf '\n' >&2
  [ -n "$SECRET" ] || { echo "密钥为空，已取消" >&2; exit 1; }
  # 32 字节密钥常见为 64 位十六进制或 base64；这里只做长度下限提示，不强判格式。
  if [ "${#SECRET}" -lt 32 ]; then
    printf '⚠ 长度只有 %s 字符，确认没粘贴完整？继续请输入 yes: ' "${#SECRET}" >&2
    read -r confirm
    [ "$confirm" = "yes" ] || { echo "已取消" >&2; exit 1; }
  fi
}

case "${1:-status}" in
  status) show_status ;;

  set)
    read_secret
    # 用 psql 变量传值：不出现在命令行参数里，也不需要自己转义引号。
    ssh -o ConnectTimeout=20 -o BatchMode=yes "$HOST" \
      "docker exec -i sub2api-postgres psql -U sub2api -d sub2api -q -v secret=\"\$(cat)\" <<'SQL'
INSERT INTO settings (key, value, updated_at)
VALUES ('toapis_webhook_secret', :'secret', NOW())
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();
INSERT INTO settings (key, value, updated_at)
VALUES ('toapis_webhook_enabled', 'true', NOW())
ON CONFLICT (key) DO UPDATE SET value = 'true', updated_at = NOW();
SQL" <<< "$SECRET"
    unset SECRET
    echo "已写入。当前状态："
    show_status
    ;;

  rotate)
    # 轮换期上游会同时发两把密钥的签名（24 小时），两边都留着才不会漏收。
    ssh_psql <<'SQL'
INSERT INTO settings (key, value, updated_at)
SELECT 'toapis_webhook_secret_previous', value, NOW()
FROM settings WHERE key='toapis_webhook_secret'
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();
SQL
    echo "当前密钥已备份为 previous（轮换期内两把都能验签）"
    read_secret
    ssh -o ConnectTimeout=20 -o BatchMode=yes "$HOST" \
      "docker exec -i sub2api-postgres psql -U sub2api -d sub2api -q -v secret=\"\$(cat)\" <<'SQL'
UPDATE settings SET value = :'secret', updated_at = NOW() WHERE key='toapis_webhook_secret';
SQL" <<< "$SECRET"
    unset SECRET
    echo "已轮换。24 小时后可执行 disable-previous 清掉旧密钥。"
    show_status
    ;;

  disable-previous)
    ssh_psql <<'SQL'
UPDATE settings SET value='', updated_at=NOW() WHERE key='toapis_webhook_secret_previous';
SQL
    echo "旧密钥已清除"
    ;;

  disable)
    ssh_psql <<'SQL'
UPDATE settings SET value='false', updated_at=NOW() WHERE key='toapis_webhook_enabled';
SQL
    echo "Webhook 已停用（密钥保留，可随时重新启用）"
    ;;

  *) echo "用法: $0 {set|rotate|status|disable|disable-previous}" >&2; exit 1 ;;
esac
