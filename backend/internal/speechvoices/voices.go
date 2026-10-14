package speechvoices

type Voice struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Language    string `json:"language"`
}

var Catalog = []Voice{
	{"warm", "晓晓 · 温柔女声", "亲切讲解，适合日常教学", "zh-CN"},
	{"bright", "云希 · 清朗男声", "自然交流，适合问答互动", "zh-CN"},
	{"gentle", "晓伊 · 轻柔女声", "舒缓清晰，适合耐心讲解", "zh-CN"},
	{"steady", "云扬 · 稳重男声", "条理清晰，适合正式授课", "zh-CN"},
	{"lively", "云夏 · 活力男声", "轻快交流，适合课堂互动", "zh-CN"},
	{"story", "云健 · 浑厚男声", "表达鲜明，适合故事与朗读", "zh-CN"},
}

func Valid(id string) bool {
	for _, v := range Catalog {
		if v.ID == id {
			return true
		}
	}
	return false
}
