//! C ABI for the cgo-free Go bindings to Monty.
//!
//! The shared library is only the host-side protocol bridge. Python code is
//! parsed here for the `New` compatibility contract, but it is always executed
//! by a version-matched `gomonty-worker` subprocess through `monty-pool`.

// Every exported function is a C ABI entrypoint whose foreign caller owns the
// raw-pointer validity contract. Keeping those symbols safe at the Rust type
// level matches cbindgen's C surface; each dereference remains in an explicit
// unsafe block below.
#![allow(clippy::not_unsafe_ptr_arg_deref)]

mod wire;

use std::{
    any::Any,
    collections::BTreeMap,
    ffi::{CStr, c_char},
    mem,
    panic::{AssertUnwindSafe, catch_unwind},
    path::PathBuf,
    ptr,
    sync::{Arc, OnceLock},
};

use monty::MontyRun;
use monty_pool::{Checkout, Pool, PoolConfig, PoolError, ReplConfig, ResumeValue, TurnEvent};
use monty_type_checking::{SourceFile, type_check};
use monty_types::{
    AssertMessageAnnotations, CompileOptions as MontyCompileOptions, ExcType, MontyException,
    MontyObject, PrintStream,
};
use wire::{
    WIRE_CALL_RESULT_EXCEPTION, WIRE_CALL_RESULT_PENDING, WIRE_CALL_RESULT_RETURN,
    WIRE_LOOKUP_RESULT_UNDEFINED, WIRE_LOOKUP_RESULT_VALUE, WIRE_PRINT_STDERR, WIRE_PRINT_STDOUT,
    WIRE_PROGRESS_COMPLETE, WIRE_PROGRESS_FUNCTION_CALL, WIRE_PROGRESS_FUTURE,
    WIRE_PROGRESS_NAME_LOOKUP, WireCallResult, WireCompileOptions, WireErrorSummary,
    WireFeedOptions, WireFutureResults, WireLookupResult, WirePrint, WireProgressPayload,
    WireReplOptions, WireStartOptions, WireValue,
};

const STATE_VERSION: u32 = 1;

static DEFAULT_POOL: OnceLock<Arc<Pool>> = OnceLock::new();

/// Opaque runner handle for Go.
pub struct MontyGoRunner {
    code: String,
    options: WireCompileOptions,
}

/// Opaque REPL handle for Go.
pub struct MontyGoRepl {
    inner: Option<ReplSession>,
}

/// Opaque in-flight progress handle for Go.
pub struct MontyGoProgress {
    inner: Option<StoredProgress>,
}

/// Opaque error handle for Go.
pub struct MontyGoError {
    inner: FfiError,
}

struct ReplSession {
    pool: Arc<Pool>,
    checkout: Checkout,
    options: WireReplOptions,
    script_name: String,
    /// Last known idle state. It is the rollback point for an in-flight feed
    /// and the recovery point when a worker dies.
    checkpoint: Vec<u8>,
}

enum StoredProgress {
    Active(ActiveProgress),
    Complete(CompleteProgress),
}

struct ActiveProgress {
    pool: Arc<Pool>,
    checkout: Checkout,
    event: TurnEvent,
    options: WireReplOptions,
    script_name: String,
    is_repl: bool,
    rollback: Option<Vec<u8>>,
}

struct CompleteProgress {
    output: MontyObject,
    script_name: String,
    is_repl: bool,
    repl: Option<ReplSession>,
}

type RecoveredRepl = Option<Box<MontyGoRepl>>;
type ProgressFailure = (FfiError, RecoveredRepl);
type OperationFailure = (FfiError, RecoveredRepl, Vec<WirePrint>);

#[derive(Debug)]
enum FfiError {
    Exception(MontyException),
    Typing(String),
    Api(String),
}

#[derive(serde::Serialize, serde::Deserialize)]
struct RunnerEnvelope {
    version: u32,
    code: String,
    options: WireCompileOptions,
}

#[derive(serde::Serialize, serde::Deserialize)]
struct ReplEnvelope {
    version: u32,
    state: Vec<u8>,
    options: WireReplOptions,
    script_name: String,
}

#[derive(serde::Serialize, serde::Deserialize)]
struct SnapshotEnvelope {
    version: u32,
    state: Vec<u8>,
    options: WireReplOptions,
    script_name: String,
    is_repl: bool,
    rollback: Option<Vec<u8>>,
}

/// Heap-allocated bytes returned through the C ABI.
#[repr(C)]
pub struct MontyGoBytes {
    pub ptr: *mut u8,
    pub len: usize,
}

/// Result of runner construction or loading.
#[repr(C)]
pub struct MontyGoRunnerResult {
    pub runner: *mut MontyGoRunner,
    pub error: *mut MontyGoError,
}

/// Result of REPL construction or loading.
#[repr(C)]
pub struct MontyGoReplResult {
    pub repl: *mut MontyGoRepl,
    pub error: *mut MontyGoError,
}

/// Result of start/resume/feed operations.
#[repr(C)]
pub struct MontyGoOpResult {
    pub progress: *mut MontyGoProgress,
    pub progress_payload: MontyGoBytes,
    pub error: *mut MontyGoError,
    pub repl: *mut MontyGoRepl,
    /// MessagePack encoded `Vec<WirePrint>`.
    pub prints: MontyGoBytes,
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
            error: error_ptr(error),
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
            error: error_ptr(error),
        }
    }
}

impl MontyGoOpResult {
    fn ok(progress: StoredProgress, prints: &[WirePrint]) -> Self {
        let progress_payload = match encode_wire(&progress.describe()) {
            Ok(payload) => payload,
            Err(error) => return Self::err(error, None, prints),
        };
        Self {
            progress: Box::into_raw(Box::new(MontyGoProgress {
                inner: Some(progress),
            })),
            progress_payload,
            error: ptr::null_mut(),
            repl: ptr::null_mut(),
            prints: encode_prints(prints),
        }
    }

    fn err(error: FfiError, repl: RecoveredRepl, prints: &[WirePrint]) -> Self {
        Self {
            progress: ptr::null_mut(),
            progress_payload: MontyGoBytes::empty(),
            error: error_ptr(error),
            repl: repl.map_or(ptr::null_mut(), Box::into_raw),
            prints: encode_prints(prints),
        }
    }
}

fn encode_prints(prints: &[WirePrint]) -> MontyGoBytes {
    rmp_serde::to_vec_named(prints).map_or_else(|_| MontyGoBytes::empty(), MontyGoBytes::from_vec)
}

fn error_ptr(error: FfiError) -> *mut MontyGoError {
    Box::into_raw(Box::new(MontyGoError { inner: error }))
}

fn panic_error(payload: &Box<dyn Any + Send>) -> FfiError {
    let message = if let Some(message) = payload.downcast_ref::<&str>() {
        (*message).to_owned()
    } else if let Some(message) = payload.downcast_ref::<String>() {
        message.clone()
    } else {
        "panic payload was not a string".to_owned()
    };
    FfiError::Api(format!("monty-go ffi panicked: {message}"))
}

