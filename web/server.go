package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/injoyai/tdx"
	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

var (
	client      *tdx.Client
	manager     *tdx.Manage
	taskManager = NewTaskManager()
)

func init() {
	var err error
	// 连接通达信服务器
	client, err = tdx.DialDefault(tdx.WithDebug(false))
	if err != nil {
		log.Fatalf("连接服务器失败: %v", err)
	}
	log.Println("成功连接到通达信服务器")

	// 初始化代码缓存
	if err = os.MkdirAll(tdx.DefaultDatabaseDir, 0755); err != nil {
		log.Printf("创建数据目录失败: %v", err)
	}
	codes, err := tdx.NewCodesSqlite(tdx.WithCodesClient(client))
	if err != nil {
		log.Printf("初始化代码库失败: %v", err)
		codes = nil
	} else {
		tdx.DefaultCodes = codes
		if err := codes.Update(); err != nil {
			log.Printf("更新代码库失败: %v", err)
		} else {
			n := 0
			for range tdx.DefaultCodes.Iter() {
				n++
			}
			log.Printf("已加载股票代码，共 %d 条", n)
		}
	}

	// 新版库：NewManage 使用函数式选项；传入已初始化的 codes 共享同一 sqlite 代码库
	op := []tdx.Option{tdx.WithClients(4)}
	if codes != nil {
		op = append(op, tdx.WithCodes(codes))
	}
	manager, err = tdx.NewManage(op...)
	if err != nil {
		log.Fatalf("初始化数据管理器失败: %v", err)
	}
	if err := manager.Workday.Update(); err != nil {
		log.Printf("更新交易日数据失败: %v", err)
	}
	// 注：新版库 NewCodes/NewWorkday 构造时经 NewTimer 自行启动每日定时更新，无需 Cron.Start()
}

// Response 统一响应结构
type Response struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data"`
}

// 返回成功响应
func successResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(Response{
		Code:    0,
		Message: "success",
		Data:    data,
	})
}

// 返回错误响应
func errorResponse(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(Response{
		Code:    -1,
		Message: message,
		Data:    nil,
	})
}

// 获取五档行情
func handleGetQuote(w http.ResponseWriter, r *http.Request) {
	codeParam := r.URL.Query().Get("code")
	if codeParam == "" {
		errorResponse(w, "股票代码不能为空")
		return
	}

	codes := splitCodes(codeParam)
	if len(codes) == 0 {
		errorResponse(w, "股票代码不能为空")
		return
	}

	quotes, err := client.GetQuote(codes...)
	if err != nil {
		errorResponse(w, fmt.Sprintf("获取行情失败: %v", err))
		return
	}

	// 根据 Decimal 修正价格倍数（ETF 等 3 位小数品种需回退 ×10）
	for _, q := range quotes {
		adjustQuotePrice(q)
	}

	// 转换为老版本 JSON 结构（新库 Quote 字段漂移适配）
	successResponse(w, toQuoteDTOs(quotes))
}

// 获取K线数据（日K线默认使用前复权）
func handleGetKline(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	klineType := r.URL.Query().Get("type") // minute1/minute5/minute15/minute30/hour/day/week/month
	if code == "" {
		errorResponse(w, "股票代码不能为空")
		return
	}

	var resp *protocol.KlineResp
	var err error

	switch klineType {
	case "minute1":
		// 分钟K线不需要复权
		resp, err = client.GetKlineMinuteAll(code)
	case "minute5":
		resp, err = client.GetKline5MinuteAll(code)
	case "minute15":
		resp, err = client.GetKline15MinuteAll(code)
	case "minute30":
		resp, err = client.GetKline30MinuteAll(code)
	case "hour":
		resp, err = client.GetKlineHourAll(code)
	case "week":
		// 周K线使用前复权（从日K线转换）
		resp, err = getQfqKlineDay(code)
		if err == nil && len(resp.List) > 0 {
			// 将日K线转换为周K线（简化版：每5个交易日合并）
			resp = convertToWeekKline(resp)
		}
	case "month":
		// 月K线使用前复权（从日K线转换）
		resp, err = getQfqKlineDay(code)
		if err == nil && len(resp.List) > 0 {
			// 将日K线转换为月K线
			resp = convertToMonthKline(resp)
		}
	case "day":
		fallthrough
	default:
		// 日K线使用前复权数据
		resp, err = getQfqKlineDay(code)
	}

	if err != nil {
		errorResponse(w, fmt.Sprintf("获取K线失败: %v", err))
		return
	}

	successResponse(w, resp)
}

