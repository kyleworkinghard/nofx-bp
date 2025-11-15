package market

import (
	"fmt"
	"log"
	"math"
)

// SignalType 信号类型
type SignalType string

const (
	SignalBullishPinBar SignalType = "bullish_pin_bar"  // 看涨针状线
	SignalBearishPinBar SignalType = "bearish_pin_bar"  // 看跌针状线
	SignalVolumeSpike   SignalType = "volume_spike"     // 成交量激增
	SignalEngulfing     SignalType = "engulfing"        // 吞没形态
)

// TradingSignal 交易信号
type TradingSignal struct {
	Symbol     string
	TimeFrame  TimeFrame
	SignalType SignalType
	Direction  string  // "long" or "short"
	Price      float64 // 触发价格
	StopLoss   float64 // 建议止损价
	Confidence int     // 信号强度 (0-100)
	Reason     string  // 信号原因
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

// DetectPinBar 检测Pin Bar（锤子线/针状线）
// 标准：上影线或下影线长度 > 实体长度的50%
func (sd *SignalDetector) DetectPinBar(symbol string, timeFrame TimeFrame) []*TradingSignal {
	var signals []*TradingSignal

	// 获取最新的K线
	latestKline, err := sd.cache.GetLatestKline(symbol, timeFrame)
	if err != nil {
		return signals
	}

	kline := *latestKline

	// 计算实体大小、上影线、下影线
	body := math.Abs(kline.Close - kline.Open)
	upperShadow := kline.High - math.Max(kline.Open, kline.Close)
	lowerShadow := math.Min(kline.Open, kline.Close) - kline.Low
	totalRange := kline.High - kline.Low

	// 防止除以0
	if totalRange == 0 || body == 0 {
		return signals
	}

	// 看涨Pin Bar（锤子线）
	// 条件：
	// 1. 下影线长度 > 实体长度 × 1.5（更严格的标准）
	// 2. 实体 < K线总长度的30%
	// 3. 上影线很短（< 实体长度）
	if lowerShadow > body*1.5 && body < totalRange*0.3 && upperShadow < body {
		confidence := calculatePinBarConfidence(lowerShadow, body, upperShadow, totalRange)

		signal := &TradingSignal{
			Symbol:     symbol,
			TimeFrame:  timeFrame,
			SignalType: SignalBullishPinBar,
			Direction:  "long",
			Price:      kline.Close,
			StopLoss:   kline.Low * 0.997, // 止损设在最低点下方0.3%
			Confidence: confidence,
			Reason:     fmt.Sprintf("看涨Pin Bar: 下影线%.2f%%, 实体%.2f%%", (lowerShadow/totalRange)*100, (body/totalRange)*100),
		}
		signals = append(signals, signal)

		log.Printf("🔔 [Signal] %s %s - 看涨Pin Bar (强度:%d%%) | 价格:%.2f | 止损:%.2f",
			symbol, timeFrame, confidence, signal.Price, signal.StopLoss)
	}

	// 看跌Pin Bar（射击之星）
	// 条件：
	// 1. 上影线长度 > 实体长度 × 1.5
	// 2. 实体 < K线总长度的30%
	// 3. 下影线很短（< 实体长度）
	if upperShadow > body*1.5 && body < totalRange*0.3 && lowerShadow < body {
		confidence := calculatePinBarConfidence(upperShadow, body, lowerShadow, totalRange)

		signal := &TradingSignal{
			Symbol:     symbol,
			TimeFrame:  timeFrame,
			SignalType: SignalBearishPinBar,
			Direction:  "short",
			Price:      kline.Close,
			StopLoss:   kline.High * 1.003, // 止损设在最高点上方0.3%
			Confidence: confidence,
			Reason:     fmt.Sprintf("看跌Pin Bar: 上影线%.2f%%, 实体%.2f%%", (upperShadow/totalRange)*100, (body/totalRange)*100),
		}
		signals = append(signals, signal)

		log.Printf("🔔 [Signal] %s %s - 看跌Pin Bar (强度:%d%%) | 价格:%.2f | 止损:%.2f",
			symbol, timeFrame, confidence, signal.Price, signal.StopLoss)
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

// DetectVolumeSpike 检测成交量放大
// 标准：最新K线成交量 >= 上一根K线的150%
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

	// 计算成交量放大倍数
	volumeRatio := currentKline.Volume / prevKline.Volume

	// 成交量放大 >= 150%
	if volumeRatio >= 1.5 {
		// 判断方向（根据K线颜色）
		direction := "long"
		if currentKline.Close < currentKline.Open {
			direction = "short"
		}

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
			Direction:  direction,
			Price:      currentKline.Close,
			StopLoss:   calculateStopLoss(currentKline, direction),
			Confidence: confidence,
			Reason:     fmt.Sprintf("成交量放大%.1fx (%.0f -> %.0f)", volumeRatio, prevKline.Volume, currentKline.Volume),
		}
		signals = append(signals, signal)

		log.Printf("🔔 [Signal] %s %s - 成交量放大%.1fx (强度:%d%%) | 方向:%s | 价格:%.2f",
			symbol, timeFrame, volumeRatio, confidence, direction, signal.Price)
	}

	return signals
}

// DetectEngulfing 检测吞没形态
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

	// 看涨吞没
	// 条件：前一根阴线，当前阳线，且当前K线完全吞没前一根
	if prevKline.Close < prevKline.Open && // 前一根是阴线
		currentKline.Close > currentKline.Open && // 当前是阳线
		currentKline.Open < prevKline.Close && // 当前开盘价 < 前一根收盘价
		currentKline.Close > prevKline.Open && // 当前收盘价 > 前一根开盘价
		currentBody > prevBody { // 当前实体 > 前一根实体

		confidence := 80
		if currentBody > prevBody*1.5 {
			confidence = 90
		}

		signal := &TradingSignal{
			Symbol:     symbol,
			TimeFrame:  timeFrame,
			SignalType: SignalEngulfing,
			Direction:  "long",
			Price:      currentKline.Close,
			StopLoss:   currentKline.Low * 0.995, // 止损设在当前K线最低点下方0.5%
			Confidence: confidence,
			Reason:     "看涨吞没形态",
		}
		signals = append(signals, signal)

		log.Printf("🔔 [Signal] %s %s - 看涨吞没 (强度:%d%%) | 价格:%.2f",
			symbol, timeFrame, confidence, signal.Price)
	}

	// 看跌吞没
	// 条件：前一根阳线，当前阴线，且当前K线完全吞没前一根
	if prevKline.Close > prevKline.Open && // 前一根是阳线
		currentKline.Close < currentKline.Open && // 当前是阴线
		currentKline.Open > prevKline.Close && // 当前开盘价 > 前一根收盘价
		currentKline.Close < prevKline.Open && // 当前收盘价 < 前一根开盘价
		currentBody > prevBody { // 当前实体 > 前一根实体

		confidence := 80
		if currentBody > prevBody*1.5 {
			confidence = 90
		}

		signal := &TradingSignal{
			Symbol:     symbol,
			TimeFrame:  timeFrame,
			SignalType: SignalEngulfing,
			Direction:  "short",
			Price:      currentKline.Close,
			StopLoss:   currentKline.High * 1.005, // 止损设在当前K线最高点上方0.5%
			Confidence: confidence,
			Reason:     "看跌吞没形态",
		}
		signals = append(signals, signal)

		log.Printf("🔔 [Signal] %s %s - 看跌吞没 (强度:%d%%) | 价格:%.2f",
			symbol, timeFrame, confidence, signal.Price)
	}

	return signals
}

// calculateStopLoss 计算止损价格
func calculateStopLoss(kline Kline, direction string) float64 {
	if direction == "long" {
		return kline.Low * 0.997 // 做多止损在最低点下方0.3%
	}
	return kline.High * 1.003 // 做空止损在最高点上方0.3%
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

// CombineSignals 合并同方向的多个信号
func CombineSignals(signals []*TradingSignal) map[string][]*TradingSignal {
	combined := make(map[string][]*TradingSignal)

	for _, signal := range signals {
		key := fmt.Sprintf("%s_%s_%s", signal.Symbol, signal.TimeFrame, signal.Direction)
		combined[key] = append(combined[key], signal)
	}

	return combined
}
