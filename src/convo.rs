// convo: parse a single JSONL line from a Claude Code conversation log and
// extract renderable text segments.

use serde::Deserialize;
use serde_json::Value;

/// One renderable prose unit from a conversation entry.
#[derive(Debug, PartialEq)]
pub struct Segment {
    pub role: String,
    pub text: String,
}

/// A parsed conversation entry: its RFC3339 timestamp (when present) and the
/// renderable text segments it carries.
#[derive(Debug, PartialEq)]
pub struct ParsedEntry {
    pub timestamp: Option<String>,
    pub segments: Vec<Segment>,
}

#[derive(Deserialize)]
struct Entry {
    #[serde(rename = "type")]
    kind: String,
    timestamp: Option<String>,
    message: Option<Message>,
}

#[derive(Deserialize)]
struct Message {
    role: Option<String>,
    content: Option<Value>,
}

/// Parse one JSONL line and return its text segments.
/// Returns an empty Vec for non-user/assistant entries, entries with no text
/// content, and any line that fails to parse.
pub fn extract(line: &[u8]) -> Vec<Segment> {
    match parse(line) {
        Some(e) => e.segments,
        None => vec![],
    }
}

/// Parse one JSONL line into a `ParsedEntry` (timestamp + text segments).
/// Returns None for non-user/assistant entries, entries with no text content,
/// and any line that fails to parse.
pub fn parse(line: &[u8]) -> Option<ParsedEntry> {
    let entry: Entry = serde_json::from_slice(line).ok()?;

    if entry.kind != "user" && entry.kind != "assistant" {
        return None;
    }

    let timestamp = entry.timestamp;
    let msg = entry.message?;
    let role = msg.role.unwrap_or(entry.kind);
    let content = msg.content?;

    // Content is either a JSON string or a JSON array of blocks.
    let segments = match &content {
        Value::String(s) => {
            if s.is_empty() {
                return None;
            }
            vec![Segment { role, text: s.clone() }]
        }
        Value::Array(blocks) => {
            let mut segs = Vec::new();
            for b in blocks {
                if b.get("type").and_then(Value::as_str) == Some("text") {
                    if let Some(text) = b.get("text").and_then(Value::as_str) {
                        if !text.is_empty() {
                            segs.push(Segment { role: role.clone(), text: text.to_string() });
                        }
                    }
                }
            }
            if segs.is_empty() {
                return None;
            }
            segs
        }
        _ => return None,
    };

    Some(ParsedEntry { timestamp, segments })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn assistant_array_with_text_and_non_text_blocks() {
        let line = br#"{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"internal"},{"type":"text","text":"hello world"},{"type":"tool_use","id":"x","name":"fn","input":{}}]}}"#;
        assert_eq!(extract(line), vec![Segment { role: "assistant".into(), text: "hello world".into() }]);
    }

    #[test]
    fn user_string_content() {
        let line = br#"{"type":"user","message":{"role":"user","content":"what is 2+2?"}}"#;
        assert_eq!(extract(line), vec![Segment { role: "user".into(), text: "what is 2+2?".into() }]);
    }

    #[test]
    fn user_array_content() {
        let line = br#"{"type":"user","message":{"role":"user","content":[{"type":"text","text":"explain this"}]}}"#;
        assert_eq!(extract(line), vec![Segment { role: "user".into(), text: "explain this".into() }]);
    }

    #[test]
    fn multiple_text_blocks() {
        let line = br#"{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"first"},{"type":"text","text":"second"}]}}"#;
        assert_eq!(
            extract(line),
            vec![
                Segment { role: "assistant".into(), text: "first".into() },
                Segment { role: "assistant".into(), text: "second".into() },
            ]
        );
    }

    #[test]
    fn non_user_assistant_type_returns_empty() {
        let line = br#"{"type":"attachment","message":{"role":"user","content":"ignored"}}"#;
        assert!(extract(line).is_empty());
    }

    #[test]
    fn file_history_snapshot_returns_empty() {
        let line = br#"{"type":"file-history-snapshot","cwd":"/tmp","uuid":"abc"}"#;
        assert!(extract(line).is_empty());
    }

    #[test]
    fn malformed_json_returns_empty() {
        let line = b"{not valid json";
        assert!(extract(line).is_empty());
    }

    #[test]
    fn role_absent_falls_back_to_top_level_type() {
        let line = br#"{"type":"user","message":{"content":"no role field"}}"#;
        assert_eq!(extract(line), vec![Segment { role: "user".into(), text: "no role field".into() }]);
    }

    #[test]
    fn array_with_only_non_text_blocks_returns_empty() {
        let line = br#"{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"x","name":"fn","input":{}},{"type":"tool_result","tool_use_id":"x","content":"res"}]}}"#;
        assert!(extract(line).is_empty());
    }

    #[test]
    fn empty_array_returns_empty() {
        let line = br#"{"type":"assistant","message":{"role":"assistant","content":[]}}"#;
        assert!(extract(line).is_empty());
    }

    #[test]
    fn parse_exposes_timestamp_and_segments() {
        let line = br#"{"type":"user","timestamp":"2026-05-27T07:33:02.123Z","message":{"role":"user","content":"hi"}}"#;
        let entry = parse(line).expect("entry");
        assert_eq!(entry.timestamp.as_deref(), Some("2026-05-27T07:33:02.123Z"));
        assert_eq!(entry.segments, vec![Segment { role: "user".into(), text: "hi".into() }]);
    }

    #[test]
    fn parse_missing_timestamp_is_none() {
        let line = br#"{"type":"user","message":{"role":"user","content":"hi"}}"#;
        let entry = parse(line).expect("entry");
        assert!(entry.timestamp.is_none());
    }
}
