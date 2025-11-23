# AI Models Configuration Guide

NOFX supports multiple AI models for trading decisions. This guide helps you configure and use different AI services.

## 📋 Supported AI Models

| Model | Provider | Best For | API Docs |
|-------|----------|----------|----------|
| **Claude** | Anthropic | Complex reasoning, long context | [Anthropic API](https://docs.anthropic.com/) |
| **Gemini** | Google | Multimodal analysis, fast response | [Gemini API](https://ai.google.dev/docs) |
| **DeepSeek** | DeepSeek | Cost optimization, Chinese support | [DeepSeek API](https://platform.deepseek.com/) |
| **Qwen** | Alibaba Cloud | China access, Chinese optimized | [Alibaba Docs](https://help.aliyun.com/zh/dashscope/) |
| **Custom** | Any | OpenAI-compatible API | - |

---

## 🚀 Quick Start

### 1. Claude (Anthropic)

**Get API Key:**
1. Visit [Anthropic Console](https://console.anthropic.com/)
2. Create account and get API Key

**Configuration Steps:**

In Web UI:
1. Go to **Settings** → **AI Models**
2. Select **Claude (Anthropic)**
3. Fill in configuration:
   - **API Key**: Your Anthropic API Key (required)
   - **Custom API URL**: Leave empty for default (`https://api.anthropic.com/v1`)
   - **Custom Model Name**: Leave empty for default (`claude-3-5-sonnet-20241022`)
4. Click **Save**

**Supported Models:**
```
claude-3-5-sonnet-20241022  # Recommended: Latest Sonnet 3.5 (balanced)
claude-3-opus-20240229      # Most powerful reasoning (slower, higher cost)
claude-3-sonnet-20240229    # Balanced option
claude-3-haiku-20240307     # Fastest response (lowest cost)
```

**API Format:**
- Endpoint: `https://api.anthropic.com/v1/messages`
- Authentication: `x-api-key` header
- System prompt: Separate `system` field

**Pricing (Claude 3.5 Sonnet):**
- Input: $3 / 1M tokens
- Output: $15 / 1M tokens

---

### 2. Gemini (Google)

**Get API Key:**
1. Visit [Google AI Studio](https://makersuite.google.com/app/apikey)
2. Create API Key

**Configuration Steps:**

In Web UI:
1. Go to **Settings** → **AI Models**
2. Select **Gemini (Google)**
3. Fill in configuration:
   - **API Key**: Your Google AI API Key (required)
   - **Custom API URL**: Leave empty for default (`https://generativelanguage.googleapis.com/v1beta`)
   - **Custom Model Name**: Leave empty for default (`gemini-2.0-flash-exp`)
4. Click **Save**

**Supported Models:**
```
gemini-2.0-flash-exp        # Recommended: Latest experimental, powerful
gemini-1.5-pro              # Pro version, strong reasoning
gemini-1.5-flash            # Flash version, fast response
```

**API Format:**
- Endpoint: `https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent`
- Authentication: API Key in URL parameter
- System prompt: `systemInstruction` field

**Pricing (Gemini 1.5 Flash):**
- Input: $0.075 / 1M tokens (up to 128K context)
- Output: $0.30 / 1M tokens

---

### 3. DeepSeek (Already Supported)

**Get API Key:**
1. Visit [DeepSeek Platform](https://platform.deepseek.com/)
2. Register and get API Key

**Configuration:**
- API URL: `https://api.deepseek.com/v1`
- Default Model: `deepseek-chat`

---

### 4. Qwen (Already Supported)

**Get API Key:**
1. Visit [Alibaba Cloud DashScope](https://dashscope.aliyun.com/)
2. Enable service and get API Key

**Configuration:**
- API URL: `https://dashscope.aliyuncs.com/compatible-mode/v1`
- Default Model: `qwen3-max`

---

## 🔧 Advanced Configuration

### Using Custom API Endpoints

If you use proxies or third-party services (like OpenRouter, Azure OpenAI), you can customize the API URL:

**Example 1: Using OpenRouter for Claude**
```
AI Model: Claude (Anthropic)
Custom API URL: https://openrouter.ai/api/v1#
Custom Model Name: anthropic/claude-3-5-sonnet
API Key: sk-or-v1-xxxxx
```

**Example 2: Using China Proxy**
```
AI Model: Claude (Anthropic)
Custom API URL: https://your-proxy.com/v1
Custom Model Name: claude-3-5-sonnet-20241022
API Key: Your API Key
```

### URL Format Explained

- **Standard Format**: `https://api.example.com/v1`
  - System automatically adds `/messages` (Claude) or `/chat/completions` (OpenAI-compatible)

- **Full Format**: `https://api.example.com/v1/custom/endpoint#`
  - Add `#` at the end to use full URL without automatic path addition

---

## 💰 Cost Comparison

| Model | Input ($/1M tokens) | Output ($/1M tokens) | Best Use Case |
|-------|---------------------|----------------------|---------------|
| Claude 3.5 Sonnet | $3 | $15 | Complex reasoning, swing trading |
| Gemini 1.5 Flash | $0.075 | $0.30 | High-frequency, quick decisions |
| DeepSeek Chat | $0.14 | $0.28 | Cost-performance balance |
| Qwen Max | ¥0.04/1K tokens | ¥0.12/1K tokens | China users |

> **Estimate**: One complete trading decision (with market data analysis) typically consumes 3000-8000 tokens (input) + 500-1500 tokens (output)

---

## 🎯 Model Selection Guide

### Scenario 1: High-Frequency Day Trading
**Recommended**: Gemini 1.5 Flash or DeepSeek
- **Why**: Fast response, low cost
- **Scan Interval**: 3-5 minutes

### Scenario 2: Swing Trading
**Recommended**: Claude 3.5 Sonnet
- **Why**: Strong reasoning, better trend recognition
- **Scan Interval**: 15-30 minutes

### Scenario 3: Cost-Sensitive
**Recommended**: DeepSeek or Gemini Flash
- **Why**: Single call cost < $0.01
- **Suitable For**: Small capital accounts

### Scenario 4: China Network Environment
**Recommended**: Qwen Max
- **Why**: Alibaba Cloud China nodes, stable access
- **Alternative**: Use proxy for other models

---

## 🔍 FAQ

### Q1: How do I know which model is best for me?
**A**: Start with Gemini Flash or DeepSeek to test your strategy, then upgrade to Claude if needed. Compare different models through backtesting.

### Q2: Claude API error "invalid x-api-key"
**A**: Check:
1. API Key copied correctly (no leading/trailing spaces)
2. Confirm API Key status is Active
3. Check account balance

### Q3: Gemini API error "API key not valid"
**A**:
1. Confirm API Key from [Google AI Studio](https://makersuite.google.com/app/apikey)
2. Check API access permissions (some regions may be restricted)
3. Try regenerating API Key

### Q4: Can I use multiple models simultaneously?
**A**: Yes! You can configure different AI models for different traders:
- Trader 1 uses Claude for BTC swing trading
- Trader 2 uses Gemini Flash for altcoin scalping

### Q5: When to add `#` to Custom API URL?
**A**:
- **Without `#`**: System auto-adds standard path (recommended)
  - Claude: `/messages`
  - OpenAI-compatible: `/chat/completions`
- **With `#`**: Use full URL without adding any path
  - Example: Some proxy services with custom endpoints

---

## 📚 More Resources

- **Claude API Docs**: https://docs.anthropic.com/claude/reference/getting-started-with-the-api
- **Gemini API Docs**: https://ai.google.dev/tutorials/get_started_web
- **NOFX Prompt Optimization**: [prompt-guide.md](../prompt-guide.md)
- **Troubleshooting**: [TROUBLESHOOTING.md](./TROUBLESHOOTING.md)

---

## 💡 Best Practices

1. **API Key Security**
   - Never share API Keys publicly
   - Rotate API Keys regularly
   - Set API usage limits

2. **Cost Control**
   - Set reasonable scan intervals (avoid too frequent)
   - Use `max_tokens` parameter to control output length (system default: 2000)
   - Monitor API usage and costs

3. **Performance Optimization**
   - Choose fast models for high-frequency trading (Gemini Flash)
   - Choose strong reasoning models for complex strategies (Claude Sonnet)
   - Adjust model selection based on actual performance

4. **Testing & Validation**
   - Test new models with small capital first
   - Compare decision quality across models
   - Review and analyze decision logs (`decision_logs/` directory)

---

**Last Updated**: 2025-01-22
**Version**: v3.1.0+
