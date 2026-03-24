//! JSON wire types used at the Rust/Go boundary.
//!
//! These types form the stable payload schema for values, options, progress
//! metadata, and error summaries. Handles remain opaque across the C ABI;
//! whenever structured data must cross the boundary it is encoded as JSON using
//! these versioned wire types.

use std::{collections::BTreeMap, time::Duration};

use monty::{ExcType, MontyException, MontyObject, ResourceLimits, StackFrame};
use num_bigint::BigInt;
use serde::{Deserialize, Serialize};

/// Current JSON schema version for the Go FFI.
pub const WIRE_VERSION: u32 = 1;

/// Key/value pair that preserves insertion order and non-string keys.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct WirePair {
    /// Dictionary or kwargs key.
    pub key: WireValue,
    /// Dictionary or kwargs value.
    pub value: WireValue,
}

/// Explicit method metadata for externally resolved function values.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct WireFunctionValue {
    /// External function name exposed to Monty.
    pub name: String,
    /// Optional docstring used for help and repr output.
    pub docstring: Option<String>,
}

/// Stable JSON representation of `MontyObject`.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case")]
pub enum WireValue {
    /// Python `None`.
    None,
    /// Python `Ellipsis`.
    Ellipsis,
    /// Boolean value.
    Bool { value: bool },
    /// Signed 64-bit integer.
    Int { value: i64 },
    /// Arbitrary-precision integer as a decimal string.
    BigInt { value: String },
    /// 64-bit float.
    Float { value: f64 },
    /// UTF-8 string.
    String { value: String },
    /// Raw bytes.
    Bytes { value: Vec<u8> },
    /// List items.
    List { items: Vec<WireValue> },
    /// Tuple items.
    Tuple { items: Vec<WireValue> },
    /// Named tuple values.
    NamedTuple {
        /// Type name used in repr output.
        type_name: String,
        /// Field names in order.
        field_names: Vec<String>,
        /// Field values in order.
        values: Vec<WireValue>,
    },
    /// Dictionary entries in insertion order.
    Dict { items: Vec<WirePair> },
    /// Set items.
    Set { items: Vec<WireValue> },
    /// Frozen set items.
    FrozenSet { items: Vec<WireValue> },
    /// Python exception instance as a value.
    Exception {
        /// Python exception type name.
        exc_type: String,
        /// Optional constructor argument / message.
        arg: Option<String>,
    },
    /// Explicit path value.
    Path { value: String },
    /// Explicit dataclass value.
    Dataclass {
        /// Class name for repr and debugging.
        name: String,
        /// Stable type identifier supplied by the host.
        type_id: u64,
        /// Declared field names in order.
        field_names: Vec<String>,
        /// Attribute mapping entries.
        attrs: Vec<WirePair>,
        /// Whether the dataclass is frozen.
        frozen: bool,
    },
    /// External function value used by low-level name lookup resolution.
    Function(WireFunctionValue),
    /// Output-only fallback representation.
    Repr { value: String },
    /// Cycle placeholder for output-only cyclic values.
    Cycle { placeholder: String },
}

