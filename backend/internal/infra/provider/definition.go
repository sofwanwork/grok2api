package provider

import (
	"fmt"
	"strings"

	"github.com/chenyme/grok2api/backend/internal/domain/account"
	modeldomain "github.com/chenyme/grok2api/backend/internal/domain/model"
)

// ModelCatalogKind 表示模型目录来自真实上游发现还是项目内置目录。
type ModelCatalogKind string

const (
	ModelCatalogRemote ModelCatalogKind = "remote"
	ModelCatalogStatic ModelCatalogKind = "static"
)

// QuotaKind 表示号池可用额度的权威来源。
type QuotaKind string

const (
	QuotaBilling      QuotaKind = "billing"
	QuotaRemoteWindow QuotaKind = "remote_window"
	QuotaLocalWindow  QuotaKind = "local_window"
)

// ConversationSurface 描述统一对话 API 在当前 Provider 上的真实能力边界。
type ConversationSurface struct {
	Responses       bool
	ChatCompletions bool
	Messages        bool
	Compact         bool
	StoredResponses bool
}

// Supports 判断统一对话协议入口是否由当前 Provider 明确声明。
func (s ConversationSurface) Supports(operation string) bool {
	switch operation {
	case "responses":
		return s.Responses
	case "compaction":
		return s.Compact
	case "chat":
		return s.ChatCompletions
	case "messages":
		return s.Messages
	default:
		return false
	}
}

// MediaSurface 描述图像与视频 API 在当前 Provider 上的真实能力边界。
type MediaSurface struct {
	ImageGeneration bool
	ImageEdit       bool
	VideoGeneration bool
	TTS             bool
	STT             bool
	Realtime        bool
}

// CredentialSurface 描述号池凭据的接入和维护方式。
type CredentialSurface struct {
	AuthType    account.AuthType
	Import      bool
	Refresh     bool
	DeviceOAuth bool
}

// UsageKind 表示文本推理 usage 的权威性；媒体请求仍由独立计费路径记录。
type UsageKind string

const (
	UsageUpstream  UsageKind = "upstream"
	UsageEstimated UsageKind = "estimated"
)

// InferencePolicy 描述与上游协议相关、但必须由统一网关执行的运行策略。
type InferencePolicy struct {
	Usage                  UsageKind
	RetryForbiddenAsEgress bool
}

// Definition 是 Provider 注册表的权威静态描述；渠道专属协议仍由各 Adapter 实现。
type Definition struct {
	Provider          account.Provider
	ModelNamespace    string
	ModelCatalog      ModelCatalogKind
	ModelCapabilities []modeldomain.Capability
	Quota             QuotaKind
	Credential        CredentialSurface
	Conversation      ConversationSurface
	Media             MediaSurface
	Inference         InferencePolicy
}

// DefinitionAdapter 由生产 Provider 实现，用于注册明确且可校验的能力边界。
type DefinitionAdapter interface {
	Adapter
	Definition() Definition
}

// Clone 返回可安全交给调用方读取的副本，避免切片字段被外部修改后污染注册表。
func (d Definition) Clone() Definition {
	d.ModelCapabilities = append([]modeldomain.Capability(nil), d.ModelCapabilities...)
	return d
}

// SupportsModelCapability 判断该渠道能否注册指定类型的模型路由。
func (d Definition) SupportsModelCapability(capability modeldomain.Capability) bool {
	for _, supported := range d.ModelCapabilities {
		if supported == capability {
			return true
		}
	}
	return false
}

// Validate 检查静态描述内部是否自洽，避免接口实现和后台路由配置悄悄漂移。
func (d Definition) Validate() error {
	if !d.Provider.IsValid() {
		return fmt.Errorf("Provider %q tidak sah", d.Provider)
	}
	if strings.TrimSpace(d.ModelNamespace) == "" || d.ModelNamespace != d.Provider.ModelNamespace() {
		return fmt.Errorf("Ruang nama model Provider %s tidak sah", d.Provider)
	}
	if d.ModelCatalog != ModelCatalogRemote && d.ModelCatalog != ModelCatalogStatic {
		return fmt.Errorf("Jenis katalog model Provider %s tidak sah", d.Provider)
	}
	if len(d.ModelCapabilities) == 0 {
		return fmt.Errorf("Provider %s tidak mengisytiharkan keupayaan model", d.Provider)
	}
	capabilities := make(map[modeldomain.Capability]struct{}, len(d.ModelCapabilities))
	for _, capability := range d.ModelCapabilities {
		switch capability {
		case modeldomain.CapabilityResponses, modeldomain.CapabilityChat, modeldomain.CapabilityImage, modeldomain.CapabilityImageEdit, modeldomain.CapabilityVideo, modeldomain.CapabilityTTS, modeldomain.CapabilitySTT, modeldomain.CapabilityRealtime:
		default:
			return fmt.Errorf("Provider %s mengisytiharkan keupayaan model tidak sah %q", d.Provider, capability)
		}
		if _, exists := capabilities[capability]; exists {
			return fmt.Errorf("Provider %s mengisytiharkan keupayaan model %q berulang", d.Provider, capability)
		}
		capabilities[capability] = struct{}{}
	}
	if d.Quota != QuotaBilling && d.Quota != QuotaRemoteWindow && d.Quota != QuotaLocalWindow {
		return fmt.Errorf("Strategi kuota Provider %s tidak sah", d.Provider)
	}
	if d.Credential.AuthType != account.AuthTypeOAuth && d.Credential.AuthType != account.AuthTypeSSO {
		return fmt.Errorf("Jenis pengesahan Provider %s tidak sah", d.Provider)
	}
	if d.Inference.Usage != UsageUpstream && d.Inference.Usage != UsageEstimated {
		return fmt.Errorf("Strategi usage Provider %s tidak sah", d.Provider)
	}
	if (d.Conversation.Compact || d.Conversation.StoredResponses) && !d.Conversation.Responses {
		return fmt.Errorf("Keupayaan lanjutan Responses Provider %s tiada keupayaan asas Responses", d.Provider)
	}
	if d.Credential.AuthType != account.AuthTypeOAuth && (d.Credential.Refresh || d.Credential.DeviceOAuth) {
		return fmt.Errorf("Kredensial bukan OAuth Provider %s tidak boleh mengisytiharkan pembaruan atau Device OAuth", d.Provider)
	}
	mediaCapabilities := []struct {
		enabled    bool
		capability modeldomain.Capability
		name       string
	}{
		{enabled: d.Media.ImageGeneration, capability: modeldomain.CapabilityImage, name: "penjanaan imej"},
		{enabled: d.Media.ImageEdit, capability: modeldomain.CapabilityImageEdit, name: "penyuntingan imej"},
		{enabled: d.Media.VideoGeneration, capability: modeldomain.CapabilityVideo, name: "penjanaan video"},
		{enabled: d.Media.TTS, capability: modeldomain.CapabilityTTS, name: "sintesis suara"},
		{enabled: d.Media.STT, capability: modeldomain.CapabilitySTT, name: "pengecaman suara"},
		{enabled: d.Media.Realtime, capability: modeldomain.CapabilityRealtime, name: "suara masa nyata"},
	}
	for _, item := range mediaCapabilities {
		_, declared := capabilities[item.capability]
		if item.enabled != declared {
			return fmt.Errorf("Antara muka %s Provider %s tidak sepadan dengan perisytiharan keupayaan model", d.Provider, item.name)
		}
	}
	return nil
}
