//! Minimal native shell for Monty's published subprocess worker protocol.
//!
//! The parent bridge links `monty-pool` but never the interpreter. This child
//! is the only gomonty artifact that enables `monty-proto`'s `worker` feature,
//! which keeps untrusted execution out of the Go host process.

use std::{env, ffi::OsStr, io, panic, process::ExitCode};

use monty_proto::{
    FrameError, FrameReader, pb,
    worker::{Child, EventSink, HandleOutcome, fatal_error_event, protocol_violation},
    write_frame,
};

fn main() -> ExitCode {
    let mut args = env::args_os();
    let _program = args.next();
    if args.next().as_deref() != Some(OsStr::new("subprocess")) || args.next().is_some() {
        eprintln!("usage: gomonty-worker subprocess");
        return ExitCode::FAILURE;
    }
    run()
}

fn run() -> ExitCode {
    install_panic_hook();
    let mut reader = FrameReader::new(io::stdin().lock());
    let mut child = Child::new();
    let mut sink = StdoutSink;

    loop {
        match reader.read::<pb::ParentRequest>() {
            Ok(Some(request)) => match child.handle(request, &mut sink) {
                Ok(HandleOutcome::Continue) => {}
                Ok(HandleOutcome::Shutdown) => return ExitCode::SUCCESS,
                Ok(HandleOutcome::Fatal) => return ExitCode::from(4),
                Err(FrameError::FrameTooLarge { len, max }) => {
                    fatal(
                        &child,
                        &mut sink,
                        &format!("response frame of {len} bytes exceeds maximum of {max} bytes"),
                    );
                    return ExitCode::from(2);
                }
                Err(_) => return ExitCode::from(3),
            },
            Ok(None) => return ExitCode::SUCCESS,
            Err(FrameError::Decode(err)) => {
                if sink
                    .send(&protocol_violation(&format!("malformed request: {err}")))
                    .is_err()
                {
                    return ExitCode::from(3);
                }
            }
            Err(err) => {
                fatal(
                    &child,
                    &mut sink,
                    &format!("malformed request frame: {err}"),
                );
                return ExitCode::from(2);
            }
        }
    }
}

struct StdoutSink;

impl EventSink for StdoutSink {
    fn send(&mut self, event: &pb::ChildEvent) -> Result<(), FrameError> {
        write_frame(&mut io::stdout(), event)
    }
}

fn fatal(child: &Child, sink: &mut impl EventSink, message: &str) {
    eprintln!("gomonty worker fatal error: {message}");
    let _ = sink.send(&child.fatal_event(message));
}

fn install_panic_hook() {
    let default_hook = panic::take_hook();
    panic::set_hook(Box::new(move |info| {
        let _ = write_frame(
            &mut io::stdout(),
            &fatal_error_event(&format!("child panicked: {info}")),
        );
        default_hook(info);
    }));
}