fn catch_runner_result(f: impl FnOnce() -> MontyGoRunnerResult) -> MontyGoRunnerResult {
    catch_unwind(AssertUnwindSafe(f))
        .unwrap_or_else(|payload| MontyGoRunnerResult::err(panic_error(&payload)))
}

fn catch_repl_result(f: impl FnOnce() -> MontyGoReplResult) -> MontyGoReplResult {
    catch_unwind(AssertUnwindSafe(f))
        .unwrap_or_else(|payload| MontyGoReplResult::err(panic_error(&payload)))
}

fn catch_op_result(f: impl FnOnce() -> MontyGoOpResult) -> MontyGoOpResult {
    catch_unwind(AssertUnwindSafe(f))
        .unwrap_or_else(|payload| MontyGoOpResult::err(panic_error(&payload), None, &[]))
}

fn catch_error_result(f: impl FnOnce() -> *mut MontyGoError) -> *mut MontyGoError {
    catch_unwind(AssertUnwindSafe(f)).unwrap_or_else(|payload| error_ptr(panic_error(&payload)))
}

fn catch_bytes_result(
    error_out: *mut *mut MontyGoError,
    f: impl FnOnce() -> Result<Vec<u8>, FfiError>,
) -> MontyGoBytes {
    match catch_unwind(AssertUnwindSafe(f)) {
        Ok(Ok(bytes)) => MontyGoBytes::from_vec(bytes),
        Ok(Err(error)) => {
            set_error_out(error_out, error);
            MontyGoBytes::empty()
        }
        Err(payload) => {
            set_error_out(error_out, panic_error(&payload));
            MontyGoBytes::empty()
        }
    }
}

fn set_error_out(error_out: *mut *mut MontyGoError, error: FfiError) {
    if !error_out.is_null() {
        // SAFETY: the C ABI caller owns this out pointer.
        unsafe { *error_out = error_ptr(error) };
    }
}

