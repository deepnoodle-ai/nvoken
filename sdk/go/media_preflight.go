package nvoken

import (
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

// Media input limits. They mirror the Runtime bounds so a mistake surfaces
// before a request is sent. Format sniffing, pixel bounds, and per-model
// modality support stay Runtime-side.
const (
	MaxMediaInputBlocks     = 8
	MaxImageInputBytes      = 5 << 20
	MaxDocumentInputBytes   = 16 << 20
	MaxMediaInputBytes      = 16 << 20
	MaxMediaTitleCharacters = 255
	MediaPreflightCode      = "media_preflight_failed"

	mediaInvalidCode         = "invalid_media"
	mediaUnsupportedTypeCode = "unsupported_media_type"
	mediaLimitCode           = "limit_exceeded"

	InputBlockTypeText     = "text"
	InputBlockTypeImage    = "image"
	InputBlockTypeDocument = "document"
)

// ImageMediaTypes and DocumentMediaTypes are the media types admission accepts.
func ImageMediaTypes() []string {
	return []string{"image/gif", "image/jpeg", "image/png", "image/webp"}
}
func DocumentMediaTypes() []string { return []string{"application/pdf"} }

type MediaIssue struct {
	Code    string
	Path    string
	Message string
}

// InputBlockSource carries inline media bytes or one public HTTPS URL.
type InputBlockSource struct {
	MediaType string
	Data      string
	URL       string
}

// InputBlock is one ordered caller-input block.
type InputBlock struct {
	Type   string
	Text   string
	Source *InputBlockSource
	Title  string
}

// TextInputBlock builds the ordinary text input block.
func TextInputBlock(text string) InputBlock {
	return InputBlock{Type: InputBlockTypeText, Text: text}
}

// ImageInputBlock inlines image bytes that are already base64 encoded.
func ImageInputBlock(mediaType, data string) InputBlock {
	return InputBlock{
		Type:   InputBlockTypeImage,
		Source: &InputBlockSource{MediaType: mediaType, Data: data},
	}
}

// DocumentInputBlock inlines document bytes that are already base64 encoded.
// An empty title uses the provider adapter default.
func DocumentInputBlock(mediaType, data, title string) InputBlock {
	return InputBlock{
		Type:   InputBlockTypeDocument,
		Source: &InputBlockSource{MediaType: mediaType, Data: data},
		Title:  title,
	}
}

// ImageURLInputBlock builds an image fetched once by nvoken during admission.
func ImageURLInputBlock(sourceURL string) InputBlock {
	return InputBlock{
		Type:   InputBlockTypeImage,
		Source: &InputBlockSource{URL: sourceURL},
	}
}

// DocumentURLInputBlock builds a PDF fetched once by nvoken during admission.
func DocumentURLInputBlock(sourceURL, title string) InputBlock {
	return InputBlock{
		Type:   InputBlockTypeDocument,
		Source: &InputBlockSource{URL: sourceURL},
		Title:  title,
	}
}

// PreflightInputBlocks rejects locally checkable input problems using the same
// issue vocabulary the Runtime uses.
func PreflightInputBlocks(blocks []InputBlock) error {
	if len(blocks) == 0 {
		return mediaInputError(MediaIssue{
			Code:    mediaInvalidCode,
			Path:    "input.content",
			Message: "input must contain at least one block",
		})
	}
	mediaBlocks := 0
	mediaBytes := 0
	for index, block := range blocks {
		path := fmt.Sprintf("input.content[%d]", index)
		switch block.Type {
		case InputBlockTypeText:
			if strings.TrimSpace(block.Text) == "" {
				return mediaInputError(MediaIssue{
					Code:    mediaInvalidCode,
					Path:    path + ".text",
					Message: "text must not be blank",
				})
			}
			if block.Source != nil || block.Title != "" {
				return mediaInputError(MediaIssue{
					Code:    mediaInvalidCode,
					Path:    path,
					Message: "text blocks accept only text",
				})
			}
		case InputBlockTypeImage, InputBlockTypeDocument:
			if block.Text != "" {
				return mediaInputError(MediaIssue{
					Code:    mediaInvalidCode,
					Path:    path,
					Message: "media blocks must not carry text",
				})
			}
			if block.Type == InputBlockTypeImage && block.Title != "" {
				return mediaInputError(MediaIssue{
					Code:    mediaInvalidCode,
					Path:    path + ".title",
					Message: "title is allowed only for document blocks",
				})
			}
			if block.Title != "" &&
				(strings.TrimSpace(block.Title) == "" ||
					utf8.RuneCountInString(block.Title) > MaxMediaTitleCharacters) {
				return mediaInputError(MediaIssue{
					Code: mediaInvalidCode,
					Path: path + ".title",
					Message: fmt.Sprintf(
						"title must be 1 to %d characters",
						MaxMediaTitleCharacters,
					),
				})
			}
			if block.Source == nil {
				return mediaInputError(MediaIssue{
					Code:    mediaInvalidCode,
					Path:    path + ".source",
					Message: "source is required",
				})
			}
			allowed := ImageMediaTypes()
			limit := MaxImageInputBytes
			if block.Type == InputBlockTypeDocument {
				allowed = DocumentMediaTypes()
				limit = MaxDocumentInputBytes
			}
			if block.Source.URL != "" {
				if block.Source.Data != "" {
					return mediaInputError(MediaIssue{
						Code:    mediaInvalidCode,
						Path:    path + ".source",
						Message: "source requires exactly one of data or url",
					})
				}
				if block.Source.MediaType != "" && !containsMediaType(allowed, block.Source.MediaType) {
					return mediaInputError(MediaIssue{
						Code: mediaUnsupportedTypeCode,
						Path: path + ".source.media_type",
						Message: "media_type must be one of " +
							strings.Join(allowed, ", "),
					})
				}
				parsed, err := url.Parse(block.Source.URL)
				if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
					return mediaInputError(MediaIssue{
						Code:    mediaInvalidCode,
						Path:    path + ".source.url",
						Message: "url must be an HTTPS URL",
					})
				}
				mediaBlocks++
				if mediaBlocks > MaxMediaInputBlocks {
					return mediaInputError(MediaIssue{
						Code:    mediaLimitCode,
						Path:    path,
						Message: fmt.Sprintf("input carries at most %d media blocks", MaxMediaInputBlocks),
					})
				}
				continue
			}
			if !containsMediaType(allowed, block.Source.MediaType) {
				return mediaInputError(MediaIssue{
					Code: mediaUnsupportedTypeCode,
					Path: path + ".source.media_type",
					Message: "media_type must be one of " +
						strings.Join(allowed, ", "),
				})
			}
			decoded, ok := decodedBase64Length(block.Source.Data)
			if !ok || decoded == 0 {
				return mediaInputError(MediaIssue{
					Code:    mediaInvalidCode,
					Path:    path + ".source.data",
					Message: "data must be standard padded base64 without whitespace",
				})
			}
			if decoded > limit {
				return mediaInputError(MediaIssue{
					Code:    mediaLimitCode,
					Path:    path + ".source.data",
					Message: fmt.Sprintf("data must decode to at most %d bytes", limit),
				})
			}
			mediaBlocks++
			mediaBytes += decoded
			if mediaBlocks > MaxMediaInputBlocks {
				return mediaInputError(MediaIssue{
					Code: mediaLimitCode,
					Path: path,
					Message: fmt.Sprintf(
						"input carries at most %d media blocks",
						MaxMediaInputBlocks,
					),
				})
			}
			if mediaBytes > MaxMediaInputBytes {
				return mediaInputError(MediaIssue{
					Code: mediaLimitCode,
					Path: path,
					Message: fmt.Sprintf(
						"input media must decode to at most %d bytes",
						MaxMediaInputBytes,
					),
				})
			}
		default:
			return mediaInputError(MediaIssue{
				Code:    mediaInvalidCode,
				Path:    path + ".type",
				Message: "type must be text, image, or document",
			})
		}
	}
	return nil
}

