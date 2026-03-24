//! C ABI for Go bindings to Monty.
//!
//! The ABI deliberately keeps compiled runners, REPL sessions, and in-flight
//! snapshots opaque. Structured values cross the boundary as versioned binary
//! payloads defined in [`wire`], while suspend/resume state stays in Rust-owned
//! handles that the Go wrapper drives explicitly.

mod wire;

use std::{
    any::Any,
    borrow::Cow,
    collections::BTreeMap,
    ffi::{CStr, c_char},
    mem,
    panic::{AssertUnwindSafe, catch_unwind},
    ptr,
};

use monty::{
    ExcType, ExtFunctionResult, LimitedTracker, MontyException, MontyObject, MontyRepl, MontyRun,
    NameLookupResult, NoLimitTracker, PrintWriter, PrintWriterCallback, ReplProgress,
    ResourceTracker, RunProgress,
};
use monty_type_checking::{SourceFile, TypeCheckingDiagnostics, type_check};
use wire::{
    WIRE_CALL_RESULT_EXCEPTION, WIRE_CALL_RESULT_PENDING, WIRE_CALL_RESULT_RETURN,
    WIRE_LOOKUP_RESULT_UNDEFINED, WIRE_LOOKUP_RESULT_VALUE, WIRE_PROGRESS_COMPLETE,
    WIRE_PROGRESS_FUNCTION_CALL, WIRE_PROGRESS_FUTURE, WIRE_PROGRESS_NAME_LOOKUP, WireCallResult,
    WireCompileOptions, WireErrorSummary, WireFeedOptions, WireFutureResults, WireLookupResult,
    WireProgressPayload, WireReplOptions, WireStartOptions, WireValue,
};

/// Opaque runner handle for the Go bindings.
pub struct MontyGoRunner {
    runner: MontyRun,
    script_name: String,
    input_names: Vec<String>,
}

/// Opaque REPL handle for the Go bindings.
pub struct MontyGoRepl {
    script_name: String,
    inner: Option<StoredRepl>,
}

/// Opaque progress handle for the Go bindings.
pub struct MontyGoProgress {
    inner: Option<StoredProgress>,
}

/// Opaque error handle for the Go bindings.
pub struct MontyGoError {
    inner: FfiError,
}

#[derive(Debug)]
enum FfiError {
    Exception(MontyException),
    Typing(TypeCheckingDiagnostics),
    Api(String),
}

fn ffi_panic_error(payload: Box<dyn Any + Send>) -> FfiError {
    let message = if let Some(message) = payload.downcast_ref::<&str>() {
        (*message).to_owned()
    } else if let Some(message) = payload.downcast_ref::<String>() {
        message.clone()
    } else {
        "panic payload was not a string".to_owned()
    };
    FfiError::Api(format!("monty-go ffi panicked: {message}"))
}

fn catch_runner_result<F>(f: F) -> MontyGoRunnerResult
where
    F: FnOnce() -> MontyGoRunnerResult,
{
    match catch_unwind(AssertUnwindSafe(f)) {
        Ok(result) => result,
        Err(payload) => MontyGoRunnerResult::err(ffi_panic_error(payload)),
    }
}

fn catch_repl_result<F>(f: F) -> MontyGoReplResult
where
    F: FnOnce() -> MontyGoReplResult,
{
    match catch_unwind(AssertUnwindSafe(f)) {
        Ok(result) => result,
        Err(payload) => MontyGoReplResult::err(ffi_panic_error(payload)),
    }
}

fn catch_op_result<F>(f: F) -> MontyGoOpResult
where
    F: FnOnce() -> MontyGoOpResult,
{
    match catch_unwind(AssertUnwindSafe(f)) {
        Ok(result) => result,
        Err(payload) => MontyGoOpResult::err(ffi_panic_error(payload), None, String::new()),
    }
}

fn catch_error_result<F>(f: F) -> *mut MontyGoError
where
    F: FnOnce() -> *mut MontyGoError,
{
    match catch_unwind(AssertUnwindSafe(f)) {
        Ok(result) => result,
        Err(payload) => Box::into_raw(Box::new(MontyGoError {
            inner: ffi_panic_error(payload),
        })),
    }
}

fn catch_bytes_result<F>(error_out: *mut *mut MontyGoError, f: F) -> MontyGoBytes
where
    F: FnOnce() -> MontyGoBytes,
{
    match catch_unwind(AssertUnwindSafe(f)) {
        Ok(result) => result,
        Err(payload) => {
            if !error_out.is_null() {
                unsafe {
                    *error_out = Box::into_raw(Box::new(MontyGoError {
                        inner: ffi_panic_error(payload),
                    }))
                };
            }
            MontyGoBytes::empty()
        }
    }
}

#[derive(Debug, serde::Serialize, serde::Deserialize)]
enum StoredRepl {
    NoLimit(MontyRepl<NoLimitTracker>),
    Limited(MontyRepl<LimitedTracker>),
}

#[derive(Debug, serde::Serialize, serde::Deserialize)]
enum StoredProgress {
    RunNoLimit {
        progress: RunProgress<NoLimitTracker>,
        script_name: String,
    },
    RunLimited {
        progress: RunProgress<LimitedTracker>,
        script_name: String,
    },
    ReplNoLimit {
        progress: ReplProgress<NoLimitTracker>,
        script_name: String,
    },
    ReplLimited {
        progress: ReplProgress<LimitedTracker>,
        script_name: String,
    },
}

/// Heap-allocated bytes returned across the C ABI.
#[repr(C)]
pub struct MontyGoBytes {
    /// Pointer to the byte buffer.
    pub ptr: *mut u8,
    /// Buffer length in bytes.
    pub len: usize,
}

impl MontyGoBytes {
    fn empty() -> Self {
        Self {
            ptr: ptr::null_mut(),
            len: 0,
        }
    }

    fn from_vec(bytes: Vec<u8>) -> Self {
        if bytes.is_empty() {
            return Self::empty();
        }
        let mut bytes = bytes.into_boxed_slice();
        let result = Self {
            ptr: bytes.as_mut_ptr(),
            len: bytes.len(),
        };
        mem::forget(bytes);
        result
    }
}

/// Result of runner construction or loading.
#[repr(C)]
pub struct MontyGoRunnerResult {
    /// Created runner handle on success.
    pub runner: *mut MontyGoRunner,
    /// Error handle on failure.
    pub error: *mut MontyGoError,
}

/// Result of REPL construction or loading.
#[repr(C)]
pub struct MontyGoReplResult {
    /// Created REPL handle on success.
    pub repl: *mut MontyGoRepl,
    /// Error handle on failure.
    pub error: *mut MontyGoError,
}

/// Result of start/resume/feed operations.
#[repr(C)]
pub struct MontyGoOpResult {
    /// Progress handle on success.
    pub progress: *mut MontyGoProgress,
    /// Decoded payload for the current progress state.
    pub progress_payload: MontyGoBytes,
    /// Error handle on failure.
    pub error: *mut MontyGoError,
    /// Recovered REPL handle for REPL runtime errors.
    pub repl: *mut MontyGoRepl,
    /// Captured `print()` output from this step.
    pub prints: MontyGoBytes,
}

