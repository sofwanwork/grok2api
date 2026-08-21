package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"regexp"
	"strings"
)

const (
	maxFunctionTools       = 128
	maxToolDescriptionSize = 16 << 10
)

var (
	toolNamePattern      = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	toolSyntaxPattern    = regexp.MustCompile(`(?i)<tool_calls|<tool_call|<function_call|<invoke\s|"tool_calls"\s*:|(?:^|\b)(?:invoke|call(?:ing)?|use|using|run(?:ning)?|execut(?:e|ing))\s+(?:the\s+)?tool\s+[A-Za-z0-9_-]`)
	toolCallsRootPattern = regexp.MustCompile(`(?is)<tool_calls\s*>(.*?)</tool_calls\s*>`)
	toolCallPattern      = regexp.MustCompile(`(?is)<tool_call\s*>(.*?)</tool_call\s*>`)
	toolNameTagPattern   = regexp.MustCompile(`(?is)<tool_name\s*>(.*?)</tool_name\s*>`)
	toolParamsTagPattern = regexp.MustCompile(`(?is)<parameters\s*>(.*?)</parameters\s*>`)
	functionCallPattern  = regexp.MustCompile(`(?is)<function_call\s*>(.*?)</function_call\s*>`)
	functionNamePattern  = regexp.MustCompile(`(?is)<name\s*>(.*?)</name\s*>`)
	functionArgsPattern  = regexp.MustCompile(`(?is)<arguments\s*>(.*?)</arguments\s*>`)
	invokePattern        = regexp.MustCompile(`(?is)<invoke\s+name=["']?([A-Za-z0-9_-]+)["']?\s*>(.*?)</invoke\s*>`)
	// nlToolCallPattern 匹配自然语言工具调用，例如 "invoke tool get_weather with city is Kuala Lumpur"
	// 或 "call tool search with {\"q\": \"x\"}"。仅在 XML / JSON 格式全部不匹配时作为最后手段启用。
	nlToolCallPattern = regexp.MustCompile(`(?is)(?:^|\b)(?:invoke|call(?:ing)?|use|using|run(?:ning)?|execut(?:e|ing))\s+(?:the\s+)?tool\s+([A-Za-z0-9_-]{1,64})\s+with\s+(.+?)\s*$`)
	// nlArgPairPattern 把 "city is Kuala Lumpur, unit is c" 这类片段拆成 key/value 对。
	nlArgPairPattern = regexp.MustCompile(`(?is)\s*([A-Za-z0-9_]{1,64})\s+(?:is|=|as|:)\s+(.+?)\s*$`)
)

type functionTool struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

type toolConfiguration struct {
	Functions       []functionTool
	HostedWebSearch bool
	available       map[string]struct{}
	Choice          string
	ForcedName      string
	ResponseTools   []any
	ResponseChoice  any
}

type parsedToolCall struct {
	ID        string
	Name      string
	Arguments string
}

type toolParseResult struct {
	Calls     []parsedToolCall
	SawSyntax bool
	Start     int
	End       int
}

type toolStreamResult struct {
	SafeText string
	Calls    []parsedToolCall
	Complete bool
	Raw      string
}

type toolStreamSieve struct {
	available map[string]struct{}
	buffer    string
	capturing bool
	done      bool
}