// getQfqKlineDay 获取前复权日K线数据
func getQfqKlineDay(code string) (*protocol.KlineResp, error) {
	// 使用同花顺API获取前复权数据（新版库返回 protocol.Klines，Time 为当日 15:00）
	klines, err := extend.GetTHSDayKline(code, extend.THS_QFQ)
	if err != nil {
		return nil, fmt.Errorf("获取前复权数据失败: %w", err)
	}

	if len(klines) == 0 {
		return nil, fmt.Errorf("同花顺前复权数据为空")
	}

	// 从 TDX 获取原始 K 线补充成交额（THS 接口不返回 Amount）
	tdxResp, tdxErr := client.GetKlineDayAll(code)
	amountMap := make(map[int64]protocol.Price)
	if tdxErr == nil && tdxResp != nil {
		for _, v := range tdxResp.List {
			amountMap[v.Time.Unix()] = v.Amount
		}
	}

	// 转换为 protocol.KlineResp 格式
	resp := &protocol.KlineResp{
		Count: uint16(len(klines)),
		List:  make([]*protocol.Kline, 0, len(klines)),
	}

	for i, k := range klines {
		pk := &protocol.Kline{
			Time:   k.Time,
			Open:   k.Open,
			High:   k.High,
			Low:    k.Low,
			Close:  k.Close,
			Volume: k.Volume,
			Amount: k.Amount,
		}
		// 从 TDX 数据补充成交额
		if pk.Amount == 0 && amountMap != nil {
			if amt, ok := amountMap[k.Time.Unix()]; ok {
				pk.Amount = amt
			}
		}
		// 设置昨收价（使用上一条K线的收盘价）
		if i > 0 {
			pk.Last = klines[i-1].Close
		}
		resp.List = append(resp.List, pk)
	}

	return resp, nil
}

// convertToWeekKline 将日K线转换为周K线（简化版）
func convertToWeekKline(dayKline *protocol.KlineResp) *protocol.KlineResp {
	if len(dayKline.List) == 0 {
		return dayKline
	}

	weekResp := &protocol.KlineResp{
		List: make([]*protocol.Kline, 0),
	}

	var currentWeek *protocol.Kline
	var lastWeekDay time.Time

	for _, k := range dayKline.List {
		year, week := k.Time.ISOWeek()

		// 判断是否是新的一周
		if currentWeek == nil || lastWeekDay.Year() != year || getISOWeek(lastWeekDay) != week {
			// 保存上一周的数据
			if currentWeek != nil {
				weekResp.List = append(weekResp.List, currentWeek)
			}
			// 创建新周
			currentWeek = &protocol.Kline{
				Time:   k.Time,
				Last:   k.Last,
				Open:   k.Open,
				High:   k.High,
				Low:    k.Low,
				Close:  k.Close,
				Volume: k.Volume,
				Amount: k.Amount,
			}
		} else {
			// 累积当周数据
			if k.High > currentWeek.High {
				currentWeek.High = k.High
			}
			if k.Low < currentWeek.Low || currentWeek.Low == 0 {
				currentWeek.Low = k.Low
			}
			currentWeek.Close = k.Close
			currentWeek.Volume += k.Volume
			currentWeek.Amount += k.Amount
			currentWeek.Time = k.Time // 使用最后一天的时间
		}
		lastWeekDay = k.Time
	}

	// 添加最后一周
	if currentWeek != nil {
		weekResp.List = append(weekResp.List, currentWeek)
	}

	weekResp.Count = uint16(len(weekResp.List))
	return weekResp
}

