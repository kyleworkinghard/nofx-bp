package trader

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// BackpackTrader Backpack交易所实现
type BackpackTrader struct {
	apiKey     string
	privateKey ed25519.PrivateKey
	baseURL    string
	client     *http.Client

	// 缓存
	symbolPrecision map[string]*SymbolPrecision
	marketInfo      map[string]interface{}
}

// SymbolPrecision 交易对精度信息
type SymbolPrecision struct {
	PricePrecision int
	QtyPrecision   int
	MinQty         float64
	MaxQty         float64
}

// NewBackpackTrader 创建Backpack交易器
// apiKey: Backpack API密钥
// privateKeyB64: base64编码的ED25519私钥
// userID: 用户ID (用于日志)
func NewBackpackTrader(apiKey, privateKeyB64, userID string) (*BackpackTrader, error) {
	// 解码base64私钥
	privateKeyBytes, err := base64.StdEncoding.DecodeString(privateKeyB64)
	if err != nil {
		return nil, fmt.Errorf("解码私钥失败: %w", err)
	}

	// 确保私钥长度正确 (ED25519私钥应该是32字节，但库使用的是64字节seed+public key)
	var privateKey ed25519.PrivateKey
	if len(privateKeyBytes) == 32 {
		// 如果是32字节，需要生成完整的64字节私钥
		privateKey = ed25519.NewKeyFromSeed(privateKeyBytes)
	} else if len(privateKeyBytes) == 64 {
		// 如果已经是64字节，直接使用
		privateKey = ed25519.PrivateKey(privateKeyBytes)
	} else {
		return nil, fmt.Errorf("私钥长度错误: 期望32或64字节，实际%d字节", len(privateKeyBytes))
	}

	trader := &BackpackTrader{
		apiKey:          apiKey,
		privateKey:      privateKey,
		baseURL:         "https://api.backpack.exchange/",
		client:          &http.Client{Timeout: 30 * time.Second},
		symbolPrecision: make(map[string]*SymbolPrecision),
		marketInfo:      make(map[string]interface{}),
	}

	log.Printf("🏦 Backpack交易器初始化成功 (用户: %s)", userID)
	return trader, nil
}

// determineInstructionType 根据请求方法和端点确定指令类型
func (t *BackpackTrader) determineInstructionType(method, endpoint string) string {
	method = strings.ToUpper(method)

	// 规范化端点
	if !strings.HasPrefix(endpoint, "/") {
		endpoint = "/" + endpoint
	}
	endpoint = strings.TrimSuffix(endpoint, "/")

	// 根据端点返回指令类型
	switch endpoint {
	case "/api/v1/account":
		if method == "GET" {
			return "accountQuery"
		}
	case "/api/v1/capital":
		if method == "GET" {
			return "balanceQuery"
		}
	case "/api/v1/capital/collateral":
		if method == "GET" {
			return "collateralQuery"
		}
	case "/api/v1/position":
		if method == "GET" {
			return "positionQuery"
		}
	case "/api/v1/orders":
		if method == "GET" {
			return "orderQueryAll"
		} else if method == "DELETE" {
			return "orderCancelAll"
		}
	case "/api/v1/order":
		if method == "POST" {
			return "orderExecute"
		} else if method == "DELETE" {
			return "orderCancel"
		} else if method == "GET" {
			return "orderQuery"
		}
	case "/api/v1/ticker":
		return "marketdataQuery"
	case "/wapi/v1/history/fills":
		if method == "GET" {
			return "fillHistoryQueryAll"
		}
	case "/wapi/v1/history/orders":
		if method == "GET" {
			return "orderHistoryQueryAll"
		}
	}

	// 未知端点，生成默认指令类型
	log.Printf("⚠️ 未知的API端点: %s %s", method, endpoint)
	return fmt.Sprintf("%s%s", strings.ToLower(method), strings.ReplaceAll(endpoint, "/", "_"))
}