// parseToolConfiguration 兼容 Chat Completions 与 Responses 的函数工具结构。
func parseToolConfiguration(rawTools, rawChoice json.RawMessage) (toolConfiguration, error) {
	configuration := toolConfiguration{Choice: "auto", ResponseChoice: "auto"}
	trimmed := bytes.TrimSpace(rawTools)
	if len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
		var values []map[string]any
		if err := json.Unmarshal(trimmed, &values); err != nil {
			return toolConfiguration{}, errors.New("tools mesti tatasusunan")
		}
		if len(values) > maxFunctionTools {
			return toolConfiguration{}, fmt.Errorf("tools tidak boleh melebihi %d", maxFunctionTools)
		}
		configuration.ResponseTools = make([]any, 0, len(values))
		for _, value := range values {
			configuration.ResponseTools = append(configuration.ResponseTools, value)
			function, supported, err := parseFunctionTool(value)
			if err != nil {
				return toolConfiguration{}, err
			}
			if supported {
				configuration.Functions = append(configuration.Functions, function)
				continue
			}
			typeName, _ := value["type"].(string)
			switch strings.ToLower(strings.TrimSpace(typeName)) {
			case "web_search", "web_search_preview":
				// Grok Web 原生搜索始终由上游执行，这两个标准声明无需注入函数提示词。
				configuration.HostedWebSearch = true
			default:
				return toolConfiguration{}, fmt.Errorf("Grok Web belum menyokong tools.type=%q", typeName)
			}
		}
	}

	choice, forcedName, responseChoice, err := parseToolChoice(rawChoice)
	if err != nil {
		return toolConfiguration{}, err
	}
	configuration.Choice = choice
	configuration.ForcedName = forcedName
	configuration.ResponseChoice = responseChoice
	configuration.available = make(map[string]struct{}, len(configuration.Functions))
	for _, function := range configuration.Functions {
		if _, exists := configuration.available[function.Name]; exists {
			return toolConfiguration{}, fmt.Errorf("Nama function tool %q berulang", function.Name)
		}
		configuration.available[function.Name] = struct{}{}
	}
	if forcedName != "" {
		if _, ok := configuration.available[forcedName]; !ok {
			return toolConfiguration{}, fmt.Errorf("Fungsi %q yang ditetapkan oleh tool_choice tidak wujud", forcedName)
		}
	}
	if (choice == "required" || forcedName != "") && len(configuration.Functions) == 0 && !configuration.HostedWebSearch {
		return toolConfiguration{}, errors.New("tool_choice memerlukan pemanggilan fungsi, tetapi tiada fungsi yang boleh digunakan dalam tools")
	}
	return configuration, nil
}

func parseFunctionTool(value map[string]any) (functionTool, bool, error) {
	typeName, _ := value["type"].(string)
	if strings.ToLower(strings.TrimSpace(typeName)) != "function" {
		return functionTool{}, false, nil
	}
	definition := value
	if nested, ok := value["function"].(map[string]any); ok {
		definition = nested
	}
	name, _ := definition["name"].(string)
	name = strings.TrimSpace(name)
	if !toolNamePattern.MatchString(name) {
		return functionTool{}, false, errors.New("name function tool mesti 1 hingga 64 aksara huruf, angka, garis bawah atau sempang")
	}
	description, _ := definition["description"].(string)
	if len(description) > maxToolDescriptionSize {
		return functionTool{}, false, fmt.Errorf("description fungsi %q terlalu panjang", name)
	}
	parameters := json.RawMessage(`{"type":"object","properties":{}}`)
	if raw, ok := definition["parameters"]; ok {
		encoded, err := json.Marshal(raw)
		if err != nil || !json.Valid(encoded) {
			return functionTool{}, false, fmt.Errorf("parameters fungsi %q bukan JSON yang sah", name)
		}
		parameters = encoded
	}
	return functionTool{Name: name, Description: strings.TrimSpace(description), Parameters: parameters}, true, nil
}

func parseToolChoice(raw json.RawMessage) (string, string, any, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "auto", "", "auto", nil
	}
	var text string
	if json.Unmarshal(trimmed, &text) == nil {
		text = strings.ToLower(strings.TrimSpace(text))
		switch text {
		case "auto", "none", "required":
			return text, "", text, nil
		default:
			return "", "", nil, errors.New("tool_choice mesti auto, none, required atau objek fungsi")
		}
	}
	var value map[string]any
	if json.Unmarshal(trimmed, &value) != nil {
		return "", "", nil, errors.New("Format tool_choice tidak sah")
	}
	typeName, _ := value["type"].(string)
	typeName = strings.ToLower(strings.TrimSpace(typeName))
	switch typeName {
	case "none", "auto", "required":
		return typeName, "", value, nil
	case "function":
		name, _ := value["name"].(string)
		if nested, ok := value["function"].(map[string]any); ok {
			name, _ = nested["name"].(string)
		}
		name = strings.TrimSpace(name)
		if !toolNamePattern.MatchString(name) {
			return "", "", nil, errors.New("tool_choice.function.name tidak sah")
		}
		return "required", name, value, nil
	default:
		return "", "", nil, fmt.Errorf("Grok Web belum menyokong tool_choice.type=%q", typeName)
	}
}

