package main

import (
	"net/http"
	"strings"

	"github.com/injoyai/tdx"
	"github.com/injoyai/tdx/protocol"
)

// 新增接口：基于新版协议库的行业归属与财务数据能力

// hyItem 行业归属条目
type hyItem struct {
	Market string `json:"market"` // sh / sz
	Code   string `json:"code"`   // 6位代码
	Name   string `json:"name"`   // 证券名称（来自代码库，可能为空）
	TdxHy  string `json:"tdx_hy"` // 通达信行业代码（T 前缀）
	SwHy   string `json:"sw_hy"`  // 申万行业代码（X 前缀）
}

// handleGetTdxHy GET /tdx/hy
// 可选参数：code=600519（精确匹配，可逗号分隔多个）、market=sh|sz
// 返回全市场代码→通达信行业/申万行业映射
func handleGetTdxHy(w http.ResponseWriter, r *http.Request) {
	if client == nil {
		errorResponse(w, "客户端未初始化")
		return
	}

	list, err := client.GetTdxHy()
	if err != nil {
		errorResponse(w, "获取行业归属失败: "+err.Error())
		return
	}
	if len(list) == 0 {
		errorResponse(w, "行业归属数据为空")
		return
	}

	// 过滤参数
	codeParam := strings.TrimSpace(r.URL.Query().Get("code"))
	var codeSet map[string]struct{}
	if codeParam != "" {
		codeSet = map[string]struct{}{}
		for _, c := range splitCodes(codeParam) {
			codeSet[strings.TrimPrefix(strings.TrimPrefix(strings.ToLower(c), "sh"), "sz")] = struct{}{}
		}
	}
	marketParam := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("market")))

	out := make([]hyItem, 0, len(list))
	for _, v := range list {
		market := "sz"
		if v.Market == 1 {
			market = "sh"
		}
		if marketParam != "" && marketParam != "all" && marketParam != market {
			continue
		}
		if codeSet != nil {
			if _, ok := codeSet[v.Code]; !ok {
				continue
			}
		}
		item := hyItem{
			Market: market,
			Code:   v.Code,
			TdxHy:  v.TdxHy,
			SwHy:   v.SwHy,
		}
		if tdx.DefaultCodes != nil {
			item.Name = tdx.DefaultCodes.GetName(market + v.Code)
		}
		out = append(out, item)
	}

	successResponse(w, map[string]interface{}{
		"count": len(out),
		"list":  out,
	})
}

// handleGetFinance GET /finance?exchange=sh&code=600519
// 返回指定证券的财务信息（流通股本、总股本、上市日期、净利润等）
func handleGetFinance(w http.ResponseWriter, r *http.Request) {
	if client == nil {
		errorResponse(w, "客户端未初始化")
		return
	}

	exchangeStr := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("exchange")))
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		errorResponse(w, "code 不能为空")
		return
	}

	exchange, err := parseExchange(exchangeStr)
	if err != nil {
		errorResponse(w, err.Error())
		return
	}

	fi, err := client.GetFinanceInfo(exchange, code)
	if err != nil {
		errorResponse(w, "获取财务信息失败: "+err.Error())
		return
	}
	if fi == nil {
		errorResponse(w, "未查询到财务信息")
		return
	}

	successResponse(w, toFinanceDTO(fi))
}

// parseExchange 解析 exchange 参数：sh/sz/bj（大小写均可）或 0/1/2
func parseExchange(s string) (protocol.Exchange, error) {
	switch s {
	case "", "0", "sz", "深圳", "深":
		return protocol.ExchangeSZ, nil
	case "1", "sh", "上海", "沪":
		return protocol.ExchangeSH, nil
	case "2", "bj", "北京":
		return protocol.ExchangeBJ, nil
	default:
		return 0, &httpError{"exchange 参数无效，应为 sh/sz/bj"}
	}
}

type httpError struct{ msg string }

func (e *httpError) Error() string { return e.msg }

