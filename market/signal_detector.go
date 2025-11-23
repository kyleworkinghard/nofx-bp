package market

import (
	"fmt"
	"log"
	"math"
)

// SignalType 信号类型
type SignalType string

const (
	SignalPinBar      SignalType = "pin_bar"       // 针状线形态（长影线）
	SignalDoji        SignalType = "doji"          // 十字星形态
	SignalVolumeSpike SignalType = "volume_spike"  // 成交量激增
	SignalEngulfing   SignalType = "engulfing"     // 吞没形态
)

// TradingSignal 交易信号（客观形态特征，不含方向预判）
type TradingSignal struct {
	Symbol     string
	TimeFrame  TimeFrame
	SignalType SignalType
	Price      float64 // 当前价格
	Confidence int     // 信号强度 (0-100)

	// 客观形态特征（供AI分析，不含方向判断）
	Features map[string]interface{} // 形态特征（影线比例、实体比例、成交量比例等）
	Reason   string                 // 客观描述
}

// SignalDetector 信号检测器
type SignalDetector struct {
	cache *KlineCache
}

// NewSignalDetector 创建信号检测器
func NewSignalDetector() *SignalDetector {
	return &SignalDetector{
		cache: GetKlineCache(),
	}
}

// DetectAllSignals 检测所有信号（锤子线 + 成交量放大）
func (sd *SignalDetector) DetectAllSignals(symbol string, timeFrames []TimeFrame) []*TradingSignal {
	var signals []*TradingSignal

	for _, tf := range timeFrames {
		// 检测Pin Bar（锤子线）
		pinBarSignals := sd.DetectPinBar(symbol, tf)
		signals = append(signals, pinBarSignals...)

		// 检测成交量放大
		volumeSignals := sd.DetectVolumeSpike(symbol, tf)
		signals = append(signals, volumeSignals...)

		// 检测吞没形态
		engulfingSignals := sd.DetectEngulfing(symbol, tf)
		signals = append(signals, engulfingSignals...)
	}

	return signals
}

