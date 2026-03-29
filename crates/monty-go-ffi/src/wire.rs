//! Binary wire types used at the Rust/Go boundary.
//!
//! These types are serialized with MessagePack on the hot path so the Go
//! wrapper can avoid JSON decoding for every start/resume/complete step. Error
//! summaries remain JSON because they are not performance-sensitive and are
//! exposed for formatting/debugging.

use std::{collections::BTreeMap, time::Duration};

use monty::{ExcType, MontyDate, MontyDateTime, MontyException, MontyObject, MontyTimeDelta, MontyTimeZone, ResourceLimits, StackFrame};
use num_bigint::BigInt;
use serde::{Deserialize, Serialize};

/// Current schema version for the Go FFI.
pub const WIRE_VERSION: u32 = 1;

pub const WIRE_VALUE_NONE: u8 = 0;
pub const WIRE_VALUE_ELLIPSIS: u8 = 1;
pub const WIRE_VALUE_BOOL: u8 = 2;
pub const WIRE_VALUE_INT: u8 = 3;
pub const WIRE_VALUE_BIG_INT: u8 = 4;
pub const WIRE_VALUE_FLOAT: u8 = 5;
pub const WIRE_VALUE_STRING: u8 = 6;
pub const WIRE_VALUE_BYTES: u8 = 7;
pub const WIRE_VALUE_LIST: u8 = 8;
pub const WIRE_VALUE_TUPLE: u8 = 9;
pub const WIRE_VALUE_NAMED_TUPLE: u8 = 10;
pub const WIRE_VALUE_DICT: u8 = 11;
pub const WIRE_VALUE_SET: u8 = 12;
pub const WIRE_VALUE_FROZEN_SET: u8 = 13;
pub const WIRE_VALUE_EXCEPTION: u8 = 14;
pub const WIRE_VALUE_PATH: u8 = 15;
pub const WIRE_VALUE_DATACLASS: u8 = 16;
pub const WIRE_VALUE_FUNCTION: u8 = 17;
pub const WIRE_VALUE_REPR: u8 = 18;
pub const WIRE_VALUE_CYCLE: u8 = 19;
pub const WIRE_VALUE_DATE: u8 = 20;
pub const WIRE_VALUE_DATETIME: u8 = 21;
pub const WIRE_VALUE_TIMEDELTA: u8 = 22;
pub const WIRE_VALUE_TIMEZONE: u8 = 23;

pub const WIRE_CALL_RESULT_RETURN: u8 = 0;
pub const WIRE_CALL_RESULT_EXCEPTION: u8 = 1;
pub const WIRE_CALL_RESULT_PENDING: u8 = 2;

pub const WIRE_LOOKUP_RESULT_VALUE: u8 = 0;
pub const WIRE_LOOKUP_RESULT_UNDEFINED: u8 = 1;

pub const WIRE_PROGRESS_FUNCTION_CALL: u8 = 0;
pub const WIRE_PROGRESS_NAME_LOOKUP: u8 = 1;
pub const WIRE_PROGRESS_FUTURE: u8 = 2;
pub const WIRE_PROGRESS_COMPLETE: u8 = 3;

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct WirePair {
    pub key: WireValue,
    pub value: WireValue,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, Default)]
pub struct WireValue {
    pub kind: u8,
    #[serde(default, skip_serializing_if = "is_false")]
    pub bool: bool,
    #[serde(default, skip_serializing_if = "is_zero_i64", rename = "int")]
    pub int_value: i64,
    #[serde(default, skip_serializing_if = "String::is_empty", rename = "big_int")]
    pub big_int: String,
    #[serde(default, skip_serializing_if = "is_zero_f64", rename = "float")]
    pub float_value: f64,
    #[serde(default, skip_serializing_if = "String::is_empty", rename = "string")]
    pub string_value: String,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub bytes: Vec<u8>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub items: Vec<WireValue>,
    #[serde(
        default,
        skip_serializing_if = "String::is_empty",
        rename = "type_name"
    )]
    pub type_name: String,
    #[serde(default, skip_serializing_if = "Vec::is_empty", rename = "field_names")]
    pub field_names: Vec<String>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub values: Vec<WireValue>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub pairs: Vec<WirePair>,
    #[serde(default, skip_serializing_if = "String::is_empty", rename = "exc_type")]
    pub exc_type: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub arg: Option<String>,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub name: String,
    #[serde(default, skip_serializing_if = "is_zero_u64", rename = "type_id")]
    pub type_id: u64,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub attrs: Vec<WirePair>,
    #[serde(default, skip_serializing_if = "is_false")]
    pub frozen: bool,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub docstring: Option<String>,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub placeholder: String,
    #[serde(default, skip_serializing_if = "is_zero_i32")]
    pub year: i32,
    #[serde(default, skip_serializing_if = "is_zero_u8")]
    pub month: u8,
    #[serde(default, skip_serializing_if = "is_zero_u8")]
    pub day: u8,
    #[serde(default, skip_serializing_if = "is_zero_u8")]
    pub hour: u8,
    #[serde(default, skip_serializing_if = "is_zero_u8")]
    pub minute: u8,
    #[serde(default, skip_serializing_if = "is_zero_u8")]
    pub second: u8,
    #[serde(default, skip_serializing_if = "is_zero_u32")]
    pub microsecond: u32,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub offset_seconds: Option<i32>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub timezone_name: Option<String>,
    #[serde(default, skip_serializing_if = "is_zero_i32")]
    pub days: i32,
    #[serde(default, skip_serializing_if = "is_zero_i32")]
    pub seconds: i32,
    #[serde(default, skip_serializing_if = "is_zero_i32")]
    pub microseconds: i32,
}