// financeDTO 财务信息响应（snake_case，便于 Python 等调用方使用）
type financeDTO struct {
	Market             uint8   `json:"market"`
	Code               string  `json:"code"`
	LiuTongGuBen       float64 `json:"liutong_guben"`       // 流通股本
	ZongGuBen          float64 `json:"zong_guben"`          // 总股本
	Province           uint16  `json:"province"`            // 地域码
	Industry           uint16  `json:"industry"`            // 行业码
	UpdatedDate        uint32  `json:"updated_date"`        // 更新日期 YYYYMMDD
	IPODate            uint32  `json:"ipo_date"`            // 上市日期 YYYYMMDD
	GuoJiaGu           float64 `json:"guojiagu"`            // 国家股
	FaQiRenFaRenGu     float64 `json:"faqirenfarengu"`      // 发起人法人股
	FaRenGu            float64 `json:"farengu"`             // 法人股
	BGu                float64 `json:"bgu"`                 // B股
	HGu                float64 `json:"hgu"`                 // H股
	ZhiGongGu          float64 `json:"zhigonggu"`           // 职工股
	ZongZiChan         float64 `json:"zongzichan"`          // 总资产
	LiuDongZiChan      float64 `json:"liudongzichan"`       // 流动资产
	GuDingZiChan       float64 `json:"gudingzichan"`        // 固定资产
	WuXingZiChan       float64 `json:"wuxingzichan"`        // 无形资产
	GuDongRenShu       float64 `json:"gudongrenshu"`        // 股东户数
	LiuDongFuZhai      float64 `json:"liudongfuzhai"`       // 流动负债
	ChangQiFuZhai      float64 `json:"changqifuzhai"`       // 长期负债
	ZiBenGongJiJin     float64 `json:"zibengongjijin"`      // 资本公积金
	JingZiChan         float64 `json:"jingzichan"`          // 净资产
	ZhuYingShouRu      float64 `json:"zhuyingshouru"`       // 主营收入
	ZhuYingLiRun       float64 `json:"zhuyinglirun"`        // 主营利润
	YingShouZhangKuan  float64 `json:"yingshouzhangkuan"`   // 应收账款
	YingYeLiRun        float64 `json:"yingyelirun"`         // 营业利润
	TouZiShouYi        float64 `json:"touzishouyi"`         // 投资收益
	JingYingXianJinLiu float64 `json:"jingyingxianjinliu"`  // 经营现金流
	ZongXianJinLiu     float64 `json:"zongxianjinliu"`      // 总现金流
	CunHuo             float64 `json:"cunhuo"`              // 存货
	LiRunZongHe        float64 `json:"lirunzonghe"`         // 利润总额
	ShuiHouLiRun       float64 `json:"shuihoulirun"`        // 税后利润
	JingLiRun          float64 `json:"jinglirun"`           // 净利润
	WeiFenLiRun        float64 `json:"weifenlirun"`         // 未分配利润
}

func toFinanceDTO(fi *protocol.FinanceInfo) *financeDTO {
	return &financeDTO{
		Market:             fi.Market,
		Code:               fi.Code,
		LiuTongGuBen:       fi.LiuTongGuBen,
		ZongGuBen:          fi.ZongGuBen,
		Province:           fi.Province,
		Industry:           fi.Industry,
		UpdatedDate:        fi.UpdatedDate,
		IPODate:            fi.IPODate,
		GuoJiaGu:           fi.GuoJiaGu,
		FaQiRenFaRenGu:     fi.FaQiRenFaRenGu,
		FaRenGu:            fi.FaRenGu,
		BGu:                fi.BGu,
		HGu:                fi.HGu,
		ZhiGongGu:          fi.ZhiGongGu,
		ZongZiChan:         fi.ZongZiChan,
		LiuDongZiChan:      fi.LiuDongZiChan,
		GuDingZiChan:       fi.GuDingZiChan,
		WuXingZiChan:       fi.WuXingZiChan,
		GuDongRenShu:       fi.GuDongRenShu,
		LiuDongFuZhai:      fi.LiuDongFuZhai,
		ChangQiFuZhai:      fi.ChangQiFuZhai,
		ZiBenGongJiJin:     fi.ZiBenGongJiJin,
		JingZiChan:         fi.JingZiChan,
		ZhuYingShouRu:      fi.ZhuYingShouRu,
		ZhuYingLiRun:       fi.ZhuYingLiRun,
		YingShouZhangKuan:  fi.YingShouZhangKuan,
		YingYeLiRun:        fi.YingYeLiRun,
		TouZiShouYi:        fi.TouZiShouYi,
		JingYingXianJinLiu: fi.JingYingXianJinLiu,
		ZongXianJinLiu:     fi.ZongXianJinLiu,
		CunHuo:             fi.CunHuo,
		LiRunZongHe:        fi.LiRunZongHe,
		ShuiHouLiRun:       fi.ShuiHouLiRun,
		JingLiRun:          fi.JingLiRun,
		WeiFenLiRun:        fi.WeiFenLiRun,
	}
}
