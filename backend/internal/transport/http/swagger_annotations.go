package httpserver

// SwaggerMessage represents one message in a Chat Completions request.
type SwaggerMessage struct {
	Role    string `json:"role" example:"user"`
	Content any    `json:"content"`
}

// SwaggerResponsesRequest represents a minimal Responses request.
type SwaggerResponsesRequest struct {
	Model              string `json:"model" example:"grok-chat-auto"`
	Input              any    `json:"input"`
	Stream             bool   `json:"stream" example:"false"`
	Store              bool   `json:"store" example:"false"`
	PreviousResponseID string `json:"previous_response_id,omitempty"`
	PromptCacheKey     string `json:"prompt_cache_key,omitempty"`
}

// SwaggerChatRequest represents a minimal Chat Completions request.
type SwaggerChatRequest struct {
	Model    string           `json:"model" example:"grok-chat-fast"`
	Messages []SwaggerMessage `json:"messages"`
	Stream   bool             `json:"stream" example:"false"`
}

// SwaggerMessagesRequest represents a minimal Anthropic Messages request.
type SwaggerMessagesRequest struct {
	Model     string           `json:"model" example:"grok-chat-expert"`
	MaxTokens int              `json:"max_tokens" example:"1024"`
	Messages  []SwaggerMessage `json:"messages"`
	Stream    bool             `json:"stream" example:"false"`
}

// SwaggerImageGenerationRequest represents an image generation request.
type SwaggerImageGenerationRequest struct {
	Model          string `json:"model" example:"grok-imagine-image-quality"`
	Prompt         string `json:"prompt" example:"A cinematic city at night"`
	N              int    `json:"n" example:"1"`
	AspectRatio    string `json:"aspect_ratio,omitempty" example:"16:9"`
	Resolution     string `json:"resolution,omitempty" example:"2k"`
	ResponseFormat string `json:"response_format,omitempty" example:"url"`
	Stream         bool   `json:"stream,omitempty" example:"false"`
	PartialImages  int    `json:"partial_images,omitempty" example:"0"`
}

// SwaggerImageReference represents an image URL input.
type SwaggerImageReference struct {
	URL string `json:"url" example:"https://example.com/source.png"`
}

// SwaggerImageEditRequest represents an image edit request.
type SwaggerImageEditRequest struct {
	Model          string                `json:"model" example:"grok-imagine-image-edit"`
	Prompt         string                `json:"prompt" example:"Change the background to black"`
	Image          SwaggerImageReference `json:"image"`
	N              int                   `json:"n" example:"1"`
	Size           string                `json:"size,omitempty" example:"1024x1024"`
	AspectRatio    string                `json:"aspect_ratio,omitempty" example:"1:1"`
	Resolution     string                `json:"resolution,omitempty" example:"1k"`
	ResponseFormat string                `json:"response_format,omitempty" example:"url"`
	Stream         bool                  `json:"stream,omitempty" example:"false"`
	PartialImages  int                   `json:"partial_images,omitempty" example:"0"`
}

// SwaggerVideoGenerationRequest represents a video generation request.
// image is mutually exclusive with reference_images/reference_audios; in
// reference-image mode the maximum resolution is 720p.
type SwaggerVideoGenerationRequest struct {
	Model            string                    `json:"model" example:"grok-imagine-video"`
	Prompt           string                    `json:"prompt" example:"A cinematic tracking shot in the rain"`
	Duration         int                       `json:"duration" example:"8"`
	AspectRatio      string                    `json:"aspect_ratio,omitempty" example:"16:9"`
	Resolution       string                    `json:"resolution,omitempty" example:"720p"`
	Image            *SwaggerVideoMediaInput   `json:"image,omitempty"`
	ReferenceImages  []SwaggerVideoMediaInput  `json:"reference_images,omitempty"`
	ReferenceAudios  []SwaggerVideoAudioInput  `json:"reference_audios,omitempty"`
	Video            *SwaggerVideoMediaInput   `json:"video,omitempty"`
}

// SwaggerVideoMediaInput represents an image/video input for video operations.
type SwaggerVideoMediaInput struct {
	URL    string `json:"url,omitempty" example:"https://example.com/input.png"`
	FileID string `json:"file_id,omitempty" example:"file_123"`
}

// SwaggerVideoAudioInput represents a reference audio (built-in voice_id).
type SwaggerVideoAudioInput struct {
	VoiceID string `json:"voice_id" example:"eve"`
}

// swaggerHealth godoc
// @Summary Liveness check
// @Tags System
// @Produce json
// @Success 200 {object} map[string]bool
// @Router /healthz [get]
func swaggerHealth() {}

