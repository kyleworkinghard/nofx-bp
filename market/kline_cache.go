package market

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// TimeFrame 时间周期类型
type TimeFrame string

const (
	TimeFrame5m  TimeFrame = "5m"
	TimeFrame15m TimeFrame = "15m"
	TimeFrame30m TimeFrame = "30m"
	TimeFrame1h  TimeFrame = "1h"
	TimeFrame4h  TimeFrame = "4h"
	TimeFrame1d  TimeFrame = "1d"
)

// TimeFrameMinutes 每个时间周期对应的分钟数
var TimeFrameMinutes = map[TimeFrame]int{
	TimeFrame5m:  5,
	TimeFrame15m: 15,
	TimeFrame30m: 30,
	TimeFrame1h:  60,
	TimeFrame4h:  240,
	TimeFrame1d:  1440,
}

// BinanceIntervalMap 时间周期到Binance API interval的映射
var BinanceIntervalMap = map[TimeFrame]string{
	TimeFrame5m:  "5m",
	TimeFrame15m: "15m",
	TimeFrame30m: "30m",
	TimeFrame1h:  "1h",
	TimeFrame4h:  "4h",
	TimeFrame1d:  "1d",
}

// MultiTimeFrameKline 多周期K线数据缓存
type MultiTimeFrameKline struct {
	Symbol string
	Data   map[TimeFrame][]Kline // 每个周期的K线数据
	mu     sync.RWMutex
}

// KlineCache 全局K线缓存
type KlineCache struct {
	cache  map[string]*MultiTimeFrameKline // key: symbol
	client *APIClient
	mu     sync.RWMutex
}

var (
	globalKlineCache *KlineCache
	once             sync.Once
)

// GetKlineCache 获取全局K线缓存实例
func GetKlineCache() *KlineCache {
	once.Do(func() {
		globalKlineCache = &KlineCache{
			cache:  make(map[string]*MultiTimeFrameKline),
			client: NewAPIClient(),
		}
	})
	return globalKlineCache
}

// InitSymbol 初始化某个交易对的多周期K线数据
func (kc *KlineCache) InitSymbol(symbol string, maxKlines int) error {
	kc.mu.Lock()
	defer kc.mu.Unlock()

	if _, exists := kc.cache[symbol]; exists {
		log.Printf("✓ [KlineCache] %s 已初始化，跳过", symbol)
		return nil
	}

	mtk := &MultiTimeFrameKline{
		Symbol: symbol,
		Data:   make(map[TimeFrame][]Kline),
	}

	// 为每个时间周期获取初始K线数据
	timeFrames := []TimeFrame{TimeFrame5m, TimeFrame15m, TimeFrame30m, TimeFrame1h, TimeFrame4h, TimeFrame1d}

	for _, tf := range timeFrames {
		interval := BinanceIntervalMap[tf]
		klines, err := kc.client.GetKlines(symbol, interval, maxKlines)
		if err != nil {
			log.Printf("⚠️ [KlineCache] 获取 %s %s K线失败: %v", symbol, tf, err)
			continue
		}

		mtk.Data[tf] = klines
		log.Printf("✓ [KlineCache] 加载 %s %s: %d根K线", symbol, tf, len(klines))
	}

	kc.cache[symbol] = mtk
	return nil
}

// UpdateSymbol 更新某个交易对的K线数据（增量更新）
func (kc *KlineCache) UpdateSymbol(symbol string) error {
	kc.mu.Lock()
	defer kc.mu.Unlock()

	mtk, exists := kc.cache[symbol]
	if !exists {
		return fmt.Errorf("symbol %s not initialized", symbol)
	}

	mtk.mu.Lock()
	defer mtk.mu.Unlock()

	// 更新每个时间周期的K线数据
	timeFrames := []TimeFrame{TimeFrame5m, TimeFrame15m, TimeFrame30m, TimeFrame1h, TimeFrame4h, TimeFrame1d}

	for _, tf := range timeFrames {
		interval := BinanceIntervalMap[tf]

		// 只获取最新的2根K线（最后一根可能还在形成中）
		newKlines, err := kc.client.GetKlines(symbol, interval, 2)
		if err != nil {
			log.Printf("⚠️ [KlineCache] 更新 %s %s K线失败: %v", symbol, tf, err)
			continue
		}

		if len(newKlines) == 0 {
			continue
		}

		existingKlines := mtk.Data[tf]
		if len(existingKlines) == 0 {
			mtk.Data[tf] = newKlines
			continue
		}

		// 检查最后一根K线是否已完成
		lastExisting := existingKlines[len(existingKlines)-1]
		lastNew := newKlines[len(newKlines)-1]

		if lastNew.OpenTime > lastExisting.OpenTime {
			// 新K线已生成，追加到数组
			mtk.Data[tf] = append(existingKlines, newKlines...)
			log.Printf("🔄 [KlineCache] %s %s: 新增K线 (时间: %s)",
				symbol, tf, time.UnixMilli(lastNew.OpenTime).Format("15:04"))
		} else {
			// 更新最后一根K线（仍在形成中）
			existingKlines[len(existingKlines)-1] = lastNew
		}

		// 保持K线数量不超过限制（保留最新的20根）
		maxKeep := 20
		if len(mtk.Data[tf]) > maxKeep {
			mtk.Data[tf] = mtk.Data[tf][len(mtk.Data[tf])-maxKeep:]
		}
	}

	return nil
}

// GetKlines 获取指定交易对和时间周期的K线数据
func (kc *KlineCache) GetKlines(symbol string, timeFrame TimeFrame, limit int) ([]Kline, error) {
	kc.mu.RLock()
	defer kc.mu.RUnlock()

	mtk, exists := kc.cache[symbol]
	if !exists {
		return nil, fmt.Errorf("symbol %s not initialized", symbol)
	}

	mtk.mu.RLock()
	defer mtk.mu.RUnlock()

	klines, exists := mtk.Data[timeFrame]
	if !exists {
		return nil, fmt.Errorf("timeframe %s not found for %s", timeFrame, symbol)
	}

	// 返回最新的limit根K线
	if len(klines) <= limit {
		return klines, nil
	}

	return klines[len(klines)-limit:], nil
}

// GetLatestKline 获取最新的一根K线
func (kc *KlineCache) GetLatestKline(symbol string, timeFrame TimeFrame) (*Kline, error) {
	klines, err := kc.GetKlines(symbol, timeFrame, 1)
	if err != nil {
		return nil, err
	}

	if len(klines) == 0 {
		return nil, fmt.Errorf("no klines available")
	}

	return &klines[0], nil
}

// GetLatestTwoKlines 获取最新的两根K线（用于比较）
func (kc *KlineCache) GetLatestTwoKlines(symbol string, timeFrame TimeFrame) ([]Kline, error) {
	return kc.GetKlines(symbol, timeFrame, 2)
}
