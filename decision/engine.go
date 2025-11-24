package decision

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"nofx/market"
	"nofx/mcp"
	"nofx/pool"
	"regexp"
	"strings"
	"sync"
	"time"
)

// 预编译正则表达式（性能优化：避免每次调用时重新编译）
var (
	// ✅ 安全的正則：精確匹配 ```json 代碼塊
	// 使用反引號 + 拼接避免轉義問題
	reJSONFence      = regexp.MustCompile(`(?is)` + "```json\\s*(\\[\\s*\\{.*?\\}\\s*\\])\\s*```")
	reJSONArray      = regexp.MustCompile(`(?is)\[\s*\{.*?\}\s*\]`)
	reArrayHead      = regexp.MustCompile(`^\[\s*\{`)
	reArrayOpenSpace = regexp.MustCompile(`^\[\s+\{`)
	reInvisibleRunes = regexp.MustCompile("[\u200B\u200C\u200D\uFEFF]")

	// 新增：XML标签提取（支持思维链中包含任何字符）
	reReasoningTag = regexp.MustCompile(`(?s)<reasoning>(.*?)</reasoning>`)
	reDecisionTag  = regexp.MustCompile(`(?s)<decision>(.*?)</decision>`)
)

// PositionInfo 持仓信息
type PositionInfo struct {
	Symbol           string  `json:"symbol"`
	Side             string  `json:"side"` // "long" or "short"
	EntryPrice       float64 `json:"entry_price"`
	MarkPrice        float64 `json:"mark_price"`
	Quantity         float64 `json:"quantity"`
	Leverage         int     `json:"leverage"`
	UnrealizedPnL    float64 `json:"unrealized_pnl"`
	UnrealizedPnLPct float64 `json:"unrealized_pnl_pct"`
	PeakPnLPct       float64 `json:"peak_pnl_pct"` // 历史最高收益率（百分比）
	LiquidationPrice float64 `json:"liquidation_price"`
	MarginUsed       float64 `json:"margin_used"`
	UpdateTime       int64   `json:"update_time"`           // 持仓更新时间戳（毫秒）
	StopLoss         float64 `json:"stop_loss,omitempty"`   // 止损价格（用于推断平仓原因）
	TakeProfit       float64 `json:"take_profit,omitempty"` // 止盈价格（用于推断平仓原因）
}

// AccountInfo 账户信息
type AccountInfo struct {
	TotalEquity      float64 `json:"total_equity"`      // 账户净值
	AvailableBalance float64 `json:"available_balance"` // 可用余额
	UnrealizedPnL    float64 `json:"unrealized_pnl"`    // 未实现盈亏
	TotalPnL         float64 `json:"total_pnl"`         // 总盈亏
	TotalPnLPct      float64 `json:"total_pnl_pct"`     // 总盈亏百分比
	MarginUsed       float64 `json:"margin_used"`       // 已用保证金
	MarginUsedPct    float64 `json:"margin_used_pct"`   // 保证金使用率
	PositionCount    int     `json:"position_count"`    // 持仓数量
}

// CandidateCoin 候选币种（来自币种池）
type CandidateCoin struct {
	Symbol  string   `json:"symbol"`
	Sources []string `json:"sources"` // 来源: "ai500" 和/或 "oi_top"
}

// OITopData 持仓量增长Top数据（用于AI决策参考）
type OITopData struct {
	Rank              int     // OI Top排名
	OIDeltaPercent    float64 // 持仓量变化百分比（1小时）
	OIDeltaValue      float64 // 持仓量变化价值
	PriceDeltaPercent float64 // 价格变化百分比
	NetLong           float64 // 净多仓
	NetShort          float64 // 净空仓
}

// Context 交易上下文（传递给AI的完整信息）
type Context struct {
	CurrentTime     string                  `json:"current_time"`
	RuntimeMinutes  int                     `json:"runtime_minutes"`
	CallCount       int                     `json:"call_count"`
	Account         AccountInfo             `json:"account"`
	Positions       []PositionInfo          `json:"positions"`
	CandidateCoins  []CandidateCoin         `json:"candidate_coins"`
	MarketDataMap   map[string]*market.Data `json:"-"` // 不序列化，但内部使用
	OITopDataMap    map[string]*OITopData   `json:"-"` // OI Top数据映射
	Performance     interface{}             `json:"-"` // 历史表现分析（logger.PerformanceAnalysis）
	BTCETHLeverage  int                     `json:"-"` // BTC/ETH杠杆倍数（从配置读取）
	AltcoinLeverage int                     `json:"-"` // 山寨币杠杆倍数（从配置读取）
	StrongSignals   []*market.TradingSignal `json:"-"` // 系统检测的强交易信号（信心度≥80%）
}

// Decision AI的交易决策
type Decision struct {
	Symbol string `json:"symbol"`
	Action string `json:"action"` // "open_long", "open_short", "close_long", "close_short", "update_stop_loss", "update_take_profit", "partial_close", "hold", "wait"

	// 开仓参数
	Leverage        int     `json:"leverage,omitempty"`
	PositionSizeUSD float64 `json:"position_size_usd,omitempty"`
	StopLoss        float64 `json:"stop_loss,omitempty"`
	TakeProfit      float64 `json:"take_profit,omitempty"`

	// 调整参数（新增）
	NewStopLoss     float64 `json:"new_stop_loss,omitempty"`    // 用于 update_stop_loss
	NewTakeProfit   float64 `json:"new_take_profit,omitempty"`  // 用于 update_take_profit
	ClosePercentage float64 `json:"close_percentage,omitempty"` // 用于 partial_close (0-100)

	// 通用参数
	Confidence int     `json:"confidence,omitempty"` // 信心度 (0-100)
	RiskUSD    float64 `json:"risk_usd,omitempty"`   // 最大美元风险
	Reasoning  string  `json:"reasoning"`
}

// FullDecision AI的完整决策（包含思维链）
type FullDecision struct {
	SystemPrompt string     `json:"system_prompt"` // 系统提示词（发送给AI的系统prompt）
	UserPrompt   string     `json:"user_prompt"`   // 发送给AI的输入prompt
	CoTTrace     string     `json:"cot_trace"`     // 思维链分析（AI输出）
	Decisions    []Decision `json:"decisions"`     // 具体决策列表
	Timestamp    time.Time  `json:"timestamp"`
	// AIRequestDurationMs 记录 AI API 调用耗时（毫秒）方便排查延迟问题
	AIRequestDurationMs int64 `json:"ai_request_duration_ms,omitempty"`
}

// GetFullDecision 获取AI的完整交易决策（批量分析所有币种和持仓）
func GetFullDecision(ctx *Context, mcpClient *mcp.Client) (*FullDecision, error) {
	return GetFullDecisionWithCustomPrompt(ctx, mcpClient, "", false, "")
}

// GetFullDecisionWithCustomPrompt 获取AI的完整交易决策（支持自定义prompt和模板选择）
func GetFullDecisionWithCustomPrompt(ctx *Context, mcpClient *mcp.Client, customPrompt string, overrideBase bool, templateName string) (*FullDecision, error) {
	// 1. 为所有币种获取市场数据
	if err := fetchMarketDataForContext(ctx); err != nil {
		return nil, fmt.Errorf("获取市场数据失败: %w", err)
	}

	// 2. 构建 System Prompt（固定规则）和 User Prompt（动态数据）
	systemPrompt := buildSystemPromptWithCustom(ctx.Account.TotalEquity, ctx.BTCETHLeverage, ctx.AltcoinLeverage, customPrompt, overrideBase, templateName)
	userPrompt := buildUserPrompt(ctx)

	// 3. 调用AI API（使用 system + user prompt）
	aiCallStart := time.Now()
	aiResponse, err := mcpClient.CallWithMessages(systemPrompt, userPrompt)
	aiCallDuration := time.Since(aiCallStart)
	if err != nil {
		return nil, fmt.Errorf("调用AI API失败: %w", err)
	}

	// 4. 解析AI响应
	decision, err := parseFullDecisionResponse(aiResponse, ctx.Account.TotalEquity, ctx.BTCETHLeverage, ctx.AltcoinLeverage)

	// 无论是否有错误，都要保存 SystemPrompt 和 UserPrompt（用于调试和决策未执行后的问题定位）
	if decision != nil {
		decision.Timestamp = time.Now()
		decision.SystemPrompt = systemPrompt // 保存系统prompt
		decision.UserPrompt = userPrompt     // 保存输入prompt
		decision.AIRequestDurationMs = aiCallDuration.Milliseconds()
	}

	if err != nil {
		return decision, fmt.Errorf("解析AI响应失败: %w", err)
	}

	decision.Timestamp = time.Now()
	decision.SystemPrompt = systemPrompt // 保存系统prompt
	decision.UserPrompt = userPrompt     // 保存输入prompt
	return decision, nil
}

