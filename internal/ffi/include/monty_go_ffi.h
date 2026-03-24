#ifndef MONTY_GO_FFI_H
#define MONTY_GO_FFI_H

#pragma once

#include <stdarg.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>

/**
 * Current JSON schema version for the Go FFI.
 */
#define WIRE_VERSION 1

/**
 * Opaque error handle for the Go bindings.
 */
typedef struct MontyGoError MontyGoError;

/**
 * Opaque progress handle for the Go bindings.
 */
typedef struct MontyGoProgress MontyGoProgress;

/**
 * Opaque REPL handle for the Go bindings.
 */
typedef struct MontyGoRepl MontyGoRepl;

/**
 * Opaque runner handle for the Go bindings.
 */
typedef struct MontyGoRunner MontyGoRunner;

/**
 * Heap-allocated bytes returned across the C ABI.
 */
typedef struct MontyGoBytes {
  /**
   * Pointer to the byte buffer.
   */
  uint8_t *ptr;
  /**
   * Buffer length in bytes.
   */
  uintptr_t len;
} MontyGoBytes;

/**
 * Result of runner construction or loading.
 */
typedef struct MontyGoRunnerResult {
  /**
   * Created runner handle on success.
   */
  struct MontyGoRunner *runner;
  /**
   * Error handle on failure.
   */
  struct MontyGoError *error;
} MontyGoRunnerResult;

/**
 * Result of start/resume/feed operations.
 */
typedef struct MontyGoOpResult {
  /**
   * Progress handle on success.
   */
  struct MontyGoProgress *progress;
  /**
   * Decoded payload for the current progress state.
   */
  struct MontyGoBytes progress_payload;
  /**
   * Error handle on failure.
   */
  struct MontyGoError *error;
  /**
   * Recovered REPL handle for REPL runtime errors.
   */
  struct MontyGoRepl *repl;
  /**
   * Captured `print()` output from this step.
   */
  struct MontyGoBytes prints;
} MontyGoOpResult;

/**
 * Result of REPL construction or loading.
 */
typedef struct MontyGoReplResult {
  /**
   * Created REPL handle on success.
   */
  struct MontyGoRepl *repl;
  /**
   * Error handle on failure.
   */
  struct MontyGoError *error;
} MontyGoReplResult;

#ifdef __cplusplus
extern "C" {
#endif // __cplusplus

void monty_go_bytes_free(struct MontyGoBytes bytes);

void monty_go_runner_free(struct MontyGoRunner *runner);

void monty_go_repl_free(struct MontyGoRepl *repl);

void monty_go_progress_free(struct MontyGoProgress *progress);

void monty_go_error_free(struct MontyGoError *error);

struct MontyGoBytes monty_go_error_json(const struct MontyGoError *error);

struct MontyGoBytes monty_go_error_display(const struct MontyGoError *error,
                                           const char *format,
                                           bool color);

struct MontyGoRunnerResult monty_go_runner_new(const uint8_t *code_ptr,
                                               uintptr_t code_len,
                                               const uint8_t *options_ptr,
                                               uintptr_t options_len);

struct MontyGoRunnerResult monty_go_runner_load(const uint8_t *data_ptr, uintptr_t data_len);

struct MontyGoBytes monty_go_runner_dump(const struct MontyGoRunner *runner,
                                         struct MontyGoError **error_out);

struct MontyGoError *monty_go_runner_type_check(const struct MontyGoRunner *runner,
                                                const uint8_t *prefix_ptr,
                                                uintptr_t prefix_len);

struct MontyGoOpResult monty_go_runner_start(const struct MontyGoRunner *runner,
                                             const uint8_t *options_ptr,
                                             uintptr_t options_len);

struct MontyGoReplResult monty_go_repl_new(const uint8_t *options_ptr, uintptr_t options_len);

struct MontyGoReplResult monty_go_repl_load(const uint8_t *data_ptr, uintptr_t data_len);

struct MontyGoBytes monty_go_repl_dump(const struct MontyGoRepl *repl,
                                       struct MontyGoError **error_out);

struct MontyGoOpResult monty_go_repl_feed_start(struct MontyGoRepl *repl,
                                                const uint8_t *code_ptr,
                                                uintptr_t code_len,
                                                const uint8_t *options_ptr,
                                                uintptr_t options_len);

struct MontyGoBytes monty_go_progress_describe(const struct MontyGoProgress *progress,
                                               struct MontyGoError **error_out);

struct MontyGoBytes monty_go_progress_dump(const struct MontyGoProgress *progress,
                                           struct MontyGoError **error_out);

struct MontyGoOpResult monty_go_progress_load(const uint8_t *data_ptr, uintptr_t data_len);

struct MontyGoReplResult monty_go_progress_take_repl(struct MontyGoProgress *progress);

struct MontyGoOpResult monty_go_progress_resume_call(struct MontyGoProgress *progress,
                                                     const uint8_t *result_ptr,
                                                     uintptr_t result_len);

struct MontyGoOpResult monty_go_progress_resume_lookup(struct MontyGoProgress *progress,
                                                       const uint8_t *result_ptr,
                                                       uintptr_t result_len);

struct MontyGoOpResult monty_go_progress_resume_futures(struct MontyGoProgress *progress,
                                                        const uint8_t *results_ptr,
                                                        uintptr_t results_len);

#ifdef __cplusplus
}  // extern "C"
#endif  // __cplusplus

#endif  /* MONTY_GO_FFI_H */
