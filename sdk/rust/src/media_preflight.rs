use serde_json::json;

use crate::client::{ErrorCategory, NvokenError};
use crate::models;

/// Media input limits mirroring the Runtime bounds. Format sniffing, pixel
/// bounds, and per-model modality support stay Runtime-side.
pub const MAX_MEDIA_INPUT_BLOCKS: usize = 8;
pub const MAX_IMAGE_INPUT_BYTES: usize = 5 * 1024 * 1024;
pub const MAX_DOCUMENT_INPUT_BYTES: usize = 16 * 1024 * 1024;
pub const MAX_MEDIA_INPUT_BYTES: usize = 16 * 1024 * 1024;
pub const MAX_MEDIA_TITLE_CHARACTERS: usize = 255;
pub const MEDIA_PREFLIGHT_CODE: &str = "media_preflight_failed";

pub const IMAGE_MEDIA_TYPES: [&str; 4] = ["image/gif", "image/jpeg", "image/png", "image/webp"];
pub const DOCUMENT_MEDIA_TYPES: [&str; 1] = ["application/pdf"];

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct MediaIssue {
    pub code: String,
    pub path: String,
    pub message: String,
}

/// Builds one inline image block from bytes that are already base64 encoded.
pub fn image_block(
    media_type: models::image_input_source::MediaType,
    data: impl Into<String>,
) -> models::InputBlock {
    let mut source = models::ImageInputSource::new();
    source.media_type = Some(media_type);
    source.data = Some(data.into());
    models::InputBlock::ImageInputBlock(Box::new(models::ImageInputBlock::new(
        models::image_input_block::Type::InputTypeImage,
        source,
    )))
}

/// Builds one image URL block. The Runtime fetches it once at admission.
pub fn image_url_block(
    url: impl Into<String>,
    media_type: Option<models::image_input_source::MediaType>,
) -> models::InputBlock {
    let mut source = models::ImageInputSource::new();
    source.media_type = media_type;
    source.url = Some(url.into());
    models::InputBlock::ImageInputBlock(Box::new(models::ImageInputBlock::new(
        models::image_input_block::Type::InputTypeImage,
        source,
    )))
}

/// Builds one inline document block from bytes that are already base64 encoded.
/// An empty title uses the provider adapter default.
pub fn document_block(
    media_type: models::document_input_source::MediaType,
    data: impl Into<String>,
    title: Option<String>,
) -> models::InputBlock {
    let mut source = models::DocumentInputSource::new();
    source.media_type = Some(media_type);
    source.data = Some(data.into());
    let mut block = models::DocumentInputBlock::new(
        models::document_input_block::Type::InputTypeDocument,
        source,
    );
    block.title = title;
    models::InputBlock::DocumentInputBlock(Box::new(block))
}

/// Builds one document URL block. The Runtime fetches it once at admission.
pub fn document_url_block(
    url: impl Into<String>,
    title: Option<String>,
    media_type: Option<models::document_input_source::MediaType>,
) -> models::InputBlock {
    let mut source = models::DocumentInputSource::new();
    source.media_type = media_type;
    source.url = Some(url.into());
    let mut block = models::DocumentInputBlock::new(
        models::document_input_block::Type::InputTypeDocument,
        source,
    );
    block.title = title;
    models::InputBlock::DocumentInputBlock(Box::new(block))
}

/// Builds one text block.
pub fn text_block(text: impl Into<String>) -> models::InputBlock {
    models::InputBlock::TextInputBlock(Box::new(models::TextInputBlock::new(
        models::text_input_block::Type::InputTypeText,
        text.into(),
    )))
}

