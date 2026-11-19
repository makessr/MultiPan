# 2026-11-18 协议检查：盘内复制与中转来源

本轮资料均仅作为协议证据读取，未安装或执行其中脚本，也未向真实账号发请求。

## 暂缓猜测夸克盘内复制

[imeiming/quark-drive原始实现](https://github.com/imeiming/quark-drive/blob/main/scripts/quark_cli.py) 声明了 `/file/copy` 方法，但[OpenList当前驱动原始实现](https://github.com/OpenListTeam/OpenList/blob/main/drivers/quark_uc/driver.go) 的 Copy 明确返回 NotSupport。前者声明函数本身不能证明现行服务支持、目标冲突策略或异步结果协议，未据此开放真实复制。保留现有能力限制，后续有可靠证据再接入。

123同样不把[OpenList当前123驱动](https://github.com/OpenListTeam/OpenList/blob/main/drivers/123/driver.go)返回NotSupport解释为服务永远不支持；本轮检索结果出现了不同OpenAPI/网页接口路径，而取回的[网页抓包文档](https://github.com/crmmc/123pan-open/blob/main/doc/api_web.md)正文不足以确认完整复制参数、冲突策略和异步核对。没有依据搜索摘要猜测生产请求，也没有将OpenAPI能力自动套用当前网页登录凭据。

## 本轮实际选择

复用项目已接入的123文件列表和下载协议，不引入新的源端修改接口。`pan123Client.list`的FileId/ParentFileId/Type/Size/Etag和`open123Download`的原MD5/受控下载解析作为输入，再自行计算下载字节MD5绑定排队原版本、计算SHA256核对DAV目标。

只扩展来源，目标仍为已实现的DAV条件PUT，不调用123复制/移动/删除。缺父目录或有效MD5时拒绝；夸克JSON MD5在现有下载实现中为建议参照且存在差异记录，未将它直接当作本轮可靠来源版本。后续扩展其他盘先核对来源版本与目标拒覆盖证据，不能通过移除检查扩大支持列表。

核验者Codex，2026-11-18 Asia/Shanghai；这是当时源码和资料证据，不是服务商能力永久声明，也不构成真实账号验收。
