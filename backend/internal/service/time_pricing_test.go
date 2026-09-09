//go:build unit

package service

import (
	"testing"
	"time"
)

// deepseekTimePricing 照 DeepSeek 官方的计费方式构造：
// 高峰为北京时间周一至周五 9:00-12:00、14:00-18:00，空闲价是高峰价的一半。
func deepseekTimePricing() *TimePricing {
	return &TimePricing{
		Timezone:          "Asia/Shanghai",
		OffPeakMultiplier: 0.5,
		PeakWindows: []PeakWindow{
			{Days: []int{1, 2, 3, 4, 5}, Start: "09:00", End: "12:00"},
			{Days: []int{1, 2, 3, 4, 5}, Start: "14:00", End: "18:00"},
		},
	}
}

func beijing(t *testing.T, s string) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	ts, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return ts
}

func TestTimePricingPeakWindows(t *testing.T) {
	tp := deepseekTimePricing()

	cases := []struct {
		name   string
		at     string // 北京时间
		isPeak bool
	}{
		// 2026-09-09 是周三
		{"周三 09:00 高峰起点（含）", "2026-09-09 09:00", true},
		{"周三 11:59 高峰内", "2026-09-09 11:59", true},
		{"周三 12:00 高峰终点（不含）", "2026-09-09 12:00", false},
		{"周三 13:59 午休空闲", "2026-09-09 13:59", false},
		{"周三 14:00 第二段高峰起点", "2026-09-09 14:00", true},
		{"周三 17:59 高峰内", "2026-09-09 17:59", true},
		{"周三 18:00 高峰结束", "2026-09-09 18:00", false},
		{"周三 03:00 凌晨空闲", "2026-09-09 03:00", false},
		{"周三 08:59 开盘前一分钟", "2026-09-09 08:59", false},
		// 2026-09-12 周六 / 2026-09-13 周日：整天空闲
		{"周六 10:00 周末不算高峰", "2026-09-12 10:00", false},
		{"周日 15:00 周末不算高峰", "2026-09-13 15:00", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := tp.IsPeakAt(beijing(t, c.at)); got != c.isPeak {
				t.Fatalf("IsPeakAt(%s) = %v, want %v", c.at, got, c.isPeak)
			}
		})
	}
}

func TestTimePricingMultiplier(t *testing.T) {
	tp := deepseekTimePricing()
	if got := tp.MultiplierAt(beijing(t, "2026-09-09 10:00")); got != 1 {
		t.Fatalf("高峰系数 = %v, want 1", got)
	}
	if got := tp.MultiplierAt(beijing(t, "2026-09-09 22:00")); got != 0.5 {
		t.Fatalf("空闲系数 = %v, want 0.5", got)
	}
}

// 时区必须按配置解释：同一个 UTC 时刻在不同时区落在不同窗口里。
func TestTimePricingUsesConfiguredTimezone(t *testing.T) {
	tp := deepseekTimePricing()
	// 北京时间 2026-09-09 10:00 == UTC 02:00，按 UTC 判断会落在空闲。
	utc := time.Date(2026, 9, 9, 2, 0, 0, 0, time.UTC)
	if !tp.IsPeakAt(utc) {
		t.Fatal("应按 Asia/Shanghai 判断窗口，而不是 UTC")
	}
}

func TestParseTimePricingRejectsBadConfig(t *testing.T) {
	bad := []string{
		"",
		"null",
		"{}",
		`not json`,
		// 折扣越界：>1 等于「空闲反而更贵」，一定是配错了
		`{"off_peak_multiplier":1.5,"peak_windows":[{"start":"09:00","end":"12:00"}]}`,
		`{"off_peak_multiplier":0,"peak_windows":[{"start":"09:00","end":"12:00"}]}`,
		`{"off_peak_multiplier":-0.5,"peak_windows":[{"start":"09:00","end":"12:00"}]}`,
		// 没有窗口 = 无从判断高峰
		`{"off_peak_multiplier":0.5,"peak_windows":[]}`,
		// 起点晚于终点（不支持跨天，须拆两条）
		`{"off_peak_multiplier":0.5,"peak_windows":[{"start":"22:00","end":"02:00"}]}`,
		`{"off_peak_multiplier":0.5,"peak_windows":[{"start":"bad","end":"12:00"}]}`,
	}
	for _, raw := range bad {
		if got := ParseTimePricing(raw); got != nil {
			t.Fatalf("ParseTimePricing(%q) 应返回 nil，实际 %+v", raw, got)
		}
	}

	ok := ParseTimePricing(`{"timezone":"Asia/Shanghai","off_peak_multiplier":0.5,` +
		`"peak_windows":[{"days":[1,2,3,4,5],"start":"09:00","end":"12:00"}]}`)
	if ok == nil {
		t.Fatal("合法配置不该被拒")
	}
	if ok.OffPeakMultiplier != 0.5 {
		t.Fatalf("OffPeakMultiplier = %v, want 0.5", ok.OffPeakMultiplier)
	}
}

