#!/usr/bin/env python3

from __future__ import annotations

import argparse
import datetime
import os
import platform
import re
import shutil
import statistics
import subprocess
import sys
from pathlib import Path


CASE_ORDER = [
    "add_two",
    "list_append",
    "loop_mod_13",
    "kitchen_sink",
    "func_call_kwargs",
    "list_append_str",
    "list_append_int",
    "fib",
    "list_comp",
    "dict_comp",
    "empty_tuples",
    "pair_tuples",
    "end_to_end",
]


def parse_args() -> argparse.Namespace:
    repo_root = Path(__file__).resolve().parent.parent
    default_upstream = repo_root.parent / "monty"

    parser = argparse.ArgumentParser(
        description="Run gomonty and upstream Monty benchmarks and print a Markdown comparison table."
    )
    parser.add_argument(
        "--repo-root",
        type=Path,
        default=repo_root,
        help=f"gomonty repository root (default: {repo_root})",
    )
    parser.add_argument(
        "--upstream",
        type=Path,
        default=default_upstream,
        help=f"upstream Monty checkout to benchmark (default: {default_upstream})",
    )
    parser.add_argument(
        "--go-count",
        type=int,
        default=3,
        help="number of Go benchmark runs for median aggregation (default: 3)",
    )
    parser.add_argument(
        "--pyo3-python",
        default=os.environ.get("PYO3_PYTHON") or shutil.which("python3"),
        help="Python interpreter for upstream PyO3 builds (default: PYO3_PYTHON or python3 on PATH)",
    )
    return parser.parse_args()


def run_command(cmd: list[str], cwd: Path, env: dict[str, str] | None = None) -> str:
    result = subprocess.run(
        cmd,
        cwd=cwd,
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        check=False,
    )
    if result.returncode != 0:
        joined = " ".join(cmd)
        raise SystemExit(
            f"command failed in {cwd}:\n{joined}\n\n{result.stdout}".rstrip()
        )
    return result.stdout


def git_revision(repo: Path) -> str:
    sha = run_command(["git", "rev-parse", "--short=12", "HEAD"], cwd=repo).strip()
    status = run_command(["git", "status", "--porcelain"], cwd=repo)
    if status.strip():
        return f"{sha}-dirty"
    return sha


def cpu_name() -> str:
    if sys.platform == "darwin":
        result = subprocess.run(
            ["sysctl", "-n", "machdep.cpu.brand_string"],
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            text=True,
            check=False,
        )
        if result.returncode == 0 and result.stdout.strip():
            return result.stdout.strip()
    cpuinfo = Path("/proc/cpuinfo")
    if cpuinfo.exists():
        for line in cpuinfo.read_text().splitlines():
            if ":" not in line:
                continue
            key, value = [part.strip() for part in line.split(":", 1)]
            if key.lower() == "model name" and value:
                return value
    name = platform.processor().strip()
    if name:
        return name
    return "unknown"


def go_host(repo: Path) -> tuple[str, str]:
    output = run_command(["go", "env", "GOOS", "GOARCH"], cwd=repo)
    values = [line.strip() for line in output.splitlines() if line.strip()]
    if len(values) != 2:
        raise SystemExit(f"unexpected go env output:\n{output}".rstrip())
    return values[0], values[1]


def parse_go_benchmarks(output: str) -> dict[str, float]:
    pattern = re.compile(
        r"^BenchmarkMonty(?:/([a-z0-9_]+)|EndToEnd)-\d+\s+\d+\s+([0-9]+(?:\.[0-9]+)?)\s+ns/op\b"
    )
    samples: dict[str, list[float]] = {}
    for raw_line in output.splitlines():
        line = raw_line.strip()
        match = pattern.match(line)
        if not match:
            continue
        name = match.group(1) or "end_to_end"
        value = float(match.group(2))
        samples.setdefault(name, []).append(value)

    missing = [name for name in CASE_ORDER if name not in samples]
    if missing:
        raise SystemExit(
            "missing Go benchmark results for: " + ", ".join(missing) + "\n\n" + output
        )
    return {name: statistics.median(samples[name]) for name in CASE_ORDER}


def time_to_ns(value: str, unit: str) -> float:
    scale = {
        "ns": 1.0,
        "us": 1_000.0,
        "µs": 1_000.0,
        "ms": 1_000_000.0,
        "s": 1_000_000_000.0,
    }
    return float(value) * scale[unit]


