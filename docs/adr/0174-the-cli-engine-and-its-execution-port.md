# 174. The CLI engine and its execution port

Status: accepted · 2026-10-18 · part of ADR 170

## Context

The project owner decided on 17 October 2026 that a CLI provider runs a
declared command as an argument array, with no shell, by the same rule as
the launcher, and parses the output as JSON (owner). ADR 101 decision 10,
ADR 133 decision 13 and ADR 134 give the rule of the launcher: a step is an
argument list, the launcher passes none of its own environment, and no
credential passes through an environment variable. ADR 133 decision 13
supersedes ADR 101 decision 10 in part.

The CLI engine implements the core port of ADR 172 from a declaration. It
has two parts: the engine, which builds commands and reads output, and a
port that starts one command, the command starter. The port is in the
package `cliexec` of `opendustpan/providerkit`.

## Decision

1. **The port, `cliexec.Starter`.** The command starter is the port. `Run`
   starts the program with the arguments, waits, and returns the exit code,
   the standard output and the standard error. A non-zero exit code is a
   result, not an error. The errors are `ErrInvalidCommand`, `ErrTimeout`,
   `ErrTooLarge`, the error of the context, and a failure to start.
2. **An argument list, no shell (owner).** The command starter starts the
   program with its arguments as they are. No character of an argument has a
   special meaning: `$(id)`, `*`, `;` and a space reach the program
   unchanged. The program is a bare name that the command starter finds on
   `PATH`, or a clean absolute path.
3. **A value cannot become an option.** A reference can fill a whole
   argument or a part of it, as in `--schedule={schedule}`. When a reference
   starts an argument, the engine expands the whole argument first. If the
   result starts with `-`, the command fails with `ErrOptionValue` and does
   not run (`Template.ExpandArg`). An empty first value then cannot let a
   later value or literal start the argument: `{a}{b}` with `a` empty and
   `b` set to `-rf`, and `{a}-x` with `a` empty, both fail.
   - A reference cannot be a part of the name of an option. When an
     argument starts with a literal `-`, `Validate` refuses a reference
     before the first `=`: `--{x}` and `-{x}` are invalid, and
     `--name={x}` is valid. Else `--{x}` with `x` set to
     `output=/etc/cron.d/x` gives the option `--output=/etc/cron.d/x`
     (`Template.OptionNameRef`).
   - The engine refuses an argument that starts with `-` also after a `--`
     argument. A program that reads `@file` arguments reads the file when a
     value starts with `@`. An author puts a value for such a program after
     `=` in an option, as in `--data={body}`, so that the program does not
     read it as a file name.
4. **The environment.** The program gets the variables that the declaration
   sets, `PATH` of the provider process, and `HOME` and `TMPDIR` in a
   private directory of the run. It gets nothing else. A declaration cannot
   set `PATH`, `HOME`, `TMPDIR` or a name that starts with `DUSTPAN_`.
5. **Secrets as files.** A sensitive value never goes in an argument or in
   an environment variable (ADR 171 decision 9). It goes on the standard
   input, or in a file that `{file:config.name}` names (ADR 175).
6. **Input and output.** The standard input is a JSON object that the
   engine builds from JSON pointers, as for an HTTP body, or empty. The
   standard output is one JSON value. The engine reads each attribute from
   a JSON pointer in it.
7. **Exit codes.** Exit code 0 is success. An exit code in `notFound` is
   `ErrNotFound`. Any other exit code is `ErrRejected`, with the masked end
   of the standard error.
8. **Process groups and limits.** The program runs in its own process group.
   The command starter stops the whole group at the timeout (default five
   minutes), when the output passes 16 MiB, and when the context ends. A
   child process of the tool then does not outlive the operation.
9. **No retry.** The engine never runs a command again on its own. A command
   that failed can have changed the object. A tool with a safe retry does
   it inside its own command.
10. **Linux and macOS first.** The command starter uses process groups of
    POSIX systems. Windows is an open question.

## Consequences

- The suite of the command starter (`cliexectest`) runs a real helper
  program: arguments arrive as they are, the program gets only the declared
  environment and a private `HOME`, the standard input arrives, an exit code
  is a result, the timeout stops the program, the timeout also stops a
  grandchild in the process group, a canceled context stops the program,
  large standard output and large standard error fail, an invalid command
  does not start, and a missing program is an error.
- The rules of `Command.Validate` and the two option rules have table tests.
- A tool that reads a credential only from an environment variable cannot
  be declared. Its team writes an SDK provider, or wraps the tool in a
  small script that reads a file.

## Open questions

- Windows. It has no process groups of the same kind. A Job Object gives the
  same effect, with code of its own.
- A version check. A declaration could name a command that prints the
  version of the tool, and a range that the provider accepts.
- A limit on parallel commands. OpenTofu and the Pulumi CLI already limit
  parallel operations. A tool with a lock of its own might need a lower
  limit.