impl MontyGoRunnerResult {
    fn ok(runner: MontyGoRunner) -> Self {
        Self {
            runner: Box::into_raw(Box::new(runner)),
            error: ptr::null_mut(),
        }
    }

    fn err(error: FfiError) -> Self {
        Self {
            runner: ptr::null_mut(),
            error: Box::into_raw(Box::new(MontyGoError { inner: error })),
        }
    }
}

impl MontyGoReplResult {
    fn ok(repl: MontyGoRepl) -> Self {
        Self {
            repl: Box::into_raw(Box::new(repl)),
            error: ptr::null_mut(),
        }
    }

    fn err(error: FfiError) -> Self {
        Self {
            repl: ptr::null_mut(),
            error: Box::into_raw(Box::new(MontyGoError { inner: error })),
        }
    }
}

impl MontyGoOpResult {
    fn ok(progress: StoredProgress, prints: String) -> Self {
        match encode_wire(&progress.describe()) {
            Ok(progress_payload) => Self {
                progress: Box::into_raw(Box::new(MontyGoProgress {
                    inner: Some(progress),
                })),
                progress_payload,
                error: ptr::null_mut(),
                repl: ptr::null_mut(),
                prints: MontyGoBytes::from_vec(prints.into_bytes()),
            },
            Err(error) => Self::err(error, None, prints),
        }
    }

    fn err(error: FfiError, repl: Option<MontyGoRepl>, prints: String) -> Self {
        Self {
            progress: ptr::null_mut(),
            progress_payload: MontyGoBytes::empty(),
            error: Box::into_raw(Box::new(MontyGoError { inner: error })),
            repl: repl
                .map(|repl| Box::into_raw(Box::new(repl)))
                .unwrap_or(ptr::null_mut()),
            prints: MontyGoBytes::from_vec(prints.into_bytes()),
        }
    }
}

struct PrintCollector {
    buffer: String,
}

impl PrintCollector {
    fn new() -> Self {
        Self {
            buffer: String::new(),
        }
    }

    fn into_string(self) -> String {
        self.buffer
    }
}

impl PrintWriterCallback for PrintCollector {
    fn stdout_write(&mut self, output: Cow<'_, str>) -> Result<(), MontyException> {
        self.buffer.push_str(&output);
        Ok(())
    }

    fn stdout_push(&mut self, end: char) -> Result<(), MontyException> {
        self.buffer.push(end);
        Ok(())
    }
}

impl FfiError {
    fn summary(&self) -> WireErrorSummary {
        match self {
            Self::Exception(error) => WireErrorSummary::from_exception(error),
            Self::Typing(error) => WireErrorSummary {
                version: wire::WIRE_VERSION,
                kind: "typing".to_owned(),
                type_name: "TypeError".to_owned(),
                message: error.to_string(),
                traceback: Vec::new(),
            },
            Self::Api(message) => WireErrorSummary {
                version: wire::WIRE_VERSION,
                kind: "api".to_owned(),
                type_name: "RuntimeError".to_owned(),
                message: message.clone(),
                traceback: Vec::new(),
            },
        }
    }

    fn display(&self, format: &str, color: bool) -> Result<String, String> {
        match self {
            Self::Exception(error) => match format {
                "traceback" => Ok(error.to_string()),
                "type-msg" => Ok(error.summary()),
                "msg" => Ok(error.message().unwrap_or_default().to_owned()),
                _ => Err(format!(
                    "invalid display format '{format}', expected 'traceback', 'type-msg', or 'msg'"
                )),
            },
            Self::Typing(error) => error
                .clone()
                .color(color)
                .format_from_str(format)
                .map(|failure| failure.to_string()),
            Self::Api(message) => match format {
                "msg" | "type-msg" | "traceback" => Ok(message.clone()),
                _ => Err(format!(
                    "invalid display format '{format}', expected 'traceback', 'type-msg', or 'msg'"
                )),
            },
        }
    }
}

impl StoredProgress {
    fn describe(&self) -> WireProgressPayload {
        match self {
            Self::RunNoLimit {
                progress,
                script_name,
            } => describe_run_progress(progress, script_name, false),
            Self::RunLimited {
                progress,
                script_name,
            } => describe_run_progress(progress, script_name, false),
            Self::ReplNoLimit {
                progress,
                script_name,
            } => describe_repl_progress(progress, script_name, true),
            Self::ReplLimited {
                progress,
                script_name,
            } => describe_repl_progress(progress, script_name, true),
        }
    }

    fn into_repl(self) -> Result<MontyGoRepl, FfiError> {
        match self {
            Self::ReplNoLimit {
                progress,
                script_name,
            } => Ok(MontyGoRepl {
                script_name,
                inner: Some(StoredRepl::NoLimit(progress.into_repl())),
            }),
            Self::ReplLimited {
                progress,
                script_name,
            } => Ok(MontyGoRepl {
                script_name,
                inner: Some(StoredRepl::Limited(progress.into_repl())),
            }),
            Self::RunNoLimit { .. } | Self::RunLimited { .. } => Err(FfiError::Api(
                "progress handle does not own a REPL session".to_owned(),
            )),
        }
    }

    fn dump(&self) -> Result<Vec<u8>, FfiError> {
        postcard::to_allocvec(self).map_err(|e| FfiError::Api(e.to_string()))
    }
}

fn describe_run_progress<T: ResourceTracker>(
    progress: &RunProgress<T>,
    script_name: &str,
    is_repl: bool,
) -> WireProgressPayload {
    match progress {
        RunProgress::FunctionCall(call) => WireProgressPayload {
            variant: WIRE_PROGRESS_FUNCTION_CALL,
            version: wire::WIRE_VERSION,
            script_name: script_name.to_owned(),
            is_repl,
            is_os_function: false,
            is_method_call: call.method_call,
            function_name: call.function_name.clone(),
            args: call.args.iter().map(WireValue::from_monty).collect(),
            kwargs: call
                .kwargs
                .iter()
                .map(|(key, value)| wire::WirePair {
                    key: WireValue::from_monty(key),
                    value: WireValue::from_monty(value),
                })
                .collect(),
            call_id: call.call_id,
            ..WireProgressPayload::default()
        },
        RunProgress::OsCall(call) => WireProgressPayload {
            variant: WIRE_PROGRESS_FUNCTION_CALL,
            version: wire::WIRE_VERSION,
            script_name: script_name.to_owned(),
            is_repl,
            is_os_function: true,
            is_method_call: false,
            function_name: call.function.to_string(),
            args: call.args.iter().map(WireValue::from_monty).collect(),
            kwargs: call
                .kwargs
                .iter()
                .map(|(key, value)| wire::WirePair {
                    key: WireValue::from_monty(key),
                    value: WireValue::from_monty(value),
                })
                .collect(),
            call_id: call.call_id,
            ..WireProgressPayload::default()
        },
        RunProgress::NameLookup(lookup) => WireProgressPayload {
            variant: WIRE_PROGRESS_NAME_LOOKUP,
            version: wire::WIRE_VERSION,
            script_name: script_name.to_owned(),
            is_repl,
            variable_name: lookup.name.clone(),
            ..WireProgressPayload::default()
        },
        RunProgress::ResolveFutures(state) => WireProgressPayload {
            variant: WIRE_PROGRESS_FUTURE,
            version: wire::WIRE_VERSION,
            script_name: script_name.to_owned(),
            is_repl,
            pending_call_ids: state.pending_call_ids().to_vec(),
            ..WireProgressPayload::default()
        },
        RunProgress::Complete(output) => WireProgressPayload {
            variant: WIRE_PROGRESS_COMPLETE,
            version: wire::WIRE_VERSION,
            script_name: script_name.to_owned(),
            is_repl,
            output: Some(WireValue::from_monty(output)),
            ..WireProgressPayload::default()
        },
    }
}