impl WireValue {
    /// Converts a `MontyObject` to the stable wire format.
    #[must_use]
    pub fn from_monty(obj: &MontyObject) -> Self {
        match obj {
            MontyObject::None => Self::None,
            MontyObject::Ellipsis => Self::Ellipsis,
            MontyObject::Bool(value) => Self::Bool { value: *value },
            MontyObject::Int(value) => Self::Int { value: *value },
            MontyObject::BigInt(value) => Self::BigInt {
                value: value.to_string(),
            },
            MontyObject::Float(value) => Self::Float { value: *value },
            MontyObject::String(value) => Self::String { value: value.clone() },
            MontyObject::Bytes(value) => Self::Bytes { value: value.clone() },
            MontyObject::List(items) => Self::List {
                items: items.iter().map(Self::from_monty).collect(),
            },
            MontyObject::Tuple(items) => Self::Tuple {
                items: items.iter().map(Self::from_monty).collect(),
            },
            MontyObject::NamedTuple {
                type_name,
                field_names,
                values,
            } => Self::NamedTuple {
                type_name: type_name.clone(),
                field_names: field_names.clone(),
                values: values.iter().map(Self::from_monty).collect(),
            },
            MontyObject::Dict(items) => Self::Dict {
                items: items
                    .into_iter()
                    .map(|(key, value)| WirePair {
                        key: Self::from_monty(key),
                        value: Self::from_monty(value),
                    })
                    .collect(),
            },
            MontyObject::Set(items) => Self::Set {
                items: items.iter().map(Self::from_monty).collect(),
            },
            MontyObject::FrozenSet(items) => Self::FrozenSet {
                items: items.iter().map(Self::from_monty).collect(),
            },
            MontyObject::Exception { exc_type, arg } => Self::Exception {
                exc_type: exc_type.to_string(),
                arg: arg.clone(),
            },
            MontyObject::Path(value) => Self::Path { value: value.clone() },
            MontyObject::Dataclass {
                name,
                type_id,
                field_names,
                attrs,
                frozen,
            } => Self::Dataclass {
                name: name.clone(),
                type_id: *type_id,
                field_names: field_names.clone(),
                attrs: attrs
                    .into_iter()
                    .map(|(key, value)| WirePair {
                        key: Self::from_monty(key),
                        value: Self::from_monty(value),
                    })
                    .collect(),
                frozen: *frozen,
            },
            MontyObject::Function { name, docstring } => Self::Function(WireFunctionValue {
                name: name.clone(),
                docstring: docstring.clone(),
            }),
            MontyObject::Repr(value) => Self::Repr { value: value.clone() },
            MontyObject::Cycle(_, placeholder) => Self::Cycle {
                placeholder: placeholder.clone(),
            },
            MontyObject::Type(value) => Self::Repr {
                value: format!("<class '{value}'>"),
            },
            MontyObject::BuiltinFunction(value) => Self::Repr {
                value: format!("<built-in function {value}>"),
            },
        }
    }

    /// Converts the wire value back to a `MontyObject`.
    ///
    /// This intentionally rejects output-only variants and representation-only
    /// types that cannot safely be injected back into the interpreter.
    pub fn into_monty(self) -> Result<MontyObject, String> {
        match self {
            Self::None => Ok(MontyObject::None),
            Self::Ellipsis => Ok(MontyObject::Ellipsis),
            Self::Bool { value } => Ok(MontyObject::Bool(value)),
            Self::Int { value } => Ok(MontyObject::Int(value)),
            Self::BigInt { value } => value
                .parse::<BigInt>()
                .map(MontyObject::BigInt)
                .map_err(|e| format!("invalid bigint: {e}")),
            Self::Float { value } => Ok(MontyObject::Float(value)),
            Self::String { value } => Ok(MontyObject::String(value)),
            Self::Bytes { value } => Ok(MontyObject::Bytes(value)),
            Self::List { items } => Ok(MontyObject::List(
                items.into_iter().map(Self::into_monty).collect::<Result<Vec<_>, _>>()?,
            )),
            Self::Tuple { items } => Ok(MontyObject::Tuple(
                items.into_iter().map(Self::into_monty).collect::<Result<Vec<_>, _>>()?,
            )),
            Self::NamedTuple {
                type_name,
                field_names,
                values,
            } => Ok(MontyObject::NamedTuple {
                type_name,
                field_names,
                values: values
                    .into_iter()
                    .map(Self::into_monty)
                    .collect::<Result<Vec<_>, _>>()?,
            }),
            Self::Dict { items } => Ok(MontyObject::dict(
                items
                    .into_iter()
                    .map(|pair| Ok((pair.key.into_monty()?, pair.value.into_monty()?)))
                    .collect::<Result<Vec<_>, String>>()?,
            )),
            Self::Set { items } => Ok(MontyObject::Set(
                items.into_iter().map(Self::into_monty).collect::<Result<Vec<_>, _>>()?,
            )),
            Self::FrozenSet { items } => Ok(MontyObject::FrozenSet(
                items.into_iter().map(Self::into_monty).collect::<Result<Vec<_>, _>>()?,
            )),
            Self::Exception { exc_type, arg } => Ok(MontyObject::Exception {
                exc_type: exc_type
                    .parse()
                    .map_err(|_| format!("unknown exception type: {exc_type}"))?,
                arg,
            }),
            Self::Path { value } => Ok(MontyObject::Path(value)),
            Self::Dataclass {
                name,
                type_id,
                field_names,
                attrs,
                frozen,
            } => Ok(MontyObject::Dataclass {
                name,
                type_id,
                field_names,
                attrs: attrs
                    .into_iter()
                    .map(|pair| Ok((pair.key.into_monty()?, pair.value.into_monty()?)))
                    .collect::<Result<Vec<_>, String>>()?
                    .into(),
                frozen,
            }),
            Self::Function(WireFunctionValue { name, docstring }) => Ok(MontyObject::Function { name, docstring }),
            Self::Repr { .. } => Err("repr values cannot be used as Monty inputs".to_owned()),
            Self::Cycle { .. } => Err("cycle placeholders cannot be used as Monty inputs".to_owned()),
        }
    }
}