// DetectPinBar 检测Pin Bar（针状线形态 - 长影线）
// 只检测客观形态特征，不预判方向
func (sd *SignalDetector) DetectPinBar(symbol string, timeFrame TimeFrame) []*TradingSignal {
	var signals []*TradingSignal

	// 获取最新的K线
	latestKline, err := sd.cache.GetLatestKline(symbol, timeFrame)
	if err != nil {
		return signals
	}

	kline := *latestKline

	// 计算客观形态数据
	body := math.Abs(kline.Close - kline.Open)
	upperShadow := kline.High - math.Max(kline.Open, kline.Close)
	lowerShadow := math.Min(kline.Open, kline.Close) - kline.Low
	totalRange := kline.High - kline.Low

	// 防止除以0
	if totalRange == 0 || body == 0 {
		return signals
	}

	// 计算比例（客观数据）
	upperShadowRatio := (upperShadow / totalRange) * 100
	lowerShadowRatio := (lowerShadow / totalRange) * 100
	bodyRatio := (body / totalRange) * 100

	// 检测长下影线（标准：下影线 > 实体×1.5 且 实体 < 总长度30%）
	if lowerShadow > body*1.5 && body < totalRange*0.3 && upperShadow < body {
		confidence := calculatePinBarConfidence(lowerShadow, body, upperShadow, totalRange)

		signal := &TradingSignal{
			Symbol:     symbol,
			TimeFrame:  timeFrame,
			SignalType: SignalPinBar,
			Price:      kline.Close,
			Confidence: confidence,
			Features: map[string]interface{}{
				"pattern_type":        "long_lower_shadow", // 形态类型：长下影线
				"lower_shadow_ratio":  lowerShadowRatio,    // 下影线占比
				"upper_shadow_ratio":  upperShadowRatio,    // 上影线占比
				"body_ratio":          bodyRatio,           // 实体占比
				"kline_open":          kline.Open,          // K线开盘价
				"kline_high":          kline.High,          // K线最高价
				"kline_low":           kline.Low,           // K线最低价
				"kline_close":         kline.Close,         // K线收盘价
				"is_bullish_candle":   kline.Close > kline.Open, // 是否阳线
			},
			Reason: fmt.Sprintf("长下影线形态: 下影线%.1f%%, 上影线%.1f%%, 实体%.1f%%",
				lowerShadowRatio, upperShadowRatio, bodyRatio),
		}
		signals = append(signals, signal)

		log.Printf("🔔 [Signal] %s %s - Pin Bar形态 (长下影线) 强度:%d%% | 价格:%.2f",
			symbol, timeFrame, confidence, signal.Price)
	}

	// 检测长上影线（标准：上影线 > 实体×1.5 且 实体 < 总长度30%）
	if upperShadow > body*1.5 && body < totalRange*0.3 && lowerShadow < body {
		confidence := calculatePinBarConfidence(upperShadow, body, lowerShadow, totalRange)

		signal := &TradingSignal{
			Symbol:     symbol,
			TimeFrame:  timeFrame,
			SignalType: SignalPinBar,
			Price:      kline.Close,
			Confidence: confidence,
			Features: map[string]interface{}{
				"pattern_type":        "long_upper_shadow", // 形态类型：长上影线
				"upper_shadow_ratio":  upperShadowRatio,    // 上影线占比
				"lower_shadow_ratio":  lowerShadowRatio,    // 下影线占比
				"body_ratio":          bodyRatio,           // 实体占比
				"kline_open":          kline.Open,          // K线开盘价
				"kline_high":          kline.High,          // K线最高价
				"kline_low":           kline.Low,           // K线最低价
				"kline_close":         kline.Close,         // K线收盘价
				"is_bullish_candle":   kline.Close > kline.Open, // 是否阳线
			},
			Reason: fmt.Sprintf("长上影线形态: 上影线%.1f%%, 下影线%.1f%%, 实体%.1f%%",
				upperShadowRatio, lowerShadowRatio, bodyRatio),
		}
		signals = append(signals, signal)

		log.Printf("🔔 [Signal] %s %s - Pin Bar形态 (长上影线) 强度:%d%% | 价格:%.2f",
			symbol, timeFrame, confidence, signal.Price)
	}

	// 检测十字星（Doji）形态
	// 标准：实体极小 且 上下影线都较长且相对均衡
	if body < totalRange*0.15 && // 实体小于总长度15%（相对宽松，包含小实体十字星）
		upperShadow > body*2 && // 上影线大于实体2倍
		lowerShadow > body*2 && // 下影线大于实体2倍
		upperShadow > totalRange*0.15 && // 上影线至少占15%（确保影线有意义）
		lowerShadow > totalRange*0.15 { // 下影线至少占15%

		confidence := calculateDojiConfidence(body, upperShadow, lowerShadow, totalRange)

		// 判断十字星类型（基于上下影线比例）
		shadowRatio := upperShadow / lowerShadow
		var dojiType, dojiDescription string
		if shadowRatio > 1.5 {
			dojiType = "long_upper_doji" // 上影线更长的十字星（偏空）
			dojiDescription = fmt.Sprintf("长上影十字星: 上影%.1f%% > 下影%.1f%%, 实体%.1f%%",
				upperShadowRatio, lowerShadowRatio, bodyRatio)
		} else if shadowRatio < 0.67 {
			dojiType = "long_lower_doji" // 下影线更长的十字星（偏多）
			dojiDescription = fmt.Sprintf("长下影十字星: 下影%.1f%% > 上影%.1f%%, 实体%.1f%%",
				lowerShadowRatio, upperShadowRatio, bodyRatio)
		} else {
			dojiType = "balanced_doji" // 均衡十字星
			dojiDescription = fmt.Sprintf("均衡十字星: 上影%.1f%%, 下影%.1f%%, 实体%.1f%%",
				upperShadowRatio, lowerShadowRatio, bodyRatio)
		}

		signal := &TradingSignal{
			Symbol:     symbol,
			TimeFrame:  timeFrame,
			SignalType: SignalDoji,
			Price:      kline.Close,
			Confidence: confidence,
			Features: map[string]interface{}{
				"pattern_type":       dojiType,          // 十字星类型
				"upper_shadow_ratio": upperShadowRatio,  // 上影线占比
				"lower_shadow_ratio": lowerShadowRatio,  // 下影线占比
				"body_ratio":         bodyRatio,         // 实体占比
				"shadow_balance":     shadowRatio,       // 影线平衡比（上/下）
				"kline_open":         kline.Open,        // K线开盘价
				"kline_high":         kline.High,        // K线最高价
				"kline_low":          kline.Low,         // K线最低价
				"kline_close":        kline.Close,       // K线收盘价
				"is_bullish_candle":  kline.Close > kline.Open, // 是否阳线
			},
			Reason: dojiDescription, // 使用客观的形态描述
		}
		signals = append(signals, signal)

		log.Printf("🔔 [Signal] %s %s - 十字星形态 (%s) 强度:%d%% | 价格:%.2f",
			symbol, timeFrame, dojiType, confidence, signal.Price)
	}

	return signals
}

