package main

// 上游 injoyai/tdx extend/httpserver 接口 1:1 挂载。
//
// 路由清单与 extend/httpserver/server.go 的 registerRoutes 保持一致（逐条对应）。
// 上游新增路由时，需同步此清单。
//
// 例外（不在挂载清单内）：
//   - "GET /"        ：上游为 health 检查，本地保留老版静态 UI（/api/health 已覆盖健康检查）
//   - "/finance"     ：本地已有实现（financeDTO 字段格式），参数一致（exchange+code）
//   - "/tdx/hy"      ：本地已有实现（{count, list:[{code,name,tdx_hy,sw_hy}]} 格式）

import (
	"log"
	"net/http"

	"github.com/injoyai/tdx"
	"github.com/injoyai/tdx/extend/httpserver"
)

// upstreamRoutes 上游 httpserver 的全部路由（除 "GET /"、"/finance"、"/tdx/hy"）
var upstreamRoutes = []string{
	// 基础
	"/count", "/code", "/code/all", "/code/stocks", "/code/etfs", "/code/indexes",
	// 行情
	"/quote", "/call_auction", "/gbbq", "/company/category", "/company/content",
	// 分时/成交
	"/minute", "/minute/history", "/trade", "/trade/all", "/trade/history", "/trade/history/day",
	// K线
	"/kline", "/kline/all",
	"/kline/minute", "/kline/minute/all",
	"/kline/5minute", "/kline/5minute/all",
	"/kline/15minute", "/kline/15minute/all",
	"/kline/30minute", "/kline/30minute/all",
	"/kline/60minute", "/kline/60minute/all",
	"/kline/day", "/kline/day/all",
	"/kline/week", "/kline/week/all",
	"/kline/month", "/kline/month/all",
	"/kline/quarter", "/kline/quarter/all",
	"/kline/year", "/kline/year/all",
	// 指数
	"/index", "/index/all",
	"/index/minute", "/index/5minute", "/index/15minute", "/index/30minute", "/index/60minute",
	"/index/day", "/index/day/all",
	"/index/week/all", "/index/month/all", "/index/quarter/all", "/index/year/all",
	// 板块/报表
	"/block/data", "/block/data/index", "/block/file",
	"/report/file", "/zhb/files",
	"/tdx/zs", "/tdx/bk", "/tdx/stat", "/tdx/stat2", "/tdx/xgsg", "/spblock",
	// 扩展行情（需扩展行情连接池，连接失败时自动降级不可用）
	"/ex/markets", "/ex/count", "/ex/instruments", "/ex/quote", "/ex/quote_list",
	"/ex/bars", "/ex/minute", "/ex/minute/hist", "/ex/trade", "/ex/trade/hist", "/ex/bars/range",
}

// newUpstreamServer 创建上游 httpserver（withEx 控制是否启用扩展行情连接池）
func newUpstreamServer(withEx bool) (*httpserver.Server, error) {
	opts := []httpserver.Option{
		httpserver.WithHosts(tdx.Hosts...),
		httpserver.WithPoolSize(3),
		// 断线重连，与本地 client 行为一致
		httpserver.WithOptions(tdx.WithRedial(), tdx.WithDebug(false)),
	}
	if withEx {
		opts = append(opts, httpserver.WithExHqHosts(tdx.ExHosts...))
	}
	return httpserver.New(opts...)
}

// mountUpstreamAPI 将上游 httpserver 的全部路由挂载到 DefaultServeMux（1:1 代理）。
// 扩展行情连接失败时自动降级为仅标准行情（/ex/* 不可用），不影响服务启动。
func mountUpstreamAPI() {
	srv, err := newUpstreamServer(true)
	if err != nil {
		log.Printf("挂载上游API(含扩展行情)失败: %v，降级为仅标准行情重试", err)
		srv, err = newUpstreamServer(false)
		if err != nil {
			log.Fatalf("挂载上游API失败: %v", err)
		}
	}

	handler := srv.Handler()
	for _, path := range upstreamRoutes {
		http.Handle("GET "+path, handler)
	}
	log.Printf("上游API路由挂载完成: %d 条（1:1 对应 extend/httpserver）", len(upstreamRoutes))
}
