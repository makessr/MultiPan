# 第三方说明

MultiPan 使用 Wails v3、Vue、Vite、TypeScript、golang.org/x/sys 等依赖。各依赖的许可证见对应官方仓库和包文件。

蓝奏云上传协议适配参考 chenhb23/lanzouyun-disk 的 UploadTask.ts、profile.ts、ls.ts、http.ts，参考提交 f2449637b1d656445803e08b1463736074ca11dd：
https://github.com/chenhb23/lanzouyun-disk

该项目采用 MIT 许可证，保留声明如下：

MIT License

Copyright (c) 2020 chenhb23

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

LitePan 仅作架构调研参考，本版本没有引入或复制其源代码。

内置登录新增使用 github.com/wailsapp/go-webview2 v1.0.23（MIT）；许可证见 https://github.com/wailsapp/go-webview2/blob/main/LICENSE 。独立原生 WebView2 登录窗口不暴露 Wails 绑定。

夸克适配按公开网页接口协议独立实现；核对过 LitePan Quark 驱动中的接口路径与响应字段，未引入其驱动源码或依赖。OSS 请求使用标准 multipart 协议。

123 云盘适配独立实现，核对公开网页生产资源、OpenList 的接口字段和 rclone-123pan 的实测协议说明，未引入这些项目的源码或依赖。S3 授权使用标准 AWS Signature V4。

诚通适配按官方 REST API 文档独立实现，未引入 OpenList 驱动源码或依赖。
- https://openapi.ctfile.com/ （客户端邮箱/短信登录、用户资料、目录、上传票据、结果状态）
- https://github.com/OpenListTeam/OpenList/tree/main/drivers/ctfile （checksum 与 multipart 协议字段核对）

百度网页接口适配独立实现，按 gettemplatevariable/list/precreate/superfile2/create/filemetas 的协议字段开发，未引入参考项目源码或依赖。核对来源：
- https://github.com/suileyan/xpmibackup_sly/blob/main/src/app/src/main/java/com/suileyan/cloud/provider/BaiduProvider.java
- https://github.com/chang-xinhai/Quark2Baidu/blob/main/Q2B.py
- https://github.com/baidu-netdisk/mcp/blob/main/src/baidu-netdisk/fileupload_tool.py （官方开放接口上传分阶段说明；本项目实际使用网页登录会话）

协议核对来源（2026-11-15）：
- https://yun.123pan.cn/
- https://statics.123pan.com/static-by-custom/utils-prod/getLogin.js （生产接口域名配置）
- https://github.com/OpenListTeam/OpenList/tree/main/drivers/123 （路径、字段和分片上界约定）
- https://github.com/Ljzd-PRO/rclone-123pan/blob/main/docs/compatibility.md （当前域名、SliceSize、秒传零 ID 等兼容性证据；第三方实测不等同于 MultiPan 已实测）

## 光鸭云盘与 OSS SDK
光鸭协议按官网公开生产资源独立实现，未复制 LitePan/OpenList 驱动源码。核对官网 https://www.guangyapan.com/static/js/index.bcbab7d6c4.js 中的当前接口及 provider 能力映射；参考 OpenList 的字段含义。新引入 github.com/aliyun/aliyun-oss-go-sdk v3.0.2+incompatible（MIT），许可证全文如下：

Copyright (c) 2015 aliyun.com

Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated
documentation files (the "Software"), to deal in the Software without restriction, including without limitation the
rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to
permit persons to whom the Software is furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all copies or substantial portions of the
Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE
WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR
COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR
OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
## 0.9.0 文件浏览协议参考
蓝奏只读文件分页字段参考 chenhb23/lanzouyun-disk 的 src/common/core/ls.ts 和 zaxtyson/LanZouCloud-API 的 lanzou/api/core.py；仅核对协议字段，独立实现 Go 代码，未复制其源代码。其余列表复用项目既有适配器。

0.10.0 蓝奏新建文件夹请求字段参考 chenhb23/lanzouyun-disk 的 src/common/core/mkdir.ts（task=2、parent_id、folder_name、folder_description），独立实现创建前检查及创建后确认；未复制源代码。

