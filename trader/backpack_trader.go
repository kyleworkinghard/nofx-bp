package trader

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"nofx/market"
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
	window := int64(60000) // 增加到60秒窗口，避免网络延迟导致过期

	// 🐛 调试：打印系统时间
	log.Printf("🐛 [Backpack] 当前系统时间: %s", time.Now().Format("2006-01-02 15:04:05.000"))

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

	// 🐛 调试：打印签名字符串
	log.Printf("🐛 [Backpack] 签名字符串: %s", signatureStr)
	log.Printf("🐛 [Backpack] 时间戳: %d, 窗口: %d", timestamp, window)

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

	// 🐛 调试：打印请求头（隐藏敏感信息）
	log.Printf("🐛 [Backpack] 请求头: X-TIMESTAMP=%d, X-WINDOW=%d", timestamp, window)
	log.Printf("🐛 [Backpack] 签名（前20字符）: %s...", signatureB64[:min(20, len(signatureB64))])

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
		log.Printf("❌ [Backpack] API错误: %s %s -> HTTP %d", method, endpoint, resp.StatusCode)
		log.Printf("❌ [Backpack] 错误响应: %s", string(bodyBytes))
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

// makeAuthenticatedRequestArray 发起认证请求并返回数组
func (t *BackpackTrader) makeAuthenticatedRequestArray(method, endpoint string, params, data map[string]string) ([]interface{}, error) {
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
		log.Printf("❌ [Backpack] API错误: %s %s -> HTTP %d", method, endpoint, resp.StatusCode)
		log.Printf("❌ [Backpack] 错误响应: %s", string(bodyBytes))
		return nil, fmt.Errorf("API请求失败: HTTP %d - %s", resp.StatusCode, string(bodyBytes))
	}

	// 解析JSON数组
	var result []interface{}
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w, 响应: %s", err, string(bodyBytes))
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
	// 使用market包的统一转换函数
	return market.ConvertToBackpackSymbol(symbol)
}

// formatFloat 格式化浮点数，去除末尾的0
func formatFloat(val float64, precision int) string {
	formatted := strconv.FormatFloat(val, 'f', precision, 64)
	// 去除末尾的0
	formatted = strings.TrimRight(formatted, "0")
	formatted = strings.TrimRight(formatted, ".")
	return formatted
}

