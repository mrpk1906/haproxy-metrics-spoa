# Security Policy

The `haproxy-metrics-spoa` maintainers take the security of this software seriously. We appreciate your efforts to responsibly disclose vulnerabilities to help us keep users safe.

---

## Supported Versions

Only the latest active major/minor release stream receives security updates and vulnerability patches.

| Version | Supported          |
| ------- | ------------------ |
| 1.x     | :white_check_mark: |
| < 1.0   | :x:                |

---

## Reporting a Vulnerability

**Please do not report security vulnerabilities through public GitHub issues.**

If you discover a potential vulnerability in `haproxy-metrics-spoa`, please disclose it responsibly through one of the following channels:

### Option 1: GitHub Private Security Advisory (Preferred)
Submit a private report using GitHub's vulnerability reporting feature:
1. Navigate to the repository's **Security** tab.
2. Under "Security", select **Advisories**.
3. Click **Report a vulnerability** to open a confidential advisory draft.

### Option 2: Direct Email
Send an encrypted or plain email to the maintainer:
- **Email:** `mrpk1906@gmail.com`
- **Subject:** `[SECURITY] haproxy-metrics-spoa: <Brief Vulnerability Summary>`

### What to Include in Your Report
To help us evaluate and address the issue efficiently, please include:
- A clear description of the vulnerability and its potential impact.
- Affected component or package (e.g., `pkg/spoa`, `pkg/normalizer`, `pkg/server`).
- Step-by-step reproduction instructions or a minimal Proof of Concept (PoC).
- Proposed mitigation or patch, if available.
- Any known exploit conditions or circumstances under which the vulnerability could be triggered.

---

## Response SLAs & Handling Process

When a security vulnerability is reported, we adhere to the following timeline:

1. **Initial Acknowledgement:** Within **48 hours**, we will confirm receipt of your report.
2. **Triage & Assessment:** Within **5 business days**, we will investigate, reproduce the issue, and assess its severity.
3. **Fix & Patch Development:** We will work on a fix in a private repository branch or security draft.
4. **Coordinated Disclosure:** We aim to release a patched version within **30 days** of triage (or sooner for critical severity issues), coordinating public disclosure with the reporter.

Credit will be given in release notes and advisories unless you request to remain anonymous.

---

## Operational Security Recommendations

When deploying `haproxy-metrics-spoa` in production, follow these security best practices:

- **Restrict SPOP Socket Access:** When using UNIX domain sockets (`unix:///...`), ensure strict filesystem permissions (`chmod 0660`) and restrict group ownership to the HAProxy daemon user to prevent untrusted local users from communicating with the agent.
- **Isolate TCP SPOP Listeners:** If using TCP (`tcp://...`), bind to loopback (`127.0.0.1`) or a dedicated internal network/overlay. Never expose the SPOP listener to the public internet without mutual TLS or network firewall isolation.
- **Tune Cardinality Safeguards:** Retain default or conservatively configured `--guard.max-hosts` limits to prevent memory exhaustion from malicious bot traffic scanning random virtual hosts.