impl FfiError {
    fn summary(&self) -> WireErrorSummary {
        match self {
            Self::Exception(error) => WireErrorSummary::from_exception(error),
            Self::Typing(message) => WireErrorSummary {
                version: wire::WIRE_VERSION,
                kind: "typing".to_owned(),
                type_name: "TypeError".to_owned(),
                message: message.clone(),
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

    fn display(&self, format: &str, _color: bool) -> Result<String, String> {
        if !matches!(format, "traceback" | "type-msg" | "msg") {
            return Err(format!(
                "invalid display format '{format}', expected 'traceback', 'type-msg', or 'msg'"
            ));
        }
        match self {
            Self::Exception(error) => match format {
                "traceback" => Ok(error.to_string()),
                "type-msg" => Ok(error.summary()),
                "msg" => Ok(error.message().unwrap_or_default().to_owned()),
                _ => unreachable!(),
            },
            Self::Typing(message) | Self::Api(message) => Ok(message.clone()),
        }
    }
}

impl From<PoolError> for FfiError {
    fn from(error: PoolError) -> Self {
        match error {
            PoolError::Runtime(error) => Self::Exception(error),
            PoolError::Typing(message) => Self::Typing(message),
            other => Self::Api(other.to_string()),
        }
    }
}

impl StoredProgress {
    fn describe(&self) -> WireProgressPayload {
        match self {
            Self::Active(active) => {
                describe_event(&active.event, &active.script_name, active.is_repl)
            }
            Self::Complete(complete) => WireProgressPayload {
                variant: WIRE_PROGRESS_COMPLETE,
                version: wire::WIRE_VERSION,
                script_name: complete.script_name.clone(),
                is_repl: complete.is_repl,
                output: Some(WireValue::from_monty(&complete.output)),
                ..WireProgressPayload::default()
            },
        }
    }

    fn into_repl(self) -> Result<MontyGoRepl, FfiError> {
        match self {
            Self::Complete(mut complete) if complete.is_repl => complete
                .repl
                .take()
                .map(|session| MontyGoRepl {
                    inner: Some(session),
                })
                .ok_or_else(|| {
                    FfiError::Api("completed progress no longer owns its REPL".to_owned())
                }),
            Self::Active(active) if active.is_repl => {
                let rollback = active.rollback.ok_or_else(|| {
                    FfiError::Api("REPL snapshot is missing its rollback checkpoint".to_owned())
                })?;
                restore_idle_session(active.pool, active.options, active.script_name, rollback)
                    .map(|session| MontyGoRepl {
                        inner: Some(session),
                    })
                    .map_err(FfiError::from)
            }
            Self::Active(_) | Self::Complete(_) => Err(FfiError::Api(
                "progress handle does not own a REPL session".to_owned(),
            )),
        }
    }
}

fn describe_event(event: &TurnEvent, script_name: &str, is_repl: bool) -> WireProgressPayload {
    match event {
        TurnEvent::FunctionCall {
            function_name,
            args,
            kwargs,
            call_id,
            method_call,
        } => WireProgressPayload {
            variant: WIRE_PROGRESS_FUNCTION_CALL,
            version: wire::WIRE_VERSION,
            script_name: script_name.to_owned(),
            is_repl,
            is_method_call: *method_call,
            function_name: function_name.clone(),
            args: args.iter().map(WireValue::from_monty).collect(),
            kwargs: wire_pairs(kwargs),
            call_id: *call_id,
            ..WireProgressPayload::default()
        },
        TurnEvent::OsCall {
            function_name,
            args,
            kwargs,
            call_id,
        } => WireProgressPayload {
            variant: WIRE_PROGRESS_FUNCTION_CALL,
            version: wire::WIRE_VERSION,
            script_name: script_name.to_owned(),
            is_repl,
            is_os_function: true,
            function_name: function_name.clone(),
            args: args.iter().map(WireValue::from_monty).collect(),
            kwargs: wire_pairs(kwargs),
            call_id: *call_id,
            ..WireProgressPayload::default()
        },
        TurnEvent::NameLookup { name } => WireProgressPayload {
            variant: WIRE_PROGRESS_NAME_LOOKUP,
            version: wire::WIRE_VERSION,
            script_name: script_name.to_owned(),
            is_repl,
            variable_name: name.clone(),
            ..WireProgressPayload::default()
        },
        TurnEvent::ResolveFutures { pending_call_ids } => WireProgressPayload {
            variant: WIRE_PROGRESS_FUTURE,
            version: wire::WIRE_VERSION,
            script_name: script_name.to_owned(),
            is_repl,
            pending_call_ids: pending_call_ids.clone(),
            ..WireProgressPayload::default()
        },
        TurnEvent::Complete(output) => WireProgressPayload {
            variant: WIRE_PROGRESS_COMPLETE,
            version: wire::WIRE_VERSION,
            script_name: script_name.to_owned(),
            is_repl,
            output: Some(WireValue::from_monty(output)),
            ..WireProgressPayload::default()
        },
    }
}

fn wire_pairs(items: &[(MontyObject, MontyObject)]) -> Vec<wire::WirePair> {
    items
        .iter()
        .map(|(key, value)| wire::WirePair {
            key: WireValue::from_monty(key),
            value: WireValue::from_monty(value),
        })
        .collect()
}

fn default_pool() -> Result<Arc<Pool>, FfiError> {
    DEFAULT_POOL
        .get()
        .cloned()
        .ok_or_else(|| FfiError::Api("Monty subprocess runtime is not initialized".to_owned()))
}

fn ensure_version(version: u32, what: &str) -> Result<(), FfiError> {
    if version == wire::WIRE_VERSION {
        Ok(())
    } else {
        Err(FfiError::Api(format!(
            "unsupported {what} wire version {version}; expected {}",
            wire::WIRE_VERSION
        )))
    }
}

fn ensure_state_version(version: u32, what: &str) -> Result<(), FfiError> {
    if version == STATE_VERSION {
        Ok(())
    } else {
        Err(FfiError::Api(format!(
            "unsupported {what} state version {version}; expected {STATE_VERSION}"
        )))
    }
}

fn repl_config(options: &WireReplOptions) -> ReplConfig {
    ReplConfig {
        script_name: options
            .script_name
            .clone()
            .unwrap_or_else(|| "main.py".to_owned()),
        limits: options.limits.clone().map(Into::into),
        type_check: options.type_check,
        type_check_stubs: options.type_check_stubs.clone(),
        assert_message_annotations: options.assert_message_annotations.map_or_else(
            AssertMessageAnnotations::default,
            AssertMessageAnnotations::from_max_bytes,
        ),
    }
}

fn new_idle_session(pool: Arc<Pool>, options: WireReplOptions) -> Result<ReplSession, PoolError> {
    let script_name = options
        .script_name
        .clone()
        .unwrap_or_else(|| "main.py".to_owned());
    let mut checkout = pool.checkout(&repl_config(&options))?;
    let checkpoint = checkout.dump()?;
    Ok(ReplSession {
        pool,
        checkout,
        options,
        script_name,
        checkpoint,
    })
}

fn restore_idle_session(
    pool: Arc<Pool>,
    options: WireReplOptions,
    fallback_script_name: String,
    state: Vec<u8>,
) -> Result<ReplSession, PoolError> {
    let mut checkout = pool.checkout(&repl_config(&options))?;
    let (event, restored_name) = checkout.restore(state.clone(), Vec::new(), &mut |_, _| {})?;
    if event.is_some() {
        return Err(PoolError::Protocol(
            "expected an idle REPL dump, found a suspended snapshot".into(),
        ));
    }
    Ok(ReplSession {
        pool,
        checkout,
        options,
        script_name: restored_name.unwrap_or(fallback_script_name),
        checkpoint: state,
    })
}

fn recover_repl(
    pool: Arc<Pool>,
    options: WireReplOptions,
    script_name: String,
    mut checkout: Checkout,
    rollback: Vec<u8>,
) -> RecoveredRepl {
    // A runtime/type error leaves the session usable. Dump it and restore it
    // into a clean checkout; if the dump is still suspended (e.g. the host
    // supplied an over-depth callback result), abandon to the pre-feed state.
    let current = checkout.dump().ok();
    drop(checkout);

    if let Some(state) = current
        && let Ok(session) = restore_idle_session(
            Arc::clone(&pool),
            options.clone(),
            script_name.clone(),
            state,
        )
    {
        return Some(Box::new(MontyGoRepl {
            inner: Some(session),
        }));
    }

    restore_idle_session(pool, options, script_name, rollback)
        .ok()
        .map(|session| {
            Box::new(MontyGoRepl {
                inner: Some(session),
            })
        })
}

fn collect_print(prints: &mut Vec<WirePrint>, stream: PrintStream, text: &str) {
    let stream = match stream {
        PrintStream::Stdout => WIRE_PRINT_STDOUT,
        PrintStream::Stderr => WIRE_PRINT_STDERR,
    };
    if let Some(last) = prints.last_mut()
        && last.stream == stream
    {
        last.text.push_str(text);
    } else {
        prints.push(WirePrint {
            stream,
            text: text.to_owned(),
        });
    }
}

fn extract_runner_inputs(
    names: &[String],
    mut inputs: BTreeMap<String, WireValue>,
) -> Result<Vec<(String, MontyObject)>, FfiError> {
    names
        .iter()
        .map(|name| {
            let value = inputs
                .remove(name)
                .ok_or_else(|| FfiError::Api(format!("missing required input '{name}'")))?;
            value
                .into_monty()
                .map(|value| (name.clone(), value))
                .map_err(FfiError::Api)
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

fn decode_call_result(result: WireCallResult) -> Result<ResumeValue, FfiError> {
    match result.kind {
        WIRE_CALL_RESULT_RETURN => result
            .value
            .ok_or_else(|| FfiError::Api("missing return value".to_owned()))?
            .into_monty()
            .map(ResumeValue::Return)
            .map_err(FfiError::Api),
        WIRE_CALL_RESULT_EXCEPTION => Ok(ResumeValue::Error(MontyException::new(
            parse_exc_type(&result.exc_type)?,
            result.arg,
        ))),
        WIRE_CALL_RESULT_PENDING => Ok(ResumeValue::Future),
        other => Err(FfiError::Api(format!("unknown call result kind: {other}"))),
    }
}

fn decode_lookup_result(result: WireLookupResult) -> Result<Option<MontyObject>, FfiError> {
    match result.kind {
        WIRE_LOOKUP_RESULT_VALUE => result
            .value
            .ok_or_else(|| FfiError::Api("missing lookup value".to_owned()))?
            .into_monty()
            .map(Some)
            .map_err(FfiError::Api),
        WIRE_LOOKUP_RESULT_UNDEFINED => Ok(None),
        other => Err(FfiError::Api(format!(
            "unknown lookup result kind: {other}"
        ))),
    }
}

fn decode_future_results(results: WireFutureResults) -> Result<Vec<(u32, ResumeValue)>, FfiError> {
    ensure_version(results.version, "future-results")?;
    results
        .results
        .into_iter()
        .map(|(call_id, result)| {
            let value = decode_call_result(result)?;
            if matches!(
                value,
                ResumeValue::Future | ResumeValue::NotFound | ResumeValue::NotHandled
            ) {
                return Err(FfiError::Api(format!(
                    "future {call_id} must resolve to a return value or exception"
                )));
            }
            Ok((call_id, value))
        })
        .collect()
}

fn parse_exc_type(exc_type: &str) -> Result<ExcType, FfiError> {
    exc_type
        .parse()
        .map_err(|_| FfiError::Api(format!("unknown exception type: {exc_type}")))
}

fn type_check_runner(runner: &MontyGoRunner, prefix: Option<&str>) -> Result<(), FfiError> {
    let script_name = runner.options.script_name.as_deref().unwrap_or("main.py");
    let source = SourceFile::new(&runner.code, script_name);
    let prefix = prefix.map(|value| SourceFile::new(value, "type_stubs.py"));
    match type_check(&source, prefix.as_ref()) {
        Ok(None) => Ok(()),
        Ok(Some(error)) => Err(FfiError::Typing(error.to_string())),
        Err(error) => Err(FfiError::Api(error)),
    }
}

fn build_runner(code: String, mut options: WireCompileOptions) -> Result<MontyGoRunner, FfiError> {
    ensure_version(options.version, "compile-options")?;
    let script_name = options
        .script_name
        .clone()
        .unwrap_or_else(|| "main.py".to_owned());
    let inputs = options.inputs.clone().unwrap_or_default();

    // Keep the established constructor contract: syntax/compile failures are
    // reported by New, before any subprocess execution starts.
    let compile_options = MontyCompileOptions {
        assert_message_annotations: options.assert_message_annotations.map_or_else(
            AssertMessageAnnotations::default,
            AssertMessageAnnotations::from_max_bytes,
        ),
    };
    MontyRun::new(code.clone(), &script_name, inputs, compile_options)
        .map_err(FfiError::Exception)?;

    options.script_name = Some(script_name);
    let runner = MontyGoRunner { code, options };
    if runner.options.type_check {
        type_check_runner(&runner, runner.options.type_check_stubs.as_deref())?;
    }
    Ok(runner)
}

fn start_runner(
    runner: &MontyGoRunner,
    options: WireStartOptions,
) -> Result<(StoredProgress, Vec<WirePrint>), (FfiError, Vec<WirePrint>)> {
    if let Err(error) = ensure_version(options.version, "start-options") {
        return Err((error, Vec::new()));
    }
    let inputs = match extract_runner_inputs(
        runner.options.inputs.as_deref().unwrap_or_default(),
        options.inputs,
    ) {
        Ok(inputs) => inputs,
        Err(error) => return Err((error, Vec::new())),
    };
    let pool = match default_pool() {
        Ok(pool) => pool,
        Err(error) => return Err((error, Vec::new())),
    };
    let repl_options = WireReplOptions {
        version: wire::WIRE_VERSION,
        script_name: runner.options.script_name.clone(),
        limits: options.limits,
        type_check: runner.options.type_check,
        type_check_stubs: runner.options.type_check_stubs.clone(),
        assert_message_annotations: runner.options.assert_message_annotations,
    };
    let mut checkout = match pool.checkout(&repl_config(&repl_options)) {
        Ok(checkout) => checkout,
        Err(error) => return Err((error.into(), Vec::new())),
    };
    let mut prints = Vec::new();
    let event = checkout.feed(
        &runner.code,
        inputs,
        Vec::new(),
        false,
        &mut |stream, text| collect_print(&mut prints, stream, text),
    );
    let event = match event {
        Ok(event) => event,
        Err(error) => return Err((error.into(), prints)),
    };
    let script_name = repl_options
        .script_name
        .clone()
        .unwrap_or_else(|| "main.py".to_owned());
    match progress_from_event(
        pool,
        checkout,
        event,
        repl_options,
        script_name,
        false,
        None,
    ) {
        Ok(progress) => Ok((progress, prints)),
        Err((error, _)) => Err((error, prints)),
    }
}

fn feed_repl(
    session: ReplSession,
    code: &str,
    options: WireFeedOptions,
) -> Result<(StoredProgress, Vec<WirePrint>), OperationFailure> {
    if let Err(error) = ensure_version(options.version, "feed-options") {
        return Err((
            error,
            Some(Box::new(MontyGoRepl {
                inner: Some(session),
            })),
            Vec::new(),
        ));
    }
    let inputs = match extract_feed_inputs(options.inputs) {
        Ok(inputs) => inputs,
        Err(error) => {
            return Err((
                error,
                Some(Box::new(MontyGoRepl {
                    inner: Some(session),
                })),
                Vec::new(),
            ));
        }
    };

    let ReplSession {
        pool,
        mut checkout,
        options: repl_options,
        script_name,
        checkpoint,
    } = session;
    let rollback = checkpoint;
    let mut prints = Vec::new();
    let event = checkout.feed(
        code,
        inputs,
        Vec::new(),
        options.skip_type_check,
        &mut |stream, text| collect_print(&mut prints, stream, text),
    );
    let event = match event {
        Ok(event) => event,
        Err(error) => {
            let repl = recover_repl(
                Arc::clone(&pool),
                repl_options,
                script_name,
                checkout,
                rollback,
            );
            return Err((error.into(), repl, prints));
        }
    };

    match progress_from_event(
        pool,
        checkout,
        event,
        repl_options,
        script_name,
        true,
        Some(rollback),
    ) {
        Ok(progress) => Ok((progress, prints)),
        Err((error, repl)) => Err((error, repl, prints)),
    }
}

fn progress_from_event(
    pool: Arc<Pool>,
    mut checkout: Checkout,
    event: TurnEvent,
    options: WireReplOptions,
    script_name: String,
    is_repl: bool,
    rollback: Option<Vec<u8>>,
) -> Result<StoredProgress, ProgressFailure> {
    match event {
        TurnEvent::Complete(output) if is_repl => {
            let checkpoint = match checkout.dump() {
                Ok(checkpoint) => checkpoint,
                Err(error) => {
                    let repl = rollback.and_then(|rollback| {
                        recover_repl(
                            Arc::clone(&pool),
                            options.clone(),
                            script_name.clone(),
                            checkout,
                            rollback,
                        )
                    });
                    return Err((error.into(), repl));
                }
            };
            Ok(StoredProgress::Complete(CompleteProgress {
                output,
                script_name: script_name.clone(),
                is_repl: true,
                repl: Some(ReplSession {
                    pool,
                    checkout,
                    options,
                    script_name,
                    checkpoint,
                }),
            }))
        }
        TurnEvent::Complete(output) => {
            checkout.finish().map_err(|error| (error.into(), None))?;
            Ok(StoredProgress::Complete(CompleteProgress {
                output,
                script_name,
                is_repl: false,
                repl: None,
            }))
        }
        event => Ok(StoredProgress::Active(ActiveProgress {
            pool,
            checkout,
            event,
            options,
            script_name,
            is_repl,
            rollback,
        })),
    }
}

fn advance_progress(
    active: ActiveProgress,
    operation: impl FnOnce(&mut Checkout, &mut Vec<WirePrint>) -> Result<TurnEvent, PoolError>,
) -> Result<(StoredProgress, Vec<WirePrint>), OperationFailure> {
    let ActiveProgress {
        pool,
        mut checkout,
        event: _,
        options,
        script_name,
        is_repl,
        rollback,
    } = active;
    let mut prints = Vec::new();
    let event = operation(&mut checkout, &mut prints);
    let event = match event {
        Ok(event) => event,
        Err(error) => {
            let repl = if is_repl {
                rollback.and_then(|rollback| {
                    recover_repl(
                        Arc::clone(&pool),
                        options.clone(),
                        script_name.clone(),
                        checkout,
                        rollback,
                    )
                })
            } else {
                None
            };
            return Err((error.into(), repl, prints));
        }
    };
    match progress_from_event(
        pool,
        checkout,
        event,
        options,
        script_name,
        is_repl,
        rollback,
    ) {
        Ok(progress) => Ok((progress, prints)),
        Err((error, repl)) => Err((error, repl, prints)),
    }
}

fn restore_active(
    envelope: SnapshotEnvelope,
) -> Result<(StoredProgress, Vec<WirePrint>), FfiError> {
    ensure_state_version(envelope.version, "snapshot")?;
    ensure_version(envelope.options.version, "repl-options")?;
    if envelope.is_repl && envelope.rollback.is_none() {
        return Err(FfiError::Api(
            "REPL snapshot is missing its rollback checkpoint".to_owned(),
        ));
    }
    let pool = default_pool()?;
    let mut checkout = pool
        .checkout(&repl_config(&envelope.options))
        .map_err(FfiError::from)?;
    let mut prints = Vec::new();
    let (event, restored_name) = checkout
        .restore(envelope.state, Vec::new(), &mut |stream, text| {
            collect_print(&mut prints, stream, text);
        })
        .map_err(FfiError::from)?;
    let event = event.ok_or_else(|| {
        FfiError::Api("expected a suspended snapshot, found an idle REPL dump".to_owned())
    })?;
    Ok((
        StoredProgress::Active(ActiveProgress {
            pool,
            checkout,
            event,
            options: envelope.options,
            script_name: restored_name.unwrap_or(envelope.script_name),
            is_repl: envelope.is_repl,
            rollback: envelope.rollback,
        }),
        prints,
    ))
}

fn decode_wire<T: serde::de::DeserializeOwned>(bytes: &[u8]) -> Result<T, FfiError> {
    rmp_serde::from_slice(bytes)
        .map_err(|error| FfiError::Api(format!("invalid wire payload: {error}")))
}

fn encode_wire<T: serde::Serialize>(value: &T) -> Result<MontyGoBytes, FfiError> {
    rmp_serde::to_vec_named(value)
        .map(MontyGoBytes::from_vec)
        .map_err(|error| FfiError::Api(format!("failed to encode wire payload: {error}")))
}

fn encode_state<T: serde::Serialize>(value: &T) -> Result<Vec<u8>, FfiError> {
    rmp_serde::to_vec_named(value)
        .map_err(|error| FfiError::Api(format!("failed to encode state: {error}")))
}

fn decode_state<T: serde::de::DeserializeOwned>(bytes: &[u8]) -> Result<T, FfiError> {
    rmp_serde::from_slice(bytes).map_err(|error| FfiError::Api(format!("invalid state: {error}")))
}

unsafe fn slice_from_raw<'a>(ptr: *const u8, len: usize) -> &'a [u8] {
    if ptr.is_null() || len == 0 {
        &[]
    } else {
        // SAFETY: the C ABI caller guarantees a valid buffer for this call.
        unsafe { std::slice::from_raw_parts(ptr, len) }
    }
}

unsafe fn string_from_cstr<'a>(ptr: *const c_char) -> Result<&'a str, FfiError> {
    if ptr.is_null() {
        return Err(FfiError::Api("expected non-null C string".to_owned()));
    }
    // SAFETY: the C ABI caller guarantees a NUL-terminated string.
    unsafe { CStr::from_ptr(ptr) }
        .to_str()
        .map_err(|error| FfiError::Api(format!("invalid UTF-8 string: {error}")))
}

fn decode_utf8<'a>(bytes: &'a [u8], field: &str) -> Result<&'a str, FfiError> {
    std::str::from_utf8(bytes)
        .map_err(|error| FfiError::Api(format!("{field} is not valid UTF-8: {error}")))
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_bytes_free(ptr: *mut u8, len: usize) {
    if !ptr.is_null() && len > 0 {
        let slice = std::ptr::slice_from_raw_parts_mut(ptr, len);
        // SAFETY: `MontyGoBytes::from_vec` leaked this exact boxed slice.
        unsafe { drop(Box::from_raw(slice)) };
    }
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_runner_free(runner: *mut MontyGoRunner) {
    if !runner.is_null() {
        // SAFETY: created by `Box::into_raw`.
        unsafe { drop(Box::from_raw(runner)) };
    }
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_repl_free(repl: *mut MontyGoRepl) {
    if !repl.is_null() {
        // SAFETY: created by `Box::into_raw`.
        let mut repl = unsafe { Box::from_raw(repl) };
        if let Some(session) = repl.inner.take() {
            // An idle session can safely return its worker to the pool. Errors
            // cannot cross a destructor boundary; `finish` already discards a
            // worker on reset failure, keeping the pool healthy.
            let _ = session.checkout.finish();
        }
    }
}

/// Returns the local worker PID for diagnostics/tests, or zero when the REPL
/// is empty or uses a non-process transport.
#[unsafe(no_mangle)]
pub extern "C" fn monty_go_repl_worker_pid(repl: *const MontyGoRepl) -> u32 {
    if repl.is_null() {
        return 0;
    }
    // SAFETY: caller passes a live REPL handle.
    let repl = unsafe { &*repl };
    repl.inner
        .as_ref()
        .and_then(|session| session.checkout.pid())
        .unwrap_or(0)
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_progress_free(progress: *mut MontyGoProgress) {
    if !progress.is_null() {
        // SAFETY: created by `Box::into_raw`.
        unsafe { drop(Box::from_raw(progress)) };
    }
}

/// Returns the local worker PID for an in-flight/REPL-complete progress
/// handle, or zero when no worker is owned.
#[unsafe(no_mangle)]
pub extern "C" fn monty_go_progress_worker_pid(progress: *const MontyGoProgress) -> u32 {
    if progress.is_null() {
        return 0;
    }
    // SAFETY: caller passes a live progress handle.
    let progress = unsafe { &*progress };
    match progress.inner.as_ref() {
        Some(StoredProgress::Active(active)) => active.checkout.pid().unwrap_or(0),
        Some(StoredProgress::Complete(complete)) => complete
            .repl
            .as_ref()
            .and_then(|session| session.checkout.pid())
            .unwrap_or(0),
        None => 0,
    }
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_error_free(error: *mut MontyGoError) {
    if !error.is_null() {
        // SAFETY: created by `Box::into_raw`.
        unsafe { drop(Box::from_raw(error)) };
    }
}

/// Initializes the process-wide default subprocess pool. The Go loader calls
/// this immediately after extracting the version-matched worker executable.
#[unsafe(no_mangle)]
pub extern "C" fn monty_go_runtime_init(
    worker_path_ptr: *const u8,
    worker_path_len: usize,
) -> *mut MontyGoError {
    catch_error_result(|| {
        if DEFAULT_POOL.get().is_some() {
            return ptr::null_mut();
        }
        // SAFETY: the caller keeps the worker-path buffer live for this call.
        let bytes = unsafe { slice_from_raw(worker_path_ptr, worker_path_len) };
        let path = match decode_utf8(bytes, "worker path") {
            Ok(path) => PathBuf::from(path),
            Err(error) => return error_ptr(error),
        };
        let pool = match Pool::new(PoolConfig::subprocess(path)) {
            Ok(pool) => Arc::new(pool),
            Err(error) => return error_ptr(error.into()),
        };
        let _ = DEFAULT_POOL.set(pool);
        ptr::null_mut()
    })
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_error_json(error: *const MontyGoError, out: *mut MontyGoBytes) {
    let bytes = if error.is_null() {
        MontyGoBytes::empty()
    } else {
        // SAFETY: caller passes a live error handle.
        let error = unsafe { &*error };
        serde_json::to_vec(&error.inner.summary())
            .map_or_else(|_| MontyGoBytes::empty(), MontyGoBytes::from_vec)
    };
    if !out.is_null() {
        // SAFETY: caller owns the out pointer.
        unsafe { *out = bytes };
    }
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_error_display(
    error: *const MontyGoError,
    format: *const c_char,
    color: bool,
    out: *mut MontyGoBytes,
) {
    let bytes = if error.is_null() {
        MontyGoBytes::empty()
    } else {
        // SAFETY: caller passes a live error handle.
        let error = unsafe { &*error };
        // SAFETY: the caller provides a NUL-terminated format string.
        let format = unsafe { string_from_cstr(format) }.unwrap_or("traceback");
        MontyGoBytes::from_vec(
            error
                .inner
                .display(format, color)
                .unwrap_or_else(|display_error| display_error)
                .into_bytes(),
        )
    };
    if !out.is_null() {
        // SAFETY: caller owns the out pointer.
        unsafe { *out = bytes };
    }
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_runner_new(
    code_ptr: *const u8,
    code_len: usize,
    options_ptr: *const u8,
    options_len: usize,
    out: *mut MontyGoRunnerResult,
) {
    let result = catch_runner_result(|| {
        // SAFETY: the caller keeps both input buffers live for this call.
        let code = unsafe { slice_from_raw(code_ptr, code_len) };
        // SAFETY: the caller keeps both input buffers live for this call.
        let options = unsafe { slice_from_raw(options_ptr, options_len) };
        let code = match decode_utf8(code, "code") {
            Ok(code) => code.to_owned(),
            Err(error) => return MontyGoRunnerResult::err(error),
        };
        let options = match decode_wire(options) {
            Ok(options) => options,
            Err(error) => return MontyGoRunnerResult::err(error),
        };
        match build_runner(code, options) {
            Ok(runner) => MontyGoRunnerResult::ok(runner),
            Err(error) => MontyGoRunnerResult::err(error),
        }
    });
    if !out.is_null() {
        // SAFETY: caller owns the out pointer.
        unsafe { *out = result };
    }
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_runner_load(
    data_ptr: *const u8,
    data_len: usize,
    out: *mut MontyGoRunnerResult,
) {
    let result = catch_runner_result(|| {
        // SAFETY: the caller keeps the serialized runner live for this call.
        let data = unsafe { slice_from_raw(data_ptr, data_len) };
        let envelope: RunnerEnvelope = match decode_state(data) {
            Ok(envelope) => envelope,
            Err(error) => return MontyGoRunnerResult::err(error),
        };
        if let Err(error) = ensure_state_version(envelope.version, "runner") {
            return MontyGoRunnerResult::err(error);
        }
        match build_runner(envelope.code, envelope.options) {
            Ok(runner) => MontyGoRunnerResult::ok(runner),
            Err(error) => MontyGoRunnerResult::err(error),
        }
    });
    if !out.is_null() {
        // SAFETY: caller owns the out pointer.
        unsafe { *out = result };
    }
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_runner_dump(
    runner: *const MontyGoRunner,
    out: *mut MontyGoBytes,
    error_out: *mut *mut MontyGoError,
) {
    if !error_out.is_null() {
        // SAFETY: caller owns the out pointer.
        unsafe { *error_out = ptr::null_mut() };
    }
    let bytes = catch_bytes_result(error_out, || {
        if runner.is_null() {
            return Err(FfiError::Api("runner handle is null".to_owned()));
        }
        // SAFETY: caller passes a live runner handle.
        let runner = unsafe { &*runner };
        encode_state(&RunnerEnvelope {
            version: STATE_VERSION,
            code: runner.code.clone(),
            options: runner.options.clone(),
        })
    });
    if !out.is_null() {
        // SAFETY: caller owns the out pointer.
        unsafe { *out = bytes };
    }
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_runner_type_check(
    runner: *const MontyGoRunner,
    prefix_ptr: *const u8,
    prefix_len: usize,
) -> *mut MontyGoError {
    catch_error_result(|| {
        if runner.is_null() {
            return error_ptr(FfiError::Api("runner handle is null".to_owned()));
        }
        // SAFETY: caller passes a live runner handle.
        let runner = unsafe { &*runner };
        // SAFETY: the caller keeps the optional prefix live for this call.
        let prefix = unsafe { slice_from_raw(prefix_ptr, prefix_len) };
        let prefix = if prefix.is_empty() {
            None
        } else {
            match decode_utf8(prefix, "type-check prefix") {
                Ok(prefix) => Some(prefix),
                Err(error) => return error_ptr(error),
            }
        };
        match type_check_runner(runner, prefix) {
            Ok(()) => ptr::null_mut(),
            Err(error) => error_ptr(error),
        }
    })
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_runner_start(
    runner: *const MontyGoRunner,
    options_ptr: *const u8,
    options_len: usize,
    out: *mut MontyGoOpResult,
) {
    let result = catch_op_result(|| {
        if runner.is_null() {
            return MontyGoOpResult::err(
                FfiError::Api("runner handle is null".to_owned()),
                None,
                &[],
            );
        }
        // SAFETY: caller passes a live runner handle.
        let runner = unsafe { &*runner };
        // SAFETY: the caller keeps the start-options buffer live for this call.
        let options = unsafe { slice_from_raw(options_ptr, options_len) };
        let options = match decode_wire(options) {
            Ok(options) => options,
            Err(error) => return MontyGoOpResult::err(error, None, &[]),
        };
        match start_runner(runner, options) {
            Ok((progress, prints)) => MontyGoOpResult::ok(progress, &prints),
            Err((error, prints)) => MontyGoOpResult::err(error, None, &prints),
        }
    });
    if !out.is_null() {
        // SAFETY: caller owns the out pointer.
        unsafe { *out = result };
    }
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_repl_new(
    options_ptr: *const u8,
    options_len: usize,
    out: *mut MontyGoReplResult,
) {
    let result = catch_repl_result(|| {
        // SAFETY: the caller keeps the REPL-options buffer live for this call.
        let bytes = unsafe { slice_from_raw(options_ptr, options_len) };
        let options: WireReplOptions = match decode_wire(bytes) {
            Ok(options) => options,
            Err(error) => return MontyGoReplResult::err(error),
        };
        if let Err(error) = ensure_version(options.version, "repl-options") {
            return MontyGoReplResult::err(error);
        }
        let pool = match default_pool() {
            Ok(pool) => pool,
            Err(error) => return MontyGoReplResult::err(error),
        };
        match new_idle_session(pool, options) {
            Ok(session) => MontyGoReplResult::ok(MontyGoRepl {
                inner: Some(session),
            }),
            Err(error) => MontyGoReplResult::err(error.into()),
        }
    });
    if !out.is_null() {
        // SAFETY: caller owns the out pointer.
        unsafe { *out = result };
    }
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_repl_load(
    data_ptr: *const u8,
    data_len: usize,
    out: *mut MontyGoReplResult,
) {
    let result = catch_repl_result(|| {
        // SAFETY: the caller keeps the serialized REPL live for this call.
        let data = unsafe { slice_from_raw(data_ptr, data_len) };
        let envelope: ReplEnvelope = match decode_state(data) {
            Ok(envelope) => envelope,
            Err(error) => return MontyGoReplResult::err(error),
        };
        if let Err(error) = ensure_state_version(envelope.version, "REPL") {
            return MontyGoReplResult::err(error);
        }
        if let Err(error) = ensure_version(envelope.options.version, "repl-options") {
            return MontyGoReplResult::err(error);
        }
        let pool = match default_pool() {
            Ok(pool) => pool,
            Err(error) => return MontyGoReplResult::err(error),
        };
        match restore_idle_session(pool, envelope.options, envelope.script_name, envelope.state) {
            Ok(session) => MontyGoReplResult::ok(MontyGoRepl {
                inner: Some(session),
            }),
            Err(error) => MontyGoReplResult::err(error.into()),
        }
    });
    if !out.is_null() {
        // SAFETY: caller owns the out pointer.
        unsafe { *out = result };
    }
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_repl_dump(
    repl: *mut MontyGoRepl,
    out: *mut MontyGoBytes,
    error_out: *mut *mut MontyGoError,
) {
    if !error_out.is_null() {
        // SAFETY: caller owns the out pointer.
        unsafe { *error_out = ptr::null_mut() };
    }
    let bytes = catch_bytes_result(error_out, || {
        if repl.is_null() {
            return Err(FfiError::Api("REPL handle is null".to_owned()));
        }
        // SAFETY: caller passes an exclusively borrowed REPL handle.
        let repl = unsafe { &mut *repl };
        let session = repl
            .inner
            .as_ref()
            .ok_or_else(|| FfiError::Api("REPL handle is empty".to_owned()))?;
        encode_state(&ReplEnvelope {
            version: STATE_VERSION,
            state: session.checkpoint.clone(),
            options: session.options.clone(),
            script_name: session.script_name.clone(),
        })
    });
    if !out.is_null() {
        // SAFETY: caller owns the out pointer.
        unsafe { *out = bytes };
    }
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_repl_feed_start(
    repl: *mut MontyGoRepl,
    code_ptr: *const u8,
    code_len: usize,
    options_ptr: *const u8,
    options_len: usize,
    out: *mut MontyGoOpResult,
) {
    let result = catch_op_result(|| {
        if repl.is_null() {
            return MontyGoOpResult::err(
                FfiError::Api("REPL handle is null".to_owned()),
                None,
                &[],
            );
        }
        // SAFETY: caller passes an exclusively borrowed REPL handle.
        let repl = unsafe { &mut *repl };
        let Some(session) = repl.inner.take() else {
            return MontyGoOpResult::err(
                FfiError::Api("REPL handle is empty".to_owned()),
                None,
                &[],
            );
        };
        // SAFETY: the caller keeps the code buffer live for this call.
        let code = unsafe { slice_from_raw(code_ptr, code_len) };
        let code = match decode_utf8(code, "code") {
            Ok(code) => code,
            Err(error) => {
                return MontyGoOpResult::err(
                    error,
                    Some(Box::new(MontyGoRepl {
                        inner: Some(session),
                    })),
                    &[],
                );
            }
        };
        // SAFETY: the caller keeps the feed-options buffer live for this call.
        let options = unsafe { slice_from_raw(options_ptr, options_len) };
        let options = match decode_wire(options) {
            Ok(options) => options,
            Err(error) => {
                return MontyGoOpResult::err(
                    error,
                    Some(Box::new(MontyGoRepl {
                        inner: Some(session),
                    })),
                    &[],
                );
            }
        };
        match feed_repl(session, code, options) {
            Ok((progress, prints)) => MontyGoOpResult::ok(progress, &prints),
            Err((error, repl, prints)) => MontyGoOpResult::err(error, repl, &prints),
        }
    });
    if !out.is_null() {
        // SAFETY: caller owns the out pointer.
        unsafe { *out = result };
    }
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_progress_describe(
    progress: *const MontyGoProgress,
    out: *mut MontyGoBytes,
    error_out: *mut *mut MontyGoError,
) {
    if !error_out.is_null() {
        // SAFETY: caller owns the out pointer.
        unsafe { *error_out = ptr::null_mut() };
    }
    let bytes = catch_bytes_result(error_out, || {
        if progress.is_null() {
            return Err(FfiError::Api("progress handle is null".to_owned()));
        }
        // SAFETY: caller passes a live progress handle.
        let progress = unsafe { &*progress };
        let progress = progress
            .inner
            .as_ref()
            .ok_or_else(|| FfiError::Api("progress handle is empty".to_owned()))?;
        rmp_serde::to_vec_named(&progress.describe())
            .map_err(|error| FfiError::Api(format!("failed to encode progress: {error}")))
    });
    if !out.is_null() {
        // SAFETY: caller owns the out pointer.
        unsafe { *out = bytes };
    }
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_progress_dump(
    progress: *mut MontyGoProgress,
    out: *mut MontyGoBytes,
    error_out: *mut *mut MontyGoError,
) {
    if !error_out.is_null() {
        // SAFETY: caller owns the out pointer.
        unsafe { *error_out = ptr::null_mut() };
    }
    let bytes = catch_bytes_result(error_out, || {
        if progress.is_null() {
            return Err(FfiError::Api("progress handle is null".to_owned()));
        }
        // SAFETY: caller passes an exclusively borrowed progress handle.
        let progress = unsafe { &mut *progress };
        let active = match progress.inner.as_mut() {
            Some(StoredProgress::Active(active)) => active,
            Some(StoredProgress::Complete(_)) => {
                return Err(FfiError::Api(
                    "completed progress cannot be dumped".to_owned(),
                ));
            }
            None => return Err(FfiError::Api("progress handle is empty".to_owned())),
        };
        let state = active.checkout.dump().map_err(FfiError::from)?;
        encode_state(&SnapshotEnvelope {
            version: STATE_VERSION,
            state,
            options: active.options.clone(),
            script_name: active.script_name.clone(),
            is_repl: active.is_repl,
            rollback: active.rollback.clone(),
        })
    });
    if !out.is_null() {
        // SAFETY: caller owns the out pointer.
        unsafe { *out = bytes };
    }
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_progress_load(
    data_ptr: *const u8,
    data_len: usize,
    out: *mut MontyGoOpResult,
) {
    let result = catch_op_result(|| {
        // SAFETY: the caller keeps the serialized snapshot live for this call.
        let data = unsafe { slice_from_raw(data_ptr, data_len) };
        let envelope: SnapshotEnvelope = match decode_state(data) {
            Ok(envelope) => envelope,
            Err(error) => return MontyGoOpResult::err(error, None, &[]),
        };
        match restore_active(envelope) {
            Ok((progress, prints)) => MontyGoOpResult::ok(progress, &prints),
            Err(error) => MontyGoOpResult::err(error, None, &[]),
        }
    });
    if !out.is_null() {
        // SAFETY: caller owns the out pointer.
        unsafe { *out = result };
    }
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_progress_take_repl(
    progress: *mut MontyGoProgress,
    out: *mut MontyGoReplResult,
) {
    let result = catch_repl_result(|| {
        if progress.is_null() {
            return MontyGoReplResult::err(FfiError::Api("progress handle is null".to_owned()));
        }
        // SAFETY: caller passes an exclusively borrowed progress handle.
        let progress = unsafe { &mut *progress };
        let Some(progress) = progress.inner.take() else {
            return MontyGoReplResult::err(FfiError::Api("progress handle is empty".to_owned()));
        };
        match progress.into_repl() {
            Ok(repl) => MontyGoReplResult::ok(repl),
            Err(error) => MontyGoReplResult::err(error),
        }
    });
    if !out.is_null() {
        // SAFETY: caller owns the out pointer.
        unsafe { *out = result };
    }
}

fn take_active(progress: *mut MontyGoProgress) -> Result<ActiveProgress, FfiError> {
    if progress.is_null() {
        return Err(FfiError::Api("progress handle is null".to_owned()));
    }
    // SAFETY: caller passes an exclusively borrowed progress handle.
    let progress = unsafe { &mut *progress };
    match progress.inner.take() {
        Some(StoredProgress::Active(active)) => Ok(active),
        Some(StoredProgress::Complete(_)) => {
            Err(FfiError::Api("execution is already complete".to_owned()))
        }
        None => Err(FfiError::Api("progress handle is empty".to_owned())),
    }
}

fn invalid_resume(active: ActiveProgress, error: FfiError) -> MontyGoOpResult {
    let repl = if active.is_repl {
        active.rollback.clone().and_then(|rollback| {
            recover_repl(
                active.pool,
                active.options,
                active.script_name,
                active.checkout,
                rollback,
            )
        })
    } else {
        None
    };
    MontyGoOpResult::err(error, repl, &[])
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_progress_resume_call(
    progress: *mut MontyGoProgress,
    result_ptr: *const u8,
    result_len: usize,
    out: *mut MontyGoOpResult,
) {
    let result = catch_op_result(|| {
        let active = match take_active(progress) {
            Ok(active) => active,
            Err(error) => return MontyGoOpResult::err(error, None, &[]),
        };
        if !matches!(
            active.event,
            TurnEvent::FunctionCall { .. } | TurnEvent::OsCall { .. }
        ) {
            return invalid_resume(
                active,
                FfiError::Api("progress is not a call snapshot".to_owned()),
            );
        }
        // SAFETY: the caller keeps the call-result buffer live for this call.
        let bytes = unsafe { slice_from_raw(result_ptr, result_len) };
        let result: WireCallResult = match decode_wire(bytes) {
            Ok(result) => result,
            Err(error) => return invalid_resume(active, error),
        };
        let value = match decode_call_result(result) {
            Ok(value) => value,
            Err(error) => return invalid_resume(active, error),
        };
        match advance_progress(active, |checkout, prints| {
            checkout.resume(value, &mut |stream, text| {
                collect_print(prints, stream, text);
            })
        }) {
            Ok((progress, prints)) => MontyGoOpResult::ok(progress, &prints),
            Err((error, repl, prints)) => MontyGoOpResult::err(error, repl, &prints),
        }
    });
    if !out.is_null() {
        // SAFETY: caller owns the out pointer.
        unsafe { *out = result };
    }
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_progress_resume_lookup(
    progress: *mut MontyGoProgress,
    result_ptr: *const u8,
    result_len: usize,
    out: *mut MontyGoOpResult,
) {
    let result = catch_op_result(|| {
        let active = match take_active(progress) {
            Ok(active) => active,
            Err(error) => return MontyGoOpResult::err(error, None, &[]),
        };
        if !matches!(active.event, TurnEvent::NameLookup { .. }) {
            return invalid_resume(
                active,
                FfiError::Api("progress is not a name lookup".to_owned()),
            );
        }
        // SAFETY: the caller keeps the lookup-result buffer live for this call.
        let bytes = unsafe { slice_from_raw(result_ptr, result_len) };
        let result: WireLookupResult = match decode_wire(bytes) {
            Ok(result) => result,
            Err(error) => return invalid_resume(active, error),
        };
        let value = match decode_lookup_result(result) {
            Ok(value) => value,
            Err(error) => return invalid_resume(active, error),
        };
        match advance_progress(active, |checkout, prints| {
            checkout.resume_name_lookup(value, &mut |stream, text| {
                collect_print(prints, stream, text);
            })
        }) {
            Ok((progress, prints)) => MontyGoOpResult::ok(progress, &prints),
            Err((error, repl, prints)) => MontyGoOpResult::err(error, repl, &prints),
        }
    });
    if !out.is_null() {
        // SAFETY: caller owns the out pointer.
        unsafe { *out = result };
    }
}

#[unsafe(no_mangle)]
pub extern "C" fn monty_go_progress_resume_futures(
    progress: *mut MontyGoProgress,
    results_ptr: *const u8,
    results_len: usize,
    out: *mut MontyGoOpResult,
) {
    let result = catch_op_result(|| {
        let active = match take_active(progress) {
            Ok(active) => active,
            Err(error) => return MontyGoOpResult::err(error, None, &[]),
        };
        if !matches!(active.event, TurnEvent::ResolveFutures { .. }) {
            return invalid_resume(
                active,
                FfiError::Api("progress is not a future snapshot".to_owned()),
            );
        }
        // SAFETY: the caller keeps the future-results buffer live for this call.
        let bytes = unsafe { slice_from_raw(results_ptr, results_len) };
        let results: WireFutureResults = match decode_wire(bytes) {
            Ok(results) => results,
            Err(error) => return invalid_resume(active, error),
        };
        let values = match decode_future_results(results) {
            Ok(values) => values,
            Err(error) => return invalid_resume(active, error),
        };
        match advance_progress(active, |checkout, prints| {
            checkout.resume_futures(values, &mut |stream, text| {
                collect_print(prints, stream, text);
            })
        }) {
            Ok((progress, prints)) => MontyGoOpResult::ok(progress, &prints),
            Err((error, repl, prints)) => MontyGoOpResult::err(error, repl, &prints),
        }
    });
    if !out.is_null() {
        // SAFETY: caller owns the out pointer.
        unsafe { *out = result };
    }
}