// fetchMarketDataForContext 为上下文中的所有币种获取市场数据和OI数据
func fetchMarketDataForContext(ctx *Context) error {
	ctx.MarketDataMap = make(map[string]*market.Data)
	ctx.OITopDataMap = make(map[string]*OITopData)

	// 收集所有需要获取数据的币种
	symbolSet := make(map[string]bool)

	// 1. 优先获取持仓币种的数据（这是必须的）
	for _, pos := range ctx.Positions {
		symbolSet[pos.Symbol] = true
	}

	// 2. 候选币种数量根据账户状态动态调整
	maxCandidates := calculateMaxCandidates(ctx)
	for i, coin := range ctx.CandidateCoins {
		if i >= maxCandidates {
			break
		}
		symbolSet[coin.Symbol] = true
	}

	// 并发获取市场数据（优化：从串行改为并发，大幅提升速度）
	// 持仓币种集合（用于判断是否跳过OI检查）
	positionSymbols := make(map[string]bool)
	for _, pos := range ctx.Positions {
		positionSymbols[pos.Symbol] = true
	}

	// 使用goroutine并发获取数据，限制并发数为10
	type result struct {
		symbol string
		data   *market.Data
		err    error
	}

	resultChan := make(chan result, len(symbolSet))
	semaphore := make(chan struct{}, 10) // 限制最多10个并发请求
	var wg sync.WaitGroup

	for symbol := range symbolSet {
		wg.Add(1)
		go func(sym string) {
			defer wg.Done()

			// 获取信号量
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			data, err := market.Get(sym)
			resultChan <- result{symbol: sym, data: data, err: err}
		}(symbol)
	}

	// 等待所有goroutine完成
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// 收集结果并过滤
	const minOIThresholdMillions = 5.0 // 可調整：15M(保守) / 10M(平衡) / 8M(寬鬆) / 5M(激進)

	for res := range resultChan {
		if res.err != nil {
			// 单个币种失败不影响整体，只记录错误
			continue
		}

		// ⚠️ 流动性过滤：持仓价值低于阈值的币种不做（多空都不做）
		// 持仓价值 = 持仓量 × 当前价格
		// 但现有持仓必须保留（需要决策是否平仓）
		isExistingPosition := positionSymbols[res.symbol]
		if !isExistingPosition && res.data.OpenInterest != nil && res.data.CurrentPrice > 0 {
			// 计算持仓价值（USD）= 持仓量 × 当前价格
			oiValue := res.data.OpenInterest.Latest * res.data.CurrentPrice
			oiValueInMillions := oiValue / 1_000_000 // 转换为百万美元单位
			if oiValueInMillions < minOIThresholdMillions {
				log.Printf("⚠️  %s 持仓价值过低(%.2fM USD < %.1fM)，跳过此币种 [持仓量:%.0f × 价格:%.4f]",
					res.symbol, oiValueInMillions, minOIThresholdMillions, res.data.OpenInterest.Latest, res.data.CurrentPrice)
				continue
			}
		}

		ctx.MarketDataMap[res.symbol] = res.data
	}

	// 加载OI Top数据（不影响主流程）
	oiPositions, err := pool.GetOITopPositions()
	if err == nil {
		for _, pos := range oiPositions {
			// 标准化符号匹配
			symbol := pos.Symbol
			ctx.OITopDataMap[symbol] = &OITopData{
				Rank:              pos.Rank,
				OIDeltaPercent:    pos.OIDeltaPercent,
				OIDeltaValue:      pos.OIDeltaValue,
				PriceDeltaPercent: pos.PriceDeltaPercent,
				NetLong:           pos.NetLong,
				NetShort:          pos.NetShort,
			}
		}
	}

	return nil
}

// calculateMaxCandidates 根据账户状态计算需要分析的候选币种数量
func calculateMaxCandidates(ctx *Context) int {
	// ⚠️ 重要：限制候选币种数量，避免 Prompt 过大
	// 根据持仓数量动态调整：持仓越少，可以分析更多候选币
	const (
		maxCandidatesWhenEmpty    = 30 // 无持仓时最多分析30个候选币
		maxCandidatesWhenHolding1 = 25 // 持仓1个时最多分析25个候选币
		maxCandidatesWhenHolding2 = 20 // 持仓2个时最多分析20个候选币
		maxCandidatesWhenHolding3 = 15 // 持仓3个时最多分析15个候选币（避免 Prompt 过大）
	)

	positionCount := len(ctx.Positions)
	var maxCandidates int

	switch positionCount {
	case 0:
		maxCandidates = maxCandidatesWhenEmpty
	case 1:
		maxCandidates = maxCandidatesWhenHolding1
	case 2:
		maxCandidates = maxCandidatesWhenHolding2
	default: // 3+ 持仓
		maxCandidates = maxCandidatesWhenHolding3
	}

	// 返回实际候选币数量和上限中的较小值
	return min(len(ctx.CandidateCoins), maxCandidates)
}

// buildSystemPromptWithCustom 构建包含自定义内容的 System Prompt
func buildSystemPromptWithCustom(accountEquity float64, btcEthLeverage, altcoinLeverage int, customPrompt string, overrideBase bool, templateName string) string {
	// 如果覆盖基础prompt且有自定义prompt，只使用自定义prompt
	if overrideBase && customPrompt != "" {
		return customPrompt
	}

	// 获取基础prompt（使用指定的模板）
	basePrompt := buildSystemPrompt(accountEquity, btcEthLeverage, altcoinLeverage, templateName)

	// 如果没有自定义prompt，直接返回基础prompt
	if customPrompt == "" {
		return basePrompt
	}

	// 添加自定义prompt部分到基础prompt
	var sb strings.Builder
	sb.WriteString(basePrompt)
	sb.WriteString("\n\n")
	sb.WriteString("# 📌 个性化交易策略\n\n")
	sb.WriteString(customPrompt)
	sb.WriteString("\n\n")
	sb.WriteString("注意: 以上个性化策略是对基础规则的补充，不能违背基础风险控制原则。\n")

	return sb.String()
}