impl WireValue {
    #[must_use]
    pub fn from_monty(obj: &MontyObject) -> Self {
        match obj {
            MontyObject::None => Self {
                kind: WIRE_VALUE_NONE,
                ..Self::default()
            },
            MontyObject::Ellipsis => Self {
                kind: WIRE_VALUE_ELLIPSIS,
                ..Self::default()
            },
            MontyObject::Bool(value) => Self {
                kind: WIRE_VALUE_BOOL,
                bool: *value,
                ..Self::default()
            },
            MontyObject::Int(value) => Self {
                kind: WIRE_VALUE_INT,
                int_value: *value,
                ..Self::default()
            },
            MontyObject::BigInt(value) => Self {
                kind: WIRE_VALUE_BIG_INT,
                big_int: value.to_string(),
                ..Self::default()
            },
            MontyObject::Float(value) => Self {
                kind: WIRE_VALUE_FLOAT,
                float_value: *value,
                ..Self::default()
            },
            MontyObject::String(value) => Self {
                kind: WIRE_VALUE_STRING,
                string_value: value.clone(),
                ..Self::default()
            },
            MontyObject::Bytes(value) => Self {
                kind: WIRE_VALUE_BYTES,
                bytes: value.clone(),
                ..Self::default()
            },
            MontyObject::List(items) => Self {
                kind: WIRE_VALUE_LIST,
                items: items.iter().map(Self::from_monty).collect(),
                ..Self::default()
            },
            MontyObject::Tuple(items) => Self {
                kind: WIRE_VALUE_TUPLE,
                items: items.iter().map(Self::from_monty).collect(),
                ..Self::default()
            },
            MontyObject::NamedTuple {
                type_name,
                field_names,
                values,
            } => Self {
                kind: WIRE_VALUE_NAMED_TUPLE,
                type_name: type_name.clone(),
                field_names: field_names.clone(),
                values: values.iter().map(Self::from_monty).collect(),
                ..Self::default()
            },
            MontyObject::Dict(items) => Self {
                kind: WIRE_VALUE_DICT,
                pairs: items
                    .into_iter()
                    .map(|(key, value)| WirePair {
                        key: Self::from_monty(key),
                        value: Self::from_monty(value),
                    })
                    .collect(),
                ..Self::default()
            },
            MontyObject::Set(items) => Self {
                kind: WIRE_VALUE_SET,
                items: items.iter().map(Self::from_monty).collect(),
                ..Self::default()
            },
            MontyObject::FrozenSet(items) => Self {
                kind: WIRE_VALUE_FROZEN_SET,
                items: items.iter().map(Self::from_monty).collect(),
                ..Self::default()
            },
            MontyObject::Exception { exc_type, arg } => Self {
                kind: WIRE_VALUE_EXCEPTION,
                exc_type: exc_type.to_string(),
                arg: arg.clone(),
                ..Self::default()
            },
            MontyObject::Path(value) => Self {
                kind: WIRE_VALUE_PATH,
                string_value: value.clone(),
                ..Self::default()
            },
            MontyObject::Dataclass {
                name,
                type_id,
                field_names,
                attrs,
                frozen,
            } => Self {
                kind: WIRE_VALUE_DATACLASS,
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
                ..Self::default()
            },
            MontyObject::Function { name, docstring } => Self {
                kind: WIRE_VALUE_FUNCTION,
                name: name.clone(),
                docstring: docstring.clone(),
                ..Self::default()
            },
            MontyObject::Repr(value) => Self {
                kind: WIRE_VALUE_REPR,
                string_value: value.clone(),
                ..Self::default()
            },
            MontyObject::Cycle(_, placeholder) => Self {
                kind: WIRE_VALUE_CYCLE,
                placeholder: placeholder.clone(),
                ..Self::default()
            },
            MontyObject::Date(date) => Self {
                kind: WIRE_VALUE_DATE,
                year: date.year,
                month: date.month,
                day: date.day,
                ..Self::default()
            },
            MontyObject::DateTime(datetime) => Self {
                kind: WIRE_VALUE_DATETIME,
                year: datetime.year,
                month: datetime.month,
                day: datetime.day,
                hour: datetime.hour,
                minute: datetime.minute,
                second: datetime.second,
                microsecond: datetime.microsecond,
                offset_seconds: datetime.offset_seconds,
                timezone_name: datetime.timezone_name.clone(),
                ..Self::default()
            },
            MontyObject::TimeDelta(delta) => Self {
                kind: WIRE_VALUE_TIMEDELTA,
                days: delta.days,
                seconds: delta.seconds,
                microseconds: delta.microseconds,
                ..Self::default()
            },
            MontyObject::TimeZone(tz) => Self {
                kind: WIRE_VALUE_TIMEZONE,
                days: tz.offset_seconds,
                timezone_name: tz.name.clone(),
                ..Self::default()
            },
            MontyObject::Type(value) => Self {
                kind: WIRE_VALUE_REPR,
                string_value: format!("<class '{value}'>"),
                ..Self::default()
            },
            MontyObject::BuiltinFunction(value) => Self {
                kind: WIRE_VALUE_REPR,
                string_value: format!("<built-in function {value}>"),
                ..Self::default()
            },
        }
    }