/// Compile-time configuration for `Runner`.
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct WireCompileOptions {
    /// Schema version.
    #[serde(default = "default_wire_version")]
    pub version: u32,
    /// Script name used in tracebacks.
    #[serde(default)]
    pub script_name: Option<String>,
    /// Input names declared for the runner.
    #[serde(default)]
    pub inputs: Option<Vec<String>>,
    /// Whether to run type checking before compilation.
    #[serde(default)]
    pub type_check: bool,
    /// Optional prefix stubs for type checking.
    #[serde(default)]
    pub type_check_stubs: Option<String>,
}

/// Resource limit configuration accepted from Go.
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct WireResourceLimits {
    /// Schema version.
    #[serde(default = "default_wire_version")]
    pub version: u32,
    /// Maximum heap allocations.
    #[serde(default)]
    pub max_allocations: Option<usize>,
    /// Maximum execution time in seconds.
    #[serde(default)]
    pub max_duration_secs: Option<f64>,
    /// Maximum memory in bytes.
    #[serde(default)]
    pub max_memory: Option<usize>,
    /// GC interval.
    #[serde(default)]
    pub gc_interval: Option<usize>,
    /// Maximum recursion depth.
    #[serde(default)]
    pub max_recursion_depth: Option<usize>,
}

impl From<WireResourceLimits> for ResourceLimits {
    fn from(value: WireResourceLimits) -> Self {
        let mut limits = ResourceLimits::new();
        limits.max_allocations = value.max_allocations;
        limits.max_duration = value.max_duration_secs.map(Duration::from_secs_f64);
        limits.max_memory = value.max_memory;
        limits.gc_interval = value.gc_interval;
        limits.max_recursion_depth = value.max_recursion_depth;
        limits
    }
}

/// Start-time configuration for a runner.
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct WireStartOptions {
    /// Schema version.
    #[serde(default = "default_wire_version")]
    pub version: u32,
    /// Named input values.
    #[serde(default)]
    pub inputs: BTreeMap<String, WireValue>,
    /// Optional resource limits.
    #[serde(default)]
    pub limits: Option<WireResourceLimits>,
}

/// REPL construction configuration.
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct WireReplOptions {
    /// Schema version.
    #[serde(default = "default_wire_version")]
    pub version: u32,
    /// Script name used in tracebacks.
    #[serde(default)]
    pub script_name: Option<String>,
    /// Optional resource limits.
    #[serde(default)]
    pub limits: Option<WireResourceLimits>,
}