fn describe_repl_progress<T: ResourceTracker>(
    progress: &ReplProgress<T>,
    script_name: &str,
    is_repl: bool,
) -> WireProgressPayload {
    match progress {
        ReplProgress::FunctionCall(call) => WireProgressPayload {
            variant: WIRE_PROGRESS_FUNCTION_CALL,
            version: wire::WIRE_VERSION,
            script_name: script_name.to_owned(),
            is_repl,
            is_os_function: false,
            is_method_call: call.method_call,
            function_name: call.function_name.clone(),
            args: call.args.iter().map(WireValue::from_monty).collect(),
            kwargs: call
                .kwargs
                .iter()
                .map(|(key, value)| wire::WirePair {
                    key: WireValue::from_monty(key),
                    value: WireValue::from_monty(value),
                })
                .collect(),
            call_id: call.call_id,
            ..WireProgressPayload::default()
        },
        ReplProgress::OsCall(call) => WireProgressPayload {
            variant: WIRE_PROGRESS_FUNCTION_CALL,
            version: wire::WIRE_VERSION,
            script_name: script_name.to_owned(),
            is_repl,
            is_os_function: true,
            is_method_call: false,
            function_name: call.function.to_string(),
            args: call.args.iter().map(WireValue::from_monty).collect(),
            kwargs: call
                .kwargs
                .iter()
                .map(|(key, value)| wire::WirePair {
                    key: WireValue::from_monty(key),
                    value: WireValue::from_monty(value),
                })
                .collect(),
            call_id: call.call_id,
            ..WireProgressPayload::default()
        },
        ReplProgress::NameLookup(lookup) => WireProgressPayload {
            variant: WIRE_PROGRESS_NAME_LOOKUP,
            version: wire::WIRE_VERSION,
            script_name: script_name.to_owned(),
            is_repl,
            variable_name: lookup.name.clone(),
            ..WireProgressPayload::default()
        },
        ReplProgress::ResolveFutures(state) => WireProgressPayload {
            variant: WIRE_PROGRESS_FUTURE,
            version: wire::WIRE_VERSION,
            script_name: script_name.to_owned(),
            is_repl,
            pending_call_ids: state.pending_call_ids().to_vec(),
            ..WireProgressPayload::default()
        },
        ReplProgress::Complete { value, .. } => WireProgressPayload {
            variant: WIRE_PROGRESS_COMPLETE,
            version: wire::WIRE_VERSION,
            script_name: script_name.to_owned(),
            is_repl,
            output: Some(WireValue::from_monty(value)),
            ..WireProgressPayload::default()
        },
    }
}

fn extract_inputs(
    input_names: &[String],
    inputs: BTreeMap<String, WireValue>,
) -> Result<Vec<MontyObject>, FfiError> {
    input_names
        .iter()
        .map(|name| {
            let value = inputs
                .get(name)
                .cloned()
                .ok_or_else(|| FfiError::Api(format!("missing required input '{name}'")))?;
            value.into_monty().map_err(FfiError::Api)
        })
        .collect()
}

fn extract_feed_inputs(
    inputs: BTreeMap<String, WireValue>,
) -> Result<Vec<(String, MontyObject)>, FfiError> {
    inputs
        .into_iter()
        .map(|(name, value)| {
            value
                .into_monty()
                .map(|value| (name, value))
                .map_err(FfiError::Api)
        })
        .collect()
}

fn decode_wire<T: serde::de::DeserializeOwned>(bytes: &[u8]) -> Result<T, FfiError> {
    rmp_serde::from_slice(bytes).map_err(|e| FfiError::Api(format!("invalid wire payload: {e}")))
}

fn encode_wire<T: serde::Serialize>(value: &T) -> Result<MontyGoBytes, FfiError> {
    rmp_serde::to_vec_named(value)
        .map(MontyGoBytes::from_vec)
        .map_err(|e| FfiError::Api(format!("failed to encode wire payload: {e}")))
}

unsafe fn slice_from_raw<'a>(ptr: *const u8, len: usize) -> &'a [u8] {
    if ptr.is_null() || len == 0 {
        &[]
    } else {
        // SAFETY: caller guarantees ptr/len point to a valid byte buffer for the duration
        unsafe { std::slice::from_raw_parts(ptr, len) }
    }
}

unsafe fn string_from_cstr<'a>(ptr: *const c_char) -> Result<&'a str, FfiError> {
    if ptr.is_null() {
        return Err(FfiError::Api("expected non-null C string".to_owned()));
    }
    // SAFETY: caller guarantees ptr is NUL-terminated
    unsafe { CStr::from_ptr(ptr) }
        .to_str()
        .map_err(|e| FfiError::Api(format!("invalid UTF-8 string: {e}")))
}

fn decode_utf8<'a>(bytes: &'a [u8], field_name: &str) -> Result<&'a str, FfiError> {
    std::str::from_utf8(bytes)
        .map_err(|error| FfiError::Api(format!("{field_name} is not valid UTF-8: {error}")))
}

fn decode_call_result(result: WireCallResult) -> Result<ExtFunctionResult, FfiError> {
    match result.kind {
        WIRE_CALL_RESULT_RETURN => result
            .value
            .ok_or_else(|| FfiError::Api("missing return value".to_owned()))?
            .into_monty()
            .map(ExtFunctionResult::Return)
            .map_err(FfiError::Api),
        WIRE_CALL_RESULT_EXCEPTION => Ok(ExtFunctionResult::Error(MontyException::new(
            parse_exc_type(&result.exc_type)?,
            result.arg,
        ))),
        WIRE_CALL_RESULT_PENDING => Ok(ExtFunctionResult::Future(0)),
        other => Err(FfiError::Api(format!("unknown call result kind: {other}"))),
    }
}

fn decode_lookup_result(result: WireLookupResult) -> Result<NameLookupResult, FfiError> {
    match result.kind {
        WIRE_LOOKUP_RESULT_VALUE => result
            .value
            .ok_or_else(|| FfiError::Api("missing lookup value".to_owned()))?
            .into_monty()
            .map(NameLookupResult::Value)
            .map_err(FfiError::Api),
        WIRE_LOOKUP_RESULT_UNDEFINED => Ok(NameLookupResult::Undefined),
        other => Err(FfiError::Api(format!(
            "unknown lookup result kind: {other}"
        ))),
    }
}

fn decode_future_results(
    results: WireFutureResults,
) -> Result<Vec<(u32, ExtFunctionResult)>, FfiError> {
    results
        .results
        .into_iter()
        .map(|(call_id, result)| {
            let result = match decode_call_result(result)? {
                ExtFunctionResult::Future(_) => ExtFunctionResult::Future(call_id),
                other => other,
            };
            Ok((call_id, result))
        })
        .collect()
}