// injectToolPrompt 将函数定义转换为 Grok Web 可稳定生成的 XML 调用约定。
func injectToolPrompt(prompt string, configuration toolConfiguration) string {
	if len(configuration.Functions) == 0 || configuration.Choice == "none" {
		return prompt
	}
	var definitions strings.Builder
	for index, function := range configuration.Functions {
		if index > 0 {
			definitions.WriteString("\n\n")
		}
		definitions.WriteString("Tool: ")
		definitions.WriteString(function.Name)
		if function.Description != "" {
			definitions.WriteString("\nDescription: ")
			definitions.WriteString(function.Description)
		}
		definitions.WriteString("\nParameters: ")
		definitions.Write(function.Parameters)
	}
	choiceInstruction := "Call a tool when it is clearly needed. Otherwise respond in plain text."
	if configuration.ForcedName != "" {
		choiceInstruction = fmt.Sprintf("You MUST call the tool named %q and must not write a plain-text reply.", configuration.ForcedName)
	} else if configuration.Choice == "required" && !configuration.HostedWebSearch {
		choiceInstruction = "You MUST call at least one available tool and must not write a plain-text reply."
	}
	system := fmt.Sprintf(`You have access to the following tools.

AVAILABLE TOOLS:
%s

TOOL CALL FORMAT - follow these rules exactly:
- When you decide to call a tool, your FINAL output must be only the XML block below — you may think privately first, but the visible reply must contain nothing except the block.
- <parameters> must contain one valid JSON object.
- Put multiple calls inside one <tool_calls> element.
- Do not use Markdown code fences.

<tool_calls>
  <tool_call>
    <tool_name>TOOL_NAME</tool_name>
    <parameters>{"key":"value"}</parameters>
  </tool_call>
</tool_calls>

WHEN TO CALL: %s`, definitions.String(), choiceInstruction)
	return "[system]\n" + system + "\n\n" + prompt
}

func toolCallsToXML(raw json.RawMessage) string {
	var values []struct {
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	}
	if json.Unmarshal(raw, &values) != nil || len(values) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("<tool_calls>")
	for _, value := range values {
		if !toolNamePattern.MatchString(value.Function.Name) {
			continue
		}
		arguments := normalizeToolArguments(value.Function.Arguments)
		builder.WriteString("\n  <tool_call>\n    <tool_name>")
		builder.WriteString(html.EscapeString(value.Function.Name))
		builder.WriteString("</tool_name>\n    <parameters>")
		builder.WriteString(arguments)
		builder.WriteString("</parameters>\n  </tool_call>")
	}
	builder.WriteString("\n</tool_calls>")
	return builder.String()
}

// parseToolCalls 解析模型输出，并仅保留请求中声明过的函数名。
func parseToolCalls(text string, available map[string]struct{}) toolParseResult {
	result := toolParseResult{SawSyntax: toolSyntaxPattern.MatchString(text), Start: -1, End: -1}
	if !result.SawSyntax {
		return result
	}
	if match := toolCallsRootPattern.FindStringSubmatchIndex(text); match != nil {
		result.Start, result.End = match[0], match[1]
		root := text[match[2]:match[3]]
		for _, raw := range toolCallPattern.FindAllStringSubmatch(root, -1) {
			nameMatch := toolNameTagPattern.FindStringSubmatch(raw[1])
			if len(nameMatch) == 0 {
				continue
			}
			arguments := "{}"
			if paramsMatch := toolParamsTagPattern.FindStringSubmatch(raw[1]); len(paramsMatch) > 0 {
				arguments = paramsMatch[1]
			}
			appendParsedToolCall(&result.Calls, html.UnescapeString(strings.TrimSpace(nameMatch[1])), arguments, available)
		}
		return result
	}
	if match := functionCallPattern.FindStringSubmatchIndex(text); match != nil {
		result.Start, result.End = match[0], match[1]
		inner := text[match[2]:match[3]]
		nameMatch := functionNamePattern.FindStringSubmatch(inner)
		if len(nameMatch) > 0 {
			arguments := "{}"
			if argsMatch := functionArgsPattern.FindStringSubmatch(inner); len(argsMatch) > 0 {
				arguments = argsMatch[1]
			}
			appendParsedToolCall(&result.Calls, html.UnescapeString(strings.TrimSpace(nameMatch[1])), arguments, available)
		}
		return result
	}
	if match := invokePattern.FindStringSubmatchIndex(text); match != nil {
		result.Start, result.End = match[0], match[1]
		appendParsedToolCall(&result.Calls, text[match[2]:match[3]], text[match[4]:match[5]], available)
		return result
	}
	if match := nlToolCallPattern.FindStringSubmatchIndex(text); match != nil {
		name := text[match[2]:match[3]]
		rawArgs := strings.TrimSpace(text[match[4]:match[5]])
		if _, ok := available[name]; ok {
			arguments := parseNaturalLanguageArgs(rawArgs)
			result.Start, result.End = match[0], match[1]
			appendParsedToolCall(&result.Calls, name, arguments, available)
			if len(result.Calls) > 0 {
				return result
			}
		}
	}
	return parseJSONToolCalls(text, available, result)
}