// convertToMonthKline 将日K线转换为月K线
func convertToMonthKline(dayKline *protocol.KlineResp) *protocol.KlineResp {
	if len(dayKline.List) == 0 {
		return dayKline
	}

	monthResp := &protocol.KlineResp{
		List: make([]*protocol.Kline, 0),
	}

	var currentMonth *protocol.Kline
	var lastMonthKey string

	for _, k := range dayKline.List {
		monthKey := k.Time.Format("200601") // YYYYMM

		// 判断是否是新的一月
		if currentMonth == nil || lastMonthKey != monthKey {
			// 保存上一月的数据
			if currentMonth != nil {
				monthResp.List = append(monthResp.List, currentMonth)
			}
			// 创建新月
			currentMonth = &protocol.Kline{
				Time:   k.Time,
				Last:   k.Last,
				Open:   k.Open,
				High:   k.High,
				Low:    k.Low,
				Close:  k.Close,
				Volume: k.Volume,
				Amount: k.Amount,
			}
		} else {
			// 累积当月数据
			if k.High > currentMonth.High {
				currentMonth.High = k.High
			}
			if k.Low < currentMonth.Low || currentMonth.Low == 0 {
				currentMonth.Low = k.Low
			}
			currentMonth.Close = k.Close
			currentMonth.Volume += k.Volume
			currentMonth.Amount += k.Amount
			currentMonth.Time = k.Time // 使用最后一天的时间
		}
		lastMonthKey = monthKey
	}

	// 添加最后一月
	if currentMonth != nil {
		monthResp.List = append(monthResp.List, currentMonth)
	}

	monthResp.Count = uint16(len(monthResp.List))
	return monthResp
}

// getISOWeek 获取ISO周数
func getISOWeek(t time.Time) int {
	_, week := t.ISOWeek()
	return week
}

// 获取分时数据
func handleGetMinute(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	date := r.URL.Query().Get("date")
	if code == "" {
		errorResponse(w, "股票代码不能为空")
		return
	}

	resp, usedDate, err := getMinuteWithFallback(code, date)
	if err != nil {
		errorResponse(w, fmt.Sprintf("获取分时数据失败: %v", err))
		return
	}

	if resp == nil {
		successResponse(w, map[string]interface{}{
			"date":  usedDate,
			"Count": 0,
			"List":  []interface{}{},
		})
		return
	}

	successResponse(w, map[string]interface{}{
		"date":  usedDate,
		"Count": resp.Count,
		"List":  resp.List,
	})
}

// 获取分时成交
func handleGetTrade(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	date := r.URL.Query().Get("date")
	if code == "" {
		errorResponse(w, "股票代码不能为空")
		return
	}

	var resp *protocol.TradeResp
	var err error

	if date == "" {
		// 获取今日分时成交（最近1800条）
		resp, err = client.GetMinuteTrade(code, 0, 1800)
	} else {
		// 获取历史某天的分时成交
		resp, err = client.GetHistoryMinuteTradeDay(date, code)
	}

	if err != nil {
		errorResponse(w, fmt.Sprintf("获取分时成交失败: %v", err))
		return
	}

	for _, v := range resp.List {
		v.Time = normalizeTradeTime(v.Time)
	}

	successResponse(w, resp)
}

