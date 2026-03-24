#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

typedef struct MontyGoRunner MontyGoRunner;
typedef struct MontyGoRepl MontyGoRepl;
typedef struct MontyGoProgress MontyGoProgress;
typedef struct MontyGoError MontyGoError;

typedef struct MontyGoBytes {
  uint8_t *ptr;
  size_t len;
} MontyGoBytes;

typedef struct MontyGoRunnerResult {
  MontyGoRunner *runner;
  MontyGoError *error;
} MontyGoRunnerResult;

typedef struct MontyGoReplResult {
  MontyGoRepl *repl;
  MontyGoError *error;
} MontyGoReplResult;

typedef struct MontyGoOpResult {
  MontyGoProgress *progress;
  MontyGoError *error;
  MontyGoRepl *repl;
  MontyGoBytes prints;
} MontyGoOpResult;

void monty_go_bytes_free(MontyGoBytes bytes);
void monty_go_runner_free(MontyGoRunner *runner);
void monty_go_repl_free(MontyGoRepl *repl);
void monty_go_progress_free(MontyGoProgress *progress);
void monty_go_error_free(MontyGoError *error);

MontyGoBytes monty_go_error_json(const MontyGoError *error);
MontyGoBytes monty_go_error_display(
    const MontyGoError *error,
    const char *format,
    bool color);

MontyGoRunnerResult monty_go_runner_new(
    const uint8_t *code_ptr,
    size_t code_len,
    const uint8_t *options_ptr,
    size_t options_len);
MontyGoRunnerResult monty_go_runner_load(
    const uint8_t *data_ptr,
    size_t data_len);
MontyGoBytes monty_go_runner_dump(
    const MontyGoRunner *runner,
    MontyGoError **error_out);
MontyGoError *monty_go_runner_type_check(
    const MontyGoRunner *runner,
    const uint8_t *prefix_ptr,
    size_t prefix_len);
MontyGoOpResult monty_go_runner_start(
    const MontyGoRunner *runner,
    const uint8_t *options_ptr,
    size_t options_len);

MontyGoReplResult monty_go_repl_new(
    const uint8_t *options_ptr,
    size_t options_len);
MontyGoReplResult monty_go_repl_load(
    const uint8_t *data_ptr,
    size_t data_len);
MontyGoBytes monty_go_repl_dump(
    const MontyGoRepl *repl,
    MontyGoError **error_out);
MontyGoOpResult monty_go_repl_feed_start(
    MontyGoRepl *repl,
    const uint8_t *code_ptr,
    size_t code_len,
    const uint8_t *options_ptr,
    size_t options_len);

MontyGoBytes monty_go_progress_describe(
    const MontyGoProgress *progress,
    MontyGoError **error_out);
MontyGoBytes monty_go_progress_dump(
    const MontyGoProgress *progress,
    MontyGoError **error_out);
MontyGoOpResult monty_go_progress_load(
    const uint8_t *data_ptr,
    size_t data_len);
MontyGoReplResult monty_go_progress_take_repl(
    MontyGoProgress *progress);
MontyGoOpResult monty_go_progress_resume_call(
    MontyGoProgress *progress,
    const uint8_t *result_ptr,
    size_t result_len);
MontyGoOpResult monty_go_progress_resume_lookup(
    MontyGoProgress *progress,
    const uint8_t *result_ptr,
    size_t result_len);
MontyGoOpResult monty_go_progress_resume_futures(
    MontyGoProgress *progress,
    const uint8_t *results_ptr,
    size_t results_len);
