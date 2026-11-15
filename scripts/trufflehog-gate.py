#!/usr/bin/env python3
"""Run the TruffleHog scan and retry a result that stays unknown.

TruffleHog sends each candidate to its provider for verification. An
"unknown" result is a candidate whose verification request did not finish,
for example because the provider did not answer. A second run can verify
such a candidate. This gate runs the scan again, at most two more times,
when every result is unknown. A verified result fails at once. A command
that exits with an error and gives no result fails at once. A line that is
not a result object fails at once.

The gate never prints the fields that can hold the secret: Raw, RawV2,
Redacted and ExtraData. It replaces each control character in a printed
field with a question mark and cuts the field to a fixed length.

Usage:
  trufflehog-gate.py scan -- <trufflehog command and arguments>
      Runs the command with --json added and without --fail. Fails when a
      result is verified, or when it stays unknown after the retries.
  trufflehog-gate.py --selftest
      Checks the decision table of this script.

Standard library only.
"""

import json
import subprocess
import sys
import unicodedata

# The scan runs at most three times: the first run and two retries.
MAX_RUNS = 3

# Each field of a result message is cut to this many characters.
MAX_FIELD = 200


class ScanError(Exception):
    """The scan command failed and gave no result."""

    def __init__(self, returncode):
        super().__init__("the scan command failed with exit code %d" % returncode)
        self.returncode = returncode


class UnparsedOutputError(Exception):
    """The scan printed lines that are not a result object."""

    def __init__(self, count):
        super().__init__(
            "the scan command printed %d line%s that did not parse as a result object"
            % (count, "" if count == 1 else "s")
        )
        self.count = count


def with_json(command):
    """Return the command with --json added and --fail removed."""
    args = [arg for arg in command if arg != "--fail" and not arg.startswith("--fail=")]
    if "--json" not in args:
        args.append("--json")
    return args


def parse_results(text):
    """Return each line of text that is a JSON object.

    Raise UnparsedOutputError when a non-blank line is not a JSON object.
    """
    results = []
    unparsed = 0
    for line in text.splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            value = json.loads(line)
        except json.JSONDecodeError:
            unparsed += 1
            continue
        if isinstance(value, dict):
            results.append(value)
        else:
            unparsed += 1
    if unparsed:
        raise UnparsedOutputError(unparsed)
    return results


def run_command(args):
    """Run one scan and return its results.

    Raise ScanError when the command exits with an error and gives no result.
    Raise UnparsedOutputError when a line is not a result object.
    """
    result = subprocess.run(args, stdout=subprocess.PIPE, text=True, check=False)
    results = parse_results(result.stdout)
    if result.returncode != 0 and not results:
        raise ScanError(result.returncode)
    return results


def is_verified(result):
    """Return True when the result carries "Verified": true."""
    return result.get("Verified") is True


def nested(value, *keys):
    """Return the value at the key path, or an empty string when a key is absent.

    A missing key, or a value that is not a mapping, gives an empty string.
    """
    for key in keys:
        if not isinstance(value, dict):
            return ""
        value = value.get(key)
    return "" if value is None else value


def safe_field(value):
    """Return value with each control character replaced and the length cut."""
    text = "" if value is None else str(value)
    safe = "".join(
        "?" if unicodedata.category(char).startswith("C") else char for char in text
    )
    return safe[:MAX_FIELD]


def result_line(state, result):
    """Return the message line of one result. Never print the secret fields."""
    return (
        "trufflehog-gate: %s result: detector %s, file %s, line %s, commit %s"
        % (
            safe_field(state),
            safe_field(nested(result, "DetectorName")),
            safe_field(nested(result, "SourceMetadata", "Data", "Git", "file")),
            safe_field(nested(result, "SourceMetadata", "Data", "Git", "line")),
            safe_field(nested(result, "SourceMetadata", "Data", "Git", "commit")),
        )
    )


def failure_lines(runs, verified, unknown):
    """Return the message lines of a failure."""
    lines = [
        "trufflehog-gate: %d run%s made: %d verified, %d unknown"
        % (runs, "" if runs == 1 else "s", len(verified), len(unknown))
    ]
    for result in verified:
        lines.append(result_line("verified", result))
    for result in unknown:
        lines.append(result_line("unknown", result))
    return lines


def decide(run):
    """Return (exit code, message lines) for the scan.

    run() returns the list of results of one run. It raises ScanError when
    the scan command fails and gives no result. It raises UnparsedOutputError
    when a line is not a result object. A verified result fails at once. A
    run with no result passes. Only unknown results give a retry, at most
    two, and a third unknown result fails.
    """
    runs = 0
    unknown = []
    while runs < MAX_RUNS:
        runs += 1
        try:
            results = run()
        except ScanError as err:
            return 1, [
                "trufflehog-gate: run %d failed with exit code %d and no result. "
                "Run the trufflehog command by hand to see the error."
                % (runs, err.returncode)
            ]
        except UnparsedOutputError as err:
            return 1, ["trufflehog-gate: run %d: %s" % (runs, err)]
        verified = [result for result in results if is_verified(result)]
        unknown = [result for result in results if not is_verified(result)]
        if verified:
            return 1, failure_lines(runs, verified, unknown)
        if not results:
            return 0, []
    return 1, failure_lines(runs, [], unknown)


