package service

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// TimePricing 时段定价。
//
// 语义：**渠道定价里配的价格一律视为「高峰价」**，落在 PeakWindows 之外的时刻
// 按 OffPeakMultiplier 打折。这样配置只需要一份价格 + 一个折扣，不用为每个
// 时段各配一套，也不会出现两套价格互相漂移。
//
// 这个模型直接对应 DeepSeek 官方的计费方式：高峰为北京时间周一至周五
// 9:00-12:00、14:00-18:00，空闲价正好是高峰价的一半（OffPeakMultiplier=0.5）。
//
// 未配置（nil）时一切照旧，不产生任何行为变化。
type TimePricing struct {
	// Timezone IANA 时区名。空值按 Asia/Shanghai 处理——时段定价目前只有国产
	// 模型在用，它们的窗口都是按北京时间定义的。
	Timezone string `json:"timezone,omitempty"`
	// OffPeakMultiplier 空闲时段折扣系数。必须落在 (0, 1]；越界或缺省视为 1（不打折）。
	OffPeakMultiplier float64 `json:"off_peak_multiplier"`
	// PeakWindows 高峰窗口。为空表示没有高峰时段，即全天空闲。
	PeakWindows []PeakWindow `json:"peak_windows,omitempty"`
}

// PeakWindow 一段高峰窗口。
type PeakWindow struct {
	// Days 生效的星期，1=周一 … 7=周日（ISO 8601）。为空表示每天。
	Days []int `json:"days,omitempty"`
	// Start/End 形如 "09:00"，闭开区间 [Start, End)。不支持跨天，
	// 跨天窗口请拆成两条（与分组级 peak_rate 的约定一致）。
	Start string `json:"start"`
	End   string `json:"end"`
}

const defaultTimePricingTimezone = "Asia/Shanghai"

// ParseTimePricing 解析 time_pricing 列。空串/非法 JSON 返回 nil（视为未配置）。
//
// 解析失败一律当作未配置而不是报错：定价链路在热路径上，一条脏配置不该让
// 所有请求失败——宁可按高峰价收费（偏贵但安全），也不能把网关打挂。
func ParseTimePricing(raw string) *TimePricing {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" || raw == "{}" {
		return nil
	}
	var tp TimePricing
	if err := json.Unmarshal([]byte(raw), &tp); err != nil {
		return nil
	}
	if !tp.valid() {
		return nil
	}
	return &tp
}

// valid 判断配置是否可用。
func (tp *TimePricing) valid() bool {
	if tp == nil {
		return false
	}
	if tp.OffPeakMultiplier <= 0 || tp.OffPeakMultiplier > 1 {
		return false
	}
	if len(tp.PeakWindows) == 0 {
		return false
	}
	for _, w := range tp.PeakWindows {
		s, ok1 := parseClockMinutes(w.Start)
		e, ok2 := parseClockMinutes(w.End)
		if !ok1 || !ok2 || s >= e {
			return false
		}
	}
	return true
}

// location 返回配置时区；解析不出来时退回北京时间。
func (tp *TimePricing) location() *time.Location {
	name := strings.TrimSpace(tp.Timezone)
	if name == "" {
		name = defaultTimePricingTimezone
	}
	if loc, err := time.LoadLocation(name); err == nil {
		return loc
	}
	// 容器里可能没装 tzdata 之外的时区库；退回固定 +08:00，
	// 总比按 UTC 判断窗口要接近事实。
	return time.FixedZone("CST", 8*3600)
}

// IsPeakAt 判断给定时刻是否落在高峰窗口内。
func (tp *TimePricing) IsPeakAt(t time.Time) bool {
	if !tp.valid() {
		return true // 未配置时一律按高峰价，等价于「不打折」
	}
	local := t.In(tp.location())
	weekday := int(local.Weekday())
	if weekday == 0 {
		weekday = 7 // time.Sunday 是 0，ISO 里周日是 7
	}
	minutes := local.Hour()*60 + local.Minute()

	for _, w := range tp.PeakWindows {
		if len(w.Days) > 0 && !containsInt(w.Days, weekday) {
			continue
		}
		s, ok1 := parseClockMinutes(w.Start)
		e, ok2 := parseClockMinutes(w.End)
		if !ok1 || !ok2 {
			continue
		}
		if minutes >= s && minutes < e {
			return true
		}
	}
	return false
}

// MultiplierAt 返回给定时刻应当施加的价格系数：高峰 1，空闲为 OffPeakMultiplier。
func (tp *TimePricing) MultiplierAt(t time.Time) float64 {
	if !tp.valid() {
		return 1
	}
	if tp.IsPeakAt(t) {
		return 1
	}
	return tp.OffPeakMultiplier
}

// parseClockMinutes 把 "09:00" 解析成当日分钟数。
func parseClockMinutes(s string) (int, bool) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 {
		return 0, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, false
	}
	if h < 0 || h > 24 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// applyTimePricing 把时段折扣应用到价格字段。
//
// **必须替换指针，不能通过指针改值。** ChannelModelPricing.Clone() 是浅拷贝
// （cp := p），所有 *float64 与缓存里的原值共享同一块内存；就地改值会永久污染
// 定价缓存，折扣在每次请求上复利叠加，直到缓存重载为止。
//
// 区间定价（Intervals）同样要处理：配了阶梯的模型，阶梯价才是真正生效的价格，
// 漏掉就等于让它们绕过了时段折扣。注意 Clone 里的 copy(cp.Intervals, ...) 也是
// 逐元素浅拷贝，所以区间内的指针同样是共享的。
func applyTimePricing(p *ChannelModelPricing, tp *TimePricing, now time.Time) {
	if p == nil || tp == nil {
		return
	}
	m := tp.MultiplierAt(now)
	if m == 1 {
		return
	}
	scaled := func(v *float64) *float64 {
		if v == nil {
			return nil
		}
		nv := *v * m
		return &nv
	}
	p.InputPrice = scaled(p.InputPrice)
	p.OutputPrice = scaled(p.OutputPrice)
	p.CacheWritePrice = scaled(p.CacheWritePrice)
	p.CacheReadPrice = scaled(p.CacheReadPrice)
	p.ImageInputPrice = scaled(p.ImageInputPrice)
	p.ImageOutputPrice = scaled(p.ImageOutputPrice)
	p.PerRequestPrice = scaled(p.PerRequestPrice)
	for i := range p.Intervals {
		iv := &p.Intervals[i]
		iv.InputPrice = scaled(iv.InputPrice)
		iv.OutputPrice = scaled(iv.OutputPrice)
		iv.CacheWritePrice = scaled(iv.CacheWritePrice)
		iv.CacheReadPrice = scaled(iv.CacheReadPrice)
		iv.PerRequestPrice = scaled(iv.PerRequestPrice)
	}
}