// calculatePinBarConfidence 计算Pin Bar信号强度
func calculatePinBarConfidence(shadowLength, body, oppositeShadow, totalRange float64) int {
	// 基础分数
	confidence := 60

	// 影线越长，信号越强（最多+25分）
	shadowRatio := shadowLength / totalRange
	if shadowRatio > 0.7 {
		confidence += 25
	} else if shadowRatio > 0.6 {
		confidence += 20
	} else if shadowRatio > 0.5 {
		confidence += 15
	}

	// 实体越小，信号越强（最多+10分）
	bodyRatio := body / totalRange
	if bodyRatio < 0.15 {
		confidence += 10
	} else if bodyRatio < 0.25 {
		confidence += 5
	}

	// 反向影线越小，信号越强（最多+5分）
	if oppositeShadow < body*0.5 {
		confidence += 5
	}

	// 限制在100以内
	if confidence > 100 {
		confidence = 100
	}

	return confidence
}

// calculateDojiConfidence 计算十字星信号强度
func calculateDojiConfidence(body, upperShadow, lowerShadow, totalRange float64) int {
	// 基础分数
	confidence := 70 // 十字星是重要的反转信号，基础分较高

	// 实体越小，信号越强（最多+15分）
	bodyRatio := body / totalRange
	if bodyRatio < 0.05 {
		confidence += 15 // 几乎完美的十字星
	} else if bodyRatio < 0.10 {
		confidence += 10
	} else if bodyRatio < 0.15 {
		confidence += 5
	}

	// 上下影线越均衡，信号越强（最多+10分）
	shadowRatio := upperShadow / lowerShadow
	if shadowRatio >= 0.8 && shadowRatio <= 1.2 {
		confidence += 10 // 非常均衡
	} else if shadowRatio >= 0.7 && shadowRatio <= 1.4 {
		confidence += 7 // 比较均衡
	} else if shadowRatio >= 0.6 && shadowRatio <= 1.6 {
		confidence += 4 // 轻微不均衡
	}

	// 影线越长，信号越强（最多+5分）
	avgShadow := (upperShadow + lowerShadow) / 2
	avgShadowRatio := avgShadow / totalRange
	if avgShadowRatio > 0.4 {
		confidence += 5 // 影线很长
	} else if avgShadowRatio > 0.3 {
		confidence += 3
	}

	// 限制在100以内
	if confidence > 100 {
		confidence = 100
	}

	return confidence
}

