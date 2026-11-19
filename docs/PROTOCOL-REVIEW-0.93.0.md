# 0.93.0：百度目录目标的受理与身份依据

2026-11-18，Codex。本轮复用项目`mkdir_cloud.go`既有网页登录创建路线：先GET gettemplatevariable取得CSRF凭据，再一次POST /api/create，a=commit、isdir=1、size=0、block_list=[]、rtype=0。沿用现有百度账号Cookie/固定源/无跳转客户端；没有接入开发者OAuth或其他账号凭据，也没有增加自动重试。

资料交叉核对：[pybdc原作者create_dir实现](https://github.com/wanghongcheng0302/pybdc/blob/master/utils.py)使用相同Web /api/create与目录参数，可佐证路线，代码较旧且未给出本轮严格受理/竞争保证，不能当官方契约。[baiduyun_api作者的mkdir实现](https://docs.rs/baiduyun_api/latest/src/baiduyun_api/yunapi.rs.html)虽有rtype=0说明，但其Create实际为/rest/2.0/xpan/file、access_token认证，**不能作为当前Web Cookie接口的兼容性证明**。TeraBox同名接口也不套用。本轮依据是项目已经采用的Web创建请求及新增严格结果门槛，真实服务端冲突行为仍需用户验收，不把本机合成响应视为服务商保证。

读取登录凭据的准备阶段在写前检查点之前完成，纯读取失败不发送目录创建。创建前保存Submitted，只发送一次既有创建请求。响应必须errno=0、准确目标path、显式isdir=1及规范非零fs_id；不接受缺字段、错误类型、改名路径或仅同名可见。将fs_id与CreatedAck一起加密保存，随后只读回查原路径/类型/ID一致才确认目录创建并允许子项。受理丢失或保存失败，重启也不重发。

目标目录ID保存于私有diskRelay.TargetFolderID，不出公开Job；恢复验证字段只能附在百度目录、必须伴随已提交受理，不能附在文件或DAV记录。子目录创建和文件上传前、目标完整回读后，沿父记录链复核目标祖先目录ID，拒绝同路径被替换。原来源祖先/版本/内容校验照常执行，目录ID不当作文件内容证明。

整棵清单预检仍在写之前，目标同名不合并；百度目标任一空文件使整棵拒绝，真正空目录允许。目录名沿既有新建规则；两端百度时保守拒绝来源/目标逻辑路径重叠，即使账号可能不同，也不靠未经验证的账户身份推断资源独立。文件复用已有百度不覆盖上传策略与完整内容回读，不新增上传会话持久化。失败可能留下部分目录、改名或未完成上传项目，不自动回滚或删除。

仅TLS合成账号/节点及隔离安装验证，无真实云端创建、上传或删除。旧0.92及更早版不应写含百度目录受理ID的新状态。其他原生目录目标/其他盘目录及删除源搬运仍未完成。