func parseJSONToolCalls(text string, available map[string]struct{}, result toolParseResult) toolParseResult {
	start := strings.IndexByte(text, '{')
	if start < 0 {
		return result
	}
	decoder := json.NewDecoder(strings.NewReader(text[start:]))
	var envelope struct {
		ToolCalls []map[string]any `json:"tool_calls"`
	}
	if decoder.Decode(&envelope) != nil || len(envelope.ToolCalls) == 0 {
		return result
	}
	result.Start = start
	result.End = len(text)
	for _, value := range envelope.ToolCalls {
		name, _ := value["name"].(string)
		arguments := value["arguments"]
		if function, ok := value["function"].(map[string]any); ok {
			name, _ = function["name"].(string)
			arguments = function["arguments"]
		}
		if name == "" {
			name, _ = value["tool_name"].(string)
		}
		if arguments == nil {
			arguments = value["parameters"]
		}
		argumentText := "{}"
		if text, ok := arguments.(string); ok {
			argumentText = text
		} else if arguments != nil {
			encoded, _ := json.Marshal(arguments)
			argumentText = string(encoded)
		}
		appendParsedToolCall(&result.Calls, strings.TrimSpace(name), argumentText, available)
	}
	return result
}

func appendParsedToolCall(calls *[]parsedToolCall, name, arguments string, available map[string]struct{}) {
	if _, ok := available[name]; !ok {
		return
	}
	arguments = normalizeToolArguments(html.UnescapeString(strings.TrimSpace(arguments)))
	if !json.Valid([]byte(arguments)) {
		return
	}
	var object map[string]any
	if json.Unmarshal([]byte(arguments), &object) != nil {
		return
	}
	*calls = append(*calls, parsedToolCall{ID: newWebID("call"), Name: name, Arguments: arguments})
}

// parseNaturalLanguageArgs 把自然语言参数段转换成 JSON object 字符串。
// 支持三种形态：
//  1. 纯 JSON：{"city":"KL"} 原样返回（规范化后）。
//  2. "key is value" / "key: value" / "key = value"，逗号或 " and " 分隔。
//  3. 单个无 key 值（如仅 "Kuala Lumpur"）——返回空对象，由 schema 默认值兜底，
//     避免瞎猜参数名造成 schema 校验失败。
func parseNaturalLanguageArgs(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "{}"
	}
	// 形态 1：直接就是 JSON
	if strings.HasPrefix(raw, "{") {
		if json.Valid([]byte(raw)) {
			return normalizeToolArguments(raw)
		}
		// 尝试截取第一个完整 JSON 对象
		if end := findJSONObjectEnd(raw); end > 0 {
			candidate := raw[:end]
			if json.Valid([]byte(candidate)) {
				return normalizeToolArguments(candidate)
			}
		}
		return "{}"
	}
	// 形态 2：key/value 对。按 ", " / " and " 切分。
	object := map[string]any{}
	parts := splitNLArgs(raw)
	matched := 0
	for _, part := range parts {
		m := nlArgPairPattern.FindStringSubmatch(strings.TrimSpace(part))
		if len(m) != 3 {
			continue
		}
		key := strings.TrimSpace(m[1])
		value := strings.Trim(strings.TrimSpace(m[2]), `"'`)
		if key == "" || value == "" {
			continue
		}
		object[key] = value
		matched++
	}
	if matched == 0 {
		return "{}"
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

// splitNLArgs 按顶层逗号或 " and " 切分参数段（不进入引号/括号内部）。
func splitNLArgs(raw string) []string {
	var parts []string
	depth := 0
	inQuote := false
	var quote rune
	start := 0
	runes := []rune(raw)
	flush := func(end int) {
		seg := strings.TrimSpace(string(runes[start:end]))
		if seg != "" {
			parts = append(parts, seg)
		}
	}
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		if inQuote {
			if ch == quote {
				inQuote = false
			}
			continue
		}
		switch ch {
		case '"', '\'':
			inQuote = true
			quote = ch
		case '{', '[', '(':
			depth++
		case '}', ']', ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				flush(i)
				start = i + 1
			}
		}
		// " and " 作为分隔符（仅顶层）
		if depth == 0 && i+5 <= len(runes) && string(runes[i:i+5]) == " and " {
			flush(i)
			start = i + 5
			i += 4
		}
	}
	flush(len(runes))
	return parts
}