/// REPL feed/start options.
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct WireFeedOptions {
    /// Schema version.
    #[serde(default = "default_wire_version")]
    pub version: u32,
    /// Named input values injected into the REPL namespace.
    #[serde(default)]
    pub inputs: BTreeMap<String, WireValue>,
}

/// Result supplied when resuming a function or OS snapshot.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case")]
pub enum WireCallResult {
    /// Return a concrete value.
    Return { value: WireValue },
    /// Raise an exception in the sandbox.
    Exception {
        /// Python exception type name.
        exc_type: String,
        /// Optional constructor argument / message.
        arg: Option<String>,
    },
    /// Continue with a pending external future.
    Pending,
}

/// Result supplied when resuming a name lookup snapshot.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case")]
pub enum WireLookupResult {
    /// Resolve the name to a value.
    Value { value: WireValue },
    /// Leave the name undefined so Monty raises `NameError`.
    Undefined,
}

/// Batched future results supplied to a future snapshot.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WireFutureResults {
    /// Schema version.
    #[serde(default = "default_wire_version")]
    pub version: u32,
    /// Results keyed by `call_id`.
    pub results: BTreeMap<u32, WireCallResult>,
}

/// High-level description of a progress handle.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(tag = "variant", rename_all = "snake_case")]
pub enum WireProgressDescription {
    /// Function or OS call snapshot.
    FunctionCall {
        /// Schema version.
        version: u32,
        /// Script name used by the host-side wrapper.
        script_name: String,
        /// Whether this snapshot originated from a REPL.
        is_repl: bool,
        /// Whether the call is an OS callback.
        is_os_function: bool,
        /// Whether the call is a dataclass method callback.
        is_method_call: bool,
        /// Function name or `OsFunction` string.
        function_name: String,
        /// Positional arguments.
        args: Vec<WireValue>,
        /// Keyword arguments in order.
        kwargs: Vec<WirePair>,
        /// Async correlation ID.
        call_id: u32,
    },
    /// Unresolved name lookup snapshot.
    NameLookup {
        /// Schema version.
        version: u32,
        /// Script name used by the host-side wrapper.
        script_name: String,
        /// Whether this snapshot originated from a REPL.
        is_repl: bool,
        /// Name being resolved.
        variable_name: String,
    },
    /// Future resolution snapshot.
    Future {
        /// Schema version.
        version: u32,
        /// Script name used by the host-side wrapper.
        script_name: String,
        /// Whether this snapshot originated from a REPL.
        is_repl: bool,
        /// Pending async call IDs.
        pending_call_ids: Vec<u32>,
    },
    /// Completed execution with a final output.
    Complete {
        /// Schema version.
        version: u32,
        /// Script name used by the host-side wrapper.
        script_name: String,
        /// Whether this completion originated from a REPL.
        is_repl: bool,
        /// Final output value.
        output: WireValue,
    },
}

/// Structured traceback frame for runtime errors.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct WireFrame {
    /// Source filename.
    pub filename: String,
    /// 1-based start line.
    pub line: u32,
    /// 1-based start column.
    pub column: u32,
    /// 1-based end line.
    pub end_line: u32,
    /// 1-based end column.
    pub end_column: u32,
    /// Optional frame / function name.
    pub function_name: Option<String>,
    /// Optional preview source line.
    pub source_line: Option<String>,
}

impl From<&StackFrame> for WireFrame {
    fn from(value: &StackFrame) -> Self {
        Self {
            filename: value.filename.clone(),
            line: u32::from(value.start.line),
            column: u32::from(value.start.column),
            end_line: u32::from(value.end.line),
            end_column: u32::from(value.end.column),
            function_name: value.frame_name.clone(),
            source_line: value.preview_line.clone(),
        }
    }
}