fn parse_exc_type(exc_type: &str) -> Result<ExcType, FfiError> {
    exc_type
        .parse()
        .map_err(|_| FfiError::Api(format!("unknown exception type: {exc_type}")))
}

fn wrap_repl_handle(script_name: &str, inner: StoredRepl) -> MontyGoRepl {
    MontyGoRepl {
        script_name: script_name.to_owned(),
        inner: Some(inner),
    }
}

fn start_runner_internal(
    handle: &MontyGoRunner,
    options: WireStartOptions,
) -> Result<(StoredProgress, String), (FfiError, String)> {
    let inputs = extract_inputs(&handle.input_names, options.inputs)
        .map_err(|error| (error, String::new()))?;
    let mut prints = PrintCollector::new();

    let start_result = if let Some(limits) = options.limits {
        handle
            .runner
            .clone()
            .start(
                inputs,
                LimitedTracker::new(limits.into()),
                PrintWriter::Callback(&mut prints),
            )
            .map(|progress| StoredProgress::RunLimited {
                progress,
                script_name: handle.script_name.clone(),
            })
    } else {
        handle
            .runner
            .clone()
            .start(inputs, NoLimitTracker, PrintWriter::Callback(&mut prints))
            .map(|progress| StoredProgress::RunNoLimit {
                progress,
                script_name: handle.script_name.clone(),
            })
    };

    match start_result {
        Ok(progress) => Ok((progress, prints.into_string())),
        Err(error) => Err((FfiError::Exception(error), prints.into_string())),
    }
}

fn feed_start_internal(
    repl_handle: &mut MontyGoRepl,
    code: &[u8],
    options: WireFeedOptions,
) -> Result<(StoredProgress, String), (FfiError, MontyGoRepl, String)> {
    let Some(repl) = repl_handle.inner.take() else {
        return Err((
            FfiError::Api("repl handle is not available for execution".to_owned()),
            MontyGoRepl {
                script_name: repl_handle.script_name.clone(),
                inner: None,
            },
            String::new(),
        ));
    };

    let code = match decode_utf8(code, "code") {
        Ok(code) => code,
        Err(error) => {
            return Err((
                error,
                wrap_repl_handle(&repl_handle.script_name, repl),
                String::new(),
            ));
        }
    };

    let inputs = match extract_feed_inputs(options.inputs) {
        Ok(inputs) => inputs,
        Err(error) => {
            return Err((
                error,
                wrap_repl_handle(&repl_handle.script_name, repl),
                String::new(),
            ));
        }
    };

    let mut prints = PrintCollector::new();
    let script_name = repl_handle.script_name.clone();

    match repl {
        StoredRepl::NoLimit(repl) => {
            match repl.feed_start(code, inputs, PrintWriter::Callback(&mut prints)) {
                Ok(progress) => Ok((
                    StoredProgress::ReplNoLimit {
                        progress,
                        script_name,
                    },
                    prints.into_string(),
                )),
                Err(error) => Err((
                    FfiError::Exception(error.error),
                    wrap_repl_handle(&script_name, StoredRepl::NoLimit(error.repl)),
                    prints.into_string(),
                )),
            }
        }
        StoredRepl::Limited(repl) => {
            match repl.feed_start(code, inputs, PrintWriter::Callback(&mut prints)) {
                Ok(progress) => Ok((
                    StoredProgress::ReplLimited {
                        progress,
                        script_name,
                    },
                    prints.into_string(),
                )),
                Err(error) => Err((
                    FfiError::Exception(error.error),
                    wrap_repl_handle(&script_name, StoredRepl::Limited(error.repl)),
                    prints.into_string(),
                )),
            }
        }
    }
}

fn resume_call_internal(
    progress: StoredProgress,
    ext_result: ExtFunctionResult,
) -> Result<(StoredProgress, String), (FfiError, Option<MontyGoRepl>, String)> {
    let mut prints = PrintCollector::new();
    match progress {
        StoredProgress::RunNoLimit {
            progress: RunProgress::FunctionCall(call),
            script_name,
        } => {
            let call_id = call.call_id;
            match call.resume(
                adjust_pending_result(ext_result, call_id),
                PrintWriter::Callback(&mut prints),
            ) {
                Ok(progress) => Ok((
                    StoredProgress::RunNoLimit {
                        progress,
                        script_name,
                    },
                    prints.into_string(),
                )),
                Err(error) => Err((FfiError::Exception(error), None, prints.into_string())),
            }
        }
        StoredProgress::RunNoLimit {
            progress: RunProgress::OsCall(call),
            script_name,
        } => {
            let call_id = call.call_id;
            match call.resume(
                adjust_pending_result(ext_result, call_id),
                PrintWriter::Callback(&mut prints),
            ) {
                Ok(progress) => Ok((
                    StoredProgress::RunNoLimit {
                        progress,
                        script_name,
                    },
                    prints.into_string(),
                )),
                Err(error) => Err((FfiError::Exception(error), None, prints.into_string())),
            }
        }
        StoredProgress::RunLimited {
            progress: RunProgress::FunctionCall(call),
            script_name,
        } => {
            let call_id = call.call_id;
            match call.resume(
                adjust_pending_result(ext_result, call_id),
                PrintWriter::Callback(&mut prints),
            ) {
                Ok(progress) => Ok((
                    StoredProgress::RunLimited {
                        progress,
                        script_name,
                    },
                    prints.into_string(),
                )),
                Err(error) => Err((FfiError::Exception(error), None, prints.into_string())),
            }
        }
        StoredProgress::RunLimited {
            progress: RunProgress::OsCall(call),
            script_name,
        } => {
            let call_id = call.call_id;
            match call.resume(
                adjust_pending_result(ext_result, call_id),
                PrintWriter::Callback(&mut prints),
            ) {
                Ok(progress) => Ok((
                    StoredProgress::RunLimited {
                        progress,
                        script_name,
                    },
                    prints.into_string(),
                )),
                Err(error) => Err((FfiError::Exception(error), None, prints.into_string())),
            }
        }
        StoredProgress::ReplNoLimit {
            progress: ReplProgress::FunctionCall(call),
            script_name,
        } => {
            let call_id = call.call_id;
            match call.resume(
                adjust_pending_result(ext_result, call_id),
                PrintWriter::Callback(&mut prints),
            ) {
                Ok(progress) => Ok((
                    StoredProgress::ReplNoLimit {
                        progress,
                        script_name,
                    },
                    prints.into_string(),
                )),
                Err(error) => Err((
                    FfiError::Exception(error.error),
                    Some(MontyGoRepl {
                        script_name,
                        inner: Some(StoredRepl::NoLimit(error.repl)),
                    }),
                    prints.into_string(),
                )),
            }
        }
        StoredProgress::ReplNoLimit {
            progress: ReplProgress::OsCall(call),
            script_name,
        } => {
            let call_id = call.call_id;
            match call.resume(
                adjust_pending_result(ext_result, call_id),
                PrintWriter::Callback(&mut prints),
            ) {
                Ok(progress) => Ok((
                    StoredProgress::ReplNoLimit {
                        progress,
                        script_name,
                    },
                    prints.into_string(),
                )),
                Err(error) => Err((
                    FfiError::Exception(error.error),
                    Some(MontyGoRepl {
                        script_name,
                        inner: Some(StoredRepl::NoLimit(error.repl)),
                    }),
                    prints.into_string(),
                )),
            }
        }
        StoredProgress::ReplLimited {
            progress: ReplProgress::FunctionCall(call),
            script_name,
        } => {
            let call_id = call.call_id;
            match call.resume(
                adjust_pending_result(ext_result, call_id),
                PrintWriter::Callback(&mut prints),
            ) {
                Ok(progress) => Ok((
                    StoredProgress::ReplLimited {
                        progress,
                        script_name,
                    },
                    prints.into_string(),
                )),
                Err(error) => Err((
                    FfiError::Exception(error.error),
                    Some(MontyGoRepl {
                        script_name,
                        inner: Some(StoredRepl::Limited(error.repl)),
                    }),
                    prints.into_string(),
                )),
            }
        }
        StoredProgress::ReplLimited {
            progress: ReplProgress::OsCall(call),
            script_name,
        } => {
            let call_id = call.call_id;
            match call.resume(
                adjust_pending_result(ext_result, call_id),
                PrintWriter::Callback(&mut prints),
            ) {
                Ok(progress) => Ok((
                    StoredProgress::ReplLimited {
                        progress,
                        script_name,
                    },
                    prints.into_string(),
                )),
                Err(error) => Err((
                    FfiError::Exception(error.error),
                    Some(MontyGoRepl {
                        script_name,
                        inner: Some(StoredRepl::Limited(error.repl)),
                    }),
                    prints.into_string(),
                )),
            }
        }
        _ => Err((
            FfiError::Api("progress is not a function or os-call snapshot".to_owned()),
            None,
            prints.into_string(),
        )),
    }
}