0.11.0 多网盘新建目录字段核对：OpenListTeam/OpenList 的 drivers/quark_uc/driver.go、drivers/123/driver.go 及 util.go；百度网页 create 协议与已有 API 适配复用，光鸭官网生产 index.bcbab7d6c4.js 中 /userres/v1/file/create_dir 及 failIfNameExist。WebDAV 使用 MKCOL。独立实现创建前后校验，未复制参考驱动代码。

0.12.0 重命名字段参考：chenhb23/lanzouyun-disk 的 src/common/core/edit.ts、Files/index.tsx，zaxtyson/LanZouCloud-API 的 core.py（读取蓝奏原描述及可编辑文件名）；OpenListTeam/OpenList 的 quark_uc、123 驱动；光鸭官网当前 /userres/v1/file/rename；百度既有网页 filemanager 协议；WebDAV MOVE/Overwrite。独立实现状态及前后核验，未复制源代码。

0.13.0 单文件移动协议字段参考 chenhb23/lanzouyun-disk 的 src/common/core/mv.ts（仅task20单文件请求，不采用其递归目录复制删除流程）；OpenListTeam/OpenList quark_uc/123 驱动；光鸭官网当前 /userres/v1/file/move_file；百度既有网页 filemanager；WebDAV MOVE。独立实现前置检查、双目录核验与响应异常核对，未复制参考代码。

0.17.0 WebDAV collection MOVE 语义核对 RFC 4918 §9.9（https://www.rfc-editor.org/rfc/rfc4918#section-9.9），独立实现目录路径校验、禁止覆盖和双侧核对。核对 chenhb23/lanzouyun-disk 的 mv.ts 发现其目录移动涉及重建/删除，未采用该流程；其他 provider 目录接口调研不等于本版已支持。没有复制参考驱动源码。

0.18.0 夸克/123文件夹原生移动字段核对 OpenListTeam/OpenList main 的 drivers/quark_uc/driver.go Move 和 drivers/123/driver.go Move（https://github.com/OpenListTeam/OpenList/blob/main/drivers/quark_uc/driver.go ，https://github.com/OpenListTeam/OpenList/blob/main/drivers/123/driver.go）。只核对目录/文件通用的原生移动协议，路径边逐层验证、显示路径更新及回归均独立实现，未复制参考驱动源码。

0.19.0 光鸭目录移动字段核对官网 https://www.guangyapan.com/static/js/index.bcbab7d6c4.js 的通用onMove处理（fileIds/parentId、taskId）；百度原生管理字段语义参考 https://github.com/OpenListTeam/OpenList/blob/main/drivers/baidu_netdisk/driver.go 的Move，同时沿用本项目已验证的网页登录filemanager协议。仅核对协议语义，层级/路径检查与状态处理独立实现，未复制源码。

0.21.0 123下载字段与params/跳转/JSON解析语义核对 https://github.com/OpenListTeam/OpenList/blob/main/drivers/123/driver.go Link、types.go和util.go（S3KeyFlag、DownloadUrl、download_info）。仅核对协议，节点校验/凭据隔离/有界探测与文件保留/落盘及MD5回归独立实现，未复制参考驱动源码。

0.22.0 光鸭下载字段核对官网 https://www.guangyapan.com/static/js/index.bcbab7d6c4.js 中cH及handleDownload（/userres/v1/get_res_download_url、fileId、signedURL）。参考 https://github.com/OpenListTeam/OpenList/blob/main/drivers/guangyapan/driver.go Link了解signedURL/downloadURL备用语义，实际采用官网网页API。独立实现状态校验、节点请求隔离与下载落盘，未复制参考驱动源码。

0.23.0 夸克下载字段与Cookie/Referer/User-Agent要求核对 https://github.com/OpenListTeam/OpenList/blob/main/drivers/quark_uc/util.go getDownloadLink 及 types.go DownResp（/file/download、fids、data数组download_url、可用md5）。独立实现逐域Cookie边界、响应匹配和内容落盘，不复制参考驱动代码，也不照搬向所有下载地址发送Cookie的做法。