/// JSON summary for an FFI error handle.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WireErrorSummary {
    /// Schema version.
    #[serde(default = "default_wire_version")]
    pub version: u32,
    /// High-level error kind: `syntax`, `runtime`, `typing`, or `api`.
    pub kind: String,
    /// Underlying Python exception type or `TypeError` for typing failures.
    pub type_name: String,
    /// Human-readable message.
    pub message: String,
    /// Traceback frames for runtime errors.
    #[serde(default)]
    pub traceback: Vec<WireFrame>,
}

impl WireErrorSummary {
    /// Builds a summary from a runtime or syntax `MontyException`.
    #[must_use]
    pub fn from_exception(error: &MontyException) -> Self {
        let kind = if error.exc_type() == ExcType::SyntaxError {
            "syntax"
        } else {
            "runtime"
        };
        Self {
            version: WIRE_VERSION,
            kind: kind.to_owned(),
            type_name: error.exc_type().to_string(),
            message: error.message().unwrap_or_default().to_owned(),
            traceback: error.traceback().iter().map(WireFrame::from).collect(),
        }
    }
}

/// Default JSON schema version used by serde defaults.
#[must_use]
pub const fn default_wire_version() -> u32 {
    WIRE_VERSION
}

#[cfg(test)]
mod tests {
    use num_bigint::BigInt;

    use super::{WirePair, WireValue};
    use monty::{ExcType, MontyObject};

    #[test]
    fn wire_value_round_trips_nested_dicts() {
        let original = MontyObject::dict(vec![
            (
                MontyObject::String("numbers".to_owned()),
                MontyObject::List(vec![
                    MontyObject::Int(1),
                    MontyObject::BigInt(BigInt::from(1_u64) << 80),
                ]),
            ),
            (
                MontyObject::String("path".to_owned()),
                MontyObject::Path("/tmp/example.txt".to_owned()),
            ),
        ]);

        let decoded = WireValue::from_monty(&original)
            .into_monty()
            .expect("wire value should round-trip");
        assert_eq!(decoded, original);
    }

    #[test]
    fn wire_value_round_trips_dataclasses() {
        let original = MontyObject::Dataclass {
            name: "Config".to_owned(),
            type_id: 7,
            field_names: vec!["enabled".to_owned(), "path".to_owned()],
            attrs: vec![
                (MontyObject::String("enabled".to_owned()), MontyObject::Bool(true)),
                (
                    MontyObject::String("path".to_owned()),
                    MontyObject::Path("/config.json".to_owned()),
                ),
            ]
            .into(),
            frozen: true,
        };

        let decoded = WireValue::from_monty(&original)
            .into_monty()
            .expect("dataclass should round-trip");
        assert_eq!(decoded, original);
    }

    #[test]
    fn wire_value_rejects_repr_inputs() {
        let error = WireValue::Repr {
            value: "<object repr>".to_owned(),
        }
        .into_monty()
        .expect_err("repr values must be rejected");
        assert_eq!(error, "repr values cannot be used as Monty inputs");
    }

    #[test]
    fn wire_value_preserves_exception_payloads() {
        let original = WireValue::Exception {
            exc_type: ExcType::ValueError.to_string(),
            arg: Some("bad input".to_owned()),
        };

        let decoded = original.clone().into_monty().expect("exception should decode");
        assert_eq!(
            decoded,
            MontyObject::Exception {
                exc_type: ExcType::ValueError,
                arg: Some("bad input".to_owned()),
            }
        );

        assert_eq!(
            WireValue::from_monty(&decoded),
            WireValue::Exception {
                exc_type: ExcType::ValueError.to_string(),
                arg: Some("bad input".to_owned()),
            }
        );
    }

    #[test]
    fn wire_pair_round_trips_non_string_keys() {
        let wire = WireValue::Dict {
            items: vec![WirePair {
                key: WireValue::Int { value: 1 },
                value: WireValue::String {
                    value: "one".to_owned(),
                },
            }],
        };

        let decoded = wire.into_monty().expect("dict should decode");
        assert_eq!(
            decoded,
            MontyObject::dict(vec![(MontyObject::Int(1), MontyObject::String("one".to_owned()))])
        );
    }
}
