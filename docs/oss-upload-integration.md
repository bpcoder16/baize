# 在项目中接入 OSS 上传

项目用一个 `ossboot.Prepare` 装配 MySQL 或 PostgreSQL 上传记录、阿里云客户端和上传服务。本文以 MySQL 8、一个公有桶、`config-file` 场景为例。把示例中的账号 ID、RAM 身份、Bucket、地域、前端域名和应用运行模式替换为部署值。App、Web、Admin 可以共享一次装配。原先直接调用 Pixiu `biz/ossservice` 的代码可改为导入 `github.com/bpcoder16/baize/service/oss`，同步切换使用的错误变量。单独使用 `MD5FromURL` 无需运行 Prepare。

## 1. 确定 Bucket、地域和对象目录

在 OSS 控制台创建或选择 Bucket，记录 Bucket 名和地域，例如 `cn-shanghai`。私有资料使用私有 ACL。配置里的 `private` 和 `public` 是写入记录的逻辑桶类型，Bucket 的实际访问权限由 OSS 管理。Key 的格式为 `<runMode>/<targetDir>/<yyyyMMdd>/<uuid><ext>`，例如 `test/config-file/20261123/<uuid>.json`。每个环境只授予自己的前缀。

上传凭据响应会把 OSS 客户端的 Endpoint 原样提供给前端。浏览器和移动 App 需要能访问该地址，可采用对应地域的公网 Endpoint。CDN 下载域名另行配置。

## 2. 建立 RAM 签发身份、角色和信任关系