// findJSONObjectEnd 返回从开头算起第一个配平结束的 JSON 对象的结束位置。
func findJSONObjectEnd(raw string) int {
	depth := 0
	inQuote := false
	escaped := false
	for i, ch := range raw {
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && inQuote {
			escaped = true
			continue
		}
		if ch == '"' {
			inQuote = !inQuote
			continue
		}
		if inQuote {
			continue
		}
		switch ch {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}

func normalizeToolArguments(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "{}"
	}
	var parsed any
	if json.Unmarshal([]byte(value), &parsed) != nil {
		return value
	}
	encoded, err := json.Marshal(parsed)
	if err != nil {
		return value
	}
	return string(encoded)
}

func removeToolSyntax(text string, parsed toolParseResult) string {
	if parsed.Start < 0 || parsed.End <= parsed.Start || parsed.End > len(text) {
		return text
	}
	return strings.TrimSpace(text[:parsed.Start] + text[parsed.End:])
}

func newToolStreamSieve(available map[string]struct{}) *toolStreamSieve {
	return &toolStreamSieve{available: available}
}

// Feed 在流中发现工具 XML 后开始缓存，完整解析前不向客户端泄露内部标记。
func (s *toolStreamSieve) Feed(chunk string) toolStreamResult {
	if s.done || chunk == "" {
		return toolStreamResult{SafeText: chunk}
	}
	combined := s.buffer + chunk
	s.buffer = ""
	if !s.capturing {
		lower := strings.ToLower(combined)
		index := strings.Index(lower, "<tool_calls")
		if index < 0 {
			safe, pending := splitToolPrefix(combined)
			s.buffer = pending
			return toolStreamResult{SafeText: safe}
		}
		s.capturing = true
		s.buffer = combined[index:]
		combined = combined[:index]
	}
	lower := strings.ToLower(s.buffer)
	endIndex := strings.Index(lower, "</tool_calls>")
	if endIndex < 0 {
		return toolStreamResult{SafeText: combined}
	}
	endIndex += len("</tool_calls>")
	raw := s.buffer[:endIndex]
	remainder := s.buffer[endIndex:]
	parsed := parseToolCalls(raw, s.available)
	s.buffer = ""
	s.capturing = false
	s.done = len(parsed.Calls) > 0
	if len(parsed.Calls) == 0 {
		raw += remainder
	}
	return toolStreamResult{SafeText: combined, Calls: parsed.Calls, Complete: true, Raw: raw}
}

func (s *toolStreamSieve) Flush() toolStreamResult {
	if s.done || s.buffer == "" {
		return toolStreamResult{}
	}
	raw := s.buffer
	s.buffer = ""
	parsed := parseToolCalls(raw, s.available)
	if len(parsed.Calls) > 0 {
		s.done = true
		return toolStreamResult{Calls: parsed.Calls, Complete: true, Raw: raw}
	}
	// Jika buffer hanyalah prefix XML yang tidak lengkap (contoh "<tool_" tanpa
	// penutup), pulangkan semula sebagai teks selamat supaya tidak hilang.
	return toolStreamResult{SafeText: raw, Complete: parsed.SawSyntax, Raw: raw}
}

// FeedNL 在流式输出中检测自然语言工具调用（不含 <tool_calls 标签）。
// 仅在 buffer 完全不含 XML 前缀时调用，作为最后手段。
func (s *toolStreamSieve) FeedNL(chunk string) toolStreamResult {
	if s.done || chunk == "" {
		return toolStreamResult{SafeText: chunk}
	}
	combined := s.buffer + chunk
	s.buffer = ""
	// 仅在发现 XML 起始时才走 XML 路径；否则缓存等待更多数据
	lower := strings.ToLower(combined)
	if strings.Contains(lower, "<tool_calls") {
		s.capturing = true
		s.buffer = combined
		return toolStreamResult{}
	}
	// 自然语言路径：尝试立即解析（可能不完整）
	parsed := parseToolCalls(combined, s.available)
	if len(parsed.Calls) > 0 {
		s.done = true
		return toolStreamResult{Calls: parsed.Calls, Complete: true, Raw: combined}
	}
	// 未匹配：缓存最后 64 字节作为前缀，其余立即放行（防止过大内存占用）
	const keep = 64
	if len(combined) > keep {
		s.buffer = combined[len(combined)-keep:]
		return toolStreamResult{SafeText: combined[:len(combined)-keep]}
	}
	s.buffer = combined
	return toolStreamResult{}
}

func splitToolPrefix(value string) (string, string) {
	prefix := "<tool_calls"
	lower := strings.ToLower(value)
	for size := min(len(prefix)-1, len(lower)); size > 0; size-- {
		if strings.HasSuffix(lower, prefix[:size]) {
			return value[:len(value)-size], value[len(value)-size:]
		}
	}
	return value, ""
}