def scan(command):
    """Run the gate over the given command and return its exit code."""
    args = with_json(command)
    code, lines = decide(lambda: run_command(args))
    for line in lines:
        print(line, file=sys.stderr)
    if code == 0:
        print("trufflehog-gate: no verified secret found")
    return code


def fake_runner(runs):
    """Return a run() over the given run outcomes, and the list of the runs made.

    An outcome is a list of results, or an exception for run() to raise.
    """
    made = []

    def run():
        if len(made) >= len(runs):
            raise AssertionError("the decision made more runs than the case has")
        outcome = runs[len(made)]
        made.append(outcome)
        if isinstance(outcome, Exception):
            raise outcome
        return outcome

    return run, made


def check_case(case):
    """Return an error string when the case fails, or None when it passes."""
    run, made = fake_runner(case["runs"])
    try:
        code, lines = decide(run)
    except Exception as err:  # The fake run raises AssertionError for an extra run.
        return "raised %s: %s" % (type(err).__name__, err)
    if code != case["code"]:
        return "exit code %d, want %d" % (code, case["code"])
    if len(made) != case["made"]:
        return "%d runs made, want %d" % (len(made), case["made"])
    text = "\n".join(lines)
    if "text_lines" in case and len(text.splitlines()) != case["text_lines"]:
        return "%d printed lines, want %d" % (len(text.splitlines()), case["text_lines"])
    for prefix in case.get("no_prefix", []):
        for line in text.splitlines():
            if line.startswith(prefix):
                return "a message line starts with %r" % prefix
    for fragment in case.get("shows", []):
        if fragment not in text:
            return "the message does not name %r" % fragment
    for fragment in case.get("hides", []):
        if fragment in text:
            return "the message prints %r" % fragment
    if code == 0 and lines:
        return "a passing case printed %r" % lines
    return None


def make_result(verified, detector="DetectorA", file="config/app.tf", line=7, commit="abc123"):
    """Return a scan result in the shape of the TruffleHog JSON output."""
    return {
        "DetectorName": detector,
        "Verified": verified,
        "SourceMetadata": {
            "Data": {"Git": {"file": file, "line": line, "commit": commit}}
        },
    }


RAW_VALUE = "raw-value-that-the-selftest-keeps"
RAW_V2_VALUE = "raw-v2-value-that-the-selftest-keeps"
REDACTED_VALUE = "redacted-value-that-the-selftest-keeps"
EXTRA_VALUE = "extra-value-that-the-selftest-keeps"


def secret_result():
    """Return a verified result that carries the secret fields."""
    result = make_result(True)
    result["Raw"] = RAW_VALUE
    result["RawV2"] = RAW_V2_VALUE
    result["Redacted"] = REDACTED_VALUE
    result["ExtraData"] = {"note": EXTRA_VALUE}
    return result


CASES = [
    {
        "name": "verified on the first run",
        "runs": [[make_result(True)]],
        "code": 1,
        "made": 1,
        "shows": ["1 run made", "verified", "DetectorA", "config/app.tf", "line 7", "abc123"],
    },
    {
        "name": "verified and unknown on the first run",
        "runs": [
            [make_result(True, detector="DetectorA"), make_result(False, detector="DetectorB")]
        ],
        "code": 1,
        "made": 1,
        "shows": ["1 run made", "DetectorA", "DetectorB", "verified", "unknown"],
    },
    {
        "name": "no result",
        "runs": [[]],
        "code": 0,
        "made": 1,
    },
    {
        "name": "unknown, then no result",
        "runs": [[make_result(False)], []],
        "code": 0,
        "made": 2,
    },
    {
        "name": "unknown, unknown, then no result",
        "runs": [[make_result(False)], [make_result(False)], []],
        "code": 0,
        "made": 3,
    },
    {
        "name": "unknown three times",
        "runs": [[make_result(False)], [make_result(False)], [make_result(False)]],
        "code": 1,
        "made": 3,
        "shows": ["3 runs made", "unknown", "DetectorA", "config/app.tf"],
    },
    {
        "name": "unknown, then verified",
        "runs": [[make_result(False)], [make_result(True)]],
        "code": 1,
        "made": 2,
        "shows": ["2 runs made", "verified", "DetectorA"],
    },
    {
        "name": "a result without SourceMetadata",
        "runs": [[{"DetectorName": "DetectorA", "Verified": True}]],
        "code": 1,
        "made": 1,
        "shows": ["1 run made", "verified", "DetectorA"],
    },
    {
        "name": "the failure message hides Raw, RawV2, Redacted and ExtraData",
        "runs": [[secret_result()]],
        "code": 1,
        "made": 1,
        "hides": [RAW_VALUE, RAW_V2_VALUE, REDACTED_VALUE, EXTRA_VALUE],
    },
    {
        "name": "control characters in the detector and the file name",
        "runs": [
            [
                make_result(
                    True,
                    detector="D\r::warning::d\nforged\x1b[31m",
                    file="x\n::error::forged\x1b[31m",
                )
            ]
        ],
        "code": 1,
        "made": 1,
        "text_lines": 2,
        "no_prefix": ["::"],
        "shows": ["D?::warning::d?forged?[31m", "x?::error::forged?[31m"],
        "hides": ["\r", "\x1b"],
    },
    {
        "name": "control characters in the line and the commit",
        "runs": [
            [
                make_result(
                    True,
                    line="7\n::error::forged\x1b[31m",
                    commit="c\r::warning::c\nforged\x1b[31m",
                )
            ]
        ],
        "code": 1,
        "made": 1,
        "text_lines": 2,
        "no_prefix": ["::"],
        "shows": ["7?::error::forged?[31m", "c?::warning::c?forged?[31m"],
        "hides": ["\r", "\x1b"],
    },
    {
        "name": "the scan command fails and gives no result",
        "runs": [ScanError(2)],
        "code": 1,
        "made": 1,
        "shows": ["exit code 2"],
    },
]