// 搜索股票代码
func handleSearchCode(w http.ResponseWriter, r *http.Request) {
	keyword := r.URL.Query().Get("keyword")
	if keyword == "" {
		errorResponse(w, "搜索关键词不能为空")
		return
	}

	keywordUpper := strings.ToUpper(keyword)
	results := []map[string]string{}
	seen := map[string]struct{}{}

	codeModels, err := getAllCodeModels()
	if err != nil {
		errorResponse(w, "搜索失败: "+err.Error())
		return
	}

	for _, model := range codeModels {
		fullCode := model.FullCode()
		if !protocol.IsStock(fullCode) {
			continue
		}
		if _, ok := seen[model.Code]; ok {
			continue
		}

		codeUpper := strings.ToUpper(model.Code)
		nameUpper := strings.ToUpper(model.Name)
		if strings.Contains(codeUpper, keywordUpper) || strings.Contains(nameUpper, keywordUpper) {
			results = append(results, map[string]string{
				"code":     model.Code,
				"name":     model.Name,
				"exchange": strings.ToLower(model.Exchange),
			})
			seen[model.Code] = struct{}{}
		}

		if len(results) >= 50 {
			break
		}
	}

	successResponse(w, results)
}

// 获取股票基本信息（整合多个接口）
func handleGetStockInfo(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		errorResponse(w, "股票代码不能为空")
		return
	}

	// 整合多个数据源
	result := make(map[string]interface{})

	// 1. 获取五档行情（老版本此处不做 Decimal 修正，保持一致）
	quotes, err := client.GetQuote(code)
	if err == nil && len(quotes) > 0 {
		result["quote"] = toQuoteDTO(quotes[0])
	}

	// 2. 获取最近30天的日K线（使用前复权）
	kline, err := getQfqKlineDay(code)
	if err == nil && len(kline.List) > 30 {
		// 只返回最近30条
		kline.List = kline.List[len(kline.List)-30:]
		kline.Count = 30
	}
	if err == nil {
		result["kline_day"] = kline
	}

	// 3. 获取今日分时数据
	minute, minuteDate, err := getMinuteWithFallback(code, "")
	if err == nil && minute != nil {
		result["minute"] = map[string]interface{}{
			"date":  minuteDate,
			"Count": minute.Count,
			"List":  minute.List,
		}
	}

	successResponse(w, result)
}

// pullKlineTypes 新版库 pull 任务支持的表类型映射（老版本 extend.KlineTableMap 的子集）
// 注意：新版库 v1 PullKline 仅支持 day/minute 两种类型
var pullKlineTypes = map[string]string{
	"day":    extend.Day,
	"minute": extend.Minute,
}

func handleCreatePullKlineTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errorResponse(w, "只支持POST请求")
		return
	}
	if manager == nil {
		errorResponse(w, "数据管理器未初始化")
		return
	}

	var req struct {
		Codes     []string `json:"codes"`
		Tables    []string `json:"tables"`
		Dir       string   `json:"dir"`
		Limit     int      `json:"limit"`
		StartDate string   `json:"start_date"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, "请求参数错误: "+err.Error())
		return
	}

	tables := req.Tables
	if len(tables) == 0 {
		tables = []string{"day"}
	} else {
		valid := make([]string, 0, len(tables))
		for _, v := range tables {
			if _, ok := pullKlineTypes[v]; ok {
				valid = append(valid, v)
			}
		}
		if len(valid) == 0 {
			errorResponse(w, "tables参数无效")
			return
		}
		tables = valid
	}
	types := make([]string, 0, len(tables))
	for _, v := range tables {
		types = append(types, pullKlineTypes[v])
	}

	dir := req.Dir
	if dir == "" {
		dir = filepath.Join(tdx.DefaultDatabaseDir, "kline")
	}

	startAt := time.Unix(0, 0)
	if req.StartDate != "" {
		var parsed bool
		for _, layout := range []string{"2006-01-02", "20060102"} {
			if t, err := time.ParseInLocation(layout, req.StartDate, time.Local); err == nil {
				startAt = t
				parsed = true
				break
			}
		}
		if !parsed {
			errorResponse(w, "start_date格式错误，应为YYYY-MM-DD或YYYYMMDD")
			return
		}
	}

	// 新版库：Limit（协程数量）对应 Goroutines 字段
	cfg := extend.PullKlineConfig{
		Codes:      req.Codes,
		Types:      types,
		Dir:        dir,
		Goroutines: req.Limit,
		StartAt:    startAt,
	}

	puller, err := extend.NewPullKline(cfg)
	if err != nil {
		errorResponse(w, "初始化拉取器失败: "+err.Error())
		return
	}

	taskID := taskManager.Run("pull_kline", func(ctx context.Context) error {
		// 新版库 Update 不感知 ctx（任务取消后当前轮会继续跑完），属已知差异
		_ = ctx
		return puller.Update(manager, true)
	})

	successResponse(w, map[string]string{
		"task_id": taskID,
	})
}

func handleCreatePullTradeTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		errorResponse(w, "只支持POST请求")
		return
	}
	if manager == nil {
		errorResponse(w, "数据管理器未初始化")
		return
	}

	var req struct {
		Code      string `json:"code"`
		Dir       string `json:"dir"`
		StartYear int    `json:"start_year"`
		EndYear   int    `json:"end_year"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, "请求参数错误: "+err.Error())
		return
	}

	if req.Code == "" {
		errorResponse(w, "code不能为空")
		return
	}

	dir := req.Dir
	if dir == "" {
		dir = filepath.Join(tdx.DefaultDatabaseDir, "trade")
	}

	// 新版库 PullTrade 无 StartYear/EndYear 字段，web 层按年循环 PullYear 复刻原语义
	puller := extend.NewPullTrade(dir)
	startYear := req.StartYear
	if startYear <= 0 {
		startYear = 2000
	}
	endYear := req.EndYear
	if endYear <= 0 {
		endYear = time.Now().Year()
	}

	taskID := taskManager.Run("pull_trade", func(ctx context.Context) error {
		for year := startYear; year <= endYear; year++ {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			if err := puller.PullYear(ctx, manager, year, req.Code); err != nil {
				return err
			}
		}
		return nil
	})

	successResponse(w, map[string]string{
		"task_id": taskID,
	})
}

func handleListTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		errorResponse(w, "只支持GET请求")
		return
	}

	tasks := taskManager.List()
	successResponse(w, tasks)
}

func handleTaskOperations(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
	path = strings.Trim(path, "/")
	if path == "" {
		http.NotFound(w, r)
		return
	}

	parts := strings.Split(path, "/")
	id := parts[0]

	if len(parts) == 2 && parts[1] == "cancel" {
		if r.Method != http.MethodPost {
			errorResponse(w, "取消任务仅支持POST")
			return
		}
		if ok := taskManager.Cancel(id); !ok {
			errorResponse(w, "任务不存在或已结束")
			return
		}
		successResponse(w, map[string]string{
			"task_id": id,
			"status":  string(TaskStatusCancelled),
		})
		return
	}

	if r.Method != http.MethodGet {
		errorResponse(w, "只支持GET请求")
		return
	}

	if task, ok := taskManager.Get(id); ok {
		successResponse(w, task)
		return
	}

	errorResponse(w, "任务不存在")
}

// adjustQuotePrice 根据 Decimal 修正 Quote 中 K 线的价格倍数
// 协议层 DecodeKline 统一 ×10 将分转为厘，但 ETF 等 3 位小数品种不应 ×10
func adjustQuotePrice(q *protocol.Quote) {
	if q == nil {
		return
	}
	decimal := getDecimal(q.Exchange.String() + q.Code)
	if decimal == 3 {
		q.Kline.Last /= 10
		q.Kline.Open /= 10
		q.Kline.High /= 10
		q.Kline.Low /= 10
		q.Kline.Close /= 10
	}
}

// adjustKlinePrices 批量修正 Kline 列表的价格倍数
func adjustKlinePrices(code string, list []*protocol.Kline) {
	decimal := getDecimal(code)
	if decimal == 3 {
		for _, k := range list {
			if k == nil {
				continue
			}
			k.Open /= 10
			k.High /= 10
			k.Low /= 10
			k.Close /= 10
			k.Last /= 10
			k.Amount /= 10
		}
	}
}