// wire renders one block in the exact canonical member shape.
func (b InputBlock) wire() map[string]any {
	if b.Type == InputBlockTypeText {
		return map[string]any{"type": InputBlockTypeText, "text": b.Text}
	}
	source := map[string]any{}
	if b.Source.URL != "" {
		source["url"] = b.Source.URL
		if b.Source.MediaType != "" {
			source["media_type"] = b.Source.MediaType
		}
	} else {
		source["media_type"] = b.Source.MediaType
		source["data"] = b.Source.Data
	}
	block := map[string]any{
		"type":   b.Type,
		"source": source,
	}
	if b.Title != "" {
		block["title"] = b.Title
	}
	return block
}

func inputBlocksWire(blocks []InputBlock) []any {
	content := make([]any, len(blocks))
	for index, block := range blocks {
		content[index] = block.wire()
	}
	return content
}

// decodedBase64Length reports the decoded size of standard padded base64
// without allocating the payload, and rejects any other encoding.
func decodedBase64Length(data string) (int, bool) {
	if data == "" || len(data)%4 != 0 {
		return 0, false
	}
	padding := 0
	for index := 0; index < len(data); index++ {
		character := data[index]
		switch {
		case character == '=':
			if index < len(data)-2 {
				return 0, false
			}
			padding++
		case character >= 'A' && character <= 'Z',
			character >= 'a' && character <= 'z',
			character >= '0' && character <= '9',
			character == '+', character == '/':
			if padding != 0 {
				return 0, false
			}
		default:
			return 0, false
		}
	}
	return len(data)/4*3 - padding, true
}

func containsMediaType(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func mediaInputError(issue MediaIssue) error {
	return &Error{
		Category: ErrorValidation,
		Code:     MediaPreflightCode,
		Message:  "input is invalid: " + issue.Message,
		Details: map[string]any{
			"kind": "input_media",
			"code": issue.Code,
			"path": issue.Path,
		},
	}
}
