# TASKS.md — HTTP 层移植清单（tdx-api → tdx-api-new）

> 目标：把 `zzyshawn/tdx-api`（web/ HTTP 层）搬到本仓库（新版协议库），接口路径与返回 JSON 与老版本完全一致，并新增 /tdx/hy、/finance 两个接口。
> 老仓库位置：`D:\mycode\tdx-api2\tdx-api`（HEAD=ea07dcc，含全部 6 个 bug 修复）。
> 修复 diff 来源：commit `200fc32`（涨速符号）+ `ea07dcc`（其余 5 个）。

## 一、协议库必要的适配（仅 2 个 bug 修复涉及，其余不动）

- [x] 1. 涨速符号修复（200fc32）：`protocol/model_quote.go` ReversedBytes9 `uint16→int16`，解码 `Uint16→Int16`；`protocol/unit.go` 新增 `Int16()` 辅助函数 ✅ 实测 /api/quote 与老版本值一致
- [x] 2. 交易日判定修复（ea07dcc #34）：`workday.go` 的 `Is()` 增加周末+法定节假日回退判断（缓存未命中时）✅ 实测 /api/workday?date=20261001 一致

## 二、web/ HTTP 层移植（独立 go module，replace => ../）

- [x] 3. web/go.mod（module web，依赖 uuid + 本库 replace）
- [x] 4. server.go 移植：
  - [x] 4.1 init() 适配：`NewCodesSqlite(WithCodesClient(client))`、`NewManage(WithClients(4), WithCodes(...))`、去掉 `manager.Cron.Start()`（新库经 NewTimer 自行调度）、代码计数改 `Iter()` 遍历
  - [x] 4.2 27 条路由原样保留
  - [x] 4.3 getQfqKlineDay 适配：新库 `GetTHSDayKline` 返回 `protocol.Klines`，Amount 补充逻辑保留（修复 #14）✅ 实测 /api/kline、/api/kline-all/ths 一致
  - [x] 4.4 Quote JSON DTO（web/dto.go）：镜像老 JSON（K 五价格、TotalHand←Kline.Volume、Amount←Kline.Amount.Float64()）✅ 实测 /api/quote、/api/batch-quote、/api/stock-info 逐字段一致
  - [x] 4.5 handleCreatePullKlineTask 适配：Tables→Types（仅支持 day/minute，见下方已知差异）、Limit→Goroutines、`Update(manager, true)` 执行；任务取消粒度降级（cancel 后标记但当前轮跑完）
  - [x] 4.6 handleCreatePullTradeTask 适配：web 层按年循环 `PullYear()` 复刻 StartYear/EndYear 语义
  - [x] 4.7 getAllCodeModels 改 `Iter()`；handleGetStockCodes/ETFCodes 用 `GetStockCodes()/GetETFCodes()`
  - [x] 4.8 handleGetIncome：新库 `DoIncomes(protocol.Klines, ...)` 直接传 resp.List ✅ 实测一致
- [x] 5. server_api_extended.go 移植（含 kline-history 日期过滤修复 #25）✅ 实测 start_date/end_date 过滤一致
- [x] 6. tasks.go 原样移植（仅依赖 uuid）
- [x] 7. static/ 原样移植（app.js 含图表挤压修复 #1，默认显示最近 120 根）
- [x] 8. adjustQuotePrice/adjustKlinePrices/getDecimal 移植（ETF 三位小数修复 #16）✅ 实测 sh510300 盘口与老版本一致

## 三、新增接口

- [x] 9. GET /tdx/hy ✅ 返回 5667 条（>5000）；抽查 000001=平安银行 T1001/X500102、600519=贵州茅台 T030501/X210205 正确；?code= ?market= 过滤可用
- [x] 10. GET /finance?exchange=sh&code=600519 ✅ liutong_guben=12.50亿、zong_guben=12.50亿、ipo_date=20010827、jinglirun/gudongrenshu/zongzichan 均非空

## 四、回归验证

- [x] 11. `go build ./...` 全仓通过（root + web 独立 module，go vet 通过）
- [x] 12. 老接口回归：35 组请求（老版本本地构建 :8081 vs 新版 :8082 逐接口 diff）——**JSON 结构零差异**，26→30 项逐字段一致（含新增字段后全兼容）。剩余差异均为数据/分类层，见「已知差异」
- [x] 13. /tdx/hy 5667 条，抽查通过
- [x] 14. /finance 600519 字段非空
- [x] 14.1 回归中发现并修复：成交时间时区（老版本 time.Local、新版本 UTC，墙上时钟一致）——web 层 `normalizeTradeTime()` 归一化，/api/trade、/api/trade-history、/api/minute-trade-all、/api/trade-history/full 四处生效，复测 PASS