func getDecimal(fullCode string) int8 {
	if tdx.DefaultCodes != nil {
		if m := tdx.DefaultCodes.Get(fullCode); m != nil {
			return m.Decimal
		}
	}
	return 2
}

// normalizeTradeTime 统一成交时间到本地时区。
// 老版本协议把成交时间解码为 time.Local（RFC3339 带 +08:00），
// 新版本解码为 UTC（RFC3339 带 Z），墙上时钟一致；此处还原老版本行为。
func normalizeTradeTime(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.Local)
}

func splitCodes(param string) []string {
	parts := strings.Split(param, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		code := strings.TrimSpace(p)
		if code != "" {
			result = append(result, code)
		}
	}
	return result
}

func getMinuteWithFallback(code, date string) (*protocol.MinuteResp, string, error) {
	target := strings.TrimSpace(date)
	if target == "" {
		target = time.Now().Format("20060102")
		resp, err := client.GetMinute(code)
		return resp, target, err
	}

	resp, err := client.GetHistoryMinute(target, code)
	return resp, target, err
}

func main() {
	// 静态文件服务
	http.Handle("/", http.FileServer(http.Dir("./static")))

	// API路由
	http.HandleFunc("/api/quote", handleGetQuote)
	http.HandleFunc("/api/kline", handleGetKline)
	http.HandleFunc("/api/minute", handleGetMinute)
	http.HandleFunc("/api/trade", handleGetTrade)
	http.HandleFunc("/api/search", handleSearchCode)
	http.HandleFunc("/api/stock-info", handleGetStockInfo)
	http.HandleFunc("/api/codes", handleGetCodes)
	http.HandleFunc("/api/batch-quote", handleBatchQuote)
	http.HandleFunc("/api/kline-history", handleGetKlineHistory)
	http.HandleFunc("/api/index", handleGetIndex)
	http.HandleFunc("/api/index/all", handleGetIndexAll)
	http.HandleFunc("/api/market-stats", handleGetMarketStats)
	http.HandleFunc("/api/market-count", handleGetMarketCount)
	http.HandleFunc("/api/stock-codes", handleGetStockCodes)
	http.HandleFunc("/api/etf-codes", handleGetETFCodes)
	http.HandleFunc("/api/server-status", handleGetServerStatus)
	http.HandleFunc("/api/health", handleHealthCheck)
	http.HandleFunc("/api/etf", handleGetETFList)
	http.HandleFunc("/api/trade-history", handleGetTradeHistory)
	http.HandleFunc("/api/trade-history/full", handleGetTradeHistoryFull)
	http.HandleFunc("/api/minute-trade-all", handleGetMinuteTradeAll)
	http.HandleFunc("/api/kline-all", handleGetKlineAllTDX)
	http.HandleFunc("/api/kline-all/tdx", handleGetKlineAllTDX)
	http.HandleFunc("/api/kline-all/ths", handleGetKlineAllTHS)
	http.HandleFunc("/api/workday", handleGetWorkday)
	http.HandleFunc("/api/workday/range", handleGetWorkdayRange)
	http.HandleFunc("/api/income", handleGetIncome)
	http.HandleFunc("/api/tasks/pull-kline", handleCreatePullKlineTask)
	http.HandleFunc("/api/tasks/pull-trade", handleCreatePullTradeTask)
	http.HandleFunc("/api/tasks", handleListTasks)
	http.HandleFunc("/api/tasks/", handleTaskOperations)

	// 新增接口（新版协议库能力）
	http.HandleFunc("/tdx/hy", handleGetTdxHy)
	http.HandleFunc("/finance", handleGetFinance)

	port := ":8080"
	log.Printf("服务启动成功，访问 http://localhost%s\n", port)
	log.Fatal(http.ListenAndServe(port, nil))
}