    pub fn into_monty(self) -> Result<MontyObject, String> {
        match self.kind {
            WIRE_VALUE_NONE => Ok(MontyObject::None),
            WIRE_VALUE_ELLIPSIS => Ok(MontyObject::Ellipsis),
            WIRE_VALUE_BOOL => Ok(MontyObject::Bool(self.bool)),
            WIRE_VALUE_INT => Ok(MontyObject::Int(self.int_value)),
            WIRE_VALUE_BIG_INT => self
                .big_int
                .parse::<BigInt>()
                .map(MontyObject::BigInt)
                .map_err(|e| format!("invalid bigint: {e}")),
            WIRE_VALUE_FLOAT => Ok(MontyObject::Float(self.float_value)),
            WIRE_VALUE_STRING => Ok(MontyObject::String(self.string_value)),
            WIRE_VALUE_BYTES => Ok(MontyObject::Bytes(self.bytes)),
            WIRE_VALUE_LIST => Ok(MontyObject::List(
                self.items
                    .into_iter()
                    .map(Self::into_monty)
                    .collect::<Result<Vec<_>, _>>()?,
            )),
            WIRE_VALUE_TUPLE => Ok(MontyObject::Tuple(
                self.items
                    .into_iter()
                    .map(Self::into_monty)
                    .collect::<Result<Vec<_>, _>>()?,
            )),
            WIRE_VALUE_NAMED_TUPLE => Ok(MontyObject::NamedTuple {
                type_name: self.type_name,
                field_names: self.field_names,
                values: self
                    .values
                    .into_iter()
                    .map(Self::into_monty)
                    .collect::<Result<Vec<_>, _>>()?,
            }),
            WIRE_VALUE_DICT => Ok(MontyObject::dict(
                self.pairs
                    .into_iter()
                    .map(|pair| Ok((pair.key.into_monty()?, pair.value.into_monty()?)))
                    .collect::<Result<Vec<_>, String>>()?,
            )),
            WIRE_VALUE_SET => Ok(MontyObject::Set(
                self.items
                    .into_iter()
                    .map(Self::into_monty)
                    .collect::<Result<Vec<_>, _>>()?,
            )),
            WIRE_VALUE_FROZEN_SET => Ok(MontyObject::FrozenSet(
                self.items
                    .into_iter()
                    .map(Self::into_monty)
                    .collect::<Result<Vec<_>, _>>()?,
            )),
            WIRE_VALUE_EXCEPTION => Ok(MontyObject::Exception {
                exc_type: self
                    .exc_type
                    .parse()
                    .map_err(|_| format!("unknown exception type: {}", self.exc_type))?,
                arg: self.arg,
            }),
            WIRE_VALUE_PATH => Ok(MontyObject::Path(self.string_value)),
            WIRE_VALUE_DATACLASS => Ok(MontyObject::Dataclass {
                name: self.name,
                type_id: self.type_id,
                field_names: self.field_names,
                attrs: self
                    .attrs
                    .into_iter()
                    .map(|pair| Ok((pair.key.into_monty()?, pair.value.into_monty()?)))
                    .collect::<Result<Vec<_>, String>>()?
                    .into(),
                frozen: self.frozen,
            }),
            WIRE_VALUE_FUNCTION => Ok(MontyObject::Function {
                name: self.name,
                docstring: self.docstring,
            }),
            WIRE_VALUE_REPR => Err("repr values cannot be used as Monty inputs".to_owned()),
            WIRE_VALUE_CYCLE => Err("cycle placeholders cannot be used as Monty inputs".to_owned()),
            WIRE_VALUE_DATE => Ok(MontyObject::Date(MontyDate {
                year: self.year,
                month: self.month,
                day: self.day,
            })),
            WIRE_VALUE_DATETIME => Ok(MontyObject::DateTime(MontyDateTime {
                year: self.year,
                month: self.month,
                day: self.day,
                hour: self.hour,
                minute: self.minute,
                second: self.second,
                microsecond: self.microsecond,
                offset_seconds: self.offset_seconds,
                timezone_name: self.timezone_name,
            })),
            WIRE_VALUE_TIMEDELTA => Ok(MontyObject::TimeDelta(MontyTimeDelta {
                days: self.days,
                seconds: self.seconds,
                microseconds: self.microseconds,
            })),
            WIRE_VALUE_TIMEZONE => Ok(MontyObject::TimeZone(MontyTimeZone {
                offset_seconds: self.days,
                name: self.timezone_name,
            })),
            other => Err(format!("unknown wire value kind: {other}")),
        }
    }
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct WireCompileOptions {
    #[serde(default = "default_wire_version")]
    pub version: u32,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub script_name: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub inputs: Option<Vec<String>>,
    #[serde(default, skip_serializing_if = "is_false")]
    pub type_check: bool,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub type_check_stubs: Option<String>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct WireResourceLimits {
    #[serde(default = "default_wire_version")]
    pub version: u32,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub max_allocations: Option<usize>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub max_duration_secs: Option<f64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub max_memory: Option<usize>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub gc_interval: Option<usize>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
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

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct WireStartOptions {
    #[serde(default = "default_wire_version")]
    pub version: u32,
    #[serde(default, skip_serializing_if = "BTreeMap::is_empty")]
    pub inputs: BTreeMap<String, WireValue>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub limits: Option<WireResourceLimits>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct WireReplOptions {
    #[serde(default = "default_wire_version")]
    pub version: u32,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub script_name: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub limits: Option<WireResourceLimits>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct WireFeedOptions {
    #[serde(default = "default_wire_version")]
    pub version: u32,
    #[serde(default, skip_serializing_if = "BTreeMap::is_empty")]
    pub inputs: BTreeMap<String, WireValue>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct WireCallResult {
    pub kind: u8,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub value: Option<WireValue>,
    #[serde(default, skip_serializing_if = "String::is_empty", rename = "exc_type")]
    pub exc_type: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub arg: Option<String>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct WireLookupResult {
    pub kind: u8,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub value: Option<WireValue>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct WireFutureResults {
    #[serde(default = "default_wire_version")]
    pub version: u32,
    #[serde(default, skip_serializing_if = "BTreeMap::is_empty")]
    pub results: BTreeMap<u32, WireCallResult>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct WireProgressPayload {
    pub variant: u8,
    #[serde(default = "default_wire_version")]
    pub version: u32,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub script_name: String,
    #[serde(default, skip_serializing_if = "is_false")]
    pub is_repl: bool,
    #[serde(default, skip_serializing_if = "is_false")]
    pub is_os_function: bool,
    #[serde(default, skip_serializing_if = "is_false")]
    pub is_method_call: bool,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub function_name: String,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub args: Vec<WireValue>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub kwargs: Vec<WirePair>,
    #[serde(default, skip_serializing_if = "is_zero_u32")]
    pub call_id: u32,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub variable_name: String,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub pending_call_ids: Vec<u32>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub output: Option<WireValue>,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct WireFrame {
    pub filename: String,
    pub line: u32,
    pub column: u32,
    pub end_line: u32,
    pub end_column: u32,
    pub function_name: Option<String>,
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

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WireErrorSummary {
    #[serde(default = "default_wire_version")]
    pub version: u32,
    pub kind: String,
    pub type_name: String,
    pub message: String,
    #[serde(default)]
    pub traceback: Vec<WireFrame>,
}

impl WireErrorSummary {
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

#[must_use]
pub const fn default_wire_version() -> u32 {
    WIRE_VERSION
}

const fn is_false(value: &bool) -> bool {
    !*value
}

const fn is_zero_u32(value: &u32) -> bool {
    *value == 0
}

const fn is_zero_u64(value: &u64) -> bool {
    *value == 0
}

const fn is_zero_i64(value: &i64) -> bool {
    *value == 0
}

fn is_zero_f64(value: &f64) -> bool {
    *value == 0.0
}

const fn is_zero_i32(value: &i32) -> bool {
    *value == 0
}

const fn is_zero_u8(value: &u8) -> bool {
    *value == 0
}

#[cfg(test)]
mod tests {
    use num_bigint::BigInt;

    use super::WireValue;
    use monty::{ExcType, MontyDate, MontyDateTime, MontyObject, MontyTimeDelta, MontyTimeZone};

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
                (
                    MontyObject::String("enabled".to_owned()),
                    MontyObject::Bool(true),
                ),
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
        let error = WireValue {
            kind: super::WIRE_VALUE_REPR,
            string_value: "<object repr>".to_owned(),
            ..WireValue::default()
        }
        .into_monty()
        .expect_err("repr values must be rejected");
        assert_eq!(error, "repr values cannot be used as Monty inputs");
    }

    #[test]
    fn wire_value_round_trips_exceptions() {
        let original = MontyObject::Exception {
            exc_type: ExcType::RuntimeError,
            arg: Some("boom".to_owned()),
        };

        let decoded = WireValue::from_monty(&original)
            .into_monty()
            .expect("exceptions should round-trip");
        assert_eq!(decoded, original);
    }

    #[test]
    fn wire_value_round_trips_date() {
        let original = MontyObject::Date(MontyDate {
            year: 2026,
            month: 3,
            day: 29,
        });

        let decoded = WireValue::from_monty(&original)
            .into_monty()
            .expect("date should round-trip");
        assert_eq!(decoded, original);
    }

    #[test]
    fn wire_value_round_trips_datetime() {
        let original = MontyObject::DateTime(MontyDateTime {
            year: 2026,
            month: 3,
            day: 29,
            hour: 14,
            minute: 30,
            second: 45,
            microsecond: 123_456,
            offset_seconds: Some(3600),
            timezone_name: Some("UTC+01:00".to_owned()),
        });

        let decoded = WireValue::from_monty(&original)
            .into_monty()
            .expect("datetime should round-trip");
        assert_eq!(decoded, original);
    }

    #[test]
    fn wire_value_round_trips_naive_datetime() {
        let original = MontyObject::DateTime(MontyDateTime {
            year: 2026,
            month: 1,
            day: 1,
            hour: 0,
            minute: 0,
            second: 0,
            microsecond: 0,
            offset_seconds: None,
            timezone_name: None,
        });

        let decoded = WireValue::from_monty(&original)
            .into_monty()
            .expect("naive datetime should round-trip");
        assert_eq!(decoded, original);
    }

    #[test]
    fn wire_value_round_trips_timedelta() {
        let original = MontyObject::TimeDelta(MontyTimeDelta {
            days: -1,
            seconds: 3600,
            microseconds: 500_000,
        });

        let decoded = WireValue::from_monty(&original)
            .into_monty()
            .expect("timedelta should round-trip");
        assert_eq!(decoded, original);
    }

    #[test]
    fn wire_value_round_trips_timezone() {
        let original = MontyObject::TimeZone(MontyTimeZone {
            offset_seconds: -18000,
            name: Some("EST".to_owned()),
        });

        let decoded = WireValue::from_monty(&original)
            .into_monty()
            .expect("timezone should round-trip");
        assert_eq!(decoded, original);
    }

    #[test]
    fn wire_value_round_trips_utc_timezone() {
        let original = MontyObject::TimeZone(MontyTimeZone {
            offset_seconds: 0,
            name: None,
        });

        let decoded = WireValue::from_monty(&original)
            .into_monty()
            .expect("utc timezone should round-trip");
        assert_eq!(decoded, original);
    }
}