/// Rejects locally checkable input problems using the Runtime's vocabulary.
pub fn preflight_input_blocks(blocks: &[models::InputBlock]) -> Result<(), NvokenError> {
    if blocks.is_empty() {
        return Err(media_error(MediaIssue {
            code: "invalid_media".to_owned(),
            path: "input.content".to_owned(),
            message: "input must contain at least one block".to_owned(),
        }));
    }
    let mut media_blocks = 0usize;
    let mut media_bytes = 0usize;
    for (index, block) in blocks.iter().enumerate() {
        let path = format!("input.content[{index}]");
        let (media_type, data, url, title, limit, allowed): (
            Option<String>,
            Option<&str>,
            Option<&str>,
            Option<&String>,
            usize,
            &[&str],
        ) = match block {
            models::InputBlock::TextInputBlock(text) => {
                if text.text.trim().is_empty() {
                    return Err(media_error(MediaIssue {
                        code: "invalid_media".to_owned(),
                        path: format!("{path}.text"),
                        message: "text must not be blank".to_owned(),
                    }));
                }
                continue;
            }
            models::InputBlock::ImageInputBlock(image) => (
                image.source.media_type.as_ref().map(media_type_string),
                image.source.data.as_deref(),
                image.source.url.as_deref(),
                None,
                MAX_IMAGE_INPUT_BYTES,
                &IMAGE_MEDIA_TYPES[..],
            ),
            models::InputBlock::DocumentInputBlock(document) => (
                document.source.media_type.as_ref().map(media_type_string),
                document.source.data.as_deref(),
                document.source.url.as_deref(),
                document.title.as_ref(),
                MAX_DOCUMENT_INPUT_BYTES,
                &DOCUMENT_MEDIA_TYPES[..],
            ),
        };
        if let Some(title) = title {
            if title.trim().is_empty() || title.chars().count() > MAX_MEDIA_TITLE_CHARACTERS {
                return Err(media_error(MediaIssue {
                    code: "invalid_media".to_owned(),
                    path: format!("{path}.title"),
                    message: format!("title must be 1 to {MAX_MEDIA_TITLE_CHARACTERS} characters"),
                }));
            }
        }
        if data.is_some() == url.is_some() {
            return Err(media_error(MediaIssue {
                code: "invalid_media".to_owned(),
                path: format!("{path}.source"),
                message: "source requires exactly one of data or url".to_owned(),
            }));
        }
        if let Some(url) = url {
            let valid_url = reqwest::Url::parse(url)
                .map(|value| value.scheme() == "https" && value.host_str().is_some())
                .unwrap_or(false);
            if !valid_url {
                return Err(media_error(MediaIssue {
                    code: "invalid_media".to_owned(),
                    path: format!("{path}.source.url"),
                    message: "url must be an HTTPS URL".to_owned(),
                }));
            }
            if media_type
                .as_deref()
                .is_some_and(|value| !allowed.contains(&value))
            {
                return Err(media_error(MediaIssue {
                    code: "unsupported_media_type".to_owned(),
                    path: format!("{path}.source.media_type"),
                    message: format!("media_type must be one of {}", allowed.join(", ")),
                }));
            }
            media_blocks += 1;
            if media_blocks > MAX_MEDIA_INPUT_BLOCKS {
                return Err(media_error(MediaIssue {
                    code: "limit_exceeded".to_owned(),
                    path,
                    message: format!("input carries at most {MAX_MEDIA_INPUT_BLOCKS} media blocks"),
                }));
            }
            continue;
        }
        let media_type = media_type.unwrap_or_default();
        if !allowed.contains(&media_type.as_str()) {
            return Err(media_error(MediaIssue {
                code: "unsupported_media_type".to_owned(),
                path: format!("{path}.source.media_type"),
                message: format!("media_type must be one of {}", allowed.join(", ")),
            }));
        }
        let decoded = match decoded_base64_length(data.unwrap_or_default()) {
            Some(decoded) if decoded > 0 => decoded,
            _ => {
                return Err(media_error(MediaIssue {
                    code: "invalid_media".to_owned(),
                    path: format!("{path}.source.data"),
                    message: "data must be standard padded base64 without whitespace".to_owned(),
                }))
            }
        };
        if decoded > limit {
            return Err(media_error(MediaIssue {
                code: "limit_exceeded".to_owned(),
                path: format!("{path}.source.data"),
                message: format!("data must decode to at most {limit} bytes"),
            }));
        }
        media_blocks += 1;
        media_bytes += decoded;
        if media_blocks > MAX_MEDIA_INPUT_BLOCKS {
            return Err(media_error(MediaIssue {
                code: "limit_exceeded".to_owned(),
                path,
                message: format!("input carries at most {MAX_MEDIA_INPUT_BLOCKS} media blocks"),
            }));
        }
        if media_bytes > MAX_MEDIA_INPUT_BYTES {
            return Err(media_error(MediaIssue {
                code: "limit_exceeded".to_owned(),
                path,
                message: format!(
                    "input media must decode to at most {MAX_MEDIA_INPUT_BYTES} bytes"
                ),
            }));
        }
    }
    Ok(())
}

/// Reports the decoded size of standard padded base64 without allocating the
/// payload, and rejects any other encoding.
fn decoded_base64_length(data: &str) -> Option<usize> {
    if data.is_empty() || data.len() % 4 != 0 {
        return None;
    }
    let bytes = data.as_bytes();
    let mut padding = 0usize;
    for (index, character) in bytes.iter().enumerate() {
        match character {
            b'=' => {
                if index + 2 < bytes.len() {
                    return None;
                }
                padding += 1;
            }
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'+' | b'/' => {
                if padding != 0 {
                    return None;
                }
            }
            _ => return None,
        }
    }
    Some(data.len() / 4 * 3 - padding)
}

/// media_type_string renders a generated media-type enum as its wire value.
fn media_type_string<T: serde::Serialize>(media_type: &T) -> String {
    serde_json::to_value(media_type)
        .ok()
        .and_then(|value| value.as_str().map(str::to_owned))
        .unwrap_or_default()
}

fn media_error(issue: MediaIssue) -> NvokenError {
    NvokenError {
        category: ErrorCategory::Validation,
        message: format!("input is invalid: {}", issue.message),
        status: None,
        code: Some(MEDIA_PREFLIGHT_CODE.to_owned()),
        request_id: None,
        retry_after: None,
        details: Some(json!({
            "kind": "input_media",
            "code": issue.code,
            "path": issue.path,
        })),
    }
}