def parse_rust_benchmarks(output: str) -> dict[str, float]:
    pattern = re.compile(
        r"^([a-z0-9_]+)__monty\s+time:\s+\["
        r"([0-9]+(?:\.[0-9]+)?)\s+(ns|us|µs|ms|s)\s+"
        r"([0-9]+(?:\.[0-9]+)?)\s+(ns|us|µs|ms|s)\s+"
        r"([0-9]+(?:\.[0-9]+)?)\s+(ns|us|µs|ms|s)\]$"
    )
    results: dict[str, float] = {}
    for raw_line in output.splitlines():
        line = raw_line.strip()
        match = pattern.match(line)
        if not match:
            continue
        name = match.group(1)
        results[name] = time_to_ns(match.group(4), match.group(5))

    missing = [name for name in CASE_ORDER if name not in results]
    if missing:
        raise SystemExit(
            "missing Rust benchmark results for: " + ", ".join(missing) + "\n\n" + output
        )
    return {name: results[name] for name in CASE_ORDER}


def format_duration(ns: float) -> str:
    if ns >= 1_000_000_000:
        return f"{ns / 1_000_000_000:.3f} s"
    if ns >= 1_000_000:
        return f"{ns / 1_000_000:.3f} ms"
    if ns >= 1_000:
        return f"{ns / 1_000:.3f} us"
    if ns >= 100:
        return f"{ns:.0f} ns"
    return f"{ns:.2f} ns"


def markdown_table(go_results: dict[str, float], rust_results: dict[str, float]) -> str:
    lines = [
        "| Case | gomonty | raw monty | Ratio |",
        "| --- | ---: | ---: | ---: |",
    ]
    for name in CASE_ORDER:
        go_value = go_results[name]
        rust_value = rust_results[name]
        lines.append(
            f"| `{name}` | `{format_duration(go_value)}` | `{format_duration(rust_value)}` | `{go_value / rust_value:.2f}x` |"
        )
    return "\n".join(lines)


def main() -> None:
    args = parse_args()
    repo_root = args.repo_root.resolve()
    upstream = args.upstream.resolve()

    if not repo_root.joinpath("go.mod").exists():
        raise SystemExit(f"repo root does not look like gomonty: {repo_root}")
    if not upstream.joinpath("Cargo.toml").exists():
        raise SystemExit(f"upstream path does not look like a Cargo repo: {upstream}")
    if not args.pyo3_python:
        raise SystemExit("could not determine PYO3_PYTHON; set --pyo3-python or install python3")

    go_output = run_command(
        [
            "go",
            "test",
            "-run",
            "^$",
            "-bench",
            "BenchmarkMonty",
            "-benchmem",
            f"-count={args.go_count}",
        ],
        cwd=repo_root,
    )
    go_results = parse_go_benchmarks(go_output)

    rust_env = os.environ.copy()
    rust_env["PYO3_PYTHON"] = args.pyo3_python
    rust_env["CARGO_TERM_COLOR"] = "never"
    rust_output = run_command(
        [
            "cargo",
            "bench",
            "-p",
            "monty",
            "--bench",
            "main",
            "--",
            "__monty",
            "--noplot",
        ],
        cwd=upstream,
        env=rust_env,
    )
    rust_results = parse_rust_benchmarks(rust_output)

    goos, goarch = go_host(repo_root)
    print("Benchmark host:")
    print(f"- Date: {datetime.date.today().isoformat()}")
    print(f"- Target: {goos}/{goarch}")
    print(f"- CPU: {cpu_name()}")
    print(f"- gomonty revision: `{git_revision(repo_root)}`")
    print(f"- upstream monty revision: `{git_revision(upstream)}`")
    print("")
    print("Commands:")
    print(f"- `go test -run '^$' -bench BenchmarkMonty -benchmem -count={args.go_count}`")
    print("- `cargo bench -p monty --bench main -- __monty --noplot`")
    print("")
    print("Comparison:")
    print(markdown_table(go_results, rust_results))
    print("")
    print(
        "Notes: Go uses `testing.B` and upstream uses Criterion, so treat the ratios as directional."
    )


if __name__ == "__main__":
    main()