// generateSignature 生成API请求签名
func (t *BackpackTrader) generateSignature(method, endpoint string, params, data map[string]string) (map[string]string, error) {
	// 获取指令类型
	instructionType := t.determineInstructionType(method, endpoint)

	// 当前时间戳（毫秒）
	timestamp := time.Now().UnixMilli()
	window := int64(5000)

	// 构建签名字符串
	signatureStr := fmt.Sprintf("instruction=%s", instructionType)

	// 添加查询参数（按字母顺序排序）
	if len(params) > 0 {
		keys := make([]string, 0, len(params))
		for k := range params {
			if params[k] != "" {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			signatureStr += fmt.Sprintf("&%s=%s", k, params[k])
		}
	}

	// 添加请求体参数（按字母顺序排序）
	if len(data) > 0 {
		keys := make([]string, 0, len(data))
		for k := range data {
			if data[k] != "" {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			signatureStr += fmt.Sprintf("&%s=%s", k, data[k])
		}
	}

	// 添加时间戳和窗口
	signatureStr += fmt.Sprintf("&timestamp=%d&window=%d", timestamp, window)

	// 使用ED25519签名
	messageBytes := []byte(signatureStr)
	signature := ed25519.Sign(t.privateKey, messageBytes)

	// Base64编码签名
	signatureB64 := base64.StdEncoding.EncodeToString(signature)

	// 构建请求头
	headers := map[string]string{
		"X-API-KEY":    t.apiKey,
		"X-SIGNATURE":  signatureB64,
		"X-TIMESTAMP":  fmt.Sprintf("%d", timestamp),
		"X-WINDOW":     fmt.Sprintf("%d", window),
		"Content-Type": "application/json",
	}

	return headers, nil
}

// makeAuthenticatedRequest 发起需要认证的API请求
func (t *BackpackTrader) makeAuthenticatedRequest(method, endpoint string, params, data map[string]string) (map[string]interface{}, error) {
	// 生成签名头部
	headers, err := t.generateSignature(method, endpoint, params, data)
	if err != nil {
		return nil, fmt.Errorf("生成签名失败: %w", err)
	}

	// 构建完整URL
	url := strings.TrimSuffix(t.baseURL, "/") + endpoint

	// 创建请求
	var req *http.Request
	method = strings.ToUpper(method)

	if method == "GET" {
		// GET请求，参数放在URL中
		if len(params) > 0 {
			queryParams := make([]string, 0, len(params))
			for k, v := range params {
				if v != "" {
					queryParams = append(queryParams, fmt.Sprintf("%s=%s", k, v))
				}
			}
			if len(queryParams) > 0 {
				url += "?" + strings.Join(queryParams, "&")
			}
		}
		req, err = http.NewRequest(method, url, nil)
	} else if method == "POST" || method == "PUT" || method == "DELETE" {
		// POST/PUT/DELETE请求，参数放在请求体中
		var body io.Reader
		if len(data) > 0 {
			jsonData, err := json.Marshal(data)
			if err != nil {
				return nil, fmt.Errorf("序列化请求体失败: %w", err)
			}
			body = strings.NewReader(string(jsonData))
		}
		req, err = http.NewRequest(method, url, body)
	} else {
		return nil, fmt.Errorf("不支持的HTTP方法: %s", method)
	}

	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	// 设置请求头
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	// 发送请求
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("发送请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	// 检查HTTP状态码
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("API请求失败: HTTP %d - %s", resp.StatusCode, string(bodyBytes))
	}

	// 尝试解析JSON
	var result map[string]interface{}
	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") {
		if err := json.Unmarshal(bodyBytes, &result); err != nil {
			// 如果不是JSON，检查是否是纯文本（如订单状态）
			textResult := string(bodyBytes)
			if textResult == "New" || textResult == "PartiallyFilled" || textResult == "Filled" {
				return map[string]interface{}{"status": textResult}, nil
			}
			return nil, fmt.Errorf("解析响应失败: %w, 响应: %s", err, string(bodyBytes))
		}
	} else {
		// 纯文本响应
		textResult := string(bodyBytes)
		return map[string]interface{}{"text": textResult}, nil
	}

	return result, nil
}

// makePublicRequest 发起公开API请求（不需要签名）
func (t *BackpackTrader) makePublicRequest(method, endpoint string, params map[string]string) (interface{}, error) {
	// 构建完整URL
	url := strings.TrimSuffix(t.baseURL, "/") + endpoint

	// GET请求，参数放在URL中
	if len(params) > 0 {
		queryParams := make([]string, 0, len(params))
		for k, v := range params {
			if v != "" {
				queryParams = append(queryParams, fmt.Sprintf("%s=%s", k, v))
			}
		}
		if len(queryParams) > 0 {
			url += "?" + strings.Join(queryParams, "&")
		}
	}

	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("发送请求失败: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("API请求失败: HTTP %d - %s", resp.StatusCode, string(bodyBytes))
	}

	// 尝试解析JSON
	var result interface{}
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	return result, nil
}

// mapSymbol 映射符号到Backpack格式
// 例如: BTCUSDT -> BTC_USDC_PERP
func (t *BackpackTrader) mapSymbol(symbol string) string {
	// 常见映射
	symbolMap := map[string]string{
		"BTCUSDT":  "BTC_USDC_PERP",
		"ETHUSDT":  "ETH_USDC_PERP",
		"SOLUSDT":  "SOL_USDC_PERP",
		"BNBUSDT":  "BNB_USDC_PERP",
		"XRPUSDT":  "XRP_USDC_PERP",
		"DOGEUSDT": "DOGE_USDC_PERP",
		"ADAUSDT":  "ADA_USDC_PERP",
		"HYPEUSDT": "HYPE_USDC_PERP",
	}

	if mapped, ok := symbolMap[symbol]; ok {
		return mapped
	}

	// 如果已经是Backpack格式，直接返回
	if strings.Contains(symbol, "_PERP") {
		return symbol
	}

	// 尝试自动转换: XXXUSDT -> XXX_USDC_PERP
	if strings.HasSuffix(symbol, "USDT") {
		base := strings.TrimSuffix(symbol, "USDT")
		return fmt.Sprintf("%s_USDC_PERP", base)
	}

	return symbol
}

// calculatePrecision 根据stepSize计算精度位数
func calculatePrecision(stepSize string) int {
	stepFloat, err := strconv.ParseFloat(stepSize, 64)
	if err != nil || stepFloat >= 1 {
		return 0
	}

	// 计算小数点后的位数
	precision := -int(math.Log10(stepFloat))
	if precision < 0 {
		precision = 0
	}
	return precision
}

// formatFloat 格式化浮点数，去除末尾的0
func formatFloat(val float64, precision int) string {
	formatted := strconv.FormatFloat(val, 'f', precision, 64)
	// 去除末尾的0
	formatted = strings.TrimRight(formatted, "0")
	formatted = strings.TrimRight(formatted, ".")
	return formatted
}

// ==================== Trader接口实现 ====================

// GetBalance 获取账户余额
func (t *BackpackTrader) GetBalance() (map[string]interface{}, error) {
	log.Printf("📊 [Backpack] 获取账户余额...")

	// 调用 /api/v1/capital/collateral 获取抵押品信息
	resp, err := t.makeAuthenticatedRequest("GET", "/api/v1/capital/collateral", nil, nil)
	if err != nil {
		return nil, fmt.Errorf("获取余额失败: %w", err)
	}

	// 解析响应
	// 响应格式: {"collateral": [{"asset": "USDC", "total": "1000.5", "available": "500.25", ...}]}
	collateralData, ok := resp["collateral"]
	if !ok {
		return nil, fmt.Errorf("响应缺少 collateral 字段")
	}

	collateralList, ok := collateralData.([]interface{})
	if !ok {
		return nil, fmt.Errorf("collateral 格式错误")
	}

	// 计算总余额
	var totalWalletBalance float64 = 0
	var availableBalance float64 = 0
	var totalUnrealizedProfit float64 = 0

	for _, item := range collateralList {
		collateral, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		// 获取总额
		if totalStr, ok := collateral["total"].(string); ok {
			if total, err := strconv.ParseFloat(totalStr, 64); err == nil {
				totalWalletBalance += total
			}
		}

		// 获取可用余额
		if availableStr, ok := collateral["available"].(string); ok {
			if available, err := strconv.ParseFloat(availableStr, 64); err == nil {
				availableBalance += available
			}
		}

		// 获取未实现盈亏（如果有）
		if unrealizedStr, ok := collateral["unrealized"].(string); ok {
			if unrealized, err := strconv.ParseFloat(unrealizedStr, 64); err == nil {
				totalUnrealizedProfit += unrealized
			}
		}
	}

	result := map[string]interface{}{
		"totalWalletBalance":    totalWalletBalance,
		"availableBalance":      availableBalance,
		"totalUnrealizedProfit": totalUnrealizedProfit,
	}

	log.Printf("✓ [Backpack] 余额: %.2f USDC (可用: %.2f, 未实现盈亏: %.2f)",
		totalWalletBalance, availableBalance, totalUnrealizedProfit)

	return result, nil
}

// GetPositions 获取当前持仓
func (t *BackpackTrader) GetPositions() ([]map[string]interface{}, error) {
	log.Printf("📊 [Backpack] 获取持仓信息...")

	// 调用 /api/v1/position 获取持仓
	resp, err := t.makeAuthenticatedRequest("GET", "/api/v1/position", nil, nil)
	if err != nil {
		return nil, fmt.Errorf("获取持仓失败: %w", err)
	}

	// 如果响应是数组，直接处理
	var positionList []interface{}
	if positions, ok := resp["positions"].([]interface{}); ok {
		positionList = positions
	} else if respArray, ok := interface{}(resp).([]interface{}); ok {
		// 如果响应本身就是数组
		positionList = respArray
	} else {
		// 可能响应是单个对象，包装成数组
		positionList = []interface{}{resp}
	}

	positions := make([]map[string]interface{}, 0)

	for _, item := range positionList {
		pos, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		// 解析持仓数量 (Backpack使用netQuantity，正数=多仓，负数=空仓)
		netQtyStr, ok := pos["netQuantity"].(string)
		if !ok {
			continue
		}

		netQty, err := strconv.ParseFloat(netQtyStr, 64)
		if err != nil || netQty == 0 {
			continue // 跳过0持仓
		}

		// 确定方向
		var side string
		var size float64
		if netQty > 0 {
			side = "long"
			size = netQty
		} else {
			side = "short"
			size = -netQty
		}

		// 获取符号
		symbol, _ := pos["symbol"].(string)

		// 获取入场价格
		entryPriceStr, _ := pos["entryPrice"].(string)
		entryPrice, _ := strconv.ParseFloat(entryPriceStr, 64)

		// 获取标记价格
		markPriceStr, _ := pos["markPrice"].(string)
		markPrice, _ := strconv.ParseFloat(markPriceStr, 64)

		// 获取未实现盈亏
		unrealizedPnLStr, _ := pos["pnlUnrealized"].(string)
		unrealizedPnL, _ := strconv.ParseFloat(unrealizedPnLStr, 64)

		// 获取清算价格
		liquidationPriceStr, _ := pos["liquidationPrice"].(string)
		liquidationPrice, _ := strconv.ParseFloat(liquidationPriceStr, 64)

		// 获取杠杆（Backpack可能不直接提供，使用默认值）
		leverage := 1.0
		if lev, ok := pos["leverage"].(float64); ok {
			leverage = lev
		}

		position := map[string]interface{}{
			"symbol":            symbol,
			"side":              side,
			"positionAmt":       size,
			"entryPrice":        entryPrice,
			"markPrice":         markPrice,
			"unRealizedProfit":  unrealizedPnL,
			"liquidationPrice":  liquidationPrice,
			"leverage":          leverage,
		}

		positions = append(positions, position)
		log.Printf("  - %s: %s %.4f @ %.2f (PnL: %.2f)", symbol, side, size, entryPrice, unrealizedPnL)
	}

	log.Printf("✓ [Backpack] 共 %d 个持仓", len(positions))
	return positions, nil
}

// GetMarketPrice 获取市场价格
func (t *BackpackTrader) GetMarketPrice(symbol string) (float64, error) {
	// 映射符号
	backpackSymbol := t.mapSymbol(symbol)

	// 调用公开API获取ticker
	resp, err := t.makePublicRequest("GET", "/api/v1/ticker", map[string]string{
		"symbol": backpackSymbol,
	})
	if err != nil {
		return 0, fmt.Errorf("获取市场价格失败: %w", err)
	}

	// 解析响应
	ticker, ok := resp.(map[string]interface{})
	if !ok {
		return 0, fmt.Errorf("ticker响应格式错误")
	}

	// 获取最后成交价
	lastPriceStr, ok := ticker["lastPrice"].(string)
	if !ok {
		return 0, fmt.Errorf("响应缺少 lastPrice 字段")
	}

	lastPrice, err := strconv.ParseFloat(lastPriceStr, 64)
	if err != nil {
		return 0, fmt.Errorf("解析价格失败: %w", err)
	}

	log.Printf("💰 [Backpack] %s 当前价格: %.2f", backpackSymbol, lastPrice)
	return lastPrice, nil
}

// createOrder 创建订单（内部方法）
// side: "Bid" (做多) 或 "Ask" (做空)
// orderType: "Market" 或 "Limit"
func (t *BackpackTrader) createOrder(symbol, side, orderType string, quantity float64, price *float64) (map[string]interface{}, error) {
	backpackSymbol := t.mapSymbol(symbol)

	// 格式化数量
	qtyStr, err := t.FormatQuantity(backpackSymbol, quantity)
	if err != nil {
		log.Printf("⚠️ [Backpack] 格式化数量失败，使用默认精度: %v", err)
		qtyStr = formatFloat(quantity, 8)
	}

	// 构建订单参数
	data := map[string]string{
		"symbol":    backpackSymbol,
		"side":      side,
		"orderType": orderType,
		"quantity":  qtyStr,
	}

	// 限价单需要价格
	if orderType == "Limit" && price != nil {
		priceStr := formatFloat(*price, 2)
		data["price"] = priceStr
	}

	log.Printf("📤 [Backpack] 下单: %s %s %s %s", side, orderType, qtyStr, backpackSymbol)

	// 发送订单
	resp, err := t.makeAuthenticatedRequest("POST", "/api/v1/order", nil, data)
	if err != nil {
		return nil, fmt.Errorf("下单失败: %w", err)
	}

	log.Printf("✓ [Backpack] 订单已创建: %+v", resp)
	return resp, nil
}

// OpenLong 开多仓
func (t *BackpackTrader) OpenLong(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	log.Printf("🟢 [Backpack] 开多仓: %s 数量=%.4f 杠杆=%dx", symbol, quantity, leverage)

	// Backpack使用Bid表示做多（买入）
	return t.createOrder(symbol, "Bid", "Market", quantity, nil)
}

// OpenShort 开空仓
func (t *BackpackTrader) OpenShort(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	log.Printf("🔴 [Backpack] 开空仓: %s 数量=%.4f 杠杆=%dx", symbol, quantity, leverage)

	// Backpack使用Ask表示做空（卖出）
	return t.createOrder(symbol, "Ask", "Market", quantity, nil)
}

// CloseLong 平多仓
func (t *BackpackTrader) CloseLong(symbol string, quantity float64) (map[string]interface{}, error) {
	log.Printf("🟡 [Backpack] 平多仓: %s 数量=%.4f", symbol, quantity)

	// 平多仓 = 卖出 = Ask
	return t.createOrder(symbol, "Ask", "Market", quantity, nil)
}

// CloseShort 平空仓
func (t *BackpackTrader) CloseShort(symbol string, quantity float64) (map[string]interface{}, error) {
	log.Printf("🟡 [Backpack] 平空仓: %s 数量=%.4f", symbol, quantity)

	// 平空仓 = 买入 = Bid
	return t.createOrder(symbol, "Bid", "Market", quantity, nil)
}

// SetLeverage 设置杠杆（Backpack可能不支持动态调整杠杆）
func (t *BackpackTrader) SetLeverage(symbol string, leverage int) error {
	log.Printf("⚙️ [Backpack] 设置杠杆: %s = %dx (Backpack可能不支持动态调整)", symbol, leverage)
	// Backpack交易所可能在账户级别或交易对级别预设杠杆
	// 如果API不支持，这里只记录日志，不报错
	return nil
}

// SetMarginMode 设置保证金模式
func (t *BackpackTrader) SetMarginMode(symbol string, isCrossMargin bool) error {
	mode := "逐仓"
	if isCrossMargin {
		mode = "全仓"
	}
	log.Printf("⚙️ [Backpack] 设置保证金模式: %s = %s (Backpack可能不支持动态调整)", symbol, mode)
	// Backpack可能在账户级别固定保证金模式
	return nil
}

// CancelAllOrders 取消所有订单
func (t *BackpackTrader) CancelAllOrders(symbol string) error {
	backpackSymbol := t.mapSymbol(symbol)
	log.Printf("🗑️ [Backpack] 取消所有订单: %s", backpackSymbol)

	params := map[string]string{
		"symbol": backpackSymbol,
	}

	_, err := t.makeAuthenticatedRequest("DELETE", "/api/v1/orders", params, nil)
	if err != nil {
		return fmt.Errorf("取消所有订单失败: %w", err)
	}

	log.Printf("✓ [Backpack] 已取消 %s 的所有订单", backpackSymbol)
	return nil
}

// CancelStopLossOrders 取消止损订单
func (t *BackpackTrader) CancelStopLossOrders(symbol string) error {
	log.Printf("🗑️ [Backpack] 取消止损订单: %s", symbol)
	// Backpack可能需要先查询止损订单，然后逐个取消
	// 这里简化处理，取消所有订单
	return t.CancelAllOrders(symbol)
}

// CancelTakeProfitOrders 取消止盈订单
func (t *BackpackTrader) CancelTakeProfitOrders(symbol string) error {
	log.Printf("🗑️ [Backpack] 取消止盈订单: %s", symbol)
	// Backpack可能需要先查询止盈订单，然后逐个取消
	// 这里简化处理，取消所有订单
	return t.CancelAllOrders(symbol)
}

// CancelStopOrders 取消止损止盈订单
func (t *BackpackTrader) CancelStopOrders(symbol string) error {
	log.Printf("🗑️ [Backpack] 取消止损止盈订单: %s", symbol)
	return t.CancelAllOrders(symbol)
}

// SetStopLoss 设置止损
func (t *BackpackTrader) SetStopLoss(symbol string, positionSide string, quantity, stopPrice float64) error {
	backpackSymbol := t.mapSymbol(symbol)
	log.Printf("🛡️ [Backpack] 设置止损: %s %s 数量=%.4f 价格=%.2f", backpackSymbol, positionSide, quantity, stopPrice)

	// 确定订单方向（止损是反向订单）
	var side string
	if positionSide == "long" {
		side = "Ask" // 多仓止损 = 卖出
	} else {
		side = "Bid" // 空仓止损 = 买入
	}

	// 创建止损订单（使用StopMarket类型）
	qtyStr, _ := t.FormatQuantity(backpackSymbol, quantity)
	data := map[string]string{
		"symbol":     backpackSymbol,
		"side":       side,
		"orderType":  "StopMarket",
		"quantity":   qtyStr,
		"triggerPrice": formatFloat(stopPrice, 2),
	}

	_, err := t.makeAuthenticatedRequest("POST", "/api/v1/order", nil, data)
	if err != nil {
		return fmt.Errorf("设置止损失败: %w", err)
	}

	log.Printf("✓ [Backpack] 止损已设置")
	return nil
}

// SetTakeProfit 设置止盈
func (t *BackpackTrader) SetTakeProfit(symbol string, positionSide string, quantity, takeProfitPrice float64) error {
	backpackSymbol := t.mapSymbol(symbol)
	log.Printf("🎯 [Backpack] 设置止盈: %s %s 数量=%.4f 价格=%.2f", backpackSymbol, positionSide, quantity, takeProfitPrice)

	// 确定订单方向（止盈是反向订单）
	var side string
	if positionSide == "long" {
		side = "Ask" // 多仓止盈 = 卖出
	} else {
		side = "Bid" // 空仓止盈 = 买入
	}

	// 创建限价止盈订单
	qtyStr, _ := t.FormatQuantity(backpackSymbol, quantity)
	data := map[string]string{
		"symbol":    backpackSymbol,
		"side":      side,
		"orderType": "Limit",
		"quantity":  qtyStr,
		"price":     formatFloat(takeProfitPrice, 2),
	}

	_, err := t.makeAuthenticatedRequest("POST", "/api/v1/order", nil, data)
	if err != nil {
		return fmt.Errorf("设置止盈失败: %w", err)
	}

	log.Printf("✓ [Backpack] 止盈已设置")
	return nil
}

// FormatQuantity 格式化数量（根据交易对精度）
func (t *BackpackTrader) FormatQuantity(symbol string, quantity float64) (string, error) {
	backpackSymbol := t.mapSymbol(symbol)

	// 获取精度信息
	precision, err := t.getSymbolPrecision(backpackSymbol)
	if err != nil {
		log.Printf("⚠️ [Backpack] 获取 %s 精度失败: %v，使用默认精度", backpackSymbol, err)
		// 使用默认精度
		return formatFloat(quantity, 8), nil
	}

	// 格式化数量
	formatted := formatFloat(quantity, precision.QtyPrecision)
	return formatted, nil
}

// getSymbolPrecision 获取交易对精度信息
func (t *BackpackTrader) getSymbolPrecision(symbol string) (*SymbolPrecision, error) {
	// 检查缓存
	if precision, ok := t.symbolPrecision[symbol]; ok {
		return precision, nil
	}

	// 从市场信息获取精度
	// 调用 /api/v1/markets 获取所有市场信息
	resp, err := t.makePublicRequest("GET", "/api/v1/markets", nil)
	if err != nil {
		return nil, fmt.Errorf("获取市场信息失败: %w", err)
	}

	markets, ok := resp.([]interface{})
	if !ok {
		return nil, fmt.Errorf("市场信息格式错误")
	}

	// 查找对应的交易对
	for _, item := range markets {
		market, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		marketSymbol, _ := market["symbol"].(string)
		if marketSymbol != symbol {
			continue
		}

		// 解析精度信息
		precision := &SymbolPrecision{
			PricePrecision: 2,  // 默认价格精度
			QtyPrecision:   8,  // 默认数量精度
			MinQty:         0.001,
			MaxQty:         1000000,
		}

		// 从filters中获取精度
		if filters, ok := market["filters"].(map[string]interface{}); ok {
			// 价格精度
			if priceFilter, ok := filters["price"].(map[string]interface{}); ok {
				if tickSize, ok := priceFilter["tickSize"].(string); ok {
					precision.PricePrecision = calculatePrecision(tickSize)
				}
			}

			// 数量精度
			if qtyFilter, ok := filters["quantity"].(map[string]interface{}); ok {
				if stepSize, ok := qtyFilter["stepSize"].(string); ok {
					precision.QtyPrecision = calculatePrecision(stepSize)
				}
				if minQty, ok := qtyFilter["minQuantity"].(string); ok {
					if min, err := strconv.ParseFloat(minQty, 64); err == nil {
						precision.MinQty = min
					}
				}
			}
		}

		// 缓存精度信息
		t.symbolPrecision[symbol] = precision
		log.Printf("✓ [Backpack] %s 精度: 价格=%d位, 数量=%d位", symbol, precision.PricePrecision, precision.QtyPrecision)
		return precision, nil
	}

	return nil, fmt.Errorf("未找到交易对 %s 的精度信息", symbol)
}
