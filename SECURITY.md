# Security Policy

## Security Posture

Sense is a read-only codebase analysis tool. Its design minimizes attack surface by default:

- **Read-only by design.** Sense never modifies source files, writes outside `.sense/`, or executes code from the indexed project.
- **No network calls.** After installation, the binary makes zero outbound connections. No telemetry, no analytics, no phone-home. The `sense update` command is the only network operation, and it is user-initiated.
- **Local-only index.** The `.sense/` directory contains a SQLite database and vector index. It never leaves the machine. No cloud sync, no shared indexes.
- **No secrets handling.** Sense parses syntax trees. It does not evaluate code, extract string literals, or store file contents in the index. Sense does not attempt to identify or extract secrets, though symbol-adjacent code snippets are stored in the local index (see [Known Limitations](#known-limitations)).
- **No exec.** Sense does not shell out to interpret or execute code from the indexed project. The only subprocess is `git` (for diff-based analysis, when git is present).

## Reporting a Vulnerability

If you discover a security vulnerability in Sense, please report it through [GitHub's private vulnerability reporting](https://github.com/luuuc/sense/security/advisories/new). Do not open a public issue.

**Response commitment:**

- Acknowledge receipt within **48 hours**.
- Provide a fix or mitigation within **7 days** for critical issues, **30 days** for others.

## Binary Verification

Each release includes a `sense_VERSION_checksums.txt` file containing SHA-256 hashes for all release artifacts. To verify a downloaded binary:

```bash
# Example for v0.30.0 — replace with your version
curl -LO https://github.com/luuuc/sense/releases/download/v0.30.0/sense_0.30.0_checksums.txt

# Linux
sha256sum -c sense_0.30.0_checksums.txt --ignore-missing

# macOS
shasum -a 256 -c sense_0.30.0_checksums.txt --ignore-missing
```

Confirm you see your filename followed by `OK` in the output. If no filenames appear, you may have downloaded the wrong checksums file.

## Supported Versions

Only the latest release receives security fixes. Older versions are not backported.

| Version | Supported |
| ------- | --------- |
| Latest  | Yes       |
| < Latest | No       |

## Known Limitations

- **Symbol names in the index.** The `.sense/` SQLite database stores symbol names, file paths, and short code snippets used for embedding generation. If your codebase contains sensitive information in symbol names or comments near symbol definitions, those strings will be present in the index. The index is local-only and never transmitted.
- **No input sandboxing.** Sense runs with the permissions of the invoking user. It does not drop privileges or use OS-level sandboxing (seccomp, App Sandbox, etc.). This is consistent with other developer tools but means a compromised Sense binary would have the user's full file system access.
- **Stdio-only transport.** Sense serves MCP over stdio, not HTTP. There is no network listener. This eliminates network-level attack vectors but means security depends on the parent process (your AI tool) not passing malicious input.
- **Shared cache directory.** Sense extracts the bundled ONNX Runtime library to `~/.cache/sense/lib` (or `$SENSE_CACHE_DIR`). On multi-user systems where this directory is world-writable, another user could replace the extracted library between extraction and loading. This is not a concern on single-user developer machines.