// buildSystemPrompt 构建 System Prompt（使用模板+动态部分）
func buildSystemPrompt(accountEquity float64, btcEthLeverage, altcoinLeverage int, templateName string) string {
	var sb strings.Builder

	// 1. 加载提示词模板（核心交易策略部分）
	if templateName == "" {
		templateName = "default" // 默认使用 default 模板
	}

	template, err := GetPromptTemplate(templateName)
	if err != nil {
		// 如果模板不存在，记录错误并使用 default
		log.Printf("⚠️  提示词模板 '%s' 不存在，使用 default: %v", templateName, err)
		template, err = GetPromptTemplate("default")
		if err != nil {
			// 如果连 default 都不存在，使用内置的简化版本
			log.Printf("❌ 无法加载任何提示词模板，使用内置简化版本")
			sb.WriteString("你是专业的加密货币交易AI。请根据市场数据做出交易决策。\n\n")
		} else {
			sb.WriteString(template.Content)
			sb.WriteString("\n\n")
		}
	} else {
		sb.WriteString(template.Content)
		sb.WriteString("\n\n")
	}

	// 1. 持仓保护铁律（最高优先级，必须首先执行）
	sb.WriteString("# 🛡️ 持仓保护铁律（最高优先级，有持仓时必须首先检查）\n\n")
	sb.WriteString("**决策优先级顺序：**\n")
	sb.WriteString("1. ⚠️ **持仓保护（保本止损）** ← 最优先，必须首先检查\n")
	sb.WriteString("2. 持仓平仓（反向信号）\n")
	sb.WriteString("3. 开新仓（反转信号验证）\n")
	sb.WriteString("4. 观望（wait）\n\n")

	sb.WriteString("## 保本止损强制规则\n\n")
	sb.WriteString("**核心原则：** 当持仓盈利达到覆盖手续费成本后，必须立即移动止损到保本价，确保最差情况零损失。\n\n")

	sb.WriteString("### 触发条件计算公式\n\n")
	sb.WriteString("**多单盈利计算：**\n")
	sb.WriteString("```\n")
	sb.WriteString("当前盈利% = (当前价格 - 开仓价格) / 开仓价格 × 100%\n")
	sb.WriteString("示例：开仓价100，当前价100.2，盈利 = (100.2-100)/100 = 0.2%\n")
	sb.WriteString("```\n\n")

	sb.WriteString("**空单盈利计算：**\n")
	sb.WriteString("```\n")
	sb.WriteString("当前盈利% = (开仓价格 - 当前价格) / 开仓价格 × 100%\n")
	sb.WriteString("示例：开仓价100，当前价99.8，盈利 = (100-99.8)/100 = 0.2%\n")
	sb.WriteString("```\n\n")

	sb.WriteString("### 保本价格计算\n\n")
	sb.WriteString("**多单保本止损：**\n")
	sb.WriteString("```\n")
	sb.WriteString("保本价 = 开仓价格 × 1.001（成本价上方0.1%）\n")
	sb.WriteString("逻辑：覆盖双向手续费（开仓0.05% + 平仓0.05% = 0.1%）\n")
	sb.WriteString("示例：开仓价100000，保本止损 = 100000 × 1.001 = 100100\n")
	sb.WriteString("```\n\n")

	sb.WriteString("**空单保本止损：**\n")
	sb.WriteString("```\n")
	sb.WriteString("保本价 = 开仓价格 × 0.999（成本价下方0.1%）\n")
	sb.WriteString("逻辑：覆盖双向手续费\n")
	sb.WriteString("示例：开仓价100000，保本止损 = 100000 × 0.999 = 99900\n")
	sb.WriteString("```\n\n")

	sb.WriteString("## 🔍 持仓保护强制检查清单（每次决策必须执行）\n\n")
	sb.WriteString("**如果有持仓，必须按以下步骤检查（优先于任何其他决策）：**\n\n")

	sb.WriteString("### 检查步骤（4步强制验证）\n\n")
	sb.WriteString("**第1步：遍历所有持仓**\n")
	sb.WriteString("- 对每个持仓执行以下检查\n\n")

	sb.WriteString("**第2步：计算当前盈利百分比**\n")
	sb.WriteString("- 多单：(当前价 - 开仓价) / 开仓价 × 100%\n")
	sb.WriteString("- 空单：(开仓价 - 当前价) / 开仓价 × 100%\n\n")

	sb.WriteString("**第3步：判断是否触发保本止损**\n")
	sb.WriteString("- ✅ 如果盈利 ≥ 0.15% → 必须移动止损到保本价\n")
	sb.WriteString("- ❌ 如果盈利 < 0.15% → 继续其他决策\n\n")

	sb.WriteString("**第4步：检查当前止损价格**\n")
	sb.WriteString("- 计算保本价（开仓价 × 1.001 或 × 0.999）\n")
	sb.WriteString("- 多单：如果当前止损 < 保本价，执行update_stop_loss\n")
	sb.WriteString("- 空单：如果当前止损 > 保本价，执行update_stop_loss\n")
	sb.WriteString("- ⚠️ 如果已经移动过，不重复移动\n\n")

	sb.WriteString("## ⚠️ 强制执行要求\n\n")
	sb.WriteString("- ✅ **必须优先**：即使发现新的交易机会，也必须先保护现有持仓\n")
	sb.WriteString("- ✅ **立即执行**：达到0.15%盈利时，当次决策就要输出update_stop_loss\n")
	sb.WriteString("- ✅ **单独决策**：如果有多个持仓需要保本止损，一次只处理一个\n")
	sb.WriteString("- ❌ **禁止忽略**：不能因为其他信号而跳过持仓保护检查\n")
	sb.WriteString("- ❌ **禁止延迟**：不能等到下次决策再移动止损\n\n")

	sb.WriteString("## 📖 保本止损示例\n\n")

	sb.WriteString("### ✅ 正确示例：立即保本\n\n")
	sb.WriteString("**当前持仓：** BTCUSDT 多单\n")
	sb.WriteString("- 开仓价：100000\n")
	sb.WriteString("- 当前价：100200\n")
	sb.WriteString("- 当前止损：99700（初始止损，成本下方0.3%）\n\n")

	sb.WriteString("**检查过程：**\n")
	sb.WriteString("1. 计算盈利：(100200-100000)/100000 = 0.2% ✅\n")
	sb.WriteString("2. 判断：0.2% ≥ 0.15% → 触发保本止损 ✅\n")
	sb.WriteString("3. 计算保本价：100000 × 1.001 = 100100\n")
	sb.WriteString("4. 检查：当前止损99700 < 保本价100100 → 需要移动 ✅\n\n")

	sb.WriteString("**输出格式：**\n")
	sb.WriteString("```json\n")
	sb.WriteString("{\n")
	sb.WriteString("  \"symbol\": \"BTCUSDT\",\n")
	sb.WriteString("  \"action\": \"update_stop_loss\",\n")
	sb.WriteString("  \"new_stop_loss\": 100100,\n")
	sb.WriteString("  \"reasoning\": \"持仓保护优先检查：BTCUSDT多单，开仓价100000，当前价100200，盈利0.2%≥0.15%触发保本止损。计算保本价=100000×1.001=100100。当前止损99700<保本价，立即移动止损到100100，确保零损失。\"\n")
	sb.WriteString("}\n")
	sb.WriteString("```\n\n")

	sb.WriteString("### ❌ 错误示例：忽略保本止损\n\n")
	sb.WriteString("**场景：** 持仓ETHUSDT多单，盈利0.3%，但AI看到BTCUSDT有新信号，忽略了保本止损\n\n")

	sb.WriteString("**错误输出：**\n")
	sb.WriteString("```json\n")
	sb.WriteString("{\n")
	sb.WriteString("  \"symbol\": \"BTCUSDT\",\n")
	sb.WriteString("  \"action\": \"open_long\",\n")
	sb.WriteString("  \"reasoning\": \"BTCUSDT看涨Pin Bar反转信号...\"\n")
	sb.WriteString("}\n")
	sb.WriteString("```\n")
	sb.WriteString("❌ **错误：** 没有优先保护ETHUSDT持仓！\n\n")

	sb.WriteString("**正确输出：**\n")
	sb.WriteString("```json\n")
	sb.WriteString("{\n")
	sb.WriteString("  \"symbol\": \"ETHUSDT\",\n")
	sb.WriteString("  \"action\": \"update_stop_loss\",\n")
	sb.WriteString("  \"new_stop_loss\": 2951.0,\n")
	sb.WriteString("  \"reasoning\": \"持仓保护优先：ETHUSDT盈利0.3%≥0.15%，立即移动止损到保本价2951.0。新信号BTCUSDT将在下次决策处理。\"\n")
	sb.WriteString("}\n")
	sb.WriteString("```\n")
	sb.WriteString("✅ **正确：** 优先保护现有持仓，新机会下次再看！\n\n")

	sb.WriteString("## 📝 reasoning格式强制要求\n\n")
	sb.WriteString("**每次决策的reasoning必须包含持仓保护检查（如果有持仓）：**\n\n")

	sb.WriteString("**格式模板：**\n")
	sb.WriteString("```\n")
	sb.WriteString("reasoning: \"持仓保护检查：[币种][多/空]单，开仓价[X]，当前价[Y]，盈利[Z]%。\n")
	sb.WriteString("  - 如果盈利≥0.15%：'触发保本止损，移动止损到保本价[价格]'\n")
	sb.WriteString("  - 如果盈利<0.15%：'未达保本触发条件，继续观察'\n")
	sb.WriteString("[然后才分析其他信号...]\"\n")
	sb.WriteString("```\n\n")

	sb.WriteString("**示例（有持仓）：**\n")
	sb.WriteString("```\n")
	sb.WriteString("reasoning: \"持仓保护检查：BTCUSDT多单，开仓价100000，当前价100200，盈利0.2%≥0.15%，触发保本止损，移动止损到保本价100100。\"\n")
	sb.WriteString("```\n\n")

	sb.WriteString("**示例（无持仓）：**\n")
	sb.WriteString("```\n")
	sb.WriteString("reasoning: \"无持仓，跳过保护检查。分析新信号：BTCUSDT看涨Pin Bar...\"\n")
	sb.WriteString("```\n\n")

	// 2. Pin Bar反转交易铁律（必须严格遵守）
	sb.WriteString("# ⚠️ Pin Bar反转交易铁律（绝对不可违反）\n\n")
	sb.WriteString("Pin Bar是**反转形态**，不是顺势形态。必须在趋势末端反向开仓。\n\n")

	sb.WriteString("## 反转交易定义\n\n")
	sb.WriteString("- **反转做多**：下跌趋势末端，出现看涨Pin Bar（长下影线），预期反转上涨 ✅\n")
	sb.WriteString("- **反转做空**：上涨趋势末端，出现看跌Pin Bar（长上影线），预期反转下跌 ✅\n")
	sb.WriteString("- **顺势追涨**：上涨趋势中，出现看涨信号，继续做多 ❌ **严禁**\n")
	sb.WriteString("- **顺势追跌**：下跌趋势中，出现看跌信号，继续做空 ❌ **严禁**\n\n")

	sb.WriteString("## 🔍 开仓前强制5步验证法\n\n")
	sb.WriteString("**每次遇到Pin Bar信号，必须完成以下5步验证，任何1步失败则输出wait：**\n\n")

	sb.WriteString("### 看涨Pin Bar（长下影线）开多单验证：\n\n")
	sb.WriteString("**第1步：近期趋势检查**\n")
	sb.WriteString("- ✅ 必须：最近3根K线整体下跌（收盘价逐步降低）\n")
	sb.WriteString("- ❌ 禁止：最近3根K线整体上涨（这是追涨）\n\n")

	sb.WriteString("**第2步：多周期趋势确认**\n")
	sb.WriteString("- ✅ 必须：至少1个更大周期（30m/1h/4h）处于下跌或盘整\n")
	sb.WriteString("- ❌ 禁止：所有更大周期都在上涨（强势上涨不是反转点）\n\n")

	sb.WriteString("**第3步：RSI超卖确认**\n")
	sb.WriteString("- ✅ 必须：RSI < 45（超卖或接近超卖）\n")
	sb.WriteString("- ❌ 禁止：RSI > 50（不是超卖区域，可能是追高）\n\n")

	sb.WriteString("**第4步：价格位置确认**\n")
	sb.WriteString("- ✅ 必须：当前价格 < EMA20（价格在均线下方）\n")
	sb.WriteString("- ❌ 禁止：当前价格 > EMA20（价格在均线上方是追高）\n\n")

	sb.WriteString("**第5步：连续K线检查**\n")
	sb.WriteString("- ✅ 必须：最近3根K线不能全部上涨\n")
	sb.WriteString("- ❌ 禁止：最近3根K线连续上涨（这是追涨行为）\n\n")

	sb.WriteString("---\n\n")

	sb.WriteString("### 看跌Pin Bar（长上影线）开空单验证：\n\n")
	sb.WriteString("**第1步：近期趋势检查**\n")
	sb.WriteString("- ✅ 必须：最近3根K线整体上涨（收盘价逐步升高）\n")
	sb.WriteString("- ❌ 禁止：最近3根K线整体下跌（这是追跌）\n\n")

	sb.WriteString("**第2步：多周期趋势确认**\n")
	sb.WriteString("- ✅ 必须：至少1个更大周期（30m/1h/4h）处于上涨或盘整\n")
	sb.WriteString("- ❌ 禁止：所有更大周期都在下跌（强势下跌不是反转点）\n\n")

	sb.WriteString("**第3步：RSI超买确认**\n")
	sb.WriteString("- ✅ 必须：RSI > 55（超买或接近超买）\n")
	sb.WriteString("- ❌ 禁止：RSI < 50（不是超买区域，可能是追跌）\n\n")

	sb.WriteString("**第4步：价格位置确认**\n")
	sb.WriteString("- ✅ 必须：当前价格 > EMA20（价格在均线上方）\n")
	sb.WriteString("- ❌ 禁止：当前价格 < EMA20（价格在均线下方是追跌）\n\n")

	sb.WriteString("**第5步：连续K线检查**\n")
	sb.WriteString("- ✅ 必须：最近3根K线不能全部下跌\n")
	sb.WriteString("- ❌ 禁止：最近3根K线连续下跌（这是追跌行为）\n\n")

	sb.WriteString("---\n\n")

	sb.WriteString("## ⚠️ 验证结果判定\n\n")
	sb.WriteString("- ✅ **5步全部满足** → 可以开仓\n")
	sb.WriteString("- ❌ **任何1步不满足** → 必须输出wait，禁止开仓\n\n")

	sb.WriteString("## ❌ 绝对禁止的顺势场景（负面清单）\n\n")
	sb.WriteString("以下场景严禁开仓，必须输出wait：\n\n")

	sb.WriteString("**禁止追涨做多：**\n")
	sb.WriteString("1. 看涨Pin Bar + 最近5根K线连续上涨 ❌\n")
	sb.WriteString("2. 看涨Pin Bar + RSI > 50 ❌\n")
	sb.WriteString("3. 看涨Pin Bar + 价格高于EMA20 ❌\n")
	sb.WriteString("4. 看涨Pin Bar + 1h周期上涨趋势 ❌\n")
	sb.WriteString("5. 看涨Pin Bar + 当前价格接近最近高点 ❌\n\n")

	sb.WriteString("**禁止追跌做空：**\n")
	sb.WriteString("1. 看跌Pin Bar + 最近5根K线连续下跌 ❌\n")
	sb.WriteString("2. 看跌Pin Bar + RSI < 50 ❌\n")
	sb.WriteString("3. 看跌Pin Bar + 价格低于EMA20 ❌\n")
	sb.WriteString("4. 看跌Pin Bar + 1h周期下跌趋势 ❌\n")
	sb.WriteString("5. 看跌Pin Bar + 当前价格接近最近低点 ❌\n\n")

	sb.WriteString("## 📖 反转交易示例\n\n")
	sb.WriteString("### ✅ 正确示例：反转做多\n\n")
	sb.WriteString("**场景：** BTCUSDT 15m出现看涨Pin Bar\n\n")
	sb.WriteString("**验证过程：**\n")
	sb.WriteString("- ✅ 第1步：最近3根K线从96000跌到93000（下跌趋势）\n")
	sb.WriteString("- ✅ 第2步：1h周期处于下跌（95000→92000）\n")
	sb.WriteString("- ✅ 第3步：RSI=38（超卖）\n")
	sb.WriteString("- ✅ 第4步：价格93500 < EMA20(94200)\n")
	sb.WriteString("- ✅ 第5步：最近3根K线：跌、跌、Pin Bar（非连续上涨）\n\n")
	sb.WriteString("**结论：** 5步全部满足，这是标准的反转抄底，可以开多单 ✅\n\n")
	sb.WriteString("**输出格式：**\n")
	sb.WriteString("```json\n")
	sb.WriteString("{\n")
	sb.WriteString("  \"symbol\": \"BTCUSDT\",\n")
	sb.WriteString("  \"action\": \"open_long\",\n")
	sb.WriteString("  \"reasoning\": \"看涨Pin Bar反转信号。验证：✅最近3根K线下跌(96000→93000)，✅1h下跌，✅RSI=38超卖，✅价格<EMA20，✅非连续上涨。满足反转条件，开多单抄底。\"\n")
	sb.WriteString("}\n")
	sb.WriteString("```\n\n")

	sb.WriteString("### ❌ 错误示例：顺势追高\n\n")
	sb.WriteString("**场景：** ETHUSDT 15m出现看涨Pin Bar\n\n")
	sb.WriteString("**验证过程：**\n")
	sb.WriteString("- ❌ 第1步：最近3根K线从2800涨到2950（上涨趋势）\n")
	sb.WriteString("- ❌ 第2步：1h周期处于上涨（2700→2950）\n")
	sb.WriteString("- ❌ 第3步：RSI=68（超买）\n")
	sb.WriteString("- ❌ 第4步：价格2950 > EMA20(2880)\n")
	sb.WriteString("- ❌ 第5步：最近3根K线：涨、涨、Pin Bar（连续上涨）\n\n")
	sb.WriteString("**结论：** 5步全部失败，这是追涨行为，严禁开仓 ❌\n\n")
	sb.WriteString("**输出格式：**\n")
	sb.WriteString("```json\n")
	sb.WriteString("{\n")
	sb.WriteString("  \"symbol\": \"ETHUSDT\",\n")
	sb.WriteString("  \"action\": \"wait\",\n")
	sb.WriteString("  \"reasoning\": \"看涨Pin Bar信号，但反转验证失败：❌最近3根K线上涨(2800→2950)，❌1h上涨，❌RSI=68超买，❌价格>EMA20，❌连续上涨。这是追涨行为，不符合反转策略，禁止开仓。\"\n")
	sb.WriteString("}\n")
	sb.WriteString("```\n\n")

	sb.WriteString("## 📝 输出格式强制要求\n\n")
	sb.WriteString("**每次遇到Pin Bar信号时，reasoning必须包含5步验证过程：**\n\n")
	sb.WriteString("格式模板：\n")
	sb.WriteString("```\n")
	sb.WriteString("reasoning: \"看涨/看跌Pin Bar信号。反转验证：✅/❌近期趋势(价格走势)，✅/❌多周期确认(1h/4h状态)，✅/❌RSI值(超买/超卖)，✅/❌价格vs均线，✅/❌连续K线检查。结论：[满足反转条件/顺势追涨追跌，禁止开仓]\"\n")
	sb.WriteString("```\n\n")
	sb.WriteString("**如果5步全部✅** → action可以是open_long/open_short\n")
	sb.WriteString("**如果任何一步❌** → action必须是wait\n\n")

	// 3. 硬约束（风险控制）- 动态生成
	sb.WriteString("# 硬约束（风险控制）\n\n")
	sb.WriteString("1. 风险回报比: 必须 ≥ 1:3（冒1%风险，赚3%+收益）\n")
	sb.WriteString("2. 最多持仓: 2个币种（质量>数量）\n")
	sb.WriteString(fmt.Sprintf("3. 单币仓位上限: 山寨%.0f U | BTC/ETH %.0f U（基于净值的仓位上限，实际开仓还需考虑可用余额）\n", accountEquity*4.5, accountEquity*10))
	sb.WriteString(fmt.Sprintf("4. 杠杆限制: **山寨币最大%dx杠杆** | **BTC/ETH最大%dx杠杆** (⚠️ 严格执行，不可超过)\n", altcoinLeverage, btcEthLeverage))
	sb.WriteString("5. 保证金: 总使用率 ≤ 90%\n")
	sb.WriteString("6. 开仓金额: 建议 **≥12 USDT** (交易所最小名义价值 10 USDT + 安全边际)\n")
	sb.WriteString("7. ⚠️ **开仓保证金检查**: 开仓前必须确保 `所需保证金 ≤ 可用余额`，所需保证金 = position_size_usd / leverage + 手续费\n\n")

	// 8. 止盈止损策略（基于形态的精确设置）
	sb.WriteString("# 止盈止损策略（严格执行）\n\n")
	sb.WriteString("## 止损位置（基于K线形态）\n\n")
	sb.WriteString("**Pin Bar形态：**\n")
	sb.WriteString("- 做多：止损设在Pin Bar最低点下方 **0.2%**（紧贴形态）\n")
	sb.WriteString("- 做空：止损设在Pin Bar最高点上方 **0.2%**\n\n")
	sb.WriteString("**吞没形态：**\n")
	sb.WriteString("- 止损设在吞没K线的最低/最高点外 **0.22%**\n\n")
	sb.WriteString("**十字星形态：**\n")
	sb.WriteString("- 止损设在十字星另一端（上影或下影顶端）外 **0.22%**\n\n")
	sb.WriteString("**硬性止损：** 单笔最大亏损 **≤账户净值的2%**（触及立即平仓）\n\n")

	sb.WriteString("## 止盈策略（分级快速止盈）\n\n")
	sb.WriteString("⚠️ **重要：Backpack交易所禁止部分平仓！** 如需止盈，必须全部平仓（close_long/close_short）\n\n")
	sb.WriteString("**第1档（保本保护）：** 盈利 ≥ 0.15% → 立即移动止损到保本价（覆盖双向手续费0.1%），确保零损失\n")
	sb.WriteString("  - 多单：移动止损到 开仓价 × 1.001（成本上方0.1%）\n")
	sb.WriteString("  - 空单：移动止损到 开仓价 × 0.999（成本下方0.1%）\n")
	sb.WriteString("  - 示例：多单开仓价100，涨到100.20（盈利0.2%）时，移动止损从99.7到100.1（保本价）\n\n")
	sb.WriteString("**第2档（快速止盈）：** 盈利 0.5-1.0% → 考虑全部平仓，锁定利润\n")
	sb.WriteString("**第3档（让利润奔跑）：** 盈利 1.0-2.0% → 移动止损到盈利的50%位置（如盈利2%，止损移到盈利1%处）\n")
	sb.WriteString("**第4档（极限止盈）：** 盈利 >2.0% → 立即全部止盈（高频交易不贪心）\n\n")

	sb.WriteString("**时间止盈：**\n")
	sb.WriteString("- 持仓15分钟盈利<0.3% → 考虑平仓换机会\n")
	sb.WriteString("- 持仓30分钟 → 重新评估是否持有\n\n")

	sb.WriteString("## ⚠️ 止损设置的绝对规则（铁律，必须严格遵守）\n\n")
	sb.WriteString("**止损价格的基本逻辑：**\n")
	sb.WriteString("- 止损是风险保护机制，当价格朝亏损方向移动时触发\n")
	sb.WriteString("- 止损价必须在当前价格的亏损方向，不能在盈利方向\n\n")

	sb.WriteString("**1. 多单（Long）止损规则：**\n")
	sb.WriteString("- ✅ **铁律：止损价 < 当前价格 - 0.2%**（必须严格小于，不能等于）\n")
	sb.WriteString("- 逻辑：价格下跌到止损价时触发平仓，保护资金\n")
	sb.WriteString("- ❌ 错误示例：当前价100，止损设在100（等于）或101（更高）\n")
	sb.WriteString("- ✅ 正确示例：当前价100，止损设在99.8或更低\n\n")

	sb.WriteString("**2. 空单（Short）止损规则：**\n")
	sb.WriteString("- ✅ **铁律：止损价 > 当前价格 + 0.2%**（必须严格大于，不能等于）\n")
	sb.WriteString("- 逻辑：价格上涨到止损价时触发平仓，保护资金\n")
	sb.WriteString("- ❌ 错误示例：当前价100，止损设在100（等于）或99（更低）\n")
	sb.WriteString("- ✅ 正确示例：当前价100，止损设在100.2或更高\n\n")

	sb.WriteString("**3. 保本止损（Break-even Stop）规则：**\n")
	sb.WriteString("- **触发条件：** 持仓浮盈 ≥ 0.15%时，立即移动止损到保本价\n")
	sb.WriteString("- **多单保本止损：** `开仓价格 × 1.001`（成本价上方0.1%，覆盖双向手续费）\n")
	sb.WriteString("  - 示例：开仓价100，涨到100.20（盈利0.2%），移动止损从99.7到100.1（保本价）\n")
	sb.WriteString("  - 验证：100.1 < 100.20（当前价），✅ 符合止损<当前价规则\n")
	sb.WriteString("  - 逻辑：最差情况盈利0.1%-手续费0.1%=0（零损失）\n")
	sb.WriteString("- **空单保本止损：** `开仓价格 × 0.999`（成本价下方0.1%，覆盖双向手续费）\n")
	sb.WriteString("  - 示例：开仓价100，跌到99.80（盈利0.2%），移动止损从100.3到99.9（保本价）\n")
	sb.WriteString("  - 验证：99.9 > 99.80（当前价），✅ 符合止损>当前价规则\n")
	sb.WriteString("  - 逻辑：最差情况盈利0.1%-手续费0.1%=0（零损失）\n")
	sb.WriteString("- **重要原则：** 保本止损移动后不可再次放宽，只能继续向有利方向收紧\n\n")

	sb.WriteString("**4. 亏损时设置止损：**\n")
	sb.WriteString("- 评估支撑/阻力位，设置在关键位置外\n")
	sb.WriteString("- 多单：止损 < 当前价（价格继续下跌时触发）\n")
	sb.WriteString("- 空单：止损 > 当前价（价格继续上涨时触发）\n")
	sb.WriteString("- 确保止损距离当前价 >= 0.2%（避免被噪音触发）\n\n")

	sb.WriteString("**检查清单（每次设置止损前必须验证）：**\n")
	sb.WriteString("- [ ] 多单：止损价 < 当前价格？（不能等于或大于）\n")
	sb.WriteString("- [ ] 空单：止损价 > 当前价格？（不能等于或小于）\n")
	sb.WriteString("- [ ] 止损价与当前价差距 >= 0.2%？\n")
	sb.WriteString("- [ ] 止损不会被轻微波动触发？\n\n")

	// 3. 输出格式 - 动态生成
	sb.WriteString("# 输出格式 (严格遵守)\n\n")
	sb.WriteString("**必须使用XML标签 <reasoning> 和 <decision> 标签分隔思维链和决策JSON，避免解析错误**\n\n")
	sb.WriteString("## 格式要求\n\n")
	sb.WriteString("<reasoning>\n")
	sb.WriteString("你的思维链分析...\n")
	sb.WriteString("- 简洁分析你的思考过程 \n")
	sb.WriteString("</reasoning>\n\n")
	sb.WriteString("<decision>\n")
	sb.WriteString("```json\n[\n")
	sb.WriteString(fmt.Sprintf("  {\"symbol\": \"BTCUSDT\", \"action\": \"open_short\", \"leverage\": %d, \"position_size_usd\": %.0f, \"stop_loss\": 95400, \"take_profit\": 94100, \"confidence\": 85, \"risk_usd\": 200, \"reasoning\": \"Pin Bar长上影线，止损设在最高点上方0.4%%, 止盈1.0%%快速离场\"},\n", btcEthLeverage, accountEquity*5))
	sb.WriteString("  {\"symbol\": \"SOLUSDT\", \"action\": \"update_stop_loss\", \"new_stop_loss\": 155, \"reasoning\": \"盈利0.4%，移动止损至接近成本价（成本价下方0.2%）\"},\n")
	sb.WriteString("  {\"symbol\": \"ETHUSDT\", \"action\": \"close_long\", \"reasoning\": \"止盈离场\"}\n")
	sb.WriteString("]\n```\n")
	sb.WriteString("</decision>\n\n")
	sb.WriteString("## 字段说明\n\n")
	sb.WriteString("- `action`: open_long | open_short | close_long | close_short | update_stop_loss | update_take_profit | hold | wait\n")
	sb.WriteString("- ⚠️ **禁止使用 partial_close**（Backpack不支持部分平仓，必须全部平仓）\n")
	sb.WriteString("- `update_stop_loss`: 更新止损价格，包括以下场景：\n")
	sb.WriteString("  - **保本止损：** 盈利≥0.15%时，移动到开仓价×1.001（多单）或×0.999（空单）\n")
	sb.WriteString("  - **追踪止损：** 盈利>1%时，移动到盈利的50%位置\n")
	sb.WriteString("  - **风险收紧：** 出现反向信号时，收紧止损减少损失\n")
	sb.WriteString("- `confidence`: 0-100（开仓建议≥75）\n")
	sb.WriteString("- 开仓时必填: leverage, position_size_usd, stop_loss, take_profit, confidence, risk_usd, reasoning\n")
	sb.WriteString("- update_stop_loss 时必填: new_stop_loss (注意是 new_stop_loss，不是 stop_loss)\n")
	sb.WriteString("- update_take_profit 时必填: new_take_profit (注意是 new_take_profit，不是 take_profit)\n\n")

	return sb.String()
}