// DetectVolumeSpike 检测成交量放大
// 只检测客观的成交量变化，不预判方向
func (sd *SignalDetector) DetectVolumeSpike(symbol string, timeFrame TimeFrame) []*TradingSignal {
	var signals []*TradingSignal

	// 获取最新的两根K线
	klines, err := sd.cache.GetLatestTwoKlines(symbol, timeFrame)
	if err != nil || len(klines) < 2 {
		return signals
	}

	prevKline := klines[0]
	currentKline := klines[1]

	// 防止除以0
	if prevKline.Volume == 0 {
		return signals
	}

	// 计算客观的成交量数据
	volumeRatio := currentKline.Volume / prevKline.Volume
	priceChange := ((currentKline.Close - currentKline.Open) / currentKline.Open) * 100
	priceChangeAbs := math.Abs(priceChange)

	// 成交量放大 >= 150%
	if volumeRatio >= 1.5 {
		// 成交量放大越多，信号越强
		confidence := 70
		if volumeRatio >= 3.0 {
			confidence = 95
		} else if volumeRatio >= 2.5 {
			confidence = 90
		} else if volumeRatio >= 2.0 {
			confidence = 85
		} else if volumeRatio >= 1.8 {
			confidence = 80
		}

		signal := &TradingSignal{
			Symbol:     symbol,
			TimeFrame:  timeFrame,
			SignalType: SignalVolumeSpike,
			Price:      currentKline.Close,
			Confidence: confidence,
			Features: map[string]interface{}{
				"volume_ratio":        volumeRatio,                       // 成交量放大倍数
				"prev_volume":         prevKline.Volume,                  // 前一根K线成交量
				"current_volume":      currentKline.Volume,               // 当前K线成交量
				"is_bullish_candle":   currentKline.Close > currentKline.Open, // 是否阳线
				"price_change_pct":    priceChange,                       // K线涨跌幅
				"price_change_abs":    priceChangeAbs,                    // K线涨跌幅绝对值
				"kline_open":          currentKline.Open,                 // K线开盘价
				"kline_high":          currentKline.High,                 // K线最高价
				"kline_low":           currentKline.Low,                  // K线最低价
				"kline_close":         currentKline.Close,                // K线收盘价
			},
			Reason: fmt.Sprintf("成交量放大%.1fx (%.0f → %.0f), K线%s%.2f%%",
				volumeRatio, prevKline.Volume, currentKline.Volume,
				map[bool]string{true: "上涨", false: "下跌"}[currentKline.Close > currentKline.Open],
				priceChangeAbs),
		}
		signals = append(signals, signal)

		log.Printf("🔔 [Signal] %s %s - 成交量放大%.1fx (强度:%d%%) | 价格:%.2f",
			symbol, timeFrame, volumeRatio, confidence, signal.Price)
	}

	return signals
}