## 五、收尾

- [x] 15. README 接口文档更新（HTTP API 章节 + 两个新接口示例）
- [x] 16. 推送 feat/web-api 分支到 tdx-api-new（不合 main）

## 已知差异（非格式变化，数据/分类层）

1. **ETF/基金分类超集**：新库 `IsETF` 覆盖 sh 50/51/52/53/56/58 + sz 15/16（老库仅 sh 51/56/58 + sz 15/16），/api/etf、/api/etf-codes 比老版本**多 262 只**（501/502/53x 等 LOF/基金，老集合 ⊂ 新集合）。列表顺序反映代码库当前数据序，与老版本的旧库快照顺序不同（limit=N 的前 N 项会不同）。
2. **代码库数据新鲜度**：新库代码表当日更新（52212 条 vs 老快照 52209 条），/api/codes 的 exchanges 计数、北交所多 3 只属正常数据演进。
3. **/api/market-stats 北交所分类**：新库北交所代码来自 tdxbjmore.cfg（无昨收价字段，LastPrice=0→计入 flat），老库来自实时代码表（有昨收→计入 up）。沪深统计一致。
4. **指数远古数据**：/api/index/all 的 1991/1994 年共 60 根 K 线 Volume 解码值与老版本不同（新库协议对远古数据解码差异），1995 年至今全部一致。
5. **/api/tasks/pull-kline 的 tables**：新库 v1 PullKline 仅支持 day/minute（老库 KlineTableMap 还支持 minute5/hour/week 等粒度表）；传其他值现在返回"tables参数无效"（老默认值 day 不受影响）。任务 cancel 后当前轮会继续跑完（新库 Update 不感知 ctx）。

## 六、上游 extend/httpserver 接口 1:1 挂载（2026-10-04 追加）

- [x] 17. `extend/httpserver/server.go` 新增导出方法 `Handler()`（additive，返回已注册全部路由的 mux，供嵌入复用）
- [x] 18. `web/upstream_api.go`：74 条上游路由 1:1 挂载（同路径/同参数/同响应格式），路由清单与 `registerRoutes` 逐条对应
  - 扩展行情连接池（`WithExHqHosts(tdx.ExHosts...)`，7727）；启动连接失败自动降级为仅标准行情，不影响服务启动
  - 上游连接池独立（poolSize=3，`WithRedial()`），与老接口的 client 互不影响
- [x] 19. 端口支持 `PORT` 环境变量（缺省仍 `:8080`）
- [x] 20. 实测：/count?exchange=sh、/quote?codes=多标的、/call_auction、/minute、/kline/day/all（6015 根）、/index/day/all（8736 根）、/code/all?exchange=sz（2MB）、/ex/markets、/spblock 全部 200 且格式为上游 `{"code":0,"msg":"ok",...}`；静态 UI `/` 与 /api/*、/tdx/hy、/finance 不受影响
- 例外：`GET /`（上游 health）不挂载（`/` 保留静态 UI）
- [x] 21. （用户追加决策）`/finance`、`/tdx/hy` 改为上游原生格式：从本地 DTO 实现切换为随上游挂载（76 条），删除 `web/server_api_new.go`（本地 handleGetTdxHy/handleGetFinance/financeDTO）。实测：
  - /finance?exchange=sh&code=600519 → 原生 FinanceInfo（LiuTongGuBen=1250081562.5、IPODate=20010827 等，字段名 Go 原生大驼峰）
  - /tdx/hy → 原生数组 5667 条，000001=T1001/X500102、600519=T030501/X210205 抽查正确
  - 注意：本地 /api/* 信封是 {"code":0,"message":"success"}，上游族是 {"code":0,"msg":"ok"}，两族各自保持与参照物一致

## 测试环境备注

- 本机无 Go 工具链，已装用户级 Go 1.24.6 到 `D:\6agents\workboddy-config\binaries\go\`（GOPROXY=goproxy.cn）
- 回测基线：老仓库源码临时副本（端口 8081） vs 本仓库移植版临时副本（端口 8082），8080 被本机 CCProxy 占用，故用临时端口；原仓库零改动