fn adjust_pending_result(result: ExtFunctionResult, call_id: u32) -> ExtFunctionResult {
    match result {
        ExtFunctionResult::Future(_) => ExtFunctionResult::Future(call_id),
        other => other,
    }
}

fn resume_lookup_internal(
    progress: StoredProgress,
    lookup_result: NameLookupResult,
) -> Result<(StoredProgress, String), (FfiError, Option<MontyGoRepl>, String)> {
    let mut prints = PrintCollector::new();
    match progress {
        StoredProgress::RunNoLimit {
            progress: RunProgress::NameLookup(lookup),
            script_name,
        } => match lookup.resume(lookup_result, PrintWriter::Callback(&mut prints)) {
            Ok(progress) => Ok((
                StoredProgress::RunNoLimit {
                    progress,
                    script_name,
                },
                prints.into_string(),
            )),
            Err(error) => Err((FfiError::Exception(error), None, prints.into_string())),
        },
        StoredProgress::RunLimited {
            progress: RunProgress::NameLookup(lookup),
            script_name,
        } => match lookup.resume(lookup_result, PrintWriter::Callback(&mut prints)) {
            Ok(progress) => Ok((
                StoredProgress::RunLimited {
                    progress,
                    script_name,
                },
                prints.into_string(),
            )),
            Err(error) => Err((FfiError::Exception(error), None, prints.into_string())),
        },
        StoredProgress::ReplNoLimit {
            progress: ReplProgress::NameLookup(lookup),
            script_name,
        } => match lookup.resume(lookup_result, PrintWriter::Callback(&mut prints)) {
            Ok(progress) => Ok((
                StoredProgress::ReplNoLimit {
                    progress,
                    script_name,
                },
                prints.into_string(),
            )),
            Err(error) => Err((
                FfiError::Exception(error.error),
                Some(MontyGoRepl {
                    script_name,
                    inner: Some(StoredRepl::NoLimit(error.repl)),
                }),
                prints.into_string(),
            )),
        },
        StoredProgress::ReplLimited {
            progress: ReplProgress::NameLookup(lookup),
            script_name,
        } => match lookup.resume(lookup_result, PrintWriter::Callback(&mut prints)) {
            Ok(progress) => Ok((
                StoredProgress::ReplLimited {
                    progress,
                    script_name,
                },
                prints.into_string(),
            )),
            Err(error) => Err((
                FfiError::Exception(error.error),
                Some(MontyGoRepl {
                    script_name,
                    inner: Some(StoredRepl::Limited(error.repl)),
                }),
                prints.into_string(),
            )),
        },
        _ => Err((
            FfiError::Api("progress is not a name-lookup snapshot".to_owned()),
            None,
            prints.into_string(),
        )),
    }
}