// swaggerReady godoc
// @Summary Readiness check
// @Tags System
// @Produce json
// @Success 200 {object} map[string]bool
// @Failure 503 {object} map[string]bool
// @Router /readyz [get]
func swaggerReady() {}

// swaggerModels godoc
// @Summary List available models
// @Description Returns the OpenAI-compatible model list with metadata such as context_window, max_output_tokens, and capabilities. Supports ETag caching (If-None-Match → 304).
// @Tags Models
// @Security BearerAuth
// @Produce json
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]any
// @Router /v1/models [get]
func swaggerModels() {}

// swaggerMetrics godoc
// @Summary Prometheus metrics
// @Description Returns metrics in Prometheus text exposition format.
// @Tags System
// @Produce text/plain
// @Success 200 {string} string
// @Router /metrics [get]
func swaggerMetrics() {}

// swaggerResponses godoc
// @Summary Create Response
// @Description Supports JSON and SSE; returns text/event-stream when stream=true.
// @Tags Responses
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body SwaggerResponsesRequest true "Request"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]any
// @Failure 401 {object} map[string]any
// @Router /v1/responses [post]
func swaggerResponses() {}

// swaggerCompactResponse godoc
// @Summary Compact Response context
// @Tags Responses
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body SwaggerResponsesRequest true "Request"
// @Success 200 {object} map[string]any
// @Router /v1/responses/compact [post]
func swaggerCompactResponse() {}

// swaggerGetResponse godoc
// @Summary Get Response
// @Tags Responses
// @Security BearerAuth
// @Produce json
// @Param response_id path string true "Response ID"
// @Success 200 {object} map[string]any
// @Failure 404 {object} map[string]any
// @Router /v1/responses/{response_id} [get]
func swaggerGetResponse() {}

// swaggerDeleteResponse godoc
// @Summary Delete Response
// @Tags Responses
// @Security BearerAuth
// @Produce json
// @Param response_id path string true "Response ID"
// @Success 200 {object} map[string]any
// @Failure 404 {object} map[string]any
// @Router /v1/responses/{response_id} [delete]
func swaggerDeleteResponse() {}

// swaggerChat godoc
// @Summary Create Chat Completion
// @Description Supports JSON and SSE, image input, and function tools.
// @Tags Chat
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body SwaggerChatRequest true "Request"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]any
// @Router /v1/chat/completions [post]
func swaggerChat() {}

// swaggerMessages godoc
// @Summary Create Anthropic Message
// @Tags Messages
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param anthropic-version header string true "Anthropic API version" default(2023-06-01)
// @Param request body SwaggerMessagesRequest true "Request"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]any
// @Router /v1/messages [post]
func swaggerMessages() {}

// swaggerGenerateImage godoc
// @Summary Generate image
// @Tags Images
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body SwaggerImageGenerationRequest true "Request"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]any
// @Router /v1/images/generations [post]
func swaggerGenerateImage() {}

// swaggerEditImage godoc
// @Summary Edit image
// @Tags Images
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body SwaggerImageEditRequest true "Request"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]any
// @Router /v1/images/edits [post]
func swaggerEditImage() {}

// swaggerGetImage godoc
// @Summary Get archived image
// @Tags Images
// @Produce image/png
// @Param asset_id path string true "Asset ID"
// @Success 200 {file} binary
// @Failure 404
// @Router /v1/media/images/{asset_id} [get]
func swaggerGetImage() {}

// swaggerGenerateVideo godoc
// @Summary Create async video task
// @Tags Videos
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body SwaggerVideoGenerationRequest true "Request"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]any
// @Router /v1/videos/generations [post]
func swaggerGenerateVideo() {}

// swaggerEditVideo godoc
// @Summary Create async video edit task
// @Tags Videos
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body SwaggerVideoGenerationRequest true "Request"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]any
// @Router /v1/videos/edits [post]
func swaggerEditVideo() {}

// swaggerExtendVideo godoc
// @Summary Create async video extend task
// @Tags Videos
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body SwaggerVideoGenerationRequest true "Request"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]any
// @Router /v1/videos/extensions [post]
func swaggerExtendVideo() {}

// swaggerGetVideo godoc
// @Summary Get async video task
// @Tags Videos
// @Security BearerAuth
// @Produce json
// @Param request_id path string true "Request ID"
// @Success 200 {object} map[string]any
// @Failure 404 {object} map[string]any
// @Router /v1/videos/{request_id} [get]
func swaggerGetVideo() {}