// buildUserPrompt 构建 User Prompt（动态数据）
func buildUserPrompt(ctx *Context) string {
	var sb strings.Builder

	// ⚠️ 强制规则：只能使用系统检测的信号
	sb.WriteString("# ⚠️ 重要规则（必须遵守）\n\n")
	sb.WriteString("先检查当前持仓代币K线数据是否出现方向相反的信号，无论强度大小如果有则先平仓\n")
	sb.WriteString("**你只能根据下方\"系统检测的交易信号\"来开仓，禁止自己分析K线数据。**\n\n")
	sb.WriteString("- ✅ 允许：根据系统检测的Pin Bar/吞没/十字星信号开仓\n")
	sb.WriteString("- ✅ 允许：使用RSI/MACD/成交量等指标评估信号强度\n")
	sb.WriteString("- ❌ 禁止：自己分析K线序列发现\"潜在的\"形态\n")
	sb.WriteString("- ❌ 禁止：仅凭RSI超买/超卖就开仓\n")
	sb.WriteString("- ❌ 禁止：仅凭价格接近支撑/阻力就开仓\n")
	sb.WriteString("- ❌ 禁止：在上涨趋势中追高做多，在下跌趋势中追低做空\n\n")
	sb.WriteString("**如果没有系统检测的信号，或信号方向与你的判断相反，则输出wait。**\n\n")
	sb.WriteString("---\n\n")

	// 📖 信号判断标准说明
	sb.WriteString("## 📖 系统信号检测标准（了解系统如何识别信号）\n\n")
	sb.WriteString("**检测周期**：系统仅在 **15m、30m、1h、4h** 周期检测信号（已移除5m，准确率太低）\n\n")
	sb.WriteString("系统使用以下严格标准检测三种反转形态信号：\n\n")

	sb.WriteString("### 1. Pin Bar（针状线/锤子线）\n")
	sb.WriteString("**看涨Pin Bar（做多）**：\n")
	sb.WriteString("- 长下影线：影线长度 > 实体长度 × 3\n")
	sb.WriteString("- 小实体：实体大小 < K线总长度 × 20%\n")
	sb.WriteString("- 收盘价接近最高价（阳线更强）\n")
	sb.WriteString("- 成交量放大（> 前5根平均成交量1.5倍）\n")
	sb.WriteString("- 出现在下跌趋势末端\n\n")

	sb.WriteString("**看跌Pin Bar（做空）**：\n")
	sb.WriteString("- 长上影线：影线长度 > 实体长度 × 3\n")
	sb.WriteString("- 小实体：实体大小 < K线总长度 × 20%\n")
	sb.WriteString("- 收盘价接近最低价\n")
	sb.WriteString("- 成交量放大\n")
	sb.WriteString("- 出现在上涨趋势末端\n\n")

	sb.WriteString("### 2. 吞没形态（Engulfing）\n")
	sb.WriteString("**看涨吞没（做多）**：\n")
	sb.WriteString("- 第一根：小阴线（下跌）\n")
	sb.WriteString("- 第二根：大阳线，完全吞没第一根\n")
	sb.WriteString("- 第二根开盘价 < 第一根收盘价\n")
	sb.WriteString("- 第二根收盘价 > 第一根开盘价\n")
	sb.WriteString("- 第二根成交量 > 第一根成交量1.5倍\n\n")

	sb.WriteString("**看跌吞没（做空）**：\n")
	sb.WriteString("- 第一根：小阳线（上涨）\n")
	sb.WriteString("- 第二根：大阴线，完全吞没第一根\n")
	sb.WriteString("- 第二根开盘价 > 第一根收盘价\n")
	sb.WriteString("- 第二根收盘价 < 第一根开盘价\n")
	sb.WriteString("- 第二根成交量放大\n\n")

	sb.WriteString("### 3. 成交量放大标准\n")
	sb.WriteString("**系统使用以下标准判断成交量是否放大：**\n")
	sb.WriteString("- 强信号：当前K线成交量 > 前5根平均成交量 × 2倍\n")
	sb.WriteString("- 中等信号：当前K线成交量 > 前5根平均成交量 × 1.5倍\n")
	sb.WriteString("- 弱信号：成交量无明显变化（系统不会标记为强信号）\n\n")

	sb.WriteString("**你的任务**：根据系统已检测到的信号，结合多周期K线数据和RSI/MACD指标，评估信号的质量和开仓时机。\n\n")
	sb.WriteString("---\n\n")

	// 系统状态
	sb.WriteString(fmt.Sprintf("时间: %s | 周期: #%d | 运行: %d分钟\n\n",
		ctx.CurrentTime, ctx.CallCount, ctx.RuntimeMinutes))

	// BTC 市场
	if btcData, hasBTC := ctx.MarketDataMap["BTCUSDT"]; hasBTC {
		sb.WriteString(fmt.Sprintf("BTC: %.2f (1h: %+.2f%%, 4h: %+.2f%%) | MACD: %.4f | RSI: %.2f\n\n",
			btcData.CurrentPrice, btcData.PriceChange1h, btcData.PriceChange4h,
			btcData.CurrentMACD, btcData.CurrentRSI7))
	}

	// 账户
	sb.WriteString(fmt.Sprintf("账户: 净值%.2f | **可用余额%.2f USDT** (%.1f%%) | 已用保证金%.2f | 盈亏%+.2f%% | 保证金使用率%.1f%% | 持仓%d个\n\n",
		ctx.Account.TotalEquity,
		ctx.Account.AvailableBalance,
		(ctx.Account.AvailableBalance/ctx.Account.TotalEquity)*100,
		ctx.Account.MarginUsed,
		ctx.Account.TotalPnLPct,
		ctx.Account.MarginUsedPct,
		ctx.Account.PositionCount))

	// 持仓（完整市场数据）
	if len(ctx.Positions) > 0 {
		sb.WriteString("## 当前持仓\n")
		for i, pos := range ctx.Positions {
			// 计算持仓时长
			holdingDuration := ""
			if pos.UpdateTime > 0 {
				durationMs := time.Now().UnixMilli() - pos.UpdateTime
				durationMin := durationMs / (1000 * 60) // 转换为分钟
				if durationMin < 60 {
					holdingDuration = fmt.Sprintf(" | 持仓时长%d分钟", durationMin)
				} else {
					durationHour := durationMin / 60
					durationMinRemainder := durationMin % 60
					holdingDuration = fmt.Sprintf(" | 持仓时长%d小时%d分钟", durationHour, durationMinRemainder)
				}
			}

			// 计算仓位价值（用于 partial_close 检查）
			positionValue := math.Abs(pos.Quantity) * pos.MarkPrice

			sb.WriteString(fmt.Sprintf("%d. %s %s | 入场价%.4f 当前价%.4f | 数量%.4f | 仓位价值%.2f USDT | 盈亏%+.2f%% | 盈亏金额%+.2f USDT | 最高收益率%.2f%% | 杠杆%dx | 保证金%.0f | 强平价%.4f%s\n\n",
				i+1, pos.Symbol, strings.ToUpper(pos.Side),
				pos.EntryPrice, pos.MarkPrice, pos.Quantity, positionValue, pos.UnrealizedPnLPct, pos.UnrealizedPnL, pos.PeakPnLPct,
				pos.Leverage, pos.MarginUsed, pos.LiquidationPrice, holdingDuration))

			// 使用FormatMarketData输出完整市场数据
			if marketData, ok := ctx.MarketDataMap[pos.Symbol]; ok {
				sb.WriteString(market.Format(marketData))
				sb.WriteString("\n")
			}
		}
	} else {
		sb.WriteString("当前持仓: 无\n\n")
	}

	// 系统检测的交易信号（最重要的部分！）
	if len(ctx.StrongSignals) > 0 {
		sb.WriteString(fmt.Sprintf("## 🎯 系统检测的形态信号 (%d个强信号，信心度≥80%%)\n\n", len(ctx.StrongSignals)))
		sb.WriteString("**以下是检测到的K线形态特征（客观数据，需要你基于多周期K线分析判断方向）：**\n\n")

		for i, sig := range ctx.StrongSignals {
			// 格式化形态特征
			featuresStr := ""
			if sig.Features != nil {
				// Pin Bar 形态 & 十字星形态
				if patternType, ok := sig.Features["pattern_type"].(string); ok {
					switch patternType {
					case "long_lower_shadow":
						featuresStr = fmt.Sprintf("长下影线: 下影%.1f%%, 上影%.1f%%, 实体%.1f%%",
							sig.Features["lower_shadow_ratio"],
							sig.Features["upper_shadow_ratio"],
							sig.Features["body_ratio"])
					case "long_upper_shadow":
						featuresStr = fmt.Sprintf("长上影线: 上影%.1f%%, 下影%.1f%%, 实体%.1f%%",
							sig.Features["upper_shadow_ratio"],
							sig.Features["lower_shadow_ratio"],
							sig.Features["body_ratio"])
					case "bullish_engulfing":
						featuresStr = fmt.Sprintf("阳线吞没阴线，实体放大%.1fx", sig.Features["body_ratio"])
					case "bearish_engulfing":
						featuresStr = fmt.Sprintf("阴线吞没阳线，实体放大%.1fx", sig.Features["body_ratio"])
					case "long_upper_doji":
						featuresStr = fmt.Sprintf("长上影十字星: 上影%.1f%%, 下影%.1f%%, 实体%.1f%%",
							sig.Features["upper_shadow_ratio"],
							sig.Features["lower_shadow_ratio"],
							sig.Features["body_ratio"])
					case "long_lower_doji":
						featuresStr = fmt.Sprintf("长下影十字星: 下影%.1f%%, 上影%.1f%%, 实体%.1f%%",
							sig.Features["lower_shadow_ratio"],
							sig.Features["upper_shadow_ratio"],
							sig.Features["body_ratio"])
					case "balanced_doji":
						featuresStr = fmt.Sprintf("均衡十字星: 上影%.1f%%, 下影%.1f%%, 实体%.1f%%",
							sig.Features["upper_shadow_ratio"],
							sig.Features["lower_shadow_ratio"],
							sig.Features["body_ratio"])
					}
				}
				// 成交量放大
				if volumeRatio, ok := sig.Features["volume_ratio"].(float64); ok {
					isBullish := sig.Features["is_bullish_candle"].(bool)
					candleType := map[bool]string{true: "阳线", false: "阴线"}[isBullish]
					featuresStr = fmt.Sprintf("成交量放大%.1fx, 当前K线为%s", volumeRatio, candleType)
				}
			}

			sb.WriteString(fmt.Sprintf("%d. **%s** %s | 信心度%d%%\n",
				i+1, sig.Symbol, sig.TimeFrame, sig.Confidence))
			sb.WriteString(fmt.Sprintf("   形态: %s\n", featuresStr))
			sb.WriteString(fmt.Sprintf("   描述: %s\n", sig.Reason))
			sb.WriteString(fmt.Sprintf("   当前价格: %.4f\n\n", sig.Price))
		}
		sb.WriteString("---\n\n")
	} else {
		sb.WriteString("## 🎯 系统检测的交易信号\n\n")
		sb.WriteString("**当前没有检测到新的强信号（信心度≥80%）。**\n\n")

		// 根据是否有持仓给出不同的指示
		if len(ctx.Positions) > 0 {
			sb.WriteString("**虽然无新开仓信号，但你需要管理现有持仓：**\n")
			sb.WriteString("- 评估持仓盈亏情况，决定是否止盈/止损\n")
			sb.WriteString("- 查看多周期K线数据，判断技术面是否转向\n")
			sb.WriteString("- 如有盈利，考虑移动止损保护利润\n")
			sb.WriteString("- 评估是否需要部分平仓锁定利润\n\n")
		} else {
			sb.WriteString("**无持仓且无新信号，建议观望(wait)。**\n\n")
		}

		sb.WriteString("---\n\n")
	}

	// 构建有信号的币种集合（用于快速查找）
	signalSymbols := make(map[string]bool)
	for _, sig := range ctx.StrongSignals {
		signalSymbols[sig.Symbol] = true
	}

	// 分离有信号和无信号的币种
	var coinsWithSignals []CandidateCoin
	var coinsWithoutSignals []CandidateCoin

	for _, coin := range ctx.CandidateCoins {
		if _, hasSignal := signalSymbols[coin.Symbol]; hasSignal {
			coinsWithSignals = append(coinsWithSignals, coin)
		} else {
			coinsWithoutSignals = append(coinsWithoutSignals, coin)
		}
	}

	// 显示有信号币种的完整数据
	if len(coinsWithSignals) > 0 {
		sb.WriteString(fmt.Sprintf("## 📊 信号币种详细数据 (%d个)\n\n", len(coinsWithSignals)))
		sb.WriteString("**以下币种检测到强信号，使用形态特征精确设置止损止盈：**\n\n")
		sb.WriteString("💡 **止损设置方法：**\n")
		sb.WriteString("- Pin Bar：使用形态中的 `kline_low`（做多）或 `kline_high`（做空）外 0.3-0.5%\n")
		sb.WriteString("- 吞没形态：使用前一根K线的 `prev_low`/`prev_high` 外 0.5%\n")
		sb.WriteString("- 十字星：使用形态中的 `kline_low`（做多）或 `kline_high`（做空）外 0.5%\n\n")

		for i, coin := range coinsWithSignals {
			marketData, hasData := ctx.MarketDataMap[coin.Symbol]
			if !hasData {
				continue
			}

			sb.WriteString(fmt.Sprintf("### %d. %s\n\n", i+1, coin.Symbol))
			sb.WriteString(market.Format(marketData))
			sb.WriteString("\n")
		}
	}

	// 显示无信号币种的简要概览
	// if len(coinsWithoutSignals) > 0 {
	// 	sb.WriteString(fmt.Sprintf("## 📈 其他候选币种概览 (%d个无信号)\n\n", len(coinsWithoutSignals)))
	// 	sb.WriteString("**以下币种无强信号，仅供市场背景参考，不可开仓：**\n\n")

	// 	for _, coin := range coinsWithoutSignals {
	// 		marketData, hasData := ctx.MarketDataMap[coin.Symbol]
	// 		if !hasData {
	// 			continue
	// 		}

	// 		// 使用已有的价格和涨跌幅数据
	// 		sb.WriteString(fmt.Sprintf("- **%s**: %.4f (1h: %+.2f%%, 4h: %+.2f%%)\n",
	// 			coin.Symbol, marketData.CurrentPrice, marketData.PriceChange1h, marketData.PriceChange4h))
	// 	}
	// 	sb.WriteString("\n")
	// }
	// sb.WriteString("\n")

	// 夏普比率（直接传值，不要复杂格式化）
	if ctx.Performance != nil {
		// 直接从interface{}中提取SharpeRatio
		type PerformanceData struct {
			SharpeRatio float64 `json:"sharpe_ratio"`
		}
		var perfData PerformanceData
		if jsonData, err := json.Marshal(ctx.Performance); err == nil {
			if err := json.Unmarshal(jsonData, &perfData); err == nil {
				sb.WriteString(fmt.Sprintf("## 📊 夏普比率: %.2f\n\n", perfData.SharpeRatio))
			}
		}
	}

	sb.WriteString("---\n\n")
	sb.WriteString("现在请分析并输出决策（思维链 + JSON）\n")

	return sb.String()
}

