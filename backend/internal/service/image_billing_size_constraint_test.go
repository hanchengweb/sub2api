//go:build unit

package service

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 数据库约束 usage_logs_image_billing_size_check 限定了 image_size 能存什么。
//
// 代码加了新档位、约束没跟上，后果是「余额照扣、调用记录插不进去」——扣费在写记录
// 之前，约束只拦得住后者。2026-09-18 加 1.5K 档时就这么漏了：B 端配的正是 1.5K，
// 之后每张图都扣了费却没有任何记录，后台看不到、对账对不上，直到 09-27 实测才发现。
//
// 这条测试读最后一个改过该约束的迁移，逐一检查代码能产出的档位标签都能落库。
// 以后再加档位，不同步改约束就过不了。
func TestImageBillingTiersSatisfyUsageLogConstraint(t *testing.T) {
	pattern := latestImageBillingSizeConstraintPattern(t)
	re := regexp.MustCompile(pattern)

	for _, tier := range []string{ImageBillingSize1K, ImageBillingSize1_5K, ImageBillingSize2K, ImageBillingSize4K} {
		for _, quality := range []string{"", ImageQualityLow, ImageQualityMedium, ImageQualityHigh} {
			label := ImageBillingTierWithQuality(tier, quality)
			require.Truef(t, re.MatchString(label),
				"档位 %q 过不了数据库约束 %q：会扣费但写不进 usage_logs", label, pattern)
		}
	}

	// 分档函数对各种输入尺寸的产出也要能落库。
	for _, size := range []string{"1k", "1.5k", "1536x1536", "1536x864", "2k", "2048x1152", "1792x1344", "4k", "3840x2160", "1024x1024", "5000x5000"} {
		tier, ok := ClassifyImageBillingTier(size)
		require.True(t, ok, size)
		require.Truef(t, re.MatchString(tier), "%s 分到的档位 %q 过不了数据库约束 %q", size, tier, pattern)
	}
}

// latestImageBillingSizeConstraintPattern 取最后一个定义该约束的迁移里的正则。
// 按文件名字典序，与迁移执行顺序一致。
func latestImageBillingSizeConstraintPattern(t *testing.T) string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.sql"))
	require.NoError(t, err)
	sort.Strings(files)

	extract := regexp.MustCompile(`image_size(?:::text)?\s*~\s*'([^']+)'`)
	pattern := ""
	for _, file := range files {
		raw, err := os.ReadFile(file)
		require.NoError(t, err)
		body := string(raw)
		if !strings.Contains(body, "usage_logs_image_billing_size_check") {
			continue
		}
		if m := extract.FindStringSubmatch(body); m != nil {
			pattern = m[1]
		}
	}
	require.NotEmpty(t, pattern, "没在迁移里找到 usage_logs_image_billing_size_check 的档位正则")
	return pattern
}
