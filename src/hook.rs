// hook: pure parser for Claude Code hook stdin payloads.
//
// Mirrors `convo`'s old purity: no laterm-module imports, serde/serde_json only,
// never panics. Turns a `UserPromptSubmit` / `Stop` payload into a neutral
// (role, text) plus the originating project `cwd`. `main` maps the role to
// `feed`'s EntryStyle (this module stays free of any laterm import) and supplies
// the watched cwd so a globally-installed hook firing for another project is
// dropped here.

use serde::Deserialize;

/// Neutral role of a parsed hook payload. NOT `feed`'s EntryStyle — `main` maps
/// it (user -> USER_STYLE, assistant -> ASSISTANT_STYLE).
#[derive(Debug, PartialEq, Eq, Clone, Copy)]
pub(crate) enum Role {
    User,
    Assistant,
}

/// A parsed hook payload: the role, the renderable text, and the originating
/// project working directory (exposed for logging; the cwd filter already ran).
#[derive(Debug, PartialEq, Eq)]
pub(crate) struct Parsed {
    pub role: Role,
    pub text: String,
    pub cwd: String,
}

/// Only the fields laterm reads. serde ignores the many others Claude Code sends.
#[derive(Deserialize)]
struct Payload {
    hook_event_name: Option<String>,
    cwd: Option<String>,
    /// `UserPromptSubmit`: the user's message text.
    prompt: Option<String>,
    /// `Stop`: the turn's FINAL assistant text (full, untruncated).
    last_assistant_message: Option<String>,
}

/// Extract just the `cwd` from a raw hook payload, without validating the event
/// or its content. Used by the `--hook` forwarder to derive the rendezvous
/// socket path (the payload cwd is the authoritative project dir). Returns None
/// on parse failure or an absent/empty cwd.
pub(crate) fn payload_cwd(bytes: &[u8]) -> Option<String> {
    let p: Payload = serde_json::from_slice(bytes).ok()?;
    p.cwd.filter(|c| !c.is_empty())
}

/// Parse a hook payload into (role, text, cwd). Returns None for an unrecognized
/// `hook_event_name`, empty content, a `cwd` that does not match `watched_cwd`
/// (a boundary check against a globally-installed hook firing for another
/// project), or any parse failure. Never panics.
pub(crate) fn parse(bytes: &[u8], watched_cwd: &str) -> Option<Parsed> {
    let p: Payload = serde_json::from_slice(bytes).ok()?;

    let cwd = p.cwd.filter(|c| !c.is_empty())?;
    if cwd != watched_cwd {
        return None;
    }

    let (role, text) = match p.hook_event_name.as_deref()? {
        "UserPromptSubmit" => (Role::User, p.prompt?),
        "Stop" => (Role::Assistant, p.last_assistant_message?),
        _ => return None,
    };
    if text.is_empty() {
        return None;
    }

    Some(Parsed { role, text, cwd })
}

#[cfg(test)]
mod tests {
    use super::*;

    const CWD: &str = "/Users/benn/projects/laterm";

    #[test]
    fn user_prompt_submit_parses_to_user() {
        let p = br#"{"hook_event_name":"UserPromptSubmit","cwd":"/Users/benn/projects/laterm","prompt":"what is $x$?"}"#;
        assert_eq!(
            parse(p, CWD),
            Some(Parsed {
                role: Role::User,
                text: "what is $x$?".into(),
                cwd: CWD.into()
            })
        );
    }

    #[test]
    fn stop_parses_to_assistant() {
        let p = br#"{"hook_event_name":"Stop","cwd":"/Users/benn/projects/laterm","last_assistant_message":"the answer is $y$"}"#;
        assert_eq!(
            parse(p, CWD),
            Some(Parsed {
                role: Role::Assistant,
                text: "the answer is $y$".into(),
                cwd: CWD.into()
            })
        );
    }

    #[test]
    fn unrecognized_event_is_none() {
        let p =
            br#"{"hook_event_name":"PreToolUse","cwd":"/Users/benn/projects/laterm","prompt":"x"}"#;
        assert_eq!(parse(p, CWD), None);
    }

    #[test]
    fn empty_content_is_none() {
        let user = br#"{"hook_event_name":"UserPromptSubmit","cwd":"/Users/benn/projects/laterm","prompt":""}"#;
        assert_eq!(parse(user, CWD), None);
        let stop = br#"{"hook_event_name":"Stop","cwd":"/Users/benn/projects/laterm","last_assistant_message":""}"#;
        assert_eq!(parse(stop, CWD), None);
    }

    #[test]
    fn missing_content_field_is_none() {
        let p = br#"{"hook_event_name":"UserPromptSubmit","cwd":"/Users/benn/projects/laterm"}"#;
        assert_eq!(parse(p, CWD), None);
    }

    #[test]
    fn cwd_mismatch_is_dropped() {
        let p =
            br#"{"hook_event_name":"UserPromptSubmit","cwd":"/some/other/project","prompt":"x"}"#;
        assert_eq!(parse(p, CWD), None);
    }

    #[test]
    fn absent_cwd_is_dropped() {
        let p = br#"{"hook_event_name":"UserPromptSubmit","prompt":"x"}"#;
        assert_eq!(parse(p, CWD), None);
    }

    #[test]
    fn malformed_json_is_none_no_panic() {
        assert_eq!(parse(b"{not valid json", CWD), None);
        assert_eq!(parse(b"", CWD), None);
    }

    #[test]
    fn payload_cwd_extracts_cwd() {
        let p = br#"{"hook_event_name":"Stop","cwd":"/Users/benn/projects/laterm","last_assistant_message":"hi"}"#;
        assert_eq!(payload_cwd(p), Some("/Users/benn/projects/laterm".into()));
        // Absent/empty cwd and malformed JSON yield None (no socket to rendezvous).
        assert_eq!(payload_cwd(br#"{"hook_event_name":"Stop"}"#), None);
        assert_eq!(payload_cwd(br#"{"cwd":""}"#), None);
        assert_eq!(payload_cwd(b"{not json"), None);
    }
}