// parseFullDecisionResponse 解析AI的完整决策响应
func parseFullDecisionResponse(aiResponse string, accountEquity float64, btcEthLeverage, altcoinLeverage int) (*FullDecision, error) {
	// 1. 提取思维链
	cotTrace := extractCoTTrace(aiResponse)

	// 2. 提取JSON决策列表
	decisions, err := extractDecisions(aiResponse)
	if err != nil {
		return &FullDecision{
			CoTTrace:  cotTrace,
			Decisions: []Decision{},
		}, fmt.Errorf("提取决策失败: %w", err)
	}

	// 3. 验证决策
	if err := validateDecisions(decisions, accountEquity, btcEthLeverage, altcoinLeverage); err != nil {
		return &FullDecision{
			CoTTrace:  cotTrace,
			Decisions: decisions,
		}, fmt.Errorf("决策验证失败: %w", err)
	}

	return &FullDecision{
		CoTTrace:  cotTrace,
		Decisions: decisions,
	}, nil
}

// extractCoTTrace 提取思维链分析
func extractCoTTrace(response string) string {
	// 方法1: 优先尝试提取 <reasoning> 标签内容
	if match := reReasoningTag.FindStringSubmatch(response); match != nil && len(match) > 1 {
		log.Printf("✓ 使用 <reasoning> 标签提取思维链")
		return strings.TrimSpace(match[1])
	}

	// 方法2: 如果没有 <reasoning> 标签，但有 <decision> 标签，提取 <decision> 之前的内容
	if decisionIdx := strings.Index(response, "<decision>"); decisionIdx > 0 {
		log.Printf("✓ 提取 <decision> 标签之前的内容作为思维链")
		return strings.TrimSpace(response[:decisionIdx])
	}

	// 方法3: 后备方案 - 查找JSON数组的开始位置
	jsonStart := strings.Index(response, "[")
	if jsonStart > 0 {
		log.Printf("⚠️  使用旧版格式（[ 字符分离）提取思维链")
		return strings.TrimSpace(response[:jsonStart])
	}

	// 如果找不到任何标记，整个响应都是思维链
	return strings.TrimSpace(response)
}