// DetectEngulfing 检测吞没形态
// 只检测客观的K线形态，不预判方向
func (sd *SignalDetector) DetectEngulfing(symbol string, timeFrame TimeFrame) []*TradingSignal {
	var signals []*TradingSignal

	// 获取最新的两根K线
	klines, err := sd.cache.GetLatestTwoKlines(symbol, timeFrame)
	if err != nil || len(klines) < 2 {
		return signals
	}

	prevKline := klines[0]
	currentKline := klines[1]

	prevBody := math.Abs(prevKline.Close - prevKline.Open)
	currentBody := math.Abs(currentKline.Close - currentKline.Open)

	// 检测吞没形态 (阴线吞没阳线)
	// 条件：前一根阴线，当前阳线，且当前K线完全吞没前一根
	if prevKline.Close < prevKline.Open && // 前一根是阴线
		currentKline.Close > currentKline.Open && // 当前是阳线
		currentKline.Open < prevKline.Close && // 当前开盘价 < 前一根收盘价
		currentKline.Close > prevKline.Open && // 当前收盘价 > 前一根开盘价
		currentBody > prevBody { // 当前实体 > 前一根实体

		confidence := 80
		bodyRatio := currentBody / prevBody
		if bodyRatio > 1.5 {
			confidence = 90
		}

		signal := &TradingSignal{
			Symbol:     symbol,
			TimeFrame:  timeFrame,
			SignalType: SignalEngulfing,
			Price:      currentKline.Close,
			Confidence: confidence,
			Features: map[string]interface{}{
				"pattern_type":        "bullish_engulfing",  // 形态类型（客观描述）
				"prev_is_bearish":     true,                 // 前一根是阴线
				"current_is_bullish":  true,                 // 当前是阳线
				"body_ratio":          bodyRatio,            // 实体比例
				"prev_open":           prevKline.Open,       // 前一根开盘价
				"prev_high":           prevKline.High,       // 前一根最高价
				"prev_low":            prevKline.Low,        // 前一根最低价
				"prev_close":          prevKline.Close,      // 前一根收盘价
				"current_open":        currentKline.Open,    // 当前开盘价
				"current_high":        currentKline.High,    // 当前最高价
				"current_low":         currentKline.Low,     // 当前最低价
				"current_close":       currentKline.Close,   // 当前收盘价
			},
			Reason: fmt.Sprintf("吞没形态: 阳线吞没阴线，实体放大%.1fx", bodyRatio),
		}
		signals = append(signals, signal)

		log.Printf("🔔 [Signal] %s %s - 吞没形态 (阳线吞没阴线) 强度:%d%% | 价格:%.2f",
			symbol, timeFrame, confidence, signal.Price)
	}

	// 检测吞没形态 (阳线吞没阴线)
	// 条件：前一根阳线，当前阴线，且当前K线完全吞没前一根
	if prevKline.Close > prevKline.Open && // 前一根是阳线
		currentKline.Close < currentKline.Open && // 当前是阴线
		currentKline.Open > prevKline.Close && // 当前开盘价 > 前一根收盘价
		currentKline.Close < prevKline.Open && // 当前收盘价 < 前一根开盘价
		currentBody > prevBody { // 当前实体 > 前一根实体

		confidence := 80
		bodyRatio := currentBody / prevBody
		if bodyRatio > 1.5 {
			confidence = 90
		}

		signal := &TradingSignal{
			Symbol:     symbol,
			TimeFrame:  timeFrame,
			SignalType: SignalEngulfing,
			Price:      currentKline.Close,
			Confidence: confidence,
			Features: map[string]interface{}{
				"pattern_type":        "bearish_engulfing",  // 形态类型（客观描述）
				"prev_is_bullish":     true,                 // 前一根是阳线
				"current_is_bearish":  true,                 // 当前是阴线
				"body_ratio":          bodyRatio,            // 实体比例
				"prev_open":           prevKline.Open,       // 前一根开盘价
				"prev_high":           prevKline.High,       // 前一根最高价
				"prev_low":            prevKline.Low,        // 前一根最低价
				"prev_close":          prevKline.Close,      // 前一根收盘价
				"current_open":        currentKline.Open,    // 当前开盘价
				"current_high":        currentKline.High,    // 当前最高价
				"current_low":         currentKline.Low,     // 当前最低价
				"current_close":       currentKline.Close,   // 当前收盘价
			},
			Reason: fmt.Sprintf("吞没形态: 阴线吞没阳线，实体放大%.1fx", bodyRatio),
		}
		signals = append(signals, signal)

		log.Printf("🔔 [Signal] %s %s - 吞没形态 (阴线吞没阳线) 强度:%d%% | 价格:%.2f",
			symbol, timeFrame, confidence, signal.Price)
	}

	return signals
}

// FilterStrongSignals 过滤强信号（信心度>=80的信号）
func FilterStrongSignals(signals []*TradingSignal) []*TradingSignal {
	var strongSignals []*TradingSignal
	for _, signal := range signals {
		if signal.Confidence >= 80 {
			strongSignals = append(strongSignals, signal)
		}
	}
	return strongSignals
}

// CombineSignals 合并同一币种、同一周期的多个信号
func CombineSignals(signals []*TradingSignal) map[string][]*TradingSignal {
	combined := make(map[string][]*TradingSignal)

	for _, signal := range signals {
		// 按币种+周期合并（不再按方向区分）
		key := fmt.Sprintf("%s_%s", signal.Symbol, signal.TimeFrame)
		combined[key] = append(combined[key], signal)
	}

	return combined
}