1. 在 RAM 控制台进入「身份管理 → 用户」，为后端创建专用程序身份，例如 `upload-issuer`，为它准备服务端 AccessKey。
2. 进入「身份管理 → 角色」，创建上传角色 `oss-upload`，复制角色 ARN，例如 `acs:ram::<账号ID>:role/oss-upload`。以当前云账号为信任主体，切到脚本编辑器，将信任范围收窄到签发用户。[创建并设置角色信任策略](https://help.aliyun.com/zh/ram/user-guide/create-a-ram-role-for-a-trusted-alibaba-cloud-account)
3. 在「权限管理 → 权限策略」创建自定义策略，允许签发用户对这个角色执行 `sts:AssumeRole`。把策略授权给 **upload-issuer 用户**。
4. 给 **oss-upload 角色** 授予目标目录的上传权限。场景的 `stsDuration` 最短为 15 分钟，最长受角色最大会话时长约束。角色最大会话时长应覆盖所有启用场景。

角色信任策略示例：

```json
{
  "Version": "1",
  "Statement": [{
    "Effect": "Allow",
    "Principal": {"RAM": "acs:ram::<账号ID>:user/upload-issuer"},
    "Action": "sts:AssumeRole"
  }]
}
```

签发用户的权限策略示例：

```json
{
  "Version": "1",
  "Statement": [{
    "Effect": "Allow",
    "Action": "sts:AssumeRole",
    "Resource": "acs:ram::<账号ID>:role/oss-upload"
  }]
}
```

上传角色的权限策略示例：

```json
{
  "Version": "1",
  "Statement": [{
    "Effect": "Allow",
    "Action": "oss:PutObject",
    "Resource": ["acs:oss:*:*:<bucket>/test/config-file/*"]
  }]
}
```

每次申请时白泽还会传入会话策略，将写权限限定到本批次的精确 Key。临时凭据的有效权限是角色权限和会话策略的交集。白泽按序列化后的 2048 字节限制会话策略，批量上限应结合 Bucket 名和实际 Key 长度设置。有效期内同一 Key 可以再次写入。[阿里云角色扮演](https://help.aliyun.com/zh/ram/user-guide/assume-a-ram-role) · [AssumeRole 参数](https://help.aliyun.com/zh/ram/developer-reference/api-sts-2015-04-01-assumerole) · [OSS 临时凭据配置](https://help.aliyun.com/zh/oss/developer-reference/use-temporary-access-credentials-provided-by-sts-to-access-oss)

## 3. 授权后端访问 OSS，配置 Web 跨域

为后端 OSS 客户端创建 RAM 程序身份，例如 `upload-server`。`Confirm` 用它调用 `HeadObject`，所需 RAM 动作为 `oss:GetObject`。`DirectUpload` 还需要 `oss:PutObject`。以下是测试环境的策略，生产仅提供凭据上传时保留 `oss:GetObject` 并替换为生产前缀。[HeadObject 权限](https://help.aliyun.com/zh/oss/developer-reference/headobject)

```json
{
  "Version": "1",
  "Statement": [{
    "Effect": "Allow",
    "Action": ["oss:GetObject", "oss:PutObject"],
    "Resource": ["acs:oss:*:*:<bucket>/test/config-file/*"]
  }]
}
```

使用浏览器 PUT 时，在 Bucket 的「跨域设置」新增规则。来源填写实际页面 Origin（含协议及非默认端口），允许方法选 `PUT`，允许 Headers 覆盖浏览器预检列出的请求头，包括签名使用的 `Authorization`、`x-oss-*` 和 `Content-Type`。前端需要读取响应标头时暴露 `ETag`、`x-oss-request-id`。用真实 Web Origin 做一次预检并上传。[阿里云 CORS 配置](https://help.aliyun.com/zh/oss/user-guide/configure-cross-origin-resource-sharing)

## 4. 准备实例、迁移与场景配置

以下文件均放在 `app.yaml` 旁。服务端 AccessKey 由部署系统填入受控配置文件。宿主可把 `CreateToken` 签发的临时凭据交给前端上传。凭据响应应避免写入应用、网关及代理的正文日志。

| 文件 | 要填写的值 |
| --- | --- |
| [mysql.default.yaml](../conf.example/mysql.default.yaml) | 已创建的业务数据库、连接地址和账号 |
| [mysql-migrate.default.yaml](../conf.example/mysql-migrate.default.yaml) | 首次部署或升级的迁移实例设 `enabled: true` 并授予相应 DDL 权限；普通副本设 `false` |
| [aliyunossx.default.yaml](../conf.example/aliyunossx.default.yaml) | 填 Bucket、地域和 **upload-server** 的 AK/SK；本例的实例名为 `default` |
| [aliyunstsx.oss.yaml](../conf.example/aliyunstsx.oss.yaml) | 填 STS 地域和 **upload-issuer** 的 AK/SK；实例名为 `oss` |
| [baize.oss.yaml](../conf.example/baize.oss.yaml) | 填 `stsRoleARN`，逐场景配置逻辑桶、目录、后缀、文件大小、批量上限和凭据时长；留空 ARN 时签发会失败 |

使用 PostgreSQL 时，将前两项换为 `postgresql.<实例名>.yaml` 和 `postgresql-migrate.<实例名>.yaml`。迁移实例需要建表、改列和维护索引的权限。

`ossboot.Prepare` 启动时读取并校验 `baize.oss.yaml`。文件缺失或场景无效会在打开仓储前报错。两个桶实例名相同表示只有公有桶，场景只能选 `bucketType: public`。需要私有桶时，为它单独提供 `aliyunossx.private.yaml`，用 `aliyunossboot.Instances("default", "private")` 声明，再将 `PrivateBucketName` 设为 `"private"`。记录保存申请时的逻辑桶类型，上线后应维持类型与物理桶的对应关系。

迁移开启时，Prepare 调用 `repository/oss.Open`，在数据库迁移锁内创建或更新 `baize_oss_upload_records`。迁移关闭时调用 `OpenExisting`，查询表的全部模型列并读取零行，空表可启动，缺表或缺列会报错。两个入口成功返回的 Store 都可以直接读写。直接构造 Store 时，将提供 `MasterDB(ctx)` 和 `SlaveDB(ctx)` 的客户端传给 `StoreConfig.Client`。

确认上传时预读记录、检查上传是否全部完成、生成下载地址，均使用实例配置的从库。配置多个从库时轮询选择，未配置时使用主库。上传登记、确认状态写入和迁移使用主库。复制延迟期间，确认上传可能返回 `oss.ErrNotFound`，`AllCompleted` 可能返回 `false, nil`。批量取地址时，结果会省略从库尚未读到的 ID，并按从库当前可见的对象 Key 生成地址。

上传记录使用应用服务器时间，同批写入共用一个微秒精度的时间。MySQL 时间列为 `TIMESTAMP(6)`，连接的 `location` 和实际会话时区须一致。PostgreSQL 使用 `timestamptz`。待确认记录的 `confirmed_at` 为 `NULL`，确认时与 `updated_at` 一起写入，直传成功时三个时间一致。

`publicBucketCDN` 用于拼接公有桶下载 URL，支持路径前缀，例如 `https://cdn.example.com/assets`。可以留空，生成公有下载地址时才要求配置。`privateBucketURLExpires` 设置私有桶 GET 签名有效期，默认 15m，范围为 1s 至 168h 的整秒。

## 5. 声明 Prepare 并调用上传服务

导入 `bootstrap/ossboot` 完成装配。项目已声明的资源可直接按名引用。

```go
plan := bootstrap.Plan{
    Resources: []bootstrap.Resource{
        mysqlboot.Instances("default"),
        aliyunossboot.Instances("default"),
        aliyunstsboot.Instances("oss"),
    },
    Prepare: []bootstrap.Step{
        ossboot.Prepare(ossboot.Config{
            Backend:           constant.MySQL,
            DatabaseName:      "default",
            STSName:           "oss",
            PublicBucketName:  "default",
            PrivateBucketName: "default",
        }),
    },
}
```

PostgreSQL 的写法相同，将数据库资源换为 `postgresqlboot.Instances("default")`，并把 `Backend` 设为 `constant.PostgreSQL`。

Prepare 成功后，导入 `github.com/bpcoder16/baize/service/oss`，通过 `oss.Default()` 取得服务。

| 方法 | 用途 |
| --- | --- |
| `CreateToken(ctx, req)` | 签发指定对象的临时上传凭据并创建待确认记录 |
| `Confirm(ctx, req)` | 核对 OSS 对象并确认整批上传记录 |
| `DirectUpload(ctx, scene, file)` | 上传调用方传入的文件并登记成功记录 |
| `AllCompleted(ctx, scene, ids)` | 按去重后的 ID 批量检查记录是否全部存在、上传成功且属于指定场景。场景按记录值忽略大小写匹配，已停用的场景也可查询 |
| `DisplayURLMap(ctx, ids)` | 从库按 ID 查询并返回展示地址。优先使用 `watermark_object_key`，为空时使用 `object_key` |
| `OriginURLMap(ctx, ids)` | 从库按 ID 查询并返回原始对象地址，始终使用 `object_key` |
| `DisplayURLMapOnMaster(ctx, ids)` | 从主库按 ID 查询并返回展示地址，适用于写入后立即查询 |
| `OriginURLMapOnMaster(ctx, ids)` | 从主库按 ID 查询并返回原始对象地址，适用于写入后立即查询 |

批量地址方法返回 `map[uint64]string`。空输入返回空 map，缺失的 ID 跳过，重复 ID 只占一项。查询或生成任一地址失败时返回 `nil, err`。待确认记录也可生成地址，需要确认状态和场景时可先调用 `AllCompleted`。

调用前由宿主校验场景和记录归属。宿主通过 HTTP 提供上传时，由宿主负责鉴权、请求体限制、直传启用条件和响应格式。

## 6. 验收服务与上传记录

1. 启动迁移实例。白泽在选定主库管理独立表 `baize_oss_upload_records`，随后发布上传服务。正常上传需要对该表执行 SELECT、INSERT、UPDATE。开启迁移的账号还要能建表、改列及维护索引。普通副本以零行查询检查已有表及所需列。迁移开关关闭且表或列缺失时，启动会报告错误。
2. 调用 `CreateToken`，传入 `config-file` 场景和一个扩展名为 `.json`、大小为 2 字节的文件。成功时取得临时凭据、过期时间、Bucket 和每个文件对应的 `ID`、`ObjectKey`。
3. 客户端使用临时凭据、Endpoint、Bucket 和 Key 执行一次 OSS PUT，正文可用 `{}`。然后以申请返回的记录 ID 调用 `Confirm`。在库中核对 `status=2`、`actual_file_size=2` 和 `confirmed_at`。重复确认会返回 `oss.ErrState`，批量确认有一条失败时整批保留原状态。
4. 用 `config-file` 场景和 `*multipart.FileHeader` 调用 `DirectUpload`。结果含 `ID` 和 `ObjectKey`，记录直接进入成功状态。
5. 为记录填写 `watermark_object_key` 后，以同一 ID 调用 `DisplayURLMap` 和 `OriginURLMap`，核对展示地址指向水印对象、原始地址指向原对象。biucards 只复用底层 `service/oss`，其上传接口和 `common_oss_upload_records` 仍由项目自身管理。

`maxSize` 用于校验申请大小与服务端直传文件；STS PUT 本身不约束对象实际大小，确认会单独登记 OSS 返回的实际字节数。若签发后落库失败，服务返回错误。若服务端直传成功后落库失败，OSS 上可能已有对象。

| 现象 | 从哪里核对 |
| --- | --- |
| 申请凭据时返回 `oss.ErrSize` | 核对场景配置的文件大小和数量限制。若签发时 STS 策略超过 2048 字节，可减少每次申请的文件数 |
| `CreateToken` 请求被 STS 拒绝 | 检查签发用户的 AssumeRole 权限、角色信任策略、ARN 和会话时长 |
| `CreateToken` 成功但客户端 PUT 返回 403 | 核对角色允许的 Bucket、环境目录、临时 SecurityToken、过期时间和返回的完整 Key |
| Web 预检失败 | 核对浏览器 Origin、PUT 和预检实际携带的请求头 |
| PUT 成功但确认失败 | 检查后端 OSS 身份的 GetObject 权限、记录对应的桶、对象是否存在，以及记录是否仍待确认 |