// extractDecisions 提取JSON决策列表
func extractDecisions(response string) ([]Decision, error) {
	// 预清洗：去零宽/BOM
	s := removeInvisibleRunes(response)
	s = strings.TrimSpace(s)

	// 🔧 关键修复 (Critical Fix)：在正则匹配之前就先修复全角字符！
	// 否则正则表达式 \[ 无法匹配全角的 ［
	s = fixMissingQuotes(s)

	// 方法1: 优先尝试从 <decision> 标签中提取
	var jsonPart string
	if match := reDecisionTag.FindStringSubmatch(s); match != nil && len(match) > 1 {
		jsonPart = strings.TrimSpace(match[1])
		log.Printf("✓ 使用 <decision> 标签提取JSON")
	} else {
		// 后备方案：使用整个响应
		jsonPart = s
		log.Printf("⚠️  未找到 <decision> 标签，使用全文搜索JSON")
	}

	// 修复 jsonPart 中的全角字符
	jsonPart = fixMissingQuotes(jsonPart)

	// 1) 优先从 ```json 代码块中提取
	if m := reJSONFence.FindStringSubmatch(jsonPart); m != nil && len(m) > 1 {
		jsonContent := strings.TrimSpace(m[1])
		jsonContent = compactArrayOpen(jsonContent) // 把 "[ {" 规整为 "[{"
		jsonContent = fixMissingQuotes(jsonContent) // 二次修复（防止 regex 提取后还有残留全角）
		if err := validateJSONFormat(jsonContent); err != nil {
			return nil, fmt.Errorf("JSON格式验证失败: %w\nJSON内容: %s\n完整响应:\n%s", err, jsonContent, response)
		}
		var decisions []Decision
		if err := json.Unmarshal([]byte(jsonContent), &decisions); err != nil {
			return nil, fmt.Errorf("JSON解析失败: %w\nJSON内容: %s", err, jsonContent)
		}
		return decisions, nil
	}

	// 2) 退而求其次 (Fallback)：全文寻找首个对象数组
	// 注意：此时 jsonPart 已经过 fixMissingQuotes()，全角字符已转换为半角
	jsonContent := strings.TrimSpace(reJSONArray.FindString(jsonPart))
	if jsonContent == "" {
		// 🔧 安全回退 (Safe Fallback)：当AI只输出思维链没有JSON时，生成保底决策（避免系统崩溃）
		log.Printf("⚠️  [SafeFallback] AI未输出JSON决策，进入安全等待模式 (AI response without JSON, entering safe wait mode)")

		// 提取思维链摘要（最多 240 字符）
		cotSummary := jsonPart
		if len(cotSummary) > 240 {
			cotSummary = cotSummary[:240] + "..."
		}

		// 生成保底决策：所有币种进入 wait 状态
		fallbackDecision := Decision{
			Symbol:    "ALL",
			Action:    "wait",
			Reasoning: fmt.Sprintf("模型未输出结构化JSON决策，进入安全等待；摘要：%s", cotSummary),
		}

		return []Decision{fallbackDecision}, nil
	}

	// 🔧 规整格式（此时全角字符已在前面修复过）
	jsonContent = compactArrayOpen(jsonContent)
	jsonContent = fixMissingQuotes(jsonContent) // 二次修复（防止 regex 提取后还有残留全角）

	// 🔧 验证 JSON 格式（检测常见错误）
	if err := validateJSONFormat(jsonContent); err != nil {
		return nil, fmt.Errorf("JSON格式验证失败: %w\nJSON内容: %s\n完整响应:\n%s", err, jsonContent, response)
	}

	// 解析JSON
	var decisions []Decision
	if err := json.Unmarshal([]byte(jsonContent), &decisions); err != nil {
		return nil, fmt.Errorf("JSON解析失败: %w\nJSON内容: %s", err, jsonContent)
	}

	return decisions, nil
}