def selftest():
    for number, case in enumerate(CASES, 1):
        error = check_case(case)
        if error is not None:
            print(
                "trufflehog-gate selftest: case %d (%s) failed: %s"
                % (number, case["name"], error),
                file=sys.stderr,
            )
            return 1
    secret_text = "RAW" + "SECRET plain text finding"
    code, lines = decide(
        lambda: run_command([sys.executable, "-c", "print(%r)" % secret_text])
    )
    text = "\n".join(lines)
    if code != 1:
        print(
            "trufflehog-gate selftest: a plain text line passed with exit code %d" % code,
            file=sys.stderr,
        )
        return 1
    if secret_text in text:
        print("trufflehog-gate selftest: a plain text line reached the message", file=sys.stderr)
        return 1
    if "1 line" not in text:
        print(
            "trufflehog-gate selftest: the message does not give the count of unparsed lines",
            file=sys.stderr,
        )
        return 1
    parsed = parse_results('{"Verified": false}\n\n{"Verified": true}\n')
    if parsed != [{"Verified": False}, {"Verified": True}]:
        print("trufflehog-gate selftest: parse_results gave %r" % (parsed,), file=sys.stderr)
        return 1
    try:
        parse_results("plain\n[1, 2]\n")
    except UnparsedOutputError as err:
        if err.count != 2:
            print(
                "trufflehog-gate selftest: parse_results counted %d unparsed lines, want 2"
                % err.count,
                file=sys.stderr,
            )
            return 1
    else:
        print(
            "trufflehog-gate selftest: parse_results did not fail on unparsed lines",
            file=sys.stderr,
        )
        return 1
    if len(safe_field("a" * (MAX_FIELD + 1))) != MAX_FIELD:
        print(
            "trufflehog-gate selftest: safe_field did not cut the field to %d characters"
            % MAX_FIELD,
            file=sys.stderr,
        )
        return 1
    try:
        run_command([sys.executable, "-c", "import sys; sys.exit(2)"])
    except ScanError as err:
        if err.returncode != 2:
            print(
                "trufflehog-gate selftest: the crashed scan gave exit code %d, want 2"
                % err.returncode,
                file=sys.stderr,
            )
            return 1
    else:
        print(
            "trufflehog-gate selftest: the crashed scan did not raise ScanError",
            file=sys.stderr,
        )
        return 1
    if run_command([sys.executable, "-c", "pass"]) != []:
        print("trufflehog-gate selftest: the empty scan gave a result", file=sys.stderr)
        return 1
    args = with_json(["trufflehog", "git", "file://.", "--fail", "--results=verified,unknown"])
    if args != ["trufflehog", "git", "file://.", "--results=verified,unknown", "--json"]:
        print("trufflehog-gate selftest: with_json gave %r" % (args,), file=sys.stderr)
        return 1
    if nested({"SourceMetadata": []}, "SourceMetadata", "Data", "Git", "file") != "":
        print("trufflehog-gate selftest: nested did not keep a non-mapping safe", file=sys.stderr)
        return 1
    print("trufflehog-gate selftest: %d cases passed" % len(CASES))
    return 0


def main(argv):
    if argv[1:] == ["--selftest"]:
        return selftest()
    if len(argv) >= 4 and argv[1] == "scan" and argv[2] == "--":
        return scan(argv[3:])
    print(__doc__, file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv))
