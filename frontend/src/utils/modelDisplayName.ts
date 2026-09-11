/**
 * 模型 ID → 上游真实版本号，全站唯一一份。
 *
 * 为什么需要：我们的模型 ID 是对外契约（用户代码里写死的），不能跟着上游版本改；
 * 但用户光看 ID 判断不出实际用的是哪一版。比如 `deepseek-v4-flash` 打到的其实是
 * 官方的 DeepSeek-V4.1-Flash——看名字会以为还是 V4。
 *
 * 只做补充展示，不替换 ID：ID 仍然是主标题，版本号作为副标题出现。
 *
 * **这张表里的每一条都必须是核实过的**。它是在对用户做一个关于上游模型的事实
 * 陈述——写错就是误导。核实方式：拿这个 ID 打一次上游，看响应里的 model 字段，
 * 再对照官方文档的「模型版本」一栏。没核实过的模型就别加，留空比写错强。
 *
 * 反过来，上游静默升级（deepseek-flash 哪天变成 V4.2）这张表不会自动跟着变。
 * 所以只收录版本号确实有信息量、且变动不频繁的模型；拿不准就不写。
 */

/** 核实记录：2026-09-11 实测 + 官方文档 api-docs.deepseek.com 价格页「模型版本」栏。 */
const MODEL_VERSIONS: Record<string, string> = {
  // 两个别名在上游都解析到 deepseek-flash（响应 model 字段实测确认），
  // 官方文档标注其模型版本为 DeepSeek-V4.1-Flash。
  'deepseek-v4-flash': 'DeepSeek-V4.1-Flash',
  'deepseek-v4-flash-vision-exp': 'DeepSeek-V4.1-Flash（视觉实验版）'
}

/**
 * 取模型的上游版本号；没收录就返回空串。
 *
 * 返回空串而不是回落到模型 ID：调用方拿到 ID 只会渲染出跟主标题一模一样的副标题。
 */
export function resolveModelVersion(model: string): string {
  return MODEL_VERSIONS[model.trim()] ?? ''
}