// min 返回两个整数中的较小值
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
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

	// 🐛 调试：打印原始响应
	log.Printf("🐛 [Backpack] 原始余额响应: %+v", resp)

	// 解析响应
	// Backpack 响应格式:
	// {
	//   "netEquity": 499.9,
	//   "netEquityAvailable": 499.9,
	//   "pnlUnrealized": 0,
	//   "collateral": [{"symbol": "USDC", "totalQuantity": "499.9", "availableQuantity": "499.9", ...}]
	// }

	// 优先使用顶层字段
	var totalWalletBalance float64 = 0
	var availableBalance float64 = 0
	var totalUnrealizedProfit float64 = 0

	// 获取总净值
	if netEquity, ok := resp["netEquity"].(float64); ok {
		totalWalletBalance = netEquity
	}

	// 获取可用净值
	if netEquityAvailable, ok := resp["netEquityAvailable"].(float64); ok {
		availableBalance = netEquityAvailable
	}

	// 获取未实现盈亏
	if pnlUnrealized, ok := resp["pnlUnrealized"].(float64); ok {
		totalUnrealizedProfit = pnlUnrealized
	}

	// 如果顶层字段为空，尝试从 collateral 数组中计算
	if totalWalletBalance == 0 {
		if collateralData, ok := resp["collateral"]; ok {
			if collateralList, ok := collateralData.([]interface{}); ok {
				for _, item := range collateralList {
					collateral, ok := item.(map[string]interface{})
					if !ok {
						continue
					}

					// 获取总额 (totalQuantity)
					if totalQtyStr, ok := collateral["totalQuantity"].(string); ok {
						if total, err := strconv.ParseFloat(totalQtyStr, 64); err == nil {
							totalWalletBalance += total
						}
					} else if totalQty, ok := collateral["totalQuantity"].(float64); ok {
						totalWalletBalance += totalQty
					}

					// 获取可用余额 (availableQuantity)
					if availableQtyStr, ok := collateral["availableQuantity"].(string); ok {
						if available, err := strconv.ParseFloat(availableQtyStr, 64); err == nil {
							availableBalance += available
						}
					} else if availableQty, ok := collateral["availableQuantity"].(float64); ok {
						availableBalance += availableQty
					}
				}
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

	// 调用 /api/v1/position 获取持仓（返回数组）
	positionList, err := t.makeAuthenticatedRequestArray("GET", "/api/v1/position", nil, nil)
	if err != nil {
		return nil, fmt.Errorf("获取持仓失败: %w", err)
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

		// 获取符号（Backpack格式）
		backpackSymbol, _ := pos["symbol"].(string)
		// 转换为币安格式，以便与系统其他部分兼容
		symbol := market.Normalize(backpackSymbol) // ETH_USDC_PERP -> ETHUSDT

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
		log.Printf("  - %s (%s): %s %.4f @ %.2f (PnL: %.2f)", symbol, backpackSymbol, side, size, entryPrice, unrealizedPnL)
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
// stopLoss: 止损价格（0表示不设置）
// takeProfit: 止盈价格（0表示不设置）
func (t *BackpackTrader) createOrder(symbol, side, orderType string, quantity float64, price *float64, stopLoss, takeProfit float64) (map[string]interface{}, error) {
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

	// ✅ Backpack 止盈止损：在开仓订单中设置（OCO订单，互相取消）
	if stopLoss > 0 {
		data["stopLossTriggerPrice"] = formatFloat(stopLoss, 2)
		log.Printf("  → 止损触发价: %.2f", stopLoss)
	}
	if takeProfit > 0 {
		data["takeProfitTriggerPrice"] = formatFloat(takeProfit, 2)
		log.Printf("  → 止盈触发价: %.2f", takeProfit)
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
	// 将币安格式转换为Backpack格式: ETHUSDT -> ETH_USDC_PERP
	backpackSymbol := market.ConvertToBackpackSymbol(symbol)
	log.Printf("🟢 [Backpack] 开多仓: %s (原始:%s) 数量=%.4f 杠杆=%dx", backpackSymbol, symbol, quantity, leverage)

	// Backpack使用Bid表示做多（买入）
	// 注意：这个方法不带止盈止损，如需止盈止损请使用 OpenLongWithProtection
	return t.createOrder(backpackSymbol, "Bid", "Market", quantity, nil, 0, 0)
}

// OpenShort 开空仓
func (t *BackpackTrader) OpenShort(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	// 将币安格式转换为Backpack格式
	backpackSymbol := market.ConvertToBackpackSymbol(symbol)
	log.Printf("🔴 [Backpack] 开空仓: %s (原始:%s) 数量=%.4f 杠杆=%dx", backpackSymbol, symbol, quantity, leverage)

	// Backpack使用Ask表示做空（卖出）
	// 注意：这个方法不带止盈止损，如需止盈止损请使用 OpenShortWithProtection
	return t.createOrder(backpackSymbol, "Ask", "Market", quantity, nil, 0, 0)
}

// CloseLong 平多仓
func (t *BackpackTrader) CloseLong(symbol string, quantity float64) (map[string]interface{}, error) {
	// 将币安格式转换为Backpack格式
	backpackSymbol := market.ConvertToBackpackSymbol(symbol)

	// 如果 quantity = 0，表示全部平仓，需要先获取实际持仓数量
	if quantity == 0 {
		positions, err := t.GetPositions()
		if err != nil {
			return nil, fmt.Errorf("获取持仓失败: %w", err)
		}

		// 查找该币种的多仓持仓
		found := false
		for _, pos := range positions {
			posSymbol, _ := pos["symbol"].(string)
			posSide, _ := pos["side"].(string)
			posAmt, _ := pos["positionAmt"].(float64)

			if posSymbol == symbol && posSide == "long" && posAmt > 0 {
				quantity = posAmt
				found = true
				log.Printf("  → 全部平仓，实际数量: %.4f", quantity)
				break
			}
		}

		if !found || quantity == 0 {
			return nil, fmt.Errorf("没有找到 %s 的多仓持仓", symbol)
		}
	}

	log.Printf("🟡 [Backpack] 平多仓: %s (原始:%s) 数量=%.4f", backpackSymbol, symbol, quantity)

	// 平多仓 = 卖出 = Ask
	return t.createOrder(backpackSymbol, "Ask", "Market", quantity, nil, 0, 0)
}

// CloseShort 平空仓
func (t *BackpackTrader) CloseShort(symbol string, quantity float64) (map[string]interface{}, error) {
	// 将币安格式转换为Backpack格式
	backpackSymbol := market.ConvertToBackpackSymbol(symbol)

	// 如果 quantity = 0，表示全部平仓，需要先获取实际持仓数量
	if quantity == 0 {
		positions, err := t.GetPositions()
		if err != nil {
			return nil, fmt.Errorf("获取持仓失败: %w", err)
		}

		// 查找该币种的空仓持仓
		found := false
		for _, pos := range positions {
			posSymbol, _ := pos["symbol"].(string)
			posSide, _ := pos["side"].(string)
			posAmt, _ := pos["positionAmt"].(float64)

			if posSymbol == symbol && posSide == "short" && posAmt > 0 {
				quantity = posAmt
				found = true
				log.Printf("  → 全部平仓，实际数量: %.4f", quantity)
				break
			}
		}

		if !found || quantity == 0 {
			return nil, fmt.Errorf("没有找到 %s 的空仓持仓", symbol)
		}
	}

	log.Printf("🟡 [Backpack] 平空仓: %s (原始:%s) 数量=%.4f", backpackSymbol, symbol, quantity)

	// 平空仓 = 买入 = Bid
	return t.createOrder(backpackSymbol, "Bid", "Market", quantity, nil, 0, 0)
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
	if positionSide == "long" || positionSide == "LONG" {
		side = "Ask" // 多仓止损 = 卖出
	} else {
		side = "Bid" // 空仓止损 = 买入
	}

	// ⚠️ Backpack 注意事项：
	// Backpack 的真正止损应该在开仓时通过 stopLossTriggerPrice 参数设置
	// 这里作为事后设置，我们使用 Limit 订单挂在止损价格
	// 虽然不是触发式止损，但可以在价格到达时自动成交
	qtyStr, _ := t.FormatQuantity(backpackSymbol, quantity)
	data := map[string]string{
		"symbol":    backpackSymbol,
		"side":      side,
		"orderType": "Limit",  // 使用 Limit 而不是 StopMarket
		"quantity":  qtyStr,
		"price":     formatFloat(stopPrice, 2),
		"timeInForce": "GTC",  // Good Till Cancel
	}

	_, err := t.makeAuthenticatedRequest("POST", "/api/v1/order", nil, data)
	if err != nil {
		return fmt.Errorf("设置止损失败: %w", err)
	}

	log.Printf("✓ [Backpack] 止损已设置（使用Limit订单）")
	return nil
}

// SetTakeProfit 设置止盈
func (t *BackpackTrader) SetTakeProfit(symbol string, positionSide string, quantity, takeProfitPrice float64) error {
	backpackSymbol := t.mapSymbol(symbol)
	log.Printf("🎯 [Backpack] 设置止盈: %s %s 数量=%.4f 价格=%.2f", backpackSymbol, positionSide, quantity, takeProfitPrice)

	// 确定订单方向（止盈是反向订单）
	var side string
	if positionSide == "long" || positionSide == "LONG" {
		side = "Ask" // 多仓止盈 = 卖出
	} else {
		side = "Bid" // 空仓止盈 = 买入
	}

	// 创建限价止盈订单
	qtyStr, _ := t.FormatQuantity(backpackSymbol, quantity)
	data := map[string]string{
		"symbol":      backpackSymbol,
		"side":        side,
		"orderType":   "Limit",
		"quantity":    qtyStr,
		"price":       formatFloat(takeProfitPrice, 2),
		"timeInForce": "GTC",  // Good Till Cancel
	}

	_, err := t.makeAuthenticatedRequest("POST", "/api/v1/order", nil, data)
	if err != nil {
		return fmt.Errorf("设置止盈失败: %w", err)
	}

	log.Printf("✓ [Backpack] 止盈已设置（使用Limit订单）")
	return nil
}

// getOrderStatus 查询订单状态
func (t *BackpackTrader) getOrderStatus(symbol, orderID string) (string, error) {
	backpackSymbol := t.mapSymbol(symbol)

	params := map[string]string{
		"symbol":  backpackSymbol,
		"orderId": orderID,
	}

	resp, err := t.makeAuthenticatedRequest("GET", "/api/v1/order", params, nil)
	if err != nil {
		return "", fmt.Errorf("查询订单状态失败: %w", err)
	}

	// 获取订单状态
	status, ok := resp["status"].(string)
	if !ok {
		return "", fmt.Errorf("无法解析订单状态")
	}

	return status, nil
}

// waitForOrderFilled 等待订单成交（最多等待30秒）
func (t *BackpackTrader) waitForOrderFilled(symbol, orderID string, maxWaitSeconds int) error {
	backpackSymbol := t.mapSymbol(symbol)
	log.Printf("⏳ [Backpack] 等待订单成交: %s (订单ID: %s)", backpackSymbol, orderID)

	if maxWaitSeconds <= 0 {
		maxWaitSeconds = 30
	}

	// 每隔0.5秒检查一次
	checkInterval := 500 * time.Millisecond
	maxAttempts := maxWaitSeconds * 2 // 每秒2次

	for attempt := 0; attempt < maxAttempts; attempt++ {
		time.Sleep(checkInterval)

		status, err := t.getOrderStatus(symbol, orderID)
		if err != nil {
			log.Printf("  ⚠️ 查询订单状态失败: %v", err)
			continue
		}

		log.Printf("  → 订单状态: %s (第%d次检查)", status, attempt+1)

		switch status {
		case "Filled":
			log.Printf("  ✓ 订单已完全成交")
			return nil
		case "PartiallyFilled":
			log.Printf("  ⏳ 订单部分成交，继续等待...")
			continue
		case "New":
			// 订单还在队列中，继续等待
			continue
		case "Cancelled", "Expired", "Rejected":
			return fmt.Errorf("订单未成交，状态: %s", status)
		default:
			log.Printf("  ⚠️ 未知订单状态: %s，继续等待...", status)
			continue
		}
	}

	return fmt.Errorf("等待订单成交超时（%d秒）", maxWaitSeconds)
}

// OpenLongWithProtection 开多仓并设置止盈止损（Backpack专用方法）
// ✅ 使用 Backpack 的 OCO 订单功能，在开仓时同时设置止盈止损
func (t *BackpackTrader) OpenLongWithProtection(symbol string, quantity float64, leverage int, stopLoss, takeProfit float64) error {
	backpackSymbol := market.ConvertToBackpackSymbol(symbol)
	log.Printf("🟢 [Backpack] 开多仓（带保护）: %s 数量=%.4f 杠杆=%dx SL=%.2f TP=%.2f",
		symbol, quantity, leverage, stopLoss, takeProfit)

	// ✅ Backpack 一次性开仓+止盈止损（OCO订单）
	// 止盈和止损是互相关联的，触发一个会自动取消另一个
	order, err := t.createOrder(backpackSymbol, "Bid", "Market", quantity, nil, stopLoss, takeProfit)
	if err != nil {
		return fmt.Errorf("开仓失败: %w", err)
	}

	log.Printf("✓ [Backpack] 开多仓完成（带OCO保护），订单ID: %v", order["id"])
	return nil
}

// OpenShortWithProtection 开空仓并设置止盈止损（Backpack专用方法）
// ✅ 使用 Backpack 的 OCO 订单功能，在开仓时同时设置止盈止损
func (t *BackpackTrader) OpenShortWithProtection(symbol string, quantity float64, leverage int, stopLoss, takeProfit float64) error {
	backpackSymbol := market.ConvertToBackpackSymbol(symbol)
	log.Printf("🔴 [Backpack] 开空仓（带保护）: %s 数量=%.4f 杠杆=%dx SL=%.2f TP=%.2f",
		symbol, quantity, leverage, stopLoss, takeProfit)

	// ✅ Backpack 一次性开仓+止盈止损（OCO订单）
	// 止盈和止损是互相关联的，触发一个会自动取消另一个
	order, err := t.createOrder(backpackSymbol, "Ask", "Market", quantity, nil, stopLoss, takeProfit)
	if err != nil {
		return fmt.Errorf("开仓失败: %w", err)
	}

	log.Printf("✓ [Backpack] 开空仓完成（带OCO保护），订单ID: %v", order["id"])
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
	formatted := formatFloat(quantity, precision.QuantityPrecision)
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
			PricePrecision:    2,     // 默认价格精度
			QuantityPrecision: 8,     // 默认数量精度
			TickSize:          0.01,  // 默认价格步进
			StepSize:          0.00000001, // 默认数量步进
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
					precision.QuantityPrecision = calculatePrecision(stepSize)
					if step, err := strconv.ParseFloat(stepSize, 64); err == nil {
						precision.StepSize = step
					}
				}
			}
		}

		// 缓存精度信息
		t.symbolPrecision[symbol] = precision
		log.Printf("✓ [Backpack] %s 精度: 价格=%d位, 数量=%d位", symbol, precision.PricePrecision, precision.QuantityPrecision)
		return precision, nil
	}

	return nil, fmt.Errorf("未找到交易对 %s 的精度信息", symbol)
}