// 未配置时必须完全不改价：这个功能上线时线上没有任何 time_pricing，
// 行为必须与之前逐位相同。
func TestApplyTimePricingNilIsNoop(t *testing.T) {
	in, out := 3.0, 9.0
	p := &ChannelModelPricing{InputPrice: &in, OutputPrice: &out}
	applyTimePricing(p, nil, time.Now())
	if *p.InputPrice != 3.0 || *p.OutputPrice != 9.0 {
		t.Fatal("未配置时段定价时不应改价")
	}
}

func TestApplyTimePricingScalesAllFields(t *testing.T) {
	mk := func(v float64) *float64 { return &v }
	p := &ChannelModelPricing{
		InputPrice:       mk(4.0),
		OutputPrice:      mk(12.0),
		CacheWritePrice:  mk(4.0),
		CacheReadPrice:   mk(0.2),
		ImageInputPrice:  mk(1.0),
		ImageOutputPrice: mk(2.0),
		PerRequestPrice:  mk(40.0),
		Intervals: []PricingInterval{
			{TierLabel: "1K", PerRequestPrice: mk(39.5), InputPrice: mk(4.0)},
		},
	}
	applyTimePricing(p, deepseekTimePricing(), beijing(t, "2026-09-09 22:00"))

	want := map[string]struct{ got, want float64 }{
		"input":       {*p.InputPrice, 2.0},
		"output":      {*p.OutputPrice, 6.0},
		"cacheWrite":  {*p.CacheWritePrice, 2.0},
		"cacheRead":   {*p.CacheReadPrice, 0.1},
		"imageInput":  {*p.ImageInputPrice, 0.5},
		"imageOutput": {*p.ImageOutputPrice, 1.0},
		"perRequest":  {*p.PerRequestPrice, 20.0},
		"intervalReq": {*p.Intervals[0].PerRequestPrice, 19.75},
		"intervalIn":  {*p.Intervals[0].InputPrice, 2.0},
	}
	for name, c := range want {
		if c.got != c.want {
			t.Fatalf("%s = %v, want %v", name, c.got, c.want)
		}
	}
}

// 这是本功能最危险的一条。
//
// ChannelModelPricing.Clone() 是浅拷贝，所有 *float64 与缓存里的原值共享内存。
// 若 applyTimePricing 通过指针改值，缓存中的原始价格会被永久改掉，
// 折扣在每一次请求上复利叠加（0.5 → 0.25 → 0.125…）直到缓存重载。
func TestApplyTimePricingDoesNotMutateSharedPointers(t *testing.T) {
	original := 4.0
	cached := ChannelModelPricing{
		InputPrice: &original,
		Intervals:  []PricingInterval{{TierLabel: "1K", PerRequestPrice: &original}},
	}
	tp := deepseekTimePricing()
	offPeak := beijing(t, "2026-09-09 22:00")

	// 模拟热路径：反复 Clone 后打折
	for i := 0; i < 5; i++ {
		cp := cached.Clone()
		applyTimePricing(&cp, tp, offPeak)
		if *cp.InputPrice != 2.0 {
			t.Fatalf("第 %d 次取价 = %v, want 2.0（折扣被复利叠加了）", i+1, *cp.InputPrice)
		}
		if *cp.Intervals[0].PerRequestPrice != 2.0 {
			t.Fatalf("第 %d 次区间价 = %v, want 2.0", i+1, *cp.Intervals[0].PerRequestPrice)
		}
	}
	if original != 4.0 {
		t.Fatalf("缓存中的原始价被改成了 %v，必须保持 4.0", original)
	}
}