fn resume_futures_internal(
    progress: StoredProgress,
    decoded: Vec<(u32, ExtFunctionResult)>,
) -> Result<(StoredProgress, String), (FfiError, Option<MontyGoRepl>, String)> {
    let mut prints = PrintCollector::new();
    match progress {
        StoredProgress::RunNoLimit {
            progress: RunProgress::ResolveFutures(state),
            script_name,
        } => match state.resume(decoded, PrintWriter::Callback(&mut prints)) {
            Ok(progress) => Ok((
                StoredProgress::RunNoLimit {
                    progress,
                    script_name,
                },
                prints.into_string(),
            )),
            Err(error) => Err((FfiError::Exception(error), None, prints.into_string())),
        },
        StoredProgress::RunLimited {
            progress: RunProgress::ResolveFutures(state),
            script_name,
        } => match state.resume(decoded, PrintWriter::Callback(&mut prints)) {
            Ok(progress) => Ok((
                StoredProgress::RunLimited {
                    progress,
                    script_name,
                },
                prints.into_string(),
            )),
            Err(error) => Err((FfiError::Exception(error), None, prints.into_string())),
        },
        StoredProgress::ReplNoLimit {
            progress: ReplProgress::ResolveFutures(state),
            script_name,
        } => match state.resume(decoded, PrintWriter::Callback(&mut prints)) {
            Ok(progress) => Ok((
                StoredProgress::ReplNoLimit {
                    progress,
                    script_name,
                },
                prints.into_string(),
            )),
            Err(error) => Err((
                FfiError::Exception(error.error),
                Some(MontyGoRepl {
                    script_name,
                    inner: Some(StoredRepl::NoLimit(error.repl)),
                }),
                prints.into_string(),
            )),
        },
        StoredProgress::ReplLimited {
            progress: ReplProgress::ResolveFutures(state),
            script_name,
        } => match state.resume(decoded, PrintWriter::Callback(&mut prints)) {
            Ok(progress) => Ok((
                StoredProgress::ReplLimited {
                    progress,
                    script_name,
                },
                prints.into_string(),
            )),
            Err(error) => Err((
                FfiError::Exception(error.error),
                Some(MontyGoRepl {
                    script_name,
                    inner: Some(StoredRepl::Limited(error.repl)),
                }),
                prints.into_string(),
            )),
        },
        _ => Err((
            FfiError::Api("progress is not a future snapshot".to_owned()),
            None,
            prints.into_string(),
        )),
    }
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_bytes_free(bytes: MontyGoBytes) {
    if !bytes.ptr.is_null() && bytes.len > 0 {
        // SAFETY: ptr/len were allocated by `MontyGoBytes::from_vec`
        unsafe {
            let _ = Vec::from_raw_parts(bytes.ptr, bytes.len, bytes.len);
        }
    }
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_runner_free(runner: *mut MontyGoRunner) {
    if !runner.is_null() {
        // SAFETY: pointer was created by `Box::into_raw`
        unsafe { drop(Box::from_raw(runner)) };
    }
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_repl_free(repl: *mut MontyGoRepl) {
    if !repl.is_null() {
        // SAFETY: pointer was created by `Box::into_raw`
        unsafe { drop(Box::from_raw(repl)) };
    }
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_progress_free(progress: *mut MontyGoProgress) {
    if !progress.is_null() {
        // SAFETY: pointer was created by `Box::into_raw`
        unsafe { drop(Box::from_raw(progress)) };
    }
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_error_free(error: *mut MontyGoError) {
    if !error.is_null() {
        // SAFETY: pointer was created by `Box::into_raw`
        unsafe { drop(Box::from_raw(error)) };
    }
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_error_json(error: *const MontyGoError) -> MontyGoBytes {
    if error.is_null() {
        return MontyGoBytes::empty();
    }
    // SAFETY: caller passes a valid handle pointer
    let error = unsafe { &*error };
    serde_json::to_vec(&error.inner.summary())
        .map(MontyGoBytes::from_vec)
        .unwrap_or_else(|_| MontyGoBytes::empty())
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_error_display(
    error: *const MontyGoError,
    format: *const c_char,
    color: bool,
) -> MontyGoBytes {
    if error.is_null() {
        return MontyGoBytes::empty();
    }
    // SAFETY: caller passes a valid handle pointer
    let error = unsafe { &*error };
    let format = unsafe { string_from_cstr(format) }.unwrap_or("traceback");
    MontyGoBytes::from_vec(
        error
            .inner
            .display(format, color)
            .unwrap_or_else(|display_error| display_error)
            .into_bytes(),
    )
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_runner_new(
    code_ptr: *const u8,
    code_len: usize,
    options_ptr: *const u8,
    options_len: usize,
) -> MontyGoRunnerResult {
    catch_runner_result(|| {
        let code = unsafe { slice_from_raw(code_ptr, code_len) };
        let options = unsafe { slice_from_raw(options_ptr, options_len) };

        let code = match std::str::from_utf8(code) {
            Ok(code) => code.to_owned(),
            Err(error) => {
                return MontyGoRunnerResult::err(FfiError::Api(format!(
                    "code is not valid UTF-8: {error}"
                )));
            }
        };
        let options: WireCompileOptions = match decode_wire(options) {
            Ok(options) => options,
            Err(error) => return MontyGoRunnerResult::err(error),
        };

        let script_name = options.script_name.unwrap_or_else(|| "main.py".to_owned());
        let input_names = options.inputs.unwrap_or_default();

        if options.type_check {
            let source = SourceFile::new(&code, &script_name);
            let prefix = options
                .type_check_stubs
                .as_deref()
                .map(|stubs| SourceFile::new(stubs, "type_stubs.py"));
            match type_check(&source, prefix.as_ref()) {
                Ok(Some(error)) => return MontyGoRunnerResult::err(FfiError::Typing(error)),
                Ok(None) => {}
                Err(error) => return MontyGoRunnerResult::err(FfiError::Api(error)),
            }
        }

        match MontyRun::new(code, &script_name, input_names.clone()) {
            Ok(runner) => MontyGoRunnerResult::ok(MontyGoRunner {
                runner,
                script_name,
                input_names,
            }),
            Err(error) => MontyGoRunnerResult::err(FfiError::Exception(error)),
        }
    })
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_runner_load(
    data_ptr: *const u8,
    data_len: usize,
) -> MontyGoRunnerResult {
    catch_runner_result(|| {
        let data = unsafe { slice_from_raw(data_ptr, data_len) };
        #[derive(serde::Deserialize)]
        struct StoredRunner {
            runner: MontyRun,
            script_name: String,
            input_names: Vec<String>,
        }

        match postcard::from_bytes::<StoredRunner>(data) {
            Ok(stored) => MontyGoRunnerResult::ok(MontyGoRunner {
                runner: stored.runner,
                script_name: stored.script_name,
                input_names: stored.input_names,
            }),
            Err(error) => MontyGoRunnerResult::err(FfiError::Api(error.to_string())),
        }
    })
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_runner_dump(
    runner: *const MontyGoRunner,
    error_out: *mut *mut MontyGoError,
) -> MontyGoBytes {
    if !error_out.is_null() {
        // SAFETY: caller owns the out pointer
        unsafe { *error_out = ptr::null_mut() };
    }
    catch_bytes_result(error_out, || {
        if runner.is_null() {
            if !error_out.is_null() {
                unsafe {
                    *error_out = Box::into_raw(Box::new(MontyGoError {
                        inner: FfiError::Api("runner handle is null".to_owned()),
                    }))
                };
            }
            return MontyGoBytes::empty();
        }
        // SAFETY: caller passes a valid handle pointer
        let runner = unsafe { &*runner };

        #[derive(serde::Serialize)]
        struct StoredRunner<'a> {
            runner: &'a MontyRun,
            script_name: &'a str,
            input_names: &'a [String],
        }

        match postcard::to_allocvec(&StoredRunner {
            runner: &runner.runner,
            script_name: &runner.script_name,
            input_names: &runner.input_names,
        }) {
            Ok(bytes) => MontyGoBytes::from_vec(bytes),
            Err(error) => {
                if !error_out.is_null() {
                    unsafe {
                        *error_out = Box::into_raw(Box::new(MontyGoError {
                            inner: FfiError::Api(error.to_string()),
                        }))
                    };
                }
                MontyGoBytes::empty()
            }
        }
    })
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_runner_type_check(
    runner: *const MontyGoRunner,
    prefix_ptr: *const u8,
    prefix_len: usize,
) -> *mut MontyGoError {
    catch_error_result(|| {
        if runner.is_null() {
            return Box::into_raw(Box::new(MontyGoError {
                inner: FfiError::Api("runner handle is null".to_owned()),
            }));
        }
        // SAFETY: caller passes a valid handle pointer
        let runner = unsafe { &*runner };
        let prefix = unsafe { slice_from_raw(prefix_ptr, prefix_len) };
        let prefix = if prefix.is_empty() {
            None
        } else {
            match std::str::from_utf8(prefix) {
                Ok(prefix) => Some(prefix.to_owned()),
                Err(error) => {
                    return Box::into_raw(Box::new(MontyGoError {
                        inner: FfiError::Api(format!("prefix is not valid UTF-8: {error}")),
                    }));
                }
            }
        };

        let source = SourceFile::new(runner.runner.code(), &runner.script_name);
        let prefix = prefix
            .as_deref()
            .map(|prefix| SourceFile::new(prefix, "type_stubs.py"));
        match type_check(&source, prefix.as_ref()) {
            Ok(None) => ptr::null_mut(),
            Ok(Some(error)) => Box::into_raw(Box::new(MontyGoError {
                inner: FfiError::Typing(error),
            })),
            Err(error) => Box::into_raw(Box::new(MontyGoError {
                inner: FfiError::Api(error),
            })),
        }
    })
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_runner_start(
    runner: *const MontyGoRunner,
    options_ptr: *const u8,
    options_len: usize,
) -> MontyGoOpResult {
    catch_op_result(|| {
        if runner.is_null() {
            return MontyGoOpResult::err(
                FfiError::Api("runner handle is null".to_owned()),
                None,
                String::new(),
            );
        }
        // SAFETY: caller passes a valid handle pointer
        let runner = unsafe { &*runner };
        let options = unsafe { slice_from_raw(options_ptr, options_len) };
        let options: WireStartOptions = match decode_wire(options) {
            Ok(options) => options,
            Err(error) => return MontyGoOpResult::err(error, None, String::new()),
        };
        match start_runner_internal(runner, options) {
            Ok((progress, prints)) => MontyGoOpResult::ok(progress, prints),
            Err((error, prints)) => MontyGoOpResult::err(error, None, prints),
        }
    })
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_repl_new(
    options_ptr: *const u8,
    options_len: usize,
) -> MontyGoReplResult {
    catch_repl_result(|| {
        let options = unsafe { slice_from_raw(options_ptr, options_len) };
        let options: WireReplOptions = match decode_wire(options) {
            Ok(options) => options,
            Err(error) => return MontyGoReplResult::err(error),
        };
        let script_name = options.script_name.unwrap_or_else(|| "main.py".to_owned());
        let inner = if let Some(limits) = options.limits {
            StoredRepl::Limited(MontyRepl::new(
                &script_name,
                LimitedTracker::new(limits.into()),
            ))
        } else {
            StoredRepl::NoLimit(MontyRepl::new(&script_name, NoLimitTracker))
        };

        MontyGoReplResult::ok(MontyGoRepl {
            script_name,
            inner: Some(inner),
        })
    })
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_repl_load(data_ptr: *const u8, data_len: usize) -> MontyGoReplResult {
    catch_repl_result(|| {
        let data = unsafe { slice_from_raw(data_ptr, data_len) };
        #[derive(serde::Deserialize)]
        struct StoredLoadedRepl {
            script_name: String,
            repl: StoredRepl,
        }

        match postcard::from_bytes::<StoredLoadedRepl>(data) {
            Ok(stored) => MontyGoReplResult::ok(MontyGoRepl {
                script_name: stored.script_name,
                inner: Some(stored.repl),
            }),
            Err(error) => MontyGoReplResult::err(FfiError::Api(error.to_string())),
        }
    })
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_repl_dump(
    repl: *const MontyGoRepl,
    error_out: *mut *mut MontyGoError,
) -> MontyGoBytes {
    if !error_out.is_null() {
        unsafe { *error_out = ptr::null_mut() };
    }
    catch_bytes_result(error_out, || {
        if repl.is_null() {
            if !error_out.is_null() {
                unsafe {
                    *error_out = Box::into_raw(Box::new(MontyGoError {
                        inner: FfiError::Api("repl handle is null".to_owned()),
                    }))
                };
            }
            return MontyGoBytes::empty();
        }
        let repl = unsafe { &*repl };
        let Some(inner) = &repl.inner else {
            if !error_out.is_null() {
                unsafe {
                    *error_out = Box::into_raw(Box::new(MontyGoError {
                        inner: FfiError::Api("repl handle is not currently available".to_owned()),
                    }))
                };
            }
            return MontyGoBytes::empty();
        };
        #[derive(serde::Serialize)]
        struct StoredReplRef<'a> {
            script_name: &'a str,
            repl: &'a StoredRepl,
        }
        match postcard::to_allocvec(&StoredReplRef {
            script_name: &repl.script_name,
            repl: inner,
        }) {
            Ok(bytes) => MontyGoBytes::from_vec(bytes),
            Err(error) => {
                if !error_out.is_null() {
                    unsafe {
                        *error_out = Box::into_raw(Box::new(MontyGoError {
                            inner: FfiError::Api(error.to_string()),
                        }))
                    };
                }
                MontyGoBytes::empty()
            }
        }
    })
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_repl_feed_start(
    repl: *mut MontyGoRepl,
    code_ptr: *const u8,
    code_len: usize,
    options_ptr: *const u8,
    options_len: usize,
) -> MontyGoOpResult {
    catch_op_result(|| {
        if repl.is_null() {
            return MontyGoOpResult::err(
                FfiError::Api("repl handle is null".to_owned()),
                None,
                String::new(),
            );
        }
        let repl = unsafe { &mut *repl };
        let code = unsafe { slice_from_raw(code_ptr, code_len) };
        let options = unsafe { slice_from_raw(options_ptr, options_len) };
        let options: WireFeedOptions = match decode_wire(options) {
            Ok(options) => options,
            Err(error) => return MontyGoOpResult::err(error, None, String::new()),
        };
        match feed_start_internal(repl, code, options) {
            Ok((progress, prints)) => MontyGoOpResult::ok(progress, prints),
            Err((error, repl, prints)) => MontyGoOpResult::err(error, Some(repl), prints),
        }
    })
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_progress_describe(
    progress: *const MontyGoProgress,
    error_out: *mut *mut MontyGoError,
) -> MontyGoBytes {
    if !error_out.is_null() {
        unsafe { *error_out = ptr::null_mut() };
    }
    catch_bytes_result(error_out, || {
        if progress.is_null() {
            if !error_out.is_null() {
                unsafe {
                    *error_out = Box::into_raw(Box::new(MontyGoError {
                        inner: FfiError::Api("progress handle is null".to_owned()),
                    }))
                };
            }
            return MontyGoBytes::empty();
        }
        let progress = unsafe { &*progress };
        let Some(inner) = &progress.inner else {
            if !error_out.is_null() {
                unsafe {
                    *error_out = Box::into_raw(Box::new(MontyGoError {
                        inner: FfiError::Api("progress handle is no longer available".to_owned()),
                    }))
                };
            }
            return MontyGoBytes::empty();
        };
        match encode_wire(&inner.describe()) {
            Ok(bytes) => bytes,
            Err(error) => {
                if !error_out.is_null() {
                    unsafe { *error_out = Box::into_raw(Box::new(MontyGoError { inner: error })) };
                }
                MontyGoBytes::empty()
            }
        }
    })
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_progress_dump(
    progress: *const MontyGoProgress,
    error_out: *mut *mut MontyGoError,
) -> MontyGoBytes {
    if !error_out.is_null() {
        unsafe { *error_out = ptr::null_mut() };
    }
    catch_bytes_result(error_out, || {
        if progress.is_null() {
            if !error_out.is_null() {
                unsafe {
                    *error_out = Box::into_raw(Box::new(MontyGoError {
                        inner: FfiError::Api("progress handle is null".to_owned()),
                    }))
                };
            }
            return MontyGoBytes::empty();
        }
        let progress = unsafe { &*progress };
        let Some(inner) = &progress.inner else {
            if !error_out.is_null() {
                unsafe {
                    *error_out = Box::into_raw(Box::new(MontyGoError {
                        inner: FfiError::Api("progress handle is no longer available".to_owned()),
                    }))
                };
            }
            return MontyGoBytes::empty();
        };
        match inner.dump() {
            Ok(bytes) => MontyGoBytes::from_vec(bytes),
            Err(error) => {
                if !error_out.is_null() {
                    unsafe { *error_out = Box::into_raw(Box::new(MontyGoError { inner: error })) };
                }
                MontyGoBytes::empty()
            }
        }
    })
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_progress_load(data_ptr: *const u8, data_len: usize) -> MontyGoOpResult {
    catch_op_result(|| {
        let data = unsafe { slice_from_raw(data_ptr, data_len) };
        match postcard::from_bytes::<StoredProgress>(data) {
            Ok(progress) => MontyGoOpResult::ok(progress, String::new()),
            Err(error) => {
                MontyGoOpResult::err(FfiError::Api(error.to_string()), None, String::new())
            }
        }
    })
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_progress_take_repl(progress: *mut MontyGoProgress) -> MontyGoReplResult {
    catch_repl_result(|| {
        if progress.is_null() {
            return MontyGoReplResult::err(FfiError::Api("progress handle is null".to_owned()));
        }
        let progress = unsafe { &mut *progress };
        let Some(inner) = progress.inner.take() else {
            return MontyGoReplResult::err(FfiError::Api(
                "progress handle is no longer available".to_owned(),
            ));
        };
        if !matches!(
            &inner,
            StoredProgress::ReplNoLimit { .. } | StoredProgress::ReplLimited { .. }
        ) {
            progress.inner = Some(inner);
            return MontyGoReplResult::err(FfiError::Api(
                "progress handle does not own a REPL session".to_owned(),
            ));
        }
        match inner.into_repl() {
            Ok(repl) => MontyGoReplResult::ok(repl),
            Err(error) => MontyGoReplResult::err(error),
        }
    })
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_progress_resume_call(
    progress: *mut MontyGoProgress,
    result_ptr: *const u8,
    result_len: usize,
) -> MontyGoOpResult {
    catch_op_result(|| {
        if progress.is_null() {
            return MontyGoOpResult::err(
                FfiError::Api("progress handle is null".to_owned()),
                None,
                String::new(),
            );
        }
        let progress = unsafe { &mut *progress };
        let result = unsafe { slice_from_raw(result_ptr, result_len) };
        let result: WireCallResult = match decode_wire(result) {
            Ok(result) => result,
            Err(error) => return MontyGoOpResult::err(error, None, String::new()),
        };
        let result = match decode_call_result(result) {
            Ok(result) => result,
            Err(error) => return MontyGoOpResult::err(error, None, String::new()),
        };
        if !matches!(
            progress.inner.as_ref(),
            Some(
                StoredProgress::RunNoLimit {
                    progress: RunProgress::FunctionCall(_) | RunProgress::OsCall(_),
                    ..
                } | StoredProgress::RunLimited {
                    progress: RunProgress::FunctionCall(_) | RunProgress::OsCall(_),
                    ..
                } | StoredProgress::ReplNoLimit {
                    progress: ReplProgress::FunctionCall(_) | ReplProgress::OsCall(_),
                    ..
                } | StoredProgress::ReplLimited {
                    progress: ReplProgress::FunctionCall(_) | ReplProgress::OsCall(_),
                    ..
                }
            )
        ) {
            return MontyGoOpResult::err(
                FfiError::Api("progress is not a function or os-call snapshot".to_owned()),
                None,
                String::new(),
            );
        }
        let Some(inner) = progress.inner.take() else {
            return MontyGoOpResult::err(
                FfiError::Api("progress handle is no longer available".to_owned()),
                None,
                String::new(),
            );
        };
        match resume_call_internal(inner, result) {
            Ok((progress, prints)) => MontyGoOpResult::ok(progress, prints),
            Err((error, repl, prints)) => MontyGoOpResult::err(error, repl, prints),
        }
    })
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_progress_resume_lookup(
    progress: *mut MontyGoProgress,
    result_ptr: *const u8,
    result_len: usize,
) -> MontyGoOpResult {
    catch_op_result(|| {
        if progress.is_null() {
            return MontyGoOpResult::err(
                FfiError::Api("progress handle is null".to_owned()),
                None,
                String::new(),
            );
        }
        let progress = unsafe { &mut *progress };
        let result = unsafe { slice_from_raw(result_ptr, result_len) };
        let result: WireLookupResult = match decode_wire(result) {
            Ok(result) => result,
            Err(error) => return MontyGoOpResult::err(error, None, String::new()),
        };
        let result = match decode_lookup_result(result) {
            Ok(result) => result,
            Err(error) => return MontyGoOpResult::err(error, None, String::new()),
        };
        if !matches!(
            progress.inner.as_ref(),
            Some(
                StoredProgress::RunNoLimit {
                    progress: RunProgress::NameLookup(_),
                    ..
                } | StoredProgress::RunLimited {
                    progress: RunProgress::NameLookup(_),
                    ..
                } | StoredProgress::ReplNoLimit {
                    progress: ReplProgress::NameLookup(_),
                    ..
                } | StoredProgress::ReplLimited {
                    progress: ReplProgress::NameLookup(_),
                    ..
                }
            )
        ) {
            return MontyGoOpResult::err(
                FfiError::Api("progress is not a name-lookup snapshot".to_owned()),
                None,
                String::new(),
            );
        }
        let Some(inner) = progress.inner.take() else {
            return MontyGoOpResult::err(
                FfiError::Api("progress handle is no longer available".to_owned()),
                None,
                String::new(),
            );
        };
        match resume_lookup_internal(inner, result) {
            Ok((progress, prints)) => MontyGoOpResult::ok(progress, prints),
            Err((error, repl, prints)) => MontyGoOpResult::err(error, repl, prints),
        }
    })
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_progress_resume_futures(
    progress: *mut MontyGoProgress,
    results_ptr: *const u8,
    results_len: usize,
) -> MontyGoOpResult {
    catch_op_result(|| {
        if progress.is_null() {
            return MontyGoOpResult::err(
                FfiError::Api("progress handle is null".to_owned()),
                None,
                String::new(),
            );
        }
        let progress = unsafe { &mut *progress };
        let results = unsafe { slice_from_raw(results_ptr, results_len) };
        let results: WireFutureResults = match decode_wire(results) {
            Ok(results) => results,
            Err(error) => return MontyGoOpResult::err(error, None, String::new()),
        };
        let results = match decode_future_results(results) {
            Ok(results) => results,
            Err(error) => return MontyGoOpResult::err(error, None, String::new()),
        };
        if !matches!(
            progress.inner.as_ref(),
            Some(
                StoredProgress::RunNoLimit {
                    progress: RunProgress::ResolveFutures(_),
                    ..
                } | StoredProgress::RunLimited {
                    progress: RunProgress::ResolveFutures(_),
                    ..
                } | StoredProgress::ReplNoLimit {
                    progress: ReplProgress::ResolveFutures(_),
                    ..
                } | StoredProgress::ReplLimited {
                    progress: ReplProgress::ResolveFutures(_),
                    ..
                }
            )
        ) {
            return MontyGoOpResult::err(
                FfiError::Api("progress is not a future snapshot".to_owned()),
                None,
                String::new(),
            );
        }
        let Some(inner) = progress.inner.take() else {
            return MontyGoOpResult::err(
                FfiError::Api("progress handle is no longer available".to_owned()),
                None,
                String::new(),
            );
        };
        match resume_futures_internal(inner, results) {
            Ok((progress, prints)) => MontyGoOpResult::ok(progress, prints),
            Err((error, repl, prints)) => MontyGoOpResult::err(error, repl, prints),
        }
    })
}
