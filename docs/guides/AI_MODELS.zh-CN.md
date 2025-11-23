# AI 模型配置指南

NOFX 支持多种 AI 模型用于交易决策。本指南将帮助你配置和使用不同的 AI 服务。

## 📋 支持的 AI 模型

| 模型 | 提供商 | 推荐用途 | API 文档 |
|------|--------|---------|----------|
| **Claude** | Anthropic | 复杂推理、长上下文分析 | [Anthropic API](https://docs.anthropic.com/) |
| **Gemini** | Google | 多模态分析、快速响应 | [Gemini API](https://ai.google.dev/docs) |
| **DeepSeek** | DeepSeek | 成本优化、中文支持 | [DeepSeek API](https://platform.deepseek.com/) |
| **Qwen** | 阿里云 | 国内访问、中文优化 | [阿里云文档](https://help.aliyun.com/zh/dashscope/) |
| **Custom** | 任意 | OpenAI 兼容 API | - |

---

## 🚀 快速开始

### 1. Claude (Anthropic)

**获取 API Key:**
1. 访问 [Anthropic Console](https://console.anthropic.com/)
2. 创建账户并获取 API Key

**配置步骤:**

在 Web UI 中：
1. 进入 **Settings** → **AI Models**
2. 选择 **Claude (Anthropic)**
3. 填写配置：
   - **API Key**: 你的 Anthropic API Key（必填）
   - **Custom API URL**: 留空使用默认（`https://api.anthropic.com/v1`）
   - **Custom Model Name**: 留空使用默认（`claude-3-5-sonnet-20241022`）
4. 点击 **Save** 保存

**支持的模型:**
```
claude-3-5-sonnet-20241022  # 推荐：最新的 Sonnet 3.5（智能与速度平衡）
claude-3-opus-20240229      # 最强推理能力（较慢，成本较高）
claude-3-sonnet-20240229    # 平衡选择
claude-3-haiku-20240307     # 最快响应（成本最低）
```

**API 格式说明:**
- Endpoint: `https://api.anthropic.com/v1/messages`
- 认证方式: `x-api-key` header
- 系统提示词: 作为单独的 `system` 字段

**成本参考 (Claude 3.5 Sonnet):**
- Input: $3 / 1M tokens
- Output: $15 / 1M tokens

---

### 2. Gemini (Google)

**获取 API Key:**
1. 访问 [Google AI Studio](https://makersuite.google.com/app/apikey)
2. 创建 API Key

**配置步骤:**

在 Web UI 中：
1. 进入 **Settings** → **AI Models**
2. 选择 **Gemini (Google)**
3. 填写配置：
   - **API Key**: 你的 Google AI API Key（必填）
   - **Custom API URL**: 留空使用默认（`https://generativelanguage.googleapis.com/v1beta`）
   - **Custom Model Name**: 留空使用默认（`gemini-2.0-flash-exp`）
4. 点击 **Save** 保存

**支持的模型:**
```
gemini-2.0-flash-exp        # 推荐：最新实验版，性能强大
gemini-1.5-pro              # Pro 版本，强大推理能力
gemini-1.5-flash            # Flash 版本，快速响应
```

**API 格式说明:**
- Endpoint: `https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent`
- 认证方式: API Key 在 URL 参数中
- 系统提示词: 作为 `systemInstruction` 字段

**成本参考 (Gemini 1.5 Flash):**
- Input: $0.075 / 1M tokens (128K上下文以内)
- Output: $0.30 / 1M tokens

---

### 3. DeepSeek（已有支持）

**获取 API Key:**
1. 访问 [DeepSeek Platform](https://platform.deepseek.com/)
2. 注册并获取 API Key

**配置:**
- API URL: `https://api.deepseek.com/v1`
- 默认模型: `deepseek-chat`

---

### 4. Qwen（已有支持）

**获取 API Key:**
1. 访问 [阿里云 DashScope](https://dashscope.aliyun.com/)
2. 开通服务并获取 API Key

**配置:**
- API URL: `https://dashscope.aliyuncs.com/compatible-mode/v1`
- 默认模型: `qwen3-max`

---

## 🔧 高级配置

### 使用自定义 API 端点

如果你使用代理或第三方服务（如 OpenRouter、Azure OpenAI），可以自定义 API 地址：

**示例 1: 使用 OpenRouter 访问 Claude**
```
AI Model: Claude (Anthropic)
Custom API URL: https://openrouter.ai/api/v1#
Custom Model Name: anthropic/claude-3-5-sonnet
API Key: sk-or-v1-xxxxx
```

**示例 2: 使用国内代理**
```
AI Model: Claude (Anthropic)
Custom API URL: https://your-proxy.com/v1
Custom Model Name: claude-3-5-sonnet-20241022
API Key: 你的API Key
```

### URL 格式说明

- **标准格式**: `https://api.example.com/v1`
  - 系统会自动添加 `/messages`（Claude）或 `/chat/completions`（OpenAI兼容）

- **完整格式**: `https://api.example.com/v1/custom/endpoint#`
  - URL末尾添加 `#` 表示使用完整URL，不自动添加路径

---

## 💰 成本对比

| 模型 | Input ($/1M tokens) | Output ($/1M tokens) | 推荐场景 |
|------|---------------------|----------------------|---------|
| Claude 3.5 Sonnet | $3 | $15 | 复杂推理、长期持仓分析 |
| Gemini 1.5 Flash | $0.075 | $0.30 | 高频交易、快速决策 |
| DeepSeek Chat | $0.14 | $0.28 | 性价比平衡 |
| Qwen Max | ¥0.04/千tokens | ¥0.12/千tokens | 国内用户 |

> **估算**: 一次完整的交易决策（包含市场数据分析）通常消耗 3000-8000 tokens（输入）+ 500-1500 tokens（输出）

---

## 🎯 模型选择建议

### 场景 1: 高频日内交易
**推荐**: Gemini 1.5 Flash 或 DeepSeek
- **原因**: 响应快、成本低
- **扫描间隔**: 3-5 分钟

### 场景 2: 波段交易
**推荐**: Claude 3.5 Sonnet
- **原因**: 推理能力强，能更好地识别趋势
- **扫描间隔**: 15-30 分钟

### 场景 3: 成本敏感
**推荐**: DeepSeek 或 Gemini Flash
- **原因**: 单次调用成本 < $0.01
- **适合**: 小额资金账户

### 场景 4: 国内网络环境
**推荐**: Qwen Max
- **原因**: 阿里云国内节点，访问稳定
- **备选**: 使用代理访问其他模型

---

## 🔍 常见问题

### Q1: 如何知道哪个模型最适合我？
**A**: 建议先用 Gemini Flash 或 DeepSeek 测试策略，稳定后再考虑升级到 Claude。可以通过回测对比不同模型的表现。

### Q2: Claude API 报错 "invalid x-api-key"
**A**: 检查以下几点：
1. API Key 是否正确复制（注意前后空格）
2. 确认 API Key 状态为 Active
3. 检查账户余额是否充足

### Q3: Gemini API 报错 "API key not valid"
**A**:
1. 确认 API Key 从 [Google AI Studio](https://makersuite.google.com/app/apikey) 获取
2. 检查 API 访问权限（某些地区可能受限）
3. 尝试重新生成 API Key

### Q4: 可以同时使用多个模型吗？
**A**: 可以！你可以为不同的交易员（Trader）配置不同的 AI 模型，例如：
- Trader 1 使用 Claude 进行 BTC 波段交易
- Trader 2 使用 Gemini Flash 进行山寨币高频交易

### Q5: 自定义 API URL 什么时候需要加 `#`？
**A**:
- **不加 `#`**: 系统自动添加标准路径（推荐）
  - Claude: `/messages`
  - OpenAI兼容: `/chat/completions`
- **加 `#`**: 使用完整 URL，不添加任何路径
  - 例如某些代理服务有自定义的 endpoint

---

## 📚 更多资源

- **Claude API 文档**: https://docs.anthropic.com/claude/reference/getting-started-with-the-api
- **Gemini API 文档**: https://ai.google.dev/tutorials/get_started_web
- **NOFX 提示词优化指南**: [prompt-guide.zh-CN.md](../prompt-guide.zh-CN.md)
- **故障排查**: [TROUBLESHOOTING.zh-CN.md](./TROUBLESHOOTING.zh-CN.md)

---

## 💡 最佳实践

1. **API Key 安全**
   - 永远不要在公开场合分享 API Key
   - 定期轮换 API Key
   - 设置 API 使用额度限制

2. **成本控制**
   - 合理设置扫描间隔（避免过于频繁）
   - 使用 `max_tokens` 参数控制输出长度（系统默认 2000）
   - 监控 API 用量和成本

3. **性能优化**
   - 高频交易选择响应快的模型（Gemini Flash）
   - 复杂策略选择推理强的模型（Claude Sonnet）
   - 根据实际表现调整模型选择

4. **测试验证**
   - 新模型先用小额资金测试
   - 对比不同模型的决策质量
   - 记录并分析决策日志（`decision_logs/` 目录）

---

**更新时间**: 2025-01-22
**版本**: v3.1.0+