0.23.1 夸克MD5兼容性调查参考 https://github.com/i-sync/quark-cli/blob/main/README.md 的校验说明，以及 OpenList quark_uc/types.go 不暴露该下载md5为文件哈希的行为。仅参考接口行为和兼容经验，独立实现参考摘要警告/Content-MD5强校验及测试，未复制源代码、未新增依赖。

0.24.0 仅核对 OpenList quark_uc/types.go 的 updated_at 版本元数据语义；123 复用既有 Etag。WebDAV ETag/Last-Modified仅作变更标记，不假定ETag为MD5。原件SHA256、Windows共享锁、持久化关联和状态分级均独立实现，未复制外部代码、未新增依赖。

0.25.0 百度网页登录下载字段核对 OpenListTeam/OpenList drivers/baidu_netdisk/util.go linkCrack（https://github.com/OpenListTeam/OpenList/blob/main/drivers/baidu_netdisk/util.go）：filemetas、target、dlink、web、origin及info.dlink。仅核对协议字段，独立实现元数据核验、分域Cookie、有限跳转及落盘，未复制驱动代码。

0.26.0 蓝奏下载协议字段参考 chenhb23/lanzouyun-disk src/common/core/download.ts、matcher.ts 和 zaxtyson/LanZouCloud-API lanzou/api/core.py 的task22/分享页/iframe/sign/ajaxm.php语义。独立实现Go的有限字符串解析及安全下载；不复制源代码，不执行网页JS，不采用关闭TLS校验或验证码绕过行为。

0.26.1 百度下载UA核对 OpenListTeam/OpenList drivers/baidu_netdisk/util.go linkCrack 和 meta.go CustomCrackUA默认netdisk（https://github.com/OpenListTeam/OpenList/blob/main/drivers/baidu_netdisk/meta.go）。仅核对请求头兼容要求，独立实现及隔离回归，没有复制源代码。

0.26.3 根据用户提供的实际蓝奏公开分享页核对dom_ajaxs/domain2、ajaxfile.php、kd及页面指定下载节点选择，独立实现有限字面量解析；不执行或复制页面脚本，无新增依赖。

0.26.4 百度DLNA错误31329/31119语义核对OpenListTeam/docs docs/zh/guide/drivers/baidu.md；网页签名/download字段核对原作者GreasyFork脚本485074的download函数及sdvcrx/pan-baidu-download util.py协议说明。使用Go标准crypto/rc4独立计算签名，不复制参考实现、不执行网页JS、不新增依赖。

0.30.0 夸克目录pdir_fid响应字段与既有父FID列表作用域核对OpenListTeam/OpenList drivers/quark_uc/types.go；其余复用本项目已验证列表/单文件协议。独立实现ID树扫描与校验，未复制外部实现，未新增依赖。

0.52.0 分享按网络协议独立实现，未引入第三方驱动源码或新增依赖；接口字段核对来源：
- 蓝奏 detail.ts、edit.ts 与 SetAccessModal.tsx（MIT，既有声明适用）；edit.ts 参考提交 ae21d8d6fbb6e0990b01c4aeba6f970fdbe59ade：
  https://github.com/chenhb23/lanzouyun-disk/blob/ae21d8d6fbb6e0990b01c4aeba6f970fdbe59ade/src/common/core/edit.ts
- 夸克分享路径、请求/响应字段与有效期枚举核对 LitePan share.go，参考提交 7778b040a7e72ff8a8f395437cae5aafe53c7e41；只作协议互操作资料，不复制或链接其驱动代码：
  https://github.com/Ponphil/LitePan/blob/7778b040a7e72ff8a8f395437cae5aafe53c7e41/drivers/Quark/share.go
这些是参考实现，不是网盘官方接口稳定性承诺；需按真实账号试用结果核对。