// fixMissingQuotes 替换中文引号和全角字符为英文引号和半角字符（避免AI输出全角JSON字符导致解析失败）
func fixMissingQuotes(jsonStr string) string {
	// 替换中文引号
	jsonStr = strings.ReplaceAll(jsonStr, "\u201c", "\"") // "
	jsonStr = strings.ReplaceAll(jsonStr, "\u201d", "\"") // "
	jsonStr = strings.ReplaceAll(jsonStr, "\u2018", "'")  // '
	jsonStr = strings.ReplaceAll(jsonStr, "\u2019", "'")  // '

	// ⚠️ 替换全角括号、冒号、逗号（防止AI输出全角JSON字符）
	jsonStr = strings.ReplaceAll(jsonStr, "［", "[") // U+FF3B 全角左方括号
	jsonStr = strings.ReplaceAll(jsonStr, "］", "]") // U+FF3D 全角右方括号
	jsonStr = strings.ReplaceAll(jsonStr, "｛", "{") // U+FF5B 全角左花括号
	jsonStr = strings.ReplaceAll(jsonStr, "｝", "}") // U+FF5D 全角右花括号
	jsonStr = strings.ReplaceAll(jsonStr, "：", ":") // U+FF1A 全角冒号
	jsonStr = strings.ReplaceAll(jsonStr, "，", ",") // U+FF0C 全角逗号

	// ⚠️ 替换CJK标点符号（AI在中文上下文中也可能输出这些）
	jsonStr = strings.ReplaceAll(jsonStr, "【", "[") // CJK左方头括号 U+3010
	jsonStr = strings.ReplaceAll(jsonStr, "】", "]") // CJK右方头括号 U+3011
	jsonStr = strings.ReplaceAll(jsonStr, "〔", "[") // CJK左龟壳括号 U+3014
	jsonStr = strings.ReplaceAll(jsonStr, "〕", "]") // CJK右龟壳括号 U+3015
	jsonStr = strings.ReplaceAll(jsonStr, "、", ",") // CJK顿号 U+3001

	// ⚠️ 替换全角空格为半角空格（JSON中不应该有全角空格）
	jsonStr = strings.ReplaceAll(jsonStr, "　", " ") // U+3000 全角空格

	return jsonStr
}

