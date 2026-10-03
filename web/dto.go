package main

import (
	"github.com/injoyai/tdx/protocol"
)

// quoteDTO 镜像老版本 tdx-api 的 protocol.Quote JSON 输出结构。
// 新版协议库中 Quote 结构发生漂移（K→Kline、移除 TotalHand、Amount 并入 Kline），
// 为保证线上调用方拿到的 JSON 与老版本完全一致，在 web 层做字段级转换。
type quoteDTO struct {
	Exchange       protocol.Exchange    // 市场
	Code           string               // 股票代码
	Active1        uint16               // 活跃度
	K              kDTO                 // k线（老版本字段名 K）
	ServerTime     string               // 时间
	ReversedBytes0 int                  // 保留
	ReversedBytes1 int                  // 保留
	TotalHand      int                  // 总手（新库移到 Kline.Volume，此处还原）
	Intuition      int                  // 现量
	Amount         float64              // 金额（新库为 Kline.Amount 厘，转回老版本的元）
	InsideDish     int                  // 内盘
	OuterDisc      int                  // 外盘
	ReversedBytes2 int                  // 保留
	ReversedBytes3 int                  // 保留
	BuyLevel       protocol.PriceLevels // 5档买盘
	SellLevel      protocol.PriceLevels // 5档卖盘
	ReversedBytes4 uint16               // 保留
	ReversedBytes5 int                  // 保留
	ReversedBytes6 int                  // 保留
	ReversedBytes7 int                  // 保留
	ReversedBytes8 int                  // 保留
	ReversedBytes9 int16                // 保留（涨速原始值，有符号）
	Rate           float64              // 涨速
	Active2        uint16               // 活跃度
}

// kDTO 镜像老版本 protocol.K（五档价格，单位厘）
type kDTO struct {
	Last  protocol.Price // 昨收
	Open  protocol.Price // 今开
	High  protocol.Price // 最高
	Low   protocol.Price // 最低
	Close protocol.Price // 今收
}

// toQuoteDTO 新版 Quote → 老版本 JSON 结构
func toQuoteDTO(q *protocol.Quote) *quoteDTO {
	if q == nil {
		return nil
	}
	d := &quoteDTO{
		Exchange:       q.Exchange,
		Code:           q.Code,
		Active1:        q.Active1,
		ServerTime:     q.ServerTime,
		ReversedBytes0: q.ReversedBytes0,
		ReversedBytes1: q.ReversedBytes1,
		Intuition:      q.Intuition,
		InsideDish:     q.InsideDish,
		OuterDisc:      q.OuterDisc,
		ReversedBytes2: q.ReversedBytes2,
		ReversedBytes3: q.ReversedBytes3,
		BuyLevel:       q.BuyLevel,
		SellLevel:      q.SellLevel,
		ReversedBytes4: q.ReversedBytes4,
		ReversedBytes5: q.ReversedBytes5,
		ReversedBytes6: q.ReversedBytes6,
		ReversedBytes7: q.ReversedBytes7,
		ReversedBytes8: q.ReversedBytes8,
		ReversedBytes9: q.ReversedBytes9,
		Rate:           q.Rate,
		Active2:        q.Active2,
	}
	if q.Kline != nil {
		d.K = kDTO{
			Last:  q.Kline.Last,
			Open:  q.Kline.Open,
			High:  q.Kline.High,
			Low:   q.Kline.Low,
			Close: q.Kline.Close,
		}
		d.TotalHand = int(q.Kline.Volume)
		d.Amount = q.Kline.Amount.Float64()
	}
	return d
}

func toQuoteDTOs(qs protocol.QuotesResp) []*quoteDTO {
	out := make([]*quoteDTO, 0, len(qs))
	for _, q := range qs {
		out = append(out, toQuoteDTO(q))
	}
	return out
}
