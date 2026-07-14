# Security

## Reporting a vulnerability

Please report a vulnerability through GitHub's private vulnerability reporting for this repository. Do not open a public issue for an unpatched vulnerability or include private local data in a report.

Include the affected version, platform, reproduction steps, and expected impact. You can expect an acknowledgement within seven days. This is a personal project, so there is no guaranteed response or patch SLA.

## Trust boundary

`bb` is a local developer tool. It runs the board in your terminal, invokes the `bd` binary in the selected workspace, reads local Beads data, and may connect to a local model server you configure or it discovers.

The live MCP server:

- listens only on `127.0.0.1`
- requires a randomly generated bearer token
- stores that token in owner-only local files
- applies request-size limits and HTTP timeouts
- permits priority changes but no other issue mutation

The stdio MCP server is read-only and inherits the trust of the process that starts it.

Optional Claude Code hooks read local conversation transcripts to find bead IDs and excerpts. They are disabled until you run `bb hook install`. See the privacy section in the README before enabling them.

## Local data

Config, MCP endpoint data, session excerpts, natural-language feedback, and logs are private local data. Files created by `bb` use mode `0600`, and its config directory uses mode `0700`. Protect any alternate locations supplied through environment variables with equivalent filesystem controls.

`bb` does not provide a network service intended for remote exposure. Do not forward or publish its MCP port.

## Supported versions

Security fixes are provided on the latest tagged release. Reports against older versions may be closed after confirming the issue is absent from the latest release.