// validateJSONFormat 验证 JSON 格式，检测常见错误
func validateJSONFormat(jsonStr string) error {
	trimmed := strings.TrimSpace(jsonStr)

	// 允许 [ 和 { 之间存在任意空白（含零宽）
	if !reArrayHead.MatchString(trimmed) {
		// 检查是否是纯数字/范围数组（常见错误）
		if strings.HasPrefix(trimmed, "[") && !strings.Contains(trimmed[:min(20, len(trimmed))], "{") {
			return fmt.Errorf("不是有效的决策数组（必须包含对象 {}），实际内容: %s", trimmed[:min(50, len(trimmed))])
		}
		return fmt.Errorf("JSON 必须以 [{ 开头（允许空白），实际: %s", trimmed[:min(20, len(trimmed))])
	}

	// 检查是否包含范围符号 ~（LLM 常见错误）
	if strings.Contains(jsonStr, "~") {
		return fmt.Errorf("JSON 中不可包含范围符号 ~，所有数字必须是精确的单一值")
	}

	// 检查是否包含千位分隔符（如 98,000）
	// 使用简单的模式匹配：数字+逗号+3位数字
	for i := 0; i < len(jsonStr)-4; i++ {
		if jsonStr[i] >= '0' && jsonStr[i] <= '9' &&
			jsonStr[i+1] == ',' &&
			jsonStr[i+2] >= '0' && jsonStr[i+2] <= '9' &&
			jsonStr[i+3] >= '0' && jsonStr[i+3] <= '9' &&
			jsonStr[i+4] >= '0' && jsonStr[i+4] <= '9' {
			return fmt.Errorf("JSON 数字不可包含千位分隔符逗号，发现: %s", jsonStr[i:min(i+10, len(jsonStr))])
		}
	}

	return nil
}

// min 返回两个整数中的较小值
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// removeInvisibleRunes 去除零宽字符和 BOM，避免肉眼看不见的前缀破坏校验
func removeInvisibleRunes(s string) string {
	return reInvisibleRunes.ReplaceAllString(s, "")
}

// compactArrayOpen 规整开头的 "[ {" → "[{"
func compactArrayOpen(s string) string {
	return reArrayOpenSpace.ReplaceAllString(strings.TrimSpace(s), "[{")
}

// validateDecisions 验证所有决策（需要账户信息和杠杆配置）
func validateDecisions(decisions []Decision, accountEquity float64, btcEthLeverage, altcoinLeverage int) error {
	for i, decision := range decisions {
		if err := validateDecision(&decision, accountEquity, btcEthLeverage, altcoinLeverage); err != nil {
			return fmt.Errorf("决策 #%d 验证失败: %w", i+1, err)
		}
	}
	return nil
}

// findMatchingBracket 查找匹配的右括号
func findMatchingBracket(s string, start int) int {
	if start >= len(s) || s[start] != '[' {
		return -1
	}

	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return i
			}
		}
	}

	return -1
}

// validateDecision 验证单个决策的有效性
func validateDecision(d *Decision, accountEquity float64, btcEthLeverage, altcoinLeverage int) error {
	// 验证action
	validActions := map[string]bool{
		"open_long":          true,
		"open_short":         true,
		"close_long":         true,
		"close_short":        true,
		"update_stop_loss":   true,
		"update_take_profit": true,
		"partial_close":      true,
		"hold":               true,
		"wait":               true,
	}

	if !validActions[d.Action] {
		return fmt.Errorf("无效的action: %s", d.Action)
	}

	// 开仓操作必须提供完整参数
	if d.Action == "open_long" || d.Action == "open_short" {
		// 根据币种使用配置的杠杆上限
		maxLeverage := altcoinLeverage          // 山寨币使用配置的杠杆
		maxPositionValue := accountEquity * 4.5 // 山寨币最多4.5倍账户净值
		if d.Symbol == "BTCUSDT" || d.Symbol == "ETHUSDT" {
			maxLeverage = btcEthLeverage          // BTC和ETH使用配置的杠杆
			maxPositionValue = accountEquity * 10 // BTC/ETH最多10倍账户净值
		}

		// ✅ Fallback 机制：杠杆超限时自动修正为上限值（而不是直接拒绝决策）
		if d.Leverage <= 0 {
			return fmt.Errorf("杠杆必须大于0: %d", d.Leverage)
		}
		if d.Leverage > maxLeverage {
			log.Printf("⚠️  [Leverage Fallback] %s 杠杆超限 (%dx > %dx)，自动调整为上限值 %dx",
				d.Symbol, d.Leverage, maxLeverage, maxLeverage)
			d.Leverage = maxLeverage // 自动修正为上限值
		}
		if d.PositionSizeUSD <= 0 {
			return fmt.Errorf("仓位大小必须大于0: %.2f", d.PositionSizeUSD)
		}

		// ✅ 验证最小开仓金额（防止数量格式化为 0 的错误）
		// Binance 最小名义价值 10 USDT + 安全边际
		const minPositionSizeGeneral = 12.0 // 10 + 20% 安全边际
		const minPositionSizeBTCETH = 60.0  // BTC/ETH 因价格高和精度限制需要更大金额（更灵活）

		if d.Symbol == "BTCUSDT" || d.Symbol == "ETHUSDT" {
			if d.PositionSizeUSD < minPositionSizeBTCETH {
				return fmt.Errorf("%s 开仓金额过小(%.2f USDT)，必须≥%.2f USDT（因价格高且精度限制，避免数量四舍五入为0）", d.Symbol, d.PositionSizeUSD, minPositionSizeBTCETH)
			}
		} else {
			if d.PositionSizeUSD < minPositionSizeGeneral {
				return fmt.Errorf("开仓金额过小(%.2f USDT)，必须≥%.2f USDT（Binance 最小名义价值要求）", d.PositionSizeUSD, minPositionSizeGeneral)
			}
		}

		// 验证仓位价值上限（加1%容差以避免浮点数精度问题）
		tolerance := maxPositionValue * 0.01 // 1%容差
		if d.PositionSizeUSD > maxPositionValue+tolerance {
			if d.Symbol == "BTCUSDT" || d.Symbol == "ETHUSDT" {
				return fmt.Errorf("BTC/ETH单币种仓位价值不能超过%.0f USDT（10倍账户净值），实际: %.0f", maxPositionValue, d.PositionSizeUSD)
			} else {
				return fmt.Errorf("山寨币单币种仓位价值不能超过%.0f USDT（10倍账户净值），实际: %.0f", maxPositionValue, d.PositionSizeUSD)
			}
		}
		if d.StopLoss <= 0 || d.TakeProfit <= 0 {
			return fmt.Errorf("止损和止盈必须大于0")
		}

		// 验证止损止盈的合理性
		if d.Action == "open_long" {
			if d.StopLoss >= d.TakeProfit {
				return fmt.Errorf("做多时止损价必须小于止盈价")
			}
		} else {
			if d.StopLoss <= d.TakeProfit {
				return fmt.Errorf("做空时止损价必须大于止盈价")
			}
		}

		// 验证风险回报比（必须≥1:3）
		// 计算入场价（假设当前市价）
		var entryPrice float64
		if d.Action == "open_long" {
			// 做多：入场价在止损和止盈之间
			entryPrice = d.StopLoss + (d.TakeProfit-d.StopLoss)*0.2 // 假设在20%位置入场
		} else {
			// 做空：入场价在止损和止盈之间
			entryPrice = d.StopLoss - (d.StopLoss-d.TakeProfit)*0.2 // 假设在20%位置入场
		}

		var riskPercent, rewardPercent, riskRewardRatio float64
		if d.Action == "open_long" {
			riskPercent = (entryPrice - d.StopLoss) / entryPrice * 100
			rewardPercent = (d.TakeProfit - entryPrice) / entryPrice * 100
			if riskPercent > 0 {
				riskRewardRatio = rewardPercent / riskPercent
			}
		} else {
			riskPercent = (d.StopLoss - entryPrice) / entryPrice * 100
			rewardPercent = (entryPrice - d.TakeProfit) / entryPrice * 100
			if riskPercent > 0 {
				riskRewardRatio = rewardPercent / riskPercent
			}
		}

		// 硬约束：风险回报比必须≥3.0
		if riskRewardRatio < 3.0 {
			return fmt.Errorf("风险回报比过低(%.2f:1)，必须≥3.0:1 [风险:%.2f%% 收益:%.2f%%] [止损:%.2f 止盈:%.2f]",
				riskRewardRatio, riskPercent, rewardPercent, d.StopLoss, d.TakeProfit)
		}
	}

	// 动态调整止损验证
	if d.Action == "update_stop_loss" {
		if d.NewStopLoss <= 0 {
			return fmt.Errorf("新止损价格必须大于0: %.2f", d.NewStopLoss)
		}
	}

	// 动态调整止盈验证
	if d.Action == "update_take_profit" {
		if d.NewTakeProfit <= 0 {
			return fmt.Errorf("新止盈价格必须大于0: %.2f", d.NewTakeProfit)
		}
	}

	// 部分平仓验证
	if d.Action == "partial_close" {
		if d.ClosePercentage <= 0 || d.ClosePercentage > 100 {
			return fmt.Errorf("平仓百分比必须在0-100之间: %.1f", d.ClosePercentage)
		}
	}

	return nil
}