0.53.0 123分享仅核对公开协议字段，独立实现，没有复制第三方驱动或新增依赖。旧ShareKey响应参考作者SDK https://github.com/Bao-qing/123pan/blob/master/pan123_core.py 。现代/b/api/share/create与share/list、shareLinkList.list、ShareId/SharePwd/Expiration、shareModality和2099永久日期核对123官网公开前端生产资源（2026-11-16只读获取，不执行资源代码）：
https://statics.123pan.com/123pan-resources/prod/dist/src_file_actions_share_ShareCreateModalAdapter_tsx.452456389c3045a89867.1794308315.chunk.js
https://statics.123pan.com/123pan-resources/prod/dist/src_pages_ShareList_ShareList_tsx.44c550a8f80136723960.1794308315.chunk.js
不实现付费分享/流量付费配置，不承诺第三方接口稳定或分享免费。

0.54.0 百度分享按协议独立实现，无新增依赖或复制驱动代码。核对作者实现BaiduPCS-Go提交1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73中的baidupcs/prepare.go（share/pset path_list、schannel、period、pwd、share_type；share/record和surlinfoinrecord路径）、share.go响应字段，以及internal/pcscommand/share.go的expiredType=-1和expiredTime为剩余秒语义。详情接口签名参数按netdisksign/share_sign.go描述的协议摘要计算，非文件校验用途。
https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/prepare.go
https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/share.go
https://github.com/qjfoidnh/BaiduPCS-Go/blob/1b9131817aaf8ca7dee24bc00e33ebc4c7a5cc73/baidupcs/netdisksign/share_sign.go
只读公开源码作互操作资料，不执行第三方代码，不承诺非官方网页协议长期稳定。

0.54.0补充核对作者源码 https://github.com/PeterDing/BaiduPCS-Py/blob/master/baidupcs_py/baidupcs/pcs.py ：record.channel=4为有提取码，surlinfoinrecord可仅返回密码。详情不含URL时从精确原分享记录的shortlink读取；不从名称或原下载节点拼造分享地址。

0.54.1 百度分享链接pwd参数用于自动填充的格式核对百度官方知识页：https://eyun.baidu.com/knowledge/百度网盘链接前缀和提取码什么关系？链接结构、pwd参数与失效处理.html ，及作者实现BaiduPCS-Go的分享链接组合参数语义。独立实现前端显示与复制选项，不发送访问请求或改变后端来源校验。

0.55.0 光鸭分享按官网公开生产资源独立实现，没有复制外部驱动或新增依赖：
https://www.guangyapan.com/static/js/index.572308ad5c.js （2026-11-16只读获取，不执行）：/userres/v1/share_file及get_share_list；validateDuration以秒传输（1/7/30天分别86400/604800/2592000，永久0）；shareType0无码、1随机、2自定义；响应shareId/shareUrl/code，列表id/shareStatus/leftTime/validateDuration。shareStatus1正常、2过期、3删除、4封禁；leftTime=-1为永久；分享页从code查询参数读取提取码。
仅交叉核对作者SDK https://github.com/DDSRem-Dev/guangyaclient/blob/master/guangyaclient/client.py 的方法语义，不采用其中旧nd.bizuserres.s路径或将有效期误当天。使用本项目已有认证客户端，无真实账号请求或验证码处理。

0.55.2 以用户提供分享链接的官方只读get_share_summary响应核对：正常shareStatus=1、永久leftTime=-1可以不含validateDuration。沿用官网index.572308ad5c.js对摘要接口及200/201无效、202过期的处理语义；仅在原记录缺状态时补读精确原ID摘要，不创建或修改真实分享，不保存分享令牌。


## Go OLE installer integration

github.com/go-ole/go-ole v1.3.0 is used by the current-user installer for Windows shortcuts.

The MIT License (MIT)

Copyright © 2013-2017 Yasuhiro Matsumoto, <mattn.jp@gmail.com>

Permission is hereby granted, free of charge, to any person obtaining a copy of
this software and associated documentation files (the “Software”), to deal in
the Software without restriction, including without limitation the rights to
use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies
of the Software, and to permit persons to whom the Software is furnished to do
so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED “AS IS”, WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

## Zalando go-keyring

The MIT License (MIT)

Copyright (c) 2016 Zalando SE

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
